package sshtunnel

import (
	"bufio"
	"os/exec"
	"strconv"
	"strings"
)

// hostSettings is what OpenSSH would use for a host: the parts of its
// effective configuration that decide where and as whom we connect.
type hostSettings struct {
	Hostname string
	User     string
	Port     int
}

// sshG asks OpenSSH to resolve a host's effective configuration. It is a
// variable so tests can supply canned output instead of running ssh.
var sshG = func(alias string) (string, error) {
	path, err := exec.LookPath("ssh")
	if err != nil {
		return "", err
	}
	out, err := exec.Command(path, "-G", alias).Output() //nolint:gosec // alias comes from the user's own config
	return string(out), err
}

// lookupHost resolves an ~/.ssh/config alias the way ssh itself would.
//
// A bastion named in d9s's config is usually the name the user types at a
// shell — `prod-bastion-vm` — which is a Host alias, not a DNS name. Rather
// than reimplement ssh_config parsing, with its Include, Match and wildcard
// rules, this asks `ssh -G` for the already-resolved answer. A host that is
// not an alias resolves to itself, so the plain case costs nothing but the
// lookup.
//
// A failure here is not fatal: the caller falls back to treating the bastion
// as a literal hostname, which is what d9s did before.
func lookupHost(alias string) hostSettings {
	s := hostSettings{Hostname: alias}
	out, err := sshG(alias)
	if err != nil {
		return s
	}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		key, value, ok := strings.Cut(strings.TrimSpace(sc.Text()), " ")
		if !ok {
			continue
		}
		switch strings.ToLower(key) {
		case "hostname":
			s.Hostname = value
		case "user":
			s.User = value
		case "port":
			if p, err := strconv.Atoi(value); err == nil {
				s.Port = p
			}
		}
	}
	return s
}
