package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	return path
}

func TestLoadAppliesDefaults(t *testing.T) {
	path := writeConfig(t, `
connections:
  - name: pg
    type: postgres
    host: db.internal
  - name: ch
    type: clickhouse
    host: ch.internal
    ssh:
      bastion: bastion.corp.com
  - name: cache
    type: redis
    host: 127.0.0.1
    port: 6380
`)

	cfg, warns, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(warns) != 0 {
		t.Errorf("got %d warnings, want none: %v", len(warns), warns)
	}
	if len(cfg.Connections) != 3 {
		t.Fatalf("got %d connections, want 3", len(cfg.Connections))
	}
	if got := cfg.Connections[0].Port; got != 5432 {
		t.Errorf("postgres default port = %d, want 5432", got)
	}
	if got := cfg.Connections[1].Port; got != 9000 {
		t.Errorf("clickhouse default port = %d, want 9000", got)
	}
	if got := cfg.Connections[1].SSH.Port; got != 22 {
		t.Errorf("ssh default port = %d, want 22", got)
	}
	if got := cfg.Connections[2].Port; got != 6380 {
		t.Errorf("explicit port = %d, want 6380 (not overridden by the default)", got)
	}
}

func TestLoadParsesTLSBlock(t *testing.T) {
	path := writeConfig(t, `
connections:
  - name: cloud
    type: postgres
    host: db.neon.tech
    tls:
      mode: verify-full
      ca: /etc/ssl/roots.pem
      cert: op://Infra/db/cert
      key: op://Infra/db/key
      server_name: db.neon.tech
`)

	cfg, _, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := cfg.Connections[0].TLS
	if got == nil {
		t.Fatal("tls block was not parsed")
	}
	want := TLS{
		Mode:       TLSVerifyFull,
		CA:         "/etc/ssl/roots.pem",
		Cert:       "op://Infra/db/cert",
		Key:        "op://Infra/db/key",
		ServerName: "db.neon.tech",
	}
	if *got != want {
		t.Errorf("tls = %+v, want %+v", *got, want)
	}
}

func TestEffectiveTLSMode(t *testing.T) {
	tests := []struct {
		name string
		body string
		want TLSMode
	}{
		{
			name: "direct connection defaults to require",
			body: "connections:\n  - name: x\n    type: postgres\n    host: h\n",
			want: TLSRequire,
		},
		{
			name: "tunneled connection defaults to disable",
			body: "connections:\n  - name: x\n    type: postgres\n    host: h\n    ssh:\n      bastion: b\n",
			want: TLSDisable,
		},
		{
			name: "empty tls block still takes the default",
			body: "connections:\n  - name: x\n    type: postgres\n    host: h\n    tls: {}\n",
			want: TLSRequire,
		},
		{
			name: "explicit mode wins over the tunnel default",
			body: "connections:\n  - name: x\n    type: postgres\n    host: h\n    ssh:\n      bastion: b\n    tls:\n      mode: verify-full\n",
			want: TLSVerifyFull,
		},
		{
			name: "explicit disable wins over the direct default",
			body: "connections:\n  - name: x\n    type: postgres\n    host: h\n    tls:\n      mode: disable\n",
			want: TLSDisable,
		},
		{
			name: "verify-ca is accepted",
			body: "connections:\n  - name: x\n    type: postgres\n    host: h\n    tls:\n      mode: verify-ca\n",
			want: TLSVerifyCA,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, _, err := Load(writeConfig(t, tt.body))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if got := cfg.Connections[0].EffectiveTLSMode(); got != tt.want {
				t.Errorf("EffectiveTLSMode() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLoadParsesConnectivityFields(t *testing.T) {
	path := writeConfig(t, `
connections:
  - name: ch
    type: clickhouse
    host: ch.internal
    protocol: http
    connect_timeout: 45s
  - name: cluster
    type: redis
    host: 10.0.2.1
    mode: cluster
    addresses:
      - 10.0.2.2:6379
      - 10.0.2.3:6380
  - name: sentinel
    type: redis
    host: sentinel-1
    port: 26379
    mode: sentinel
    master_name: mymaster
    addresses:
      - sentinel-2:26379
`)

	cfg, _, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	ch, cluster, sentinel := &cfg.Connections[0], &cfg.Connections[1], &cfg.Connections[2]

	if got := ch.EffectiveProtocol(); got != ProtocolHTTP {
		t.Errorf("clickhouse protocol = %q, want %q", got, ProtocolHTTP)
	}
	if got := ch.Port; got != clickHouseHTTPPort {
		t.Errorf("clickhouse http port = %d, want %d (the http interface, not the native one)", got, clickHouseHTTPPort)
	}
	if got, want := ch.EffectiveConnectTimeout(), 45*time.Second; got != want {
		t.Errorf("connect_timeout = %v, want %v", got, want)
	}

	if got := cluster.EffectiveRedisMode(); got != RedisCluster {
		t.Errorf("redis mode = %q, want %q", got, RedisCluster)
	}
	wantNodes := []string{"10.0.2.1:6379", "10.0.2.2:6379", "10.0.2.3:6380"}
	if got := cluster.Nodes(); !slices.Equal(got, wantNodes) {
		t.Errorf("cluster nodes = %v, want %v (host:port first, then the extra addresses)", got, wantNodes)
	}

	if got := sentinel.MasterName; got != "mymaster" {
		t.Errorf("master_name = %q, want %q", got, "mymaster")
	}
	wantSentinels := []string{"sentinel-1:26379", "sentinel-2:26379"}
	if got := sentinel.Nodes(); !slices.Equal(got, wantSentinels) {
		t.Errorf("sentinel nodes = %v, want %v", got, wantSentinels)
	}
}

func TestConnectivityDefaults(t *testing.T) {
	path := writeConfig(t, `
connections:
  - name: ch
    type: clickhouse
    host: h
  - name: cache
    type: redis
    host: h
  - name: pg
    type: postgres
    host: h
`)

	cfg, _, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	ch, cache := &cfg.Connections[0], &cfg.Connections[1]
	if got := ch.EffectiveProtocol(); got != ProtocolNative {
		t.Errorf("clickhouse protocol = %q, want %q", got, ProtocolNative)
	}
	if got := ch.Port; got != 9000 {
		t.Errorf("clickhouse default port = %d, want the native 9000", got)
	}
	if got := cache.EffectiveRedisMode(); got != RedisStandalone {
		t.Errorf("redis mode = %q, want %q", got, RedisStandalone)
	}
	for i := range cfg.Connections {
		conn := &cfg.Connections[i]
		if got := conn.EffectiveConnectTimeout(); got != DefaultConnectTimeout {
			t.Errorf("%s connect timeout = %v, want %v", conn.Name, got, DefaultConnectTimeout)
		}
	}
}

func TestUnixSocketHost(t *testing.T) {
	path := writeConfig(t, `
connections:
  - name: socket
    type: postgres
    host: /var/run/postgresql
  - name: socket-explicit-disable
    type: postgres
    host: /var/run/postgresql
    tls:
      mode: disable
  - name: tcp
    type: postgres
    host: db.internal
`)

	cfg, _, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	socket, explicit, tcp := &cfg.Connections[0], &cfg.Connections[1], &cfg.Connections[2]
	if !socket.IsUnixSocket() {
		t.Error("a host starting with / is not recognised as a unix socket")
	}
	if tcp.IsUnixSocket() {
		t.Error("a hostname was mistaken for a unix socket")
	}
	// The require-by-default rule must not reach a socket: postgres refuses
	// TLS on one, so the connection would fail before it started.
	if got := socket.EffectiveTLSMode(); got != TLSDisable {
		t.Errorf("unix socket tls mode = %q, want %q", got, TLSDisable)
	}
	if got := explicit.EffectiveTLSMode(); got != TLSDisable {
		t.Errorf("explicitly disabled unix socket tls mode = %q, want %q", got, TLSDisable)
	}
	if got := tcp.EffectiveTLSMode(); got != TLSRequire {
		t.Errorf("tcp tls mode = %q, want %q", got, TLSRequire)
	}
}

func TestLoadMissingFileIsNotAnError(t *testing.T) {
	cfg, warns, err := Load(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Connections) != 0 || len(warns) != 0 {
		t.Errorf("got %d connections and %d warnings, want an empty config", len(cfg.Connections), len(warns))
	}
}

func TestLoadWarnsOnPlaintextPassword(t *testing.T) {
	path := writeConfig(t, `
connections:
  - name: literal
    type: postgres
    host: h
    password: hunter2
  - name: opref
    type: postgres
    host: h
    password: op://Infra/pg/password
  - name: envref
    type: postgres
    host: h
    password: ${PGPASS}
`)

	_, warns, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(warns) != 1 {
		t.Fatalf("got %d warnings, want 1: %v", len(warns), warns)
	}
	if warns[0].Connection != "literal" {
		t.Errorf("warning names %q, want %q", warns[0].Connection, "literal")
	}
}

func TestLoadRejectsInvalidConfigs(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "unknown engine",
			body: "connections:\n  - name: x\n    type: mysql\n    host: h\n",
			want: "unknown type",
		},
		{
			name: "missing name",
			body: "connections:\n  - type: postgres\n    host: h\n",
			want: "missing name",
		},
		{
			name: "missing host",
			body: "connections:\n  - name: x\n    type: postgres\n",
			want: "missing host",
		},
		{
			name: "duplicate name",
			body: "connections:\n  - name: x\n    type: postgres\n    host: h\n  - name: x\n    type: redis\n    host: h\n",
			want: "duplicate connection name",
		},
		{
			name: "ssh without bastion",
			body: "connections:\n  - name: x\n    type: postgres\n    host: h\n    ssh:\n      user: deploy\n",
			want: "missing bastion",
		},
		{
			name: "unknown field",
			body: "connections:\n  - name: x\n    type: postgres\n    host: h\n    passwrod: oops\n",
			want: "field passwrod not found",
		},
		{
			name: "port above the uint16 range",
			body: "connections:\n  - name: x\n    type: postgres\n    host: h\n    port: 70000\n",
			want: "port 70000 is out of range",
		},
		{
			name: "negative port",
			body: "connections:\n  - name: x\n    type: postgres\n    host: h\n    port: -1\n",
			want: "port -1 is out of range",
		},
		{
			name: "ssh port out of range",
			body: "connections:\n  - name: x\n    type: postgres\n    host: h\n    ssh:\n      bastion: b\n      port: 99999\n",
			want: "ssh port 99999 is out of range",
		},
		{
			name: "unknown tls mode",
			body: "connections:\n  - name: cloud\n    type: postgres\n    host: h\n    tls:\n      mode: yes-please\n",
			want: `connection "cloud": unknown tls mode "yes-please" (want disable, require, verify-ca, verify-full)`,
		},
		{
			name: "unknown tls field",
			body: "connections:\n  - name: x\n    type: postgres\n    host: h\n    tls:\n      verify: full\n",
			want: "field verify not found",
		},
		{
			name: "client certificate without a key",
			body: "connections:\n  - name: x\n    type: postgres\n    host: h\n    tls:\n      cert: /tmp/c.pem\n",
			want: "tls cert and key must be set together",
		},
		{
			name: "client key without a certificate",
			body: "connections:\n  - name: x\n    type: postgres\n    host: h\n    tls:\n      key: /tmp/k.pem\n",
			want: "tls cert and key must be set together",
		},
		{
			name: "sentinel without a master name",
			body: "connections:\n  - name: sent\n    type: redis\n    host: h\n    mode: sentinel\n",
			want: `connection "sent": mode sentinel requires master_name`,
		},
		{
			name: "unknown redis mode",
			body: "connections:\n  - name: cache\n    type: redis\n    host: h\n    mode: replica\n",
			want: `connection "cache": unknown mode "replica" (want standalone, cluster, sentinel)`,
		},
		{
			name: "unknown clickhouse protocol",
			body: "connections:\n  - name: ch\n    type: clickhouse\n    host: h\n    protocol: grpc\n",
			want: `connection "ch": unknown protocol "grpc" (want native, http)`,
		},
		{
			name: "protocol on a redis connection",
			body: "connections:\n  - name: cache\n    type: redis\n    host: h\n    protocol: http\n",
			want: "protocol applies to clickhouse connections, not redis",
		},
		{
			name: "mode on a postgres connection",
			body: "connections:\n  - name: pg\n    type: postgres\n    host: h\n    mode: cluster\n",
			want: "mode applies to redis connections, not postgres",
		},
		{
			name: "addresses on a clickhouse connection",
			body: "connections:\n  - name: ch\n    type: clickhouse\n    host: h\n    addresses:\n      - other:9000\n",
			want: "addresses applies to redis connections, not clickhouse",
		},
		{
			name: "master_name outside sentinel mode",
			body: "connections:\n  - name: cache\n    type: redis\n    host: h\n    mode: cluster\n    master_name: mymaster\n",
			want: "master_name applies to mode sentinel, not cluster",
		},
		{
			name: "addresses on a standalone redis",
			body: "connections:\n  - name: cache\n    type: redis\n    host: h\n    addresses:\n      - other:6379\n",
			want: "addresses applies to mode cluster or sentinel, not standalone",
		},
		{
			name: "cluster with a database other than 0",
			body: "connections:\n  - name: cache\n    type: redis\n    host: h\n    mode: cluster\n    database: \"3\"\n",
			want: `mode cluster has only database 0, not "3"`,
		},
		{
			name: "address without a port",
			body: "connections:\n  - name: cache\n    type: redis\n    host: h\n    mode: cluster\n    addresses:\n      - other\n",
			want: `address "other" is not host:port`,
		},
		{
			name: "address with a non-numeric port",
			body: "connections:\n  - name: cache\n    type: redis\n    host: h\n    mode: cluster\n    addresses:\n      - other:redis\n",
			want: `address "other:redis" has a non-numeric port`,
		},
		{
			name: "address port out of range",
			body: "connections:\n  - name: cache\n    type: redis\n    host: h\n    mode: cluster\n    addresses:\n      - other:70000\n",
			want: "address other:70000 port 70000 is out of range",
		},
		{
			name: "unix socket behind a bastion",
			body: "connections:\n  - name: sock\n    type: postgres\n    host: /var/run/postgresql\n    ssh:\n      bastion: b\n",
			want: "the unix socket host /var/run/postgresql cannot be reached through an ssh bastion",
		},
		{
			name: "unix socket asked to verify tls",
			body: "connections:\n  - name: sock\n    type: postgres\n    host: /var/run/postgresql\n    tls:\n      mode: verify-full\n",
			want: `tls mode "verify-full" cannot be used with the unix socket host /var/run/postgresql`,
		},
		{
			name: "unix socket on a non-postgres engine",
			body: "connections:\n  - name: sock\n    type: redis\n    host: /var/run/redis.sock\n",
			want: "only postgres connects over a unix socket, not redis",
		},
		{
			name: "connect_timeout written as a bare number",
			body: "connections:\n  - name: pg\n    type: postgres\n    host: h\n    connect_timeout: 30\n",
			want: "cannot unmarshal !!int `30` into time.Duration",
		},
		{
			name: "connect_timeout too short to reach anything",
			body: "connections:\n  - name: pg\n    type: postgres\n    host: h\n    connect_timeout: 500us\n",
			want: "connect_timeout 500µs is not a usable duration (write it like 10s)",
		},
		{
			name: "negative connect_timeout",
			body: "connections:\n  - name: pg\n    type: postgres\n    host: h\n    connect_timeout: -5s\n",
			want: "connect_timeout -5s is not a usable duration",
		},
		{
			name: "malformed yaml",
			body: "connections: [\n",
			want: "parsing config",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := Load(writeConfig(t, tt.body))
			if err == nil {
				t.Fatalf("Load succeeded, want an error mentioning %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want it to mention %q", err, tt.want)
			}
		})
	}
}

func TestSecretReferenceDetection(t *testing.T) {
	tests := []struct {
		value string
		op    bool
		env   bool
	}{
		{value: "op://Infra/pg/password", op: true},
		{value: "${PGPASS}", env: true},
		{value: "hunter2"},
		{value: ""},
		{value: "${}"},
		{value: "op:/Infra/pg"},
	}

	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			if got := IsOpRef(tt.value); got != tt.op {
				t.Errorf("IsOpRef(%q) = %v, want %v", tt.value, got, tt.op)
			}
			if got := IsEnvRef(tt.value); got != tt.env {
				t.Errorf("IsEnvRef(%q) = %v, want %v", tt.value, got, tt.env)
			}
		})
	}
}

func TestDefaultPathHonorsEnvOverride(t *testing.T) {
	t.Setenv("D9S_CONFIG", "/custom/d9s.yaml")
	if got := DefaultPath(); got != "/custom/d9s.yaml" {
		t.Errorf("DefaultPath() = %q, want the D9S_CONFIG value", got)
	}

	t.Setenv("D9S_CONFIG", "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home directory available: %v", err)
	}
	want := filepath.Join(home, ".config", "d9s", "config.yaml")
	if got := DefaultPath(); got != want {
		t.Errorf("DefaultPath() = %q, want %q", got, want)
	}
}

func TestSampleIsValid(t *testing.T) {
	cfg, _, err := Load(writeConfig(t, Sample))
	if err != nil {
		t.Fatalf("the sample config does not load: %v", err)
	}
	if len(cfg.Connections) != 4 {
		t.Errorf("sample has %d connections, want 4", len(cfg.Connections))
	}
	// The sample doubles as documentation of the connectivity fields, so the
	// defaults it demonstrates have to be the ones the loader applies.
	if got := cfg.Connections[1].Port; got != clickHouseHTTPPort {
		t.Errorf("the http clickhouse sample got port %d, want %d", got, clickHouseHTTPPort)
	}
	if got := cfg.Connections[3].EffectiveTLSMode(); got != TLSDisable {
		t.Errorf("the unix socket sample got tls mode %q, want %q", got, TLSDisable)
	}
}
