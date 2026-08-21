package mcp

import (
	"testing"

	"github.com/andreim/d9s/internal/config"
)

// bastioned returns a connection reached through an SSH bastion. No test here
// dials it: sshtunnel.New performs no I/O, and the bastion is only contacted
// by a driver that actually connects.
func bastioned(name string) config.Connection {
	return config.Connection{
		Name: name, Type: config.Postgres, Host: "10.0.0.1", Port: 5432,
		SSH: &config.SSH{Bastion: "bastion.corp.com", User: "deploy", Port: 22},
	}
}

// TestTunnelIsSharedAcrossDatabasesOfOneConnection pins the arrangement
// sshtunnel.Tunnel is documented for. Sessions are per database, but the
// bastion connection belongs to the d9s connection: giving each database its
// own would turn browsing three databases into three SSH handshakes.
func TestTunnelIsSharedAcrossDatabasesOfOneConnection(t *testing.T) {
	opener := newLiveOpener(&recordingResolver{red: &redactor{}})
	conn := bastioned("prod-pg")

	first := opener.tunnelFor(conn)
	second := opener.tunnelFor(conn)
	if first != second {
		t.Error("a second database on the same connection got its own tunnel, so it would re-dial the bastion")
	}
	if other := opener.tunnelFor(bastioned("staging-pg")); other == first {
		t.Error("two different connections share one tunnel, which would send staging through production's bastion")
	}
	if got := len(opener.tunnels); got != 2 {
		t.Errorf("the opener holds %d tunnels, want one per connection (2)", got)
	}
}

// TestClosingTheOpenerReleasesEveryTunnel covers the shutdown path: the pool
// closes sessions, then the connector closes what they shared.
func TestClosingTheOpenerReleasesEveryTunnel(t *testing.T) {
	opener := newLiveOpener(&recordingResolver{red: &redactor{}})
	opener.tunnelFor(bastioned("prod-pg"))
	opener.tunnelFor(bastioned("staging-pg"))

	if err := opener.close(); err != nil {
		t.Fatalf("closing the opener: %v", err)
	}
	if got := len(opener.tunnels); got != 0 {
		t.Errorf("%d tunnels survived shutdown, want 0", got)
	}
}

// TestPoolClosesSessionsThenSharedResources checks the ordering that makes the
// shared tunnel safe: no session is still using one when it is torn down.
func TestPoolClosesSessionsThenSharedResources(t *testing.T) {
	opener := newFakeOpener()
	p := newPool(opener)

	for _, database := range []string{"", "other"} {
		if _, err := p.get(t.Context(), testConfig().Connections[0], database); err != nil {
			t.Fatalf("opening the session for database %q: %v", database, err)
		}
	}
	if opener.openCount() != 2 {
		t.Fatalf("opened %d sessions, want one per database", opener.openCount())
	}

	if err := p.close(); err != nil {
		t.Fatalf("closing the pool: %v", err)
	}
	if !opener.driver("prod-pg").isClosed() {
		t.Error("a pooled session survived shutdown")
	}
	if !opener.closed {
		t.Error("the pool closed its sessions but not the resources they shared")
	}

	// A closed pool must not hand out another session.
	if _, err := p.get(t.Context(), testConfig().Connections[0], ""); err == nil {
		t.Error("the pool opened a session after shutdown")
	}
}
