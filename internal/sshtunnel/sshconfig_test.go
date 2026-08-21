package sshtunnel

import (
	"errors"
	"testing"
)

// sshGOutput is the shape `ssh -G host` prints: one lowercased key and its
// value per line, with plenty of settings we ignore.
const sshGOutput = `user amartynenko
hostname 34.91.26.209
port 22
addressfamily any
forwardagent no
identityagent ~/Library/Group Containers/2BUA8C4S2C.com.1password/t/agent.sock
`

// stubSSHG swaps the ssh invocation for the duration of one test.
func stubSSHG(t *testing.T, fn func(string) (string, error)) {
	t.Helper()
	orig := sshG
	sshG = fn
	t.Cleanup(func() { sshG = orig })
}

func TestLookupHostResolvesAnAlias(t *testing.T) {
	var asked string
	stubSSHG(t, func(alias string) (string, error) {
		asked = alias
		return sshGOutput, nil
	})

	got := lookupHost("prod-bastion-vm")
	if asked != "prod-bastion-vm" {
		t.Errorf("asked ssh about %q, want the alias", asked)
	}
	if got.Hostname != "34.91.26.209" {
		t.Errorf("hostname = %q, want the alias resolved to its address", got.Hostname)
	}
	if got.User != "amartynenko" {
		t.Errorf("user = %q, want the one ssh_config sets", got.User)
	}
	if got.Port != 22 {
		t.Errorf("port = %d, want 22", got.Port)
	}
}

func TestLookupHostFallsBackToTheLiteralName(t *testing.T) {
	stubSSHG(t, func(string) (string, error) { return "", errors.New("ssh not found") })

	got := lookupHost("bastion.corp.com")
	if got.Hostname != "bastion.corp.com" {
		t.Errorf("hostname = %q, want the name unchanged when ssh cannot answer", got.Hostname)
	}
	if got.User != "" || got.Port != 0 {
		t.Errorf("got user %q port %d, want both unset so the caller's defaults apply", got.User, got.Port)
	}
}

func TestLookupHostIgnoresMalformedLines(t *testing.T) {
	stubSSHG(t, func(string) (string, error) {
		return "hostname db.internal\nnovalue\nport notanumber\n", nil
	})

	got := lookupHost("whatever")
	if got.Hostname != "db.internal" {
		t.Errorf("hostname = %q, want the one good line honoured", got.Hostname)
	}
	if got.Port != 0 {
		t.Errorf("port = %d, want 0: a non-numeric port must not be invented", got.Port)
	}
}
