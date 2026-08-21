package sshtunnel

import (
	"context"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lergor11/d9s/internal/config"
)

// fakeHome creates a short-pathed temp dir (unix socket paths are limited to
// ~104 bytes on darwin, so t.TempDir's long paths are unsafe here) and points
// HOME at it so os.UserHomeDir resolves hermetically.
func fakeHome(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "d9s")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("HOME", dir)
	return dir
}

// listenUnix creates a real unix socket at path.
func listenUnix(t *testing.T, path string) {
	t.Helper()
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
}

func TestResolveAgentSocketExplicitWins(t *testing.T) {
	home := fakeHome(t)
	t.Setenv("SSH_AUTH_SOCK", "/should/not/be/used")

	got, err := resolveAgentSocket("~/custom.sock")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, "custom.sock"); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolveAgentSocketExplicitAbsolute(t *testing.T) {
	fakeHome(t)
	got, err := resolveAgentSocket("/some/agent.sock")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/some/agent.sock" {
		t.Errorf("got %q, want /some/agent.sock", got)
	}
}

func TestResolveAgentSocketPrefers1Password(t *testing.T) {
	home := fakeHome(t)
	opSock := filepath.Join(home, ".1password", "agent.sock")
	if err := os.MkdirAll(filepath.Dir(opSock), 0o700); err != nil {
		t.Fatal(err)
	}
	listenUnix(t, opSock)
	t.Setenv("SSH_AUTH_SOCK", "/should/not/be/used")

	got, err := resolveAgentSocket("")
	if err != nil {
		t.Fatal(err)
	}
	if got != opSock {
		t.Errorf("got %q, want 1Password socket %q", got, opSock)
	}
}

func TestResolveAgentSocketIgnoresPlainFile(t *testing.T) {
	home := fakeHome(t)
	opSock := filepath.Join(home, ".1password", "agent.sock")
	if err := os.MkdirAll(filepath.Dir(opSock), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(opSock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SSH_AUTH_SOCK", "/env/agent.sock")

	got, err := resolveAgentSocket("")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/env/agent.sock" {
		t.Errorf("got %q, want SSH_AUTH_SOCK fallback (regular file is not a socket)", got)
	}
}

func TestResolveAgentSocketFallsBackToEnv(t *testing.T) {
	fakeHome(t)
	t.Setenv("SSH_AUTH_SOCK", "/env/agent.sock")

	got, err := resolveAgentSocket("")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/env/agent.sock" {
		t.Errorf("got %q, want /env/agent.sock", got)
	}
}

func TestResolveAgentSocketNoneAvailable(t *testing.T) {
	fakeHome(t)
	t.Setenv("SSH_AUTH_SOCK", "")

	_, err := resolveAgentSocket("")
	if err == nil {
		t.Fatal("expected an error when no agent is available")
	}
	if !strings.Contains(err.Error(), "1Password") {
		t.Errorf("error should explain enabling the 1Password agent, got: %v", err)
	}
}

func TestResolveUserConfigured(t *testing.T) {
	got, err := resolveUser("deploy")
	if err != nil {
		t.Fatal(err)
	}
	if got != "deploy" {
		t.Errorf("got %q, want deploy", got)
	}
}

func TestResolveUserDefaultsToOSUser(t *testing.T) {
	current, err := user.Current()
	if err != nil {
		t.Skipf("cannot determine current user: %v", err)
	}
	got, err := resolveUser("")
	if err != nil {
		t.Fatal(err)
	}
	if got != current.Username {
		t.Errorf("got %q, want current OS user %q", got, current.Username)
	}
}

func TestBastionAddr(t *testing.T) {
	if got := bastionAddr(config.SSH{Bastion: "b.example.com"}); got != "b.example.com:22" {
		t.Errorf("default port: got %q, want b.example.com:22", got)
	}
	if got := bastionAddr(config.SSH{Bastion: "b.example.com", Port: 2222}); got != "b.example.com:2222" {
		t.Errorf("explicit port: got %q, want b.example.com:2222", got)
	}
}

func TestDialNoAgentFails(t *testing.T) {
	fakeHome(t)
	t.Setenv("SSH_AUTH_SOCK", "")

	tun := New(config.SSH{Bastion: "bastion.example.com"})
	_, err := tun.Dial(context.Background(), "tcp", "db.internal:5432")
	if err == nil {
		t.Fatal("expected Dial to fail without an agent")
	}
	if !strings.Contains(err.Error(), "1Password") {
		t.Errorf("error should mention the 1Password agent, got: %v", err)
	}
}

func TestDialMissingKnownHostsFails(t *testing.T) {
	home := fakeHome(t)
	sock := filepath.Join(home, "agent.sock")
	listenUnix(t, sock)
	t.Setenv("SSH_AUTH_SOCK", sock)

	tun := New(config.SSH{Bastion: "bastion.example.com"})
	_, err := tun.Dial(context.Background(), "tcp", "db.internal:5432")
	if err == nil {
		t.Fatal("expected Dial to fail without known_hosts")
	}
	if !strings.Contains(err.Error(), "known_hosts") || !strings.Contains(err.Error(), "ssh bastion.example.com") {
		t.Errorf("error should tell the user to ssh to the bastion once, got: %v", err)
	}
}

func TestDialAfterCloseFails(t *testing.T) {
	tun := New(config.SSH{Bastion: "bastion.example.com"})
	if err := tun.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := tun.Dial(context.Background(), "tcp", "db.internal:5432"); err == nil {
		t.Fatal("expected Dial after Close to fail")
	}
}

func TestCloseWithoutDialIsNoop(t *testing.T) {
	if err := New(config.SSH{Bastion: "b"}).Close(); err != nil {
		t.Fatal(err)
	}
}
