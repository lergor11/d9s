// Package sshtunnel provides bastion-host tunneling authenticated via an SSH
// agent (1Password agent preferred). Private key files are never read.
package sshtunnel

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/lergor11/d9s/internal/config"
)

const (
	handshakeTimeout  = 15 * time.Second
	keepaliveInterval = 30 * time.Second
)

// Tunnel is a lazily-established SSH connection to a bastion, shared by all
// database sessions of one d9s connection.
type Tunnel struct {
	cfg config.SSH

	mu      sync.Mutex
	client  *ssh.Client
	dropped bool // a previously working connection died; next Dial reconnects
	closed  bool
}

// New creates a tunnel handle; no network I/O happens until Dial.
func New(cfg config.SSH) *Tunnel {
	return &Tunnel{cfg: cfg}
}

// Dial opens a connection to addr ("host:port") through the bastion
// (direct-tcpip). Establishes the SSH session on first use; if an earlier
// attempt failed or the connection dropped, it tries again.
func (t *Tunnel) Dial(ctx context.Context, network, addr string) (net.Conn, error) {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil, errors.New("sshtunnel: tunnel is closed")
	}
	client := t.client
	if client == nil {
		c, err := t.connect(ctx)
		if err != nil {
			dropped := t.dropped
			t.mu.Unlock()
			if dropped {
				return nil, fmt.Errorf("ssh connection to %s dropped; reconnect failed: %w", t.cfg.Bastion, err)
			}
			return nil, err
		}
		t.client = c
		t.dropped = false
		client = c
		go t.monitor(c)
	}
	t.mu.Unlock()
	return client.DialContext(ctx, "tcp", addr)
}

// Close tears down the SSH connection (no-op if never established).
func (t *Tunnel) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.closed = true
	if t.client == nil {
		return nil
	}
	err := t.client.Close()
	t.client = nil
	return err
}

// connect establishes the SSH client. Called with t.mu held, which serializes
// concurrent first Dials onto a single handshake.
func (t *Tunnel) connect(ctx context.Context) (*ssh.Client, error) {
	sockPath, err := resolveAgentSocket(t.cfg.AgentSocket)
	if err != nil {
		return nil, err
	}
	agentConn, err := net.Dial("unix", sockPath)
	if err != nil {
		return nil, fmt.Errorf("connecting to SSH agent at %s: %w", sockPath, err)
	}
	defer func() { _ = agentConn.Close() }() // signers are only needed during the handshake

	// A bastion is usually named by its ~/.ssh/config alias, so ask ssh what
	// that resolves to. Anything set explicitly in the d9s config still wins.
	host := lookupHost(t.cfg.Bastion)
	sshUser, err := resolveUser(cmp.Or(t.cfg.User, host.User))
	if err != nil {
		return nil, err
	}
	hostKeyCB, err := hostKeyCallback(host.Hostname)
	if err != nil {
		return nil, err
	}

	addr := bastionAddr(host.Hostname, cmp.Or(t.cfg.Port, host.Port))
	var d net.Dialer
	tcpConn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("dialing bastion %s: %w", addr, err)
	}

	deadline := time.Now().Add(handshakeTimeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	_ = tcpConn.SetDeadline(deadline)
	handshakeDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = tcpConn.Close()
		case <-handshakeDone:
		}
	}()

	clientCfg := &ssh.ClientConfig{
		User:            sshUser,
		Auth:            []ssh.AuthMethod{ssh.PublicKeysCallback(agent.NewClient(agentConn).Signers)},
		HostKeyCallback: hostKeyCB,
		Timeout:         handshakeTimeout,
	}
	sshConn, chans, reqs, err := ssh.NewClientConn(tcpConn, addr, clientCfg)
	close(handshakeDone)
	if err != nil {
		_ = tcpConn.Close()
		if ctx.Err() != nil {
			return nil, fmt.Errorf("ssh handshake with %s: %w", addr, ctx.Err())
		}
		var keyErr *knownhosts.KeyError
		if errors.As(err, &keyErr) {
			if len(keyErr.Want) == 0 {
				return nil, fmt.Errorf("bastion %s is not in known_hosts; run `ssh %s@%s` once to trust its host key: %w", t.cfg.Bastion, sshUser, t.cfg.Bastion, err)
			}
			return nil, fmt.Errorf("host key mismatch for bastion %s (possible MITM, or the host key changed); verify and update ~/.ssh/known_hosts, e.g. via `ssh %s@%s`: %w", t.cfg.Bastion, sshUser, t.cfg.Bastion, err)
		}
		return nil, fmt.Errorf("ssh handshake with %s: %w", addr, err)
	}
	_ = tcpConn.SetDeadline(time.Time{})
	return ssh.NewClient(sshConn, chans, reqs), nil
}

// monitor watches an established client and clears it from the tunnel when it
// dies, so the next Dial re-establishes. Keepalives detect silently dead TCP
// connections that would otherwise hang until kernel timeouts.
func (t *Tunnel) monitor(c *ssh.Client) {
	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(keepaliveInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				if _, _, err := c.SendRequest("keepalive@openssh.com", true, nil); err != nil {
					_ = c.Close()
					return
				}
			}
		}
	}()
	_ = c.Wait()
	close(stop)
	t.mu.Lock()
	if t.client == c {
		t.client = nil
		t.dropped = true
	}
	t.mu.Unlock()
}

// resolveAgentSocket picks the SSH agent socket: explicit config override,
// then the 1Password agent, then the environment's agent.
func resolveAgentSocket(explicit string) (string, error) {
	if explicit != "" {
		return expandTilde(explicit)
	}
	if home, err := os.UserHomeDir(); err == nil {
		p := filepath.Join(home, ".1password", "agent.sock")
		if st, err := os.Stat(p); err == nil && st.Mode()&os.ModeSocket != 0 {
			return p, nil
		}
	}
	if s := os.Getenv("SSH_AUTH_SOCK"); s != "" {
		return s, nil
	}
	return "", errors.New("no SSH agent found: enable the 1Password SSH agent (1Password → Settings → Developer → \"Use the SSH agent\"), or set SSH_AUTH_SOCK, or set agent_socket in the connection's ssh config")
}

func expandTilde(p string) (string, error) {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("expanding %q: %w", p, err)
	}
	return filepath.Join(home, p[1:]), nil
}

func resolveUser(configured string) (string, error) {
	if configured != "" {
		return configured, nil
	}
	u, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("ssh user not configured and current OS user unknown: %w", err)
	}
	return u.Username, nil
}

func bastionAddr(hostname string, port int) string {
	if port == 0 {
		port = 22
	}
	return net.JoinHostPort(hostname, strconv.Itoa(port))
}

// hostKeyCallback verifies the bastion against ~/.ssh/known_hosts. There is
// deliberately no insecure fallback.
func hostKeyCallback(bastion string) (ssh.HostKeyCallback, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("locating known_hosts: %w", err)
	}
	var files []string
	for _, name := range []string{"known_hosts", "known_hosts2"} {
		p := filepath.Join(home, ".ssh", name)
		if _, err := os.Stat(p); err == nil {
			files = append(files, p)
		}
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no ~/.ssh/known_hosts file; run `ssh %s` once to trust the bastion's host key", bastion)
	}
	cb, err := knownhosts.New(files...)
	if err != nil {
		return nil, fmt.Errorf("parsing known_hosts: %w", err)
	}
	return cb, nil
}
