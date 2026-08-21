package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/andreim/d9s/internal/config"
	"github.com/andreim/d9s/internal/secrets"
)

// typeText feeds a string in one rune at a time.
func typeText(m *model, s string) {
	for _, r := range s {
		m.updateConnections(key(string(r)))
	}
}

// testModel returns a model over a temporary config file holding body.
func testModel(t *testing.T, body string) *model {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if body != "" {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
	}
	cfg, warns, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	conns := make([]*connState, len(cfg.Connections))
	for i, c := range cfg.Connections {
		conns[i] = &connState{cfg: c}
	}
	return &model{
		cfg: cfg, warns: warns, cfgPath: path,
		width: 100, height: 40,
		resolver:   secrets.NewResolver(),
		spinner:    spinner.New(),
		conns:      conns,
		activeConn: -1,
	}
}

const twoConnections = `# keep me
connections:
  - name: prod-pg
    type: postgres
    host: 10.0.1.5
    user: app
    password: op://Infra/prod-pg/password
    ssh:
      bastion: bastion.corp.com
      user: deploy
    tls:
      mode: verify-full
      ca: /etc/ssl/ca.pem

  - name: cache-redis
    type: redis
    host: 10.0.2.1
    mode: cluster
    addresses:
      - 10.0.2.2:6379
    allow_write: true
`

func TestFormOpensFromTheList(t *testing.T) {
	tests := []struct {
		name     string
		press    string
		wantForm bool
		wantMode formMode
		wantName string
		confirm  bool
	}{
		{name: "a adds", press: "a", wantForm: true, wantMode: formAdd},
		{name: "e edits the selection", press: "e", wantForm: true, wantMode: formEdit, wantName: "prod-pg"},
		{name: "d asks before deleting", press: "d", confirm: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := testModel(t, twoConnections)
			m.updateConnections(key(tt.press))
			if m.editor == nil {
				t.Fatal("no editor opened")
			}
			if tt.confirm {
				if m.editor.confirm == nil || m.editor.confirm.kind != editorConfirmDelete {
					t.Fatalf("confirm = %#v, want a delete confirmation", m.editor.confirm)
				}
				if m.editor.confirm.name != "prod-pg" {
					t.Errorf("confirm names %q, want prod-pg", m.editor.confirm.name)
				}
				return
			}
			if !tt.wantForm || m.editor.form == nil {
				t.Fatal("no form opened")
			}
			if m.editor.form.mode != tt.wantMode {
				t.Errorf("mode = %v, want %v", m.editor.form.mode, tt.wantMode)
			}
			if m.editor.form.original != tt.wantName {
				t.Errorf("original = %q, want %q", m.editor.form.original, tt.wantName)
			}
		})
	}
}

func TestEditFormLoadsTheConnection(t *testing.T) {
	m := testModel(t, twoConnections)
	m.updateConnections(key("e"))
	f := m.editor.form

	want := map[formFieldID]string{
		fldName:       "prod-pg",
		fldType:       "postgres",
		fldHost:       "10.0.1.5",
		fldPort:       "5432", // filled in by the loader
		fldUser:       "app",
		fldPassword:   "op://Infra/prod-pg/password",
		fldSSHBastion: "bastion.corp.com",
		fldSSHUser:    "deploy",
		fldSSHPort:    "22",
	}
	for id, w := range want {
		if got := f.values[id]; got != w {
			t.Errorf("%s = %q, want %q", formFields[id].label, got, w)
		}
	}
}

func TestFormNavigationAndTyping(t *testing.T) {
	m := testModel(t, twoConnections)
	m.updateConnections(key("a"))
	f := m.editor.form

	typeText(m, "newdb")
	if f.values[fldName] != "newdb" {
		t.Errorf("name = %q, want newdb", f.values[fldName])
	}
	m.updateConnections(key("backspace"))
	if f.values[fldName] != "newd" {
		t.Errorf("after backspace name = %q, want newd", f.values[fldName])
	}

	// j and k are ordinary characters inside a field, not movement.
	typeText(m, "jk")
	if f.values[fldName] != "newdjk" {
		t.Errorf("name = %q, want newdjk: j/k must type, not move", f.values[fldName])
	}
	if f.sel != fldName {
		t.Errorf("selection moved to %v while typing", f.sel)
	}

	m.updateConnections(key("tab"))
	if f.sel != fldType {
		t.Errorf("tab moved to %v, want the engine field", f.sel)
	}
	m.updateConnections(key("shift+tab"))
	if f.sel != fldName {
		t.Errorf("shift+tab moved to %v, want the name field", f.sel)
	}

	// The selection stops at the ends instead of wrapping.
	for i := 0; i < int(fieldCount)+3; i++ {
		m.updateConnections(key("down"))
	}
	if f.sel != fieldCount-1 {
		t.Errorf("selection = %v, want the last field", f.sel)
	}
	for i := 0; i < int(fieldCount)+3; i++ {
		m.updateConnections(key("up"))
	}
	if f.sel != 0 {
		t.Errorf("selection = %v, want the first field", f.sel)
	}
}

func TestEngineFieldCyclesAndRefusesTyping(t *testing.T) {
	m := testModel(t, twoConnections)
	m.updateConnections(key("a"))
	f := m.editor.form
	m.updateConnections(key("tab")) // onto the engine field

	if f.values[fldType] != "postgres" {
		t.Fatalf("engine starts at %q, want postgres", f.values[fldType])
	}
	m.updateConnections(key("right"))
	if f.values[fldType] != "clickhouse" {
		t.Errorf("after right engine = %q, want clickhouse", f.values[fldType])
	}
	m.updateConnections(key("right"))
	m.updateConnections(key("right"))
	if f.values[fldType] != "postgres" {
		t.Errorf("engine = %q, want it to wrap back to postgres", f.values[fldType])
	}
	m.updateConnections(key("left"))
	if f.values[fldType] != "redis" {
		t.Errorf("after left engine = %q, want redis", f.values[fldType])
	}

	typeText(m, "mysql")
	if f.values[fldType] != "redis" {
		t.Errorf("engine = %q, want typing to be ignored on a choice field", f.values[fldType])
	}
}

func TestFormValidation(t *testing.T) {
	tests := []struct {
		name   string
		set    map[formFieldID]string
		reject string // substring of the expected complaint; "" means accepted
	}{
		{
			name: "complete form",
			set:  map[formFieldID]string{fldName: "db", fldHost: "h"},
		},
		{
			name:   "no name",
			set:    map[formFieldID]string{fldHost: "h"},
			reject: "name is required",
		},
		{
			name:   "no host",
			set:    map[formFieldID]string{fldName: "db"},
			reject: "host is required",
		},
		{
			name:   "port not a number",
			set:    map[formFieldID]string{fldName: "db", fldHost: "h", fldPort: "abc"},
			reject: "port must be a number",
		},
		{
			name:   "port too high",
			set:    map[formFieldID]string{fldName: "db", fldHost: "h", fldPort: "70000"},
			reject: "between 1 and 65535",
		},
		{
			name:   "port zero",
			set:    map[formFieldID]string{fldName: "db", fldHost: "h", fldPort: "0"},
			reject: "between 1 and 65535",
		},
		{
			name: "blank port is the engine default",
			set:  map[formFieldID]string{fldName: "db", fldHost: "h", fldPort: ""},
		},
		{
			name:   "ssh port without a bastion",
			set:    map[formFieldID]string{fldName: "db", fldHost: "h", fldSSHPort: "22"},
			reject: "needs a bastion",
		},
		{
			name:   "ssh user without a bastion",
			set:    map[formFieldID]string{fldName: "db", fldHost: "h", fldSSHUser: "deploy"},
			reject: "needs a bastion",
		},
		{
			name: "ssh block",
			set: map[formFieldID]string{fldName: "db", fldHost: "h",
				fldSSHBastion: "b.corp", fldSSHUser: "deploy", fldSSHPort: "2222"},
		},
		{
			name:   "short op reference",
			set:    map[formFieldID]string{fldName: "db", fldHost: "h", fldPassword: "op://Infra/prod-pg"},
			reject: "vault/item/field",
		},
		{
			name:   "op reference with an empty segment",
			set:    map[formFieldID]string{fldName: "db", fldHost: "h", fldPassword: "op://Infra//password"},
			reject: "vault/item/field",
		},
		{
			name: "complete op reference",
			set: map[formFieldID]string{fldName: "db", fldHost: "h",
				fldPassword: "op://Infra/prod-pg/password"},
		},
		{
			name: "env reference",
			set:  map[formFieldID]string{fldName: "db", fldHost: "h", fldPassword: "${PGPASSWORD}"},
		},
		{
			name: "literal password passes validation and is warned about later",
			set:  map[formFieldID]string{fldName: "db", fldHost: "h", fldPassword: "hunter2"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newConnForm()
			for id, v := range tt.set {
				f.values[id] = v
			}
			got := f.validate()
			if tt.reject == "" {
				if got != "" {
					t.Errorf("validate() = %q, want it accepted", got)
				}
				return
			}
			if !strings.Contains(got, tt.reject) {
				t.Errorf("validate() = %q, want it to mention %q", got, tt.reject)
			}
		})
	}
}

func TestFormBuildsTheConnection(t *testing.T) {
	f := newConnForm()
	f.values[fldName] = " db "
	f.values[fldHost] = " 10.0.0.1 "
	f.values[fldPort] = "5433"
	f.values[fldUser] = "app"
	f.values[fldPassword] = "op://Infra/db/password"
	f.values[fldDatabase] = "main"
	f.values[fldSSHBastion] = "b.corp"
	f.values[fldSSHUser] = "deploy"
	f.values[fldSSHPort] = "2222"

	conn, err := f.connection()
	if err != nil {
		t.Fatalf("connection() error = %v", err)
	}
	switch {
	case conn.Name != "db":
		t.Errorf("name = %q, want it trimmed to db", conn.Name)
	case conn.Host != "10.0.0.1":
		t.Errorf("host = %q, want it trimmed", conn.Host)
	case conn.Port != 5433:
		t.Errorf("port = %d, want 5433", conn.Port)
	case conn.Type != config.Postgres:
		t.Errorf("type = %q, want postgres", conn.Type)
	case conn.SSH == nil || conn.SSH.Bastion != "b.corp" || conn.SSH.Port != 2222:
		t.Errorf("ssh = %#v", conn.SSH)
	}

	// Clearing the bastion drops the whole block.
	f.values[fldSSHBastion] = ""
	f.values[fldSSHUser] = ""
	f.values[fldSSHPort] = ""
	conn, err = f.connection()
	if err != nil {
		t.Fatalf("connection() error = %v", err)
	}
	if conn.SSH != nil {
		t.Errorf("ssh = %#v, want nil once the bastion is cleared", conn.SSH)
	}
}

// TestEditKeepsFieldsTheFormDoesNotShow is the correctness point of editing
// through a form with fewer fields than the file has.
func TestEditKeepsFieldsTheFormDoesNotShow(t *testing.T) {
	m := testModel(t, twoConnections)
	m.updateConnections(key("e")) // prod-pg, which has a tls block
	f := m.editor.form
	f.values[fldHost] = "10.0.1.9"

	conn, err := f.connection()
	if err != nil {
		t.Fatalf("connection() error = %v", err)
	}
	if conn.TLS == nil || conn.TLS.Mode != config.TLSVerifyFull || conn.TLS.CA != "/etc/ssl/ca.pem" {
		t.Errorf("tls = %#v, want the file's verify-full block carried through", conn.TLS)
	}

	// The redis entry carries mode, addresses, and allow_write.
	m.closeEditor()
	m.selConn = 1
	m.updateConnections(key("e"))
	conn, err = m.editor.form.connection()
	if err != nil {
		t.Fatalf("connection() error = %v", err)
	}
	switch {
	case conn.Mode != config.RedisCluster:
		t.Errorf("mode = %q, want cluster", conn.Mode)
	case len(conn.Addresses) != 1:
		t.Errorf("addresses = %v, want the file's list", conn.Addresses)
	case !conn.AllowWrite:
		t.Error("allow_write was dropped")
	}
}

func TestSaveWritesTheFile(t *testing.T) {
	m := testModel(t, twoConnections)
	m.updateConnections(key("a"))
	f := m.editor.form
	f.values[fldName] = "new-pg"
	f.values[fldHost] = "db.new"
	f.values[fldUser] = "app"
	f.values[fldPassword] = "op://Infra/new/password"

	cmd := m.saveForm()
	if cmd == nil {
		t.Fatal("saveForm() returned no command")
	}
	if m.editor.busy == "" {
		t.Error("the editor is not marked busy while saving")
	}
	runFormCmd(t, m, cmd)

	if m.editor != nil {
		t.Errorf("the editor stayed open after a save: %#v", m.editor)
	}
	cfg, _, err := config.Load(m.cfgPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(cfg.Connections) != 3 {
		t.Fatalf("got %d connections, want 3", len(cfg.Connections))
	}
	if cfg.Connections[2].Name != "new-pg" {
		t.Errorf("added %q, want new-pg", cfg.Connections[2].Name)
	}
	// The list behind the overlay reflects the write.
	if len(m.conns) != 3 {
		t.Errorf("the connection list has %d entries, want 3", len(m.conns))
	}
	raw, err := os.ReadFile(m.cfgPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(raw), "# keep me") {
		t.Errorf("the file's comment did not survive the save:\n%s", raw)
	}
}

// runFormCmd runs a command the editor returned and feeds every connFormMsg it
// produces back into the model, the way the Bubble Tea loop would.
func runFormCmd(t *testing.T, m *model, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			runFormCmd(t, m, c)
		}
		return
	}
	if fm, ok := msg.(connFormMsg); ok {
		runFormCmd(t, m, m.handleConnFormMsg(fm))
	}
}

func TestSaveRejectsAnInvalidForm(t *testing.T) {
	m := testModel(t, twoConnections)
	m.updateConnections(key("a"))
	m.editor.form.values[fldName] = "x" // no host

	if cmd := m.saveForm(); cmd != nil {
		t.Fatal("saveForm() ran a command for an invalid form")
	}
	if !strings.Contains(m.editor.err, "host is required") {
		t.Errorf("err = %q, want it to name the missing host", m.editor.err)
	}
	if m.editor == nil {
		t.Fatal("the editor closed on a validation failure")
	}
}

// TestDuplicateNameIsReported checks the writer's own rules reach the form,
// since the form cannot see the other entries.
func TestDuplicateNameIsReported(t *testing.T) {
	m := testModel(t, twoConnections)
	m.updateConnections(key("a"))
	f := m.editor.form
	f.values[fldName] = "prod-pg"
	f.values[fldHost] = "db.other"

	runFormCmd(t, m, m.saveForm())

	if m.editor == nil {
		t.Fatal("the editor closed despite the save failing")
	}
	if !strings.Contains(m.editor.err, "already exists") {
		t.Errorf("err = %q, want it to report the duplicate", m.editor.err)
	}
	cfg, _, err := config.Load(m.cfgPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(cfg.Connections) != 2 {
		t.Errorf("got %d connections, want the file untouched at 2", len(cfg.Connections))
	}
}

func TestLiteralPasswordAsksBeforeSaving(t *testing.T) {
	m := testModel(t, twoConnections)
	m.updateConnections(key("a"))
	f := m.editor.form
	f.values[fldName] = "sloppy"
	f.values[fldHost] = "db"
	f.values[fldPassword] = "hunter2"

	if cmd := m.saveForm(); cmd != nil {
		t.Fatal("saveForm() wrote the file without asking about the literal")
	}
	if m.editor.confirm == nil || m.editor.confirm.kind != editorConfirmPlaintext {
		t.Fatalf("confirm = %#v, want the plaintext warning", m.editor.confirm)
	}

	view := m.confirmView()
	for _, want := range []string{"plaintext", "op://vault/item/field", "${ENV_VAR}"} {
		if !strings.Contains(view, want) {
			t.Errorf("the warning does not mention %q:\n%s", want, view)
		}
	}

	// Declining leaves the file alone and keeps the form open.
	m.updateConnections(key("n"))
	if m.editor == nil || m.editor.form == nil {
		t.Fatal("declining the warning closed the form")
	}
	if m.editor.confirm != nil {
		t.Error("the warning is still up after declining")
	}
	cfg, _, err := config.Load(m.cfgPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(cfg.Connections) != 2 {
		t.Errorf("declining still wrote the connection")
	}

	// Accepting saves it.
	m.saveForm()
	if m.editor.confirm == nil {
		t.Fatal("the warning was not asked a second time")
	}
	runFormCmd(t, m, m.updateConnections(key("y")))

	cfg, warns, err := config.Load(m.cfgPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(cfg.Connections) != 3 {
		t.Fatalf("got %d connections, want 3 after confirming", len(cfg.Connections))
	}
	if len(warns) != 1 || warns[0].Connection != "sloppy" {
		t.Errorf("warnings = %#v, want the plaintext warning from the loader", warns)
	}
}

func TestReferencePasswordSavesWithoutAsking(t *testing.T) {
	for _, pw := range []string{"op://Infra/db/password", "${PGPASSWORD}", ""} {
		t.Run(pw, func(t *testing.T) {
			m := testModel(t, twoConnections)
			m.updateConnections(key("a"))
			f := m.editor.form
			f.values[fldName] = "quiet"
			f.values[fldHost] = "db"
			f.values[fldPassword] = pw

			cmd := m.saveForm()
			if m.editor.confirm != nil {
				t.Fatalf("a reference triggered the plaintext warning: %q", pw)
			}
			if cmd == nil {
				t.Fatal("saveForm() did not write")
			}
		})
	}
}

// TestPlaintextWarningOffersThePicker covers the spec's requirement that the
// warning points at the 1Password alternative.
func TestPlaintextWarningOffersThePicker(t *testing.T) {
	m := testModel(t, twoConnections)
	m.updateConnections(key("a"))
	f := m.editor.form
	f.values[fldName] = "sloppy"
	f.values[fldHost] = "db"
	f.values[fldPassword] = "hunter2"
	m.saveForm()

	m.updateConnections(key("p"))
	if m.editor.confirm != nil {
		t.Error("the warning stayed up after choosing 1Password")
	}
	if m.editor.picker == nil {
		t.Fatal("choosing 1Password did not open the picker")
	}
	if m.editor.form.sel != fldPassword {
		t.Errorf("selection = %v, want the password field", m.editor.form.sel)
	}
}

func TestDeleteRemovesAfterConfirming(t *testing.T) {
	tests := []struct {
		name      string
		press     string
		wantCount int
	}{
		{name: "confirmed", press: "y", wantCount: 1},
		{name: "declined", press: "n", wantCount: 2},
		{name: "escaped", press: "esc", wantCount: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := testModel(t, twoConnections)
			m.updateConnections(key("d"))
			if !strings.Contains(m.confirmView(), "prod-pg") {
				t.Errorf("the confirmation does not name the connection:\n%s", m.confirmView())
			}
			runFormCmd(t, m, m.updateConnections(key(tt.press)))

			cfg, _, err := config.Load(m.cfgPath)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if len(cfg.Connections) != tt.wantCount {
				t.Errorf("got %d connections, want %d", len(cfg.Connections), tt.wantCount)
			}
			if m.editor != nil {
				t.Errorf("the editor stayed open: %#v", m.editor)
			}
			if len(m.conns) != tt.wantCount {
				t.Errorf("the list has %d entries, want %d", len(m.conns), tt.wantCount)
			}
		})
	}
}

func TestEscapeCancelsTheForm(t *testing.T) {
	m := testModel(t, twoConnections)
	m.updateConnections(key("e"))
	m.editor.form.values[fldHost] = "changed"
	m.updateConnections(key("esc"))

	if m.editor != nil {
		t.Fatal("esc left the editor open")
	}
	cfg, _, err := config.Load(m.cfgPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Connections[0].Host != "10.0.1.5" {
		t.Errorf("host = %q, want the cancelled edit discarded", cfg.Connections[0].Host)
	}
}

// TestFirstRunOffersToCreateTheFile covers the empty-config dead end.
func TestFirstRunOffersToCreateTheFile(t *testing.T) {
	m := testModel(t, "")
	if len(m.conns) != 0 {
		t.Fatalf("got %d connections, want none", len(m.conns))
	}
	view := m.connectionsView()
	if !strings.Contains(view, "a") || !strings.Contains(view, m.cfgPath) {
		t.Errorf("the onboarding view does not offer to create the file:\n%s", view)
	}

	m.updateConnections(key("a"))
	if m.editor == nil || m.editor.form == nil {
		t.Fatal("a did not open the form on an empty config")
	}
	f := m.editor.form
	f.values[fldName] = "first"
	f.values[fldHost] = "db.local"
	runFormCmd(t, m, m.saveForm())

	if _, err := os.Stat(m.cfgPath); err != nil {
		t.Fatalf("the config file was not created: %v", err)
	}
	cfg, _, err := config.Load(m.cfgPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(cfg.Connections) != 1 || cfg.Connections[0].Name != "first" {
		t.Errorf("loaded %#v, want one connection named first", cfg.Connections)
	}
	if len(m.conns) != 1 {
		t.Errorf("the list has %d entries, want 1", len(m.conns))
	}
}

// TestReloadKeepsUnchangedSessions checks an edit does not tear down the live
// connections it did not touch.
func TestReloadKeepsUnchangedSessions(t *testing.T) {
	m := testModel(t, twoConnections)
	kept := m.conns[1] // cache-redis
	kept.status = statusConnected
	kept.driver = &fakeDriver{}

	m.updateConnections(key("e")) // edit prod-pg
	m.editor.form.values[fldHost] = "10.0.1.9"
	runFormCmd(t, m, m.saveForm())

	if len(m.conns) != 2 {
		t.Fatalf("got %d connections, want 2", len(m.conns))
	}
	if m.conns[1] != kept {
		t.Error("the untouched connection lost its live session")
	}
	if m.conns[1].status != statusConnected {
		t.Errorf("status = %v, want it still connected", m.conns[1].status)
	}
	if m.conns[0].status != statusDisconnected {
		t.Errorf("the edited connection kept status %v, want it reset", m.conns[0].status)
	}
}

func TestEditorCapturesKeys(t *testing.T) {
	m := testModel(t, twoConnections)
	if m.capturesKeys() {
		t.Error("the connection list captures keys with no editor open")
	}
	m.updateConnections(key("a"))
	if !m.capturesKeys() {
		t.Error("the form does not capture keys, so ? would open help instead of typing")
	}

	// '?' is a character in a field, not the help key.
	typeText(m, "a?b")
	if got := m.editor.form.values[fldName]; got != "a?b" {
		t.Errorf("name = %q, want a?b", got)
	}
	// So is q, which would otherwise quit.
	typeText(m, "q")
	if got := m.editor.form.values[fldName]; got != "a?bq" {
		t.Errorf("name = %q, want q to be typed rather than quit", got)
	}
}

func TestBusyEditorIgnoresKeysButCanBeAbandoned(t *testing.T) {
	m := testModel(t, twoConnections)
	m.updateConnections(key("a"))
	m.editor.busy = "connecting to db"
	before := m.editor.form.values[fldName]

	typeText(m, "xyz")
	if m.editor.form.values[fldName] != before {
		t.Errorf("keys reached the form while it was busy")
	}

	gen := m.editor.gen
	m.updateConnections(key("esc"))
	if m.editor == nil {
		t.Fatal("esc closed the whole editor, want it to stop waiting only")
	}
	if m.editor.busy != "" {
		t.Error("esc did not stop the wait")
	}
	if m.editor.gen == gen {
		t.Error("the generation did not move, so a late result would still be applied")
	}
}

// TestStaleResultsAreDropped checks a result arriving after a cancel cannot
// overwrite what the user did next.
func TestStaleResultsAreDropped(t *testing.T) {
	m := testModel(t, twoConnections)
	m.updateConnections(key("a"))
	stale := connTestMsg{gen: m.editor.gen, name: "old"}
	m.editor.gen++

	m.handleConnFormMsg(stale)
	if m.editor.notice != "" {
		t.Errorf("notice = %q, want a stale result ignored", m.editor.notice)
	}
}

func TestTestConnectionValidatesFirst(t *testing.T) {
	m := testModel(t, twoConnections)
	m.updateConnections(key("a"))
	m.editor.form.values[fldName] = "x" // no host

	if cmd := m.testForm(); cmd != nil {
		t.Fatal("testForm() dialled an invalid connection")
	}
	if !strings.Contains(m.editor.err, "host is required") {
		t.Errorf("err = %q, want the validation message", m.editor.err)
	}
	if m.editor.busy != "" {
		t.Errorf("busy = %q, want nothing in flight", m.editor.busy)
	}
}

// TestTestConnectionLeavesTheListTunnelAlone is the trap the design note warns
// about: sshtunnel.Close is terminal, so a test must never touch a tunnel the
// connection list owns.
func TestTestConnectionLeavesTheListTunnelAlone(t *testing.T) {
	m := testModel(t, twoConnections)
	m.updateConnections(key("e")) // prod-pg, which has an ssh block
	cs := m.conns[0]
	if cs.tunnel != nil {
		t.Fatal("the fixture starts with a tunnel already up")
	}

	cmd := m.testForm()
	if cmd == nil {
		t.Fatal("testForm() returned no command")
	}
	if cs.tunnel != nil {
		t.Error("the test action attached a tunnel to the connection list's state")
	}
	if m.editor.busy == "" {
		t.Error("the editor is not marked busy while testing")
	}
}

func TestPasswordNote(t *testing.T) {
	tests := []struct {
		name string
		pw   string
		want string
	}{
		{name: "empty", pw: "", want: "no password"},
		{name: "op reference", pw: "op://Infra/db/password", want: "1Password reference"},
		{name: "short op reference", pw: "op://Infra/db", want: "incomplete reference"},
		{name: "env reference", pw: "${PGPASSWORD}", want: "environment reference"},
		{name: "literal", pw: "hunter2", want: "plaintext"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newConnForm()
			f.values[fldPassword] = tt.pw
			if got := f.passwordNote(); !strings.Contains(got, tt.want) {
				t.Errorf("passwordNote() = %q, want it to mention %q", got, tt.want)
			}
		})
	}
}

func TestFormViewShowsTheFields(t *testing.T) {
	m := testModel(t, twoConnections)
	m.updateConnections(key("e"))
	view := m.formView()

	for _, want := range []string{
		"Edit connection prod-pg", m.cfgPath,
		"Name", "Engine", "Host", "Port", "User", "Password", "Database",
		"SSH bastion", "10.0.1.5", "op://Infra/prod-pg/password", "bastion.corp.com",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("the form does not show %q:\n%s", want, view)
		}
	}
}

func TestConnectionsHints(t *testing.T) {
	m := testModel(t, twoConnections)
	if got := m.connectionsHints(); !strings.Contains(got, "a add") {
		t.Errorf("list hints = %q, want the add binding", got)
	}
	m.updateConnections(key("a"))
	if got := m.connectionsHints(); !strings.Contains(got, "ctrl+p") {
		t.Errorf("form hints = %q, want the 1Password binding", got)
	}
	m.editor.busy = "saving"
	if got := m.connectionsHints(); !strings.Contains(got, "esc") {
		t.Errorf("busy hints = %q, want a way out", got)
	}
}
