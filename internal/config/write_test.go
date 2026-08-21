package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// richConfig exercises every block the writer has to preserve: head comments,
// aligned inline comments, blank lines between entries, ssh and tls blocks,
// and the per-engine fields.
const richConfig = `# d9s connections
# Checked into dotfiles; keep the comments.

connections:
  # production, read replica only
  - name: prod-pg
    type: postgres
    host: 10.0.1.5
    user: app
    password: op://Infra/prod-pg/password   # 1Password reference
    database: app
    ssh:
      bastion: bastion.corp.com
      user: deploy          # key comes from the 1Password SSH agent
      port: 2222

  - name: analytics-ch
    type: clickhouse
    host: ch.internal
    port: 8123
    protocol: http          # native (9000) | http (8123)
    user: default
    password: ${CH_PASSWORD}
    connect_timeout: 30s
    tls:
      mode: verify-full     # disable | require | verify-ca | verify-full
      ca: /etc/ssl/certs/ch-root.pem
      server_name: ch.public

  # scratch cache, safe to write to
  - name: cache-redis
    type: redis
    host: 10.0.2.1
    mode: cluster
    addresses:
      - 10.0.2.2:6379
      - 10.0.2.3:6379
    allow_write: true

  - name: local-pg
    type: postgres
    host: /var/run/postgresql
    user: postgres
`

// writeConfig puts body in a temporary file and returns its path.
func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}

// entryOf returns the lines of the named connection, from its "- name:" line
// through the last line indented under it.
func entryOf(t *testing.T, body, name string) string {
	t.Helper()
	lines := strings.Split(body, "\n")
	start := -1
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "- name: "+name) {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatalf("connection %q is not in:\n%s", name, body)
	}
	dash := indentOf(lines[start])
	end := start
	for i := start + 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "" {
			continue
		}
		if indentOf(lines[i]) <= dash {
			break
		}
		end = i
	}
	return strings.Join(lines[start:end+1], "\n")
}

// connByName returns the named connection out of a loaded config.
func connByName(t *testing.T, conns []Connection, name string) Connection {
	t.Helper()
	for _, c := range conns {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("connection %q not loaded", name)
	return Connection{}
}

// TestUpdateLeavesEverythingElseByteIdentical is the core promise: editing one
// connection may not disturb a single byte of the rest of the file.
func TestUpdateLeavesEverythingElseByteIdentical(t *testing.T) {
	untouched := []string{"prod-pg", "cache-redis", "local-pg"}

	path := writeConfig(t, richConfig)
	conns, _, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	edited := connByName(t, conns.Connections, "analytics-ch")
	edited.Host = "ch-new.internal"

	if err := UpdateConnection(path, "analytics-ch", edited); err != nil {
		t.Fatalf("UpdateConnection() error = %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	got := string(after)

	for _, name := range untouched {
		if before, now := entryOf(t, richConfig, name), entryOf(t, got, name); before != now {
			t.Errorf("connection %q changed:\n--- before ---\n%s\n--- after ---\n%s", name, before, now)
		}
	}
	for _, comment := range []string{
		"# d9s connections",
		"# Checked into dotfiles; keep the comments.",
		"  # production, read replica only",
		"  # scratch cache, safe to write to",
	} {
		if !strings.Contains(got, comment) {
			t.Errorf("comment %q did not survive:\n%s", comment, got)
		}
	}
	if !strings.Contains(got, "host: ch-new.internal") {
		t.Errorf("the edit was not applied:\n%s", got)
	}
	if strings.Contains(got, "host: ch.internal") {
		t.Errorf("the old host is still there:\n%s", got)
	}
}

// TestUpdateTouchesOnlyTheChangedKey checks the merge is surgical inside the
// edited entry too: its other keys, and their inline comments, stay put.
func TestUpdateTouchesOnlyTheChangedKey(t *testing.T) {
	path := writeConfig(t, richConfig)
	conns, _, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	edited := connByName(t, conns.Connections, "analytics-ch")
	edited.Host = "ch-new.internal"
	if err := UpdateConnection(path, "analytics-ch", edited); err != nil {
		t.Fatalf("UpdateConnection() error = %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	entry := entryOf(t, string(after), "analytics-ch")

	for _, want := range []string{
		"protocol: http",
		"# native (9000) | http (8123)",
		"connect_timeout: 30s",
		"mode: verify-full",
		"# disable | require | verify-ca | verify-full",
		"ca: /etc/ssl/certs/ch-root.pem",
		"server_name: ch.public",
		"password: ${CH_PASSWORD}",
	} {
		if !strings.Contains(entry, want) {
			t.Errorf("edited entry lost %q:\n%s", want, entry)
		}
	}
	// The port was already 8123 in the file and did not change, so it must not
	// have been rewritten or duplicated.
	if n := strings.Count(entry, "port:"); n != 1 {
		t.Errorf("port appears %d times, want 1:\n%s", n, entry)
	}
}

// TestUpdateDoesNotMaterializeDefaults guards the annoyance where editing one
// field makes the loader's inferred defaults appear in the user's file.
func TestUpdateDoesNotMaterializeDefaults(t *testing.T) {
	const src = `connections:
  - name: prod-pg
    type: postgres
    host: 10.0.1.5
    user: app
    ssh:
      bastion: bastion.corp.com
      user: deploy
`
	path := writeConfig(t, src)
	cfg, _, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	// Load fills these in; writing them back would edit lines the user never
	// touched.
	edited := cfg.Connections[0]
	if edited.Port != 5432 || edited.SSH.Port != 22 {
		t.Fatalf("Load() did not fill the defaults: port=%d ssh.port=%d", edited.Port, edited.SSH.Port)
	}
	edited.User = "readonly"

	if err := UpdateConnection(path, "prod-pg", edited); err != nil {
		t.Fatalf("UpdateConnection() error = %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	got := string(after)
	if strings.Contains(got, "port: 5432") {
		t.Errorf("the default port was written into the file:\n%s", got)
	}
	if strings.Contains(got, "port: 22") {
		t.Errorf("the default ssh port was written into the file:\n%s", got)
	}
	if !strings.Contains(got, "user: readonly") {
		t.Errorf("the edit was not applied:\n%s", got)
	}
}

// TestRoundTripPreservesEveryField is the one the lead called out: a file with
// every block must survive a write with all of its values intact.
func TestRoundTripPreservesEveryField(t *testing.T) {
	path := writeConfig(t, richConfig)
	before, _, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	// Touch one connection so the file is genuinely rewritten.
	edited := connByName(t, before.Connections, "prod-pg")
	edited.Database = "app2"
	if err := UpdateConnection(path, "prod-pg", edited); err != nil {
		t.Fatalf("UpdateConnection() error = %v", err)
	}

	after, _, err := Load(path)
	if err != nil {
		t.Fatalf("Load() after write error = %v", err)
	}
	if len(after.Connections) != len(before.Connections) {
		t.Fatalf("connection count changed: %d -> %d", len(before.Connections), len(after.Connections))
	}
	for i := range before.Connections {
		want := before.Connections[i]
		if want.Name == "prod-pg" {
			want.Database = "app2"
		}
		if !reflect.DeepEqual(after.Connections[i], want) {
			t.Errorf("connection %d changed:\n got %#v\nwant %#v", i, after.Connections[i], want)
		}
	}

	// Spot-check the fields a naive writer drops.
	ch := connByName(t, after.Connections, "analytics-ch")
	switch {
	case ch.Protocol != ProtocolHTTP:
		t.Errorf("protocol = %q, want http", ch.Protocol)
	case ch.ConnectTimeout != 30*time.Second:
		t.Errorf("connect_timeout = %v, want 30s", ch.ConnectTimeout)
	case ch.TLS == nil || ch.TLS.Mode != TLSVerifyFull:
		t.Errorf("tls = %#v, want mode verify-full", ch.TLS)
	case ch.TLS.ServerName != "ch.public":
		t.Errorf("tls.server_name = %q, want ch.public", ch.TLS.ServerName)
	}
	redis := connByName(t, after.Connections, "cache-redis")
	switch {
	case redis.Mode != RedisCluster:
		t.Errorf("mode = %q, want cluster", redis.Mode)
	case !reflect.DeepEqual(redis.Addresses, []string{"10.0.2.2:6379", "10.0.2.3:6379"}):
		t.Errorf("addresses = %v", redis.Addresses)
	case !redis.AllowWrite:
		t.Error("allow_write was lost")
	}
	pg := connByName(t, after.Connections, "prod-pg")
	if pg.SSH == nil || pg.SSH.Bastion != "bastion.corp.com" || pg.SSH.Port != 2222 {
		t.Errorf("ssh = %#v, want bastion.corp.com port 2222", pg.SSH)
	}
}

// TestWriteThenLoadIsStable checks a second write changes nothing, so repeated
// saves do not slowly reformat the file.
func TestWriteThenLoadIsStable(t *testing.T) {
	path := writeConfig(t, richConfig)
	cfg, _, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	edited := connByName(t, cfg.Connections, "prod-pg")
	edited.User = "readonly"
	if err := UpdateConnection(path, "prod-pg", edited); err != nil {
		t.Fatalf("first UpdateConnection() error = %v", err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if err := UpdateConnection(path, "prod-pg", edited); err != nil {
		t.Fatalf("second UpdateConnection() error = %v", err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(first) != string(second) {
		t.Errorf("a repeated save changed the file:\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
}

func TestAdd(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		conn     Connection
		wantErr  error
		contains []string
	}{
		{
			name: "into an existing list",
			src:  richConfig,
			conn: Connection{Name: "new-pg", Type: Postgres, Host: "db.new", User: "app",
				Password: "op://Infra/new/password"},
			contains: []string{"- name: new-pg", "type: postgres", "host: db.new",
				"password: op://Infra/new/password"},
		},
		{
			name: "with every block",
			src:  "connections: []\n",
			conn: Connection{
				Name: "full", Type: Redis, Host: "r1.internal", Port: 6380,
				Mode: RedisSentinel, MasterName: "mymaster",
				Addresses:      []string{"r2.internal:26379"},
				ConnectTimeout: 45 * time.Second,
				AllowWrite:     true,
				SSH:            &SSH{Bastion: "bastion.corp.com", User: "deploy", Port: 2222},
				TLS:            &TLS{Mode: TLSVerifyCA, CA: "/etc/ssl/ca.pem"},
			},
			contains: []string{"master_name: mymaster", "connect_timeout: 45s",
				"allow_write: true", "bastion: bastion.corp.com", "mode: verify-ca",
				"- r2.internal:26379"},
		},
		{
			name:    "duplicate name",
			src:     richConfig,
			conn:    Connection{Name: "prod-pg", Type: Postgres, Host: "other"},
			wantErr: ErrExists,
		},
		{
			name:    "invalid connection is refused",
			src:     richConfig,
			conn:    Connection{Name: "broken", Type: "mysql", Host: "db"},
			wantErr: nil, // a plain validation error, not a sentinel
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeConfig(t, tt.src)
			err := AddConnection(path, tt.conn)

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("AddConnection() error = %v, want one wrapping %v", err, tt.wantErr)
				}
				return
			}
			if tt.name == "invalid connection is refused" {
				if err == nil {
					t.Fatal("AddConnection() error = nil, want a validation error")
				}
				return
			}
			if err != nil {
				t.Fatalf("AddConnection() error = %v", err)
			}

			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("ReadFile() error = %v", err)
			}
			for _, want := range tt.contains {
				if !strings.Contains(string(raw), want) {
					t.Errorf("written file is missing %q:\n%s", want, raw)
				}
			}
			cfg, _, err := Load(path)
			if err != nil {
				t.Fatalf("Load() after add error = %v:\n%s", err, raw)
			}
			if indexByName(cfg.Connections, tt.conn.Name) < 0 {
				t.Errorf("connection %q not loadable after add:\n%s", tt.conn.Name, raw)
			}
		})
	}
}

// TestAddPreservesTheRestOfTheFile checks appending does not disturb what is
// already there.
func TestAddPreservesTheRestOfTheFile(t *testing.T) {
	path := writeConfig(t, richConfig)
	conn := Connection{Name: "new-pg", Type: Postgres, Host: "db.new", User: "app"}
	if err := AddConnection(path, conn); err != nil {
		t.Fatalf("AddConnection() error = %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	got := string(raw)
	if !strings.HasPrefix(got, richConfig[:strings.Index(richConfig, "  - name: local-pg")]) {
		t.Errorf("the file above the new entry changed:\n%s", got)
	}
	for _, name := range []string{"prod-pg", "analytics-ch", "cache-redis", "local-pg"} {
		if before, now := entryOf(t, richConfig, name), entryOf(t, got, name); before != now {
			t.Errorf("connection %q changed:\n--- before ---\n%s\n--- after ---\n%s", name, before, now)
		}
	}
}

func TestAddToMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.yaml")
	conn := Connection{Name: "first", Type: Postgres, Host: "db.local", User: "app"}
	if err := AddConnection(path, conn); err != nil {
		t.Fatalf("AddConnection() error = %v", err)
	}
	cfg, _, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(cfg.Connections) != 1 || cfg.Connections[0].Name != "first" {
		t.Errorf("loaded %#v, want one connection named first", cfg.Connections)
	}
}

func TestUpdate(t *testing.T) {
	tests := []struct {
		name    string
		target  string
		mutate  func(c *Connection)
		wantErr error
		check   func(t *testing.T, body string)
	}{
		{
			name:   "change a scalar",
			target: "prod-pg",
			mutate: func(c *Connection) { c.Host = "10.0.1.9" },
			check: func(t *testing.T, body string) {
				if !strings.Contains(body, "host: 10.0.1.9") {
					t.Errorf("host not updated:\n%s", body)
				}
			},
		},
		{
			name:   "clear an optional field",
			target: "prod-pg",
			mutate: func(c *Connection) { c.Database = "" },
			check: func(t *testing.T, body string) {
				if strings.Contains(entryOf(t, body, "prod-pg"), "database:") {
					t.Errorf("database key was not removed:\n%s", body)
				}
			},
		},
		{
			name:   "rename",
			target: "prod-pg",
			mutate: func(c *Connection) { c.Name = "prod-primary" },
			check: func(t *testing.T, body string) {
				if !strings.Contains(body, "- name: prod-primary") {
					t.Errorf("rename not applied:\n%s", body)
				}
				if strings.Contains(body, "- name: prod-pg") {
					t.Errorf("old name still present:\n%s", body)
				}
			},
		},
		{
			name:   "remove a whole block",
			target: "prod-pg",
			mutate: func(c *Connection) { c.SSH = nil },
			check: func(t *testing.T, body string) {
				entry := entryOf(t, body, "prod-pg")
				if strings.Contains(entry, "ssh:") || strings.Contains(entry, "bastion:") {
					t.Errorf("ssh block was not removed:\n%s", entry)
				}
			},
		},
		{
			name:   "add a block",
			target: "local-pg",
			mutate: func(c *Connection) { c.TLS = &TLS{Mode: TLSDisable} },
			check: func(t *testing.T, body string) {
				entry := entryOf(t, body, "local-pg")
				if !strings.Contains(entry, "tls:") || !strings.Contains(entry, "mode: disable") {
					t.Errorf("tls block was not added:\n%s", entry)
				}
			},
		},
		{
			name:   "change a nested field keeps its siblings",
			target: "prod-pg",
			mutate: func(c *Connection) { c.SSH.User = "ops" },
			check: func(t *testing.T, body string) {
				entry := entryOf(t, body, "prod-pg")
				for _, want := range []string{"bastion: bastion.corp.com", "user: ops", "port: 2222"} {
					if !strings.Contains(entry, want) {
						t.Errorf("ssh block lost %q:\n%s", want, entry)
					}
				}
			},
		},
		{
			name:   "change a list",
			target: "cache-redis",
			mutate: func(c *Connection) { c.Addresses = []string{"10.0.2.9:6379"} },
			check: func(t *testing.T, body string) {
				entry := entryOf(t, body, "cache-redis")
				if !strings.Contains(entry, "- 10.0.2.9:6379") {
					t.Errorf("addresses not updated:\n%s", entry)
				}
				if strings.Contains(entry, "10.0.2.2") {
					t.Errorf("old address still present:\n%s", entry)
				}
			},
		},
		{
			name:    "unknown connection",
			target:  "nope",
			mutate:  func(c *Connection) {},
			wantErr: ErrNotFound,
		},
		{
			name:    "rename onto a taken name",
			target:  "prod-pg",
			mutate:  func(c *Connection) { c.Name = "cache-redis" },
			wantErr: ErrExists,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeConfig(t, richConfig)
			cfg, _, err := Load(path)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}

			conn := Connection{Name: tt.target}
			if i := indexByName(cfg.Connections, tt.target); i >= 0 {
				conn = cfg.Connections[i]
			}
			tt.mutate(&conn)

			err = UpdateConnection(path, tt.target, conn)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("UpdateConnection() error = %v, want one wrapping %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("UpdateConnection() error = %v", err)
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("ReadFile() error = %v", err)
			}
			if _, _, err := Load(path); err != nil {
				t.Fatalf("Load() after update error = %v:\n%s", err, raw)
			}
			tt.check(t, string(raw))
		})
	}
}

func TestRemove(t *testing.T) {
	tests := []struct {
		name    string
		target  string
		wantErr error
		gone    []string
	}{
		{
			name:   "middle entry",
			target: "analytics-ch",
			gone:   []string{"analytics-ch", "ch.internal", "verify-full", "ch-root.pem"},
		},
		{
			name:   "entry with a head comment",
			target: "cache-redis",
			gone:   []string{"cache-redis", "# scratch cache, safe to write to", "10.0.2.2:6379"},
		},
		{
			name:   "first entry",
			target: "prod-pg",
			gone:   []string{"prod-pg", "# production, read replica only", "bastion.corp.com"},
		},
		{
			name:   "last entry",
			target: "local-pg",
			gone:   []string{"local-pg", "/var/run/postgresql"},
		},
		{
			name:    "unknown entry",
			target:  "nope",
			wantErr: ErrNotFound,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeConfig(t, richConfig)
			err := RemoveConnection(path, tt.target)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("RemoveConnection() error = %v, want one wrapping %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("RemoveConnection() error = %v", err)
			}

			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("ReadFile() error = %v", err)
			}
			got := string(raw)
			for _, want := range tt.gone {
				if strings.Contains(got, want) {
					t.Errorf("%q survived the removal:\n%s", want, got)
				}
			}
			cfg, _, err := Load(path)
			if err != nil {
				t.Fatalf("Load() after remove error = %v:\n%s", err, got)
			}
			if indexByName(cfg.Connections, tt.target) >= 0 {
				t.Errorf("connection %q is still loadable", tt.target)
			}
			if len(cfg.Connections) != 3 {
				t.Errorf("got %d connections, want 3", len(cfg.Connections))
			}
			if strings.Contains(got, "\n\n\n") {
				t.Errorf("removal left a double blank line:\n%q", got)
			}
		})
	}
}

// TestRemoveKeepsTheOtherEntriesByteIdentical checks a deletion is as surgical
// as an edit.
func TestRemoveKeepsTheOtherEntriesByteIdentical(t *testing.T) {
	path := writeConfig(t, richConfig)
	if err := RemoveConnection(path, "analytics-ch"); err != nil {
		t.Fatalf("RemoveConnection() error = %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	got := string(raw)
	for _, name := range []string{"prod-pg", "cache-redis", "local-pg"} {
		if before, now := entryOf(t, richConfig, name), entryOf(t, got, name); before != now {
			t.Errorf("connection %q changed:\n--- before ---\n%s\n--- after ---\n%s", name, before, now)
		}
	}
}

// TestWriteIsAtomicOnFailure makes the write fail once the file already has
// contents and checks the old one survives with no temp file left over.
func TestWriteIsAtomicOnFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root, directory permissions are not enforced")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(richConfig), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	f, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if err := f.Remove("analytics-ch"); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}

	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("Chmod() error = %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	if err := f.Write(); err == nil {
		t.Fatal("Write() into a read-only directory succeeded, want an error")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(after) != richConfig {
		t.Errorf("the failed write changed the file:\n%s", after)
	}

	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("Chmod() error = %v", err)
	}
	left, err := filepath.Glob(filepath.Join(dir, "*"))
	if err != nil {
		t.Fatalf("Glob() error = %v", err)
	}
	if len(left) != 1 || left[0] != path {
		t.Errorf("directory holds %v, want only %s", left, path)
	}
}

func TestWritePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.yaml")
	conn := Connection{Name: "first", Type: Postgres, Host: "db.local", User: "app"}
	if err := AddConnection(path, conn); err != nil {
		t.Fatalf("AddConnection() error = %v", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Errorf("file mode = %04o, want %04o", got, 0o600)
	}
	di, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("Stat(dir) error = %v", err)
	}
	if got := di.Mode().Perm(); got != 0o700 {
		t.Errorf("directory mode = %04o, want %04o", got, 0o700)
	}
}

// TestWriteRefusesToProduceAnUnloadableFile is the guarantee that the writer's
// validation matches the loader's.
func TestWriteRefusesToProduceAnUnloadableFile(t *testing.T) {
	tests := []struct {
		name string
		conn Connection
	}{
		{"unknown engine", Connection{Name: "x", Type: "mysql", Host: "db"}},
		{"missing host", Connection{Name: "x", Type: Postgres}},
		{"port out of range", Connection{Name: "x", Type: Postgres, Host: "db", Port: 70000}},
		{"protocol on the wrong engine", Connection{Name: "x", Type: Postgres, Host: "db", Protocol: ProtocolHTTP}},
		{"sentinel without a master", Connection{Name: "x", Type: Redis, Host: "r", Mode: RedisSentinel}},
		{"ssh without a bastion", Connection{Name: "x", Type: Postgres, Host: "db", SSH: &SSH{User: "deploy"}}},
		{"tls cert without a key", Connection{Name: "x", Type: Postgres, Host: "db", TLS: &TLS{Cert: "/c.pem"}}},
		{"ssh to a unix socket", Connection{Name: "x", Type: Postgres, Host: "/var/run/postgresql",
			SSH: &SSH{Bastion: "b.corp"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeConfig(t, richConfig)
			if err := AddConnection(path, tt.conn); err == nil {
				t.Fatal("AddConnection() error = nil, want a validation error")
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("ReadFile() error = %v", err)
			}
			if string(after) != richConfig {
				t.Errorf("a rejected add changed the file:\n%s", after)
			}
		})
	}
}

func TestOpenRejectsABrokenFile(t *testing.T) {
	path := writeConfig(t, "connections:\n  - name: one\n   type: postgres\n")
	if _, err := Open(path); err == nil {
		t.Fatal("Open() error = nil, want a parse error")
	}
}

// TestIndentIsCopiedFromTheFile checks a four-space file stays four-space.
func TestIndentIsCopiedFromTheFile(t *testing.T) {
	const src = `connections:
    - name: prod-pg
      type: postgres
      host: 10.0.1.5
      ssh:
          bastion: bastion.corp.com
          user: deploy
`
	path := writeConfig(t, src)
	conn := Connection{Name: "new-pg", Type: Postgres, Host: "db.new",
		SSH: &SSH{Bastion: "b.corp", User: "deploy"}}
	if err := AddConnection(path, conn); err != nil {
		t.Fatalf("AddConnection() error = %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	got := string(raw)
	if !strings.Contains(got, "\n    - name: new-pg") {
		t.Errorf("the new item does not line up with the existing ones:\n%s", got)
	}
	if !strings.Contains(got, "\n          bastion: b.corp") {
		t.Errorf("the nested block does not use the file's indent:\n%s", got)
	}
	if before, now := entryOf(t, src, "prod-pg"), entryOf(t, got, "prod-pg"); before != now {
		t.Errorf("the existing entry changed:\n--- before ---\n%s\n--- after ---\n%s", before, now)
	}
}

// TestFileEditsAccumulate checks several changes can be staged before one save.
func TestFileEditsAccumulate(t *testing.T) {
	path := writeConfig(t, richConfig)
	f, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if err := f.Remove("analytics-ch"); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	if err := f.Add(Connection{Name: "new-pg", Type: Postgres, Host: "db.new"}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}

	// Nothing is on disk until Write.
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(before) != richConfig {
		t.Errorf("the file changed before Write:\n%s", before)
	}

	if err := f.Write(); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	cfg, _, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if indexByName(cfg.Connections, "analytics-ch") >= 0 {
		t.Error("analytics-ch survived")
	}
	if indexByName(cfg.Connections, "new-pg") < 0 {
		t.Error("new-pg was not added")
	}
}

// TestRejectedEditLeavesTheFileObjectUnchanged checks a refused mutation does
// not half-apply.
func TestRejectedEditLeavesTheFileObjectUnchanged(t *testing.T) {
	path := writeConfig(t, richConfig)
	f, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	before := string(f.Bytes())

	if err := f.Add(Connection{Name: "prod-pg", Type: Postgres, Host: "x"}); !errors.Is(err, ErrExists) {
		t.Fatalf("Add() error = %v, want one wrapping ErrExists", err)
	}
	if err := f.Update("nope", Connection{Name: "nope", Type: Postgres, Host: "x"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Update() error = %v, want one wrapping ErrNotFound", err)
	}
	if got := string(f.Bytes()); got != before {
		t.Errorf("a rejected edit changed the document:\n%s", got)
	}
}

func TestConnections(t *testing.T) {
	path := writeConfig(t, richConfig)
	f, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	conns, warns, err := f.Connections()
	if err != nil {
		t.Fatalf("Connections() error = %v", err)
	}
	if len(conns) != 4 {
		t.Errorf("got %d connections, want 4", len(conns))
	}
	if len(warns) != 0 {
		t.Errorf("got warnings %v, want none: every password here is a reference", warns)
	}
	if f.Path() != path {
		t.Errorf("Path() = %q, want %q", f.Path(), path)
	}
}

// TestPlaintextPasswordStillWarns checks the writer does not quietly change
// how a literal password is reported.
func TestPlaintextPasswordStillWarns(t *testing.T) {
	path := writeConfig(t, richConfig)
	conn := Connection{Name: "sloppy", Type: Postgres, Host: "db", Password: "hunter2"}
	if err := AddConnection(path, conn); err != nil {
		t.Fatalf("AddConnection() error = %v", err)
	}
	_, warns, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(warns) != 1 || warns[0].Connection != "sloppy" {
		t.Errorf("warnings = %#v, want one about sloppy", warns)
	}
}
