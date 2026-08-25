package ui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lergor11/d9s/internal/config"
	"github.com/lergor11/d9s/internal/session"
)

// connFormMsg marks every result the connection editor and its 1Password
// picker deliver from a tea.Cmd. The root model routes all of them to
// handleConnFormMsg, so the editor can grow new asynchronous steps without
// another case in the Update switch.
type connFormMsg interface{ isConnFormMsg() }

// configSavedMsg reports the outcome of writing the configuration file.
type configSavedMsg struct {
	gen    int
	action string // "added", "updated", or "removed", for the status line
	name   string
	err    error
}

// connTestMsg reports whether a throwaway session to the edited connection
// could be opened.
type connTestMsg struct {
	gen  int
	name string
	err  error
}

func (configSavedMsg) isConnFormMsg() {}
func (connTestMsg) isConnFormMsg()    {}

// formMode says whether the form creates a connection or edits one.
type formMode int

const (
	formAdd formMode = iota
	formEdit
)

// formFieldID identifies one editable field, and doubles as its position in
// the form.
type formFieldID int

const (
	fldName formFieldID = iota
	fldType
	fldHost
	fldPort
	fldUser
	fldPassword
	fldDatabase
	fldProtocol
	fldTLS
	fldSSHBastion
	fldSSHUser
	fldSSHPort
	fieldCount
)

// formField describes how one field is presented.
type formField struct {
	label   string
	choices []string // non-empty for a field cycled with ←/→ instead of typed
	hint    string
}

// formFields is the form's layout, in the order the fields are shown.
var formFields = [fieldCount]formField{
	fldName: {label: "Name"},
	fldType: {label: "Engine", choices: []string{
		string(config.Postgres), string(config.ClickHouse), string(config.Redis),
	}},
	fldHost:     {label: "Host", hint: "host, IP, or a /path unix socket (postgres only)"},
	fldPort:     {label: "Port", hint: "blank uses the engine default"},
	fldUser:     {label: "User"},
	fldPassword: {label: "Password", hint: "op://vault/item/field, ${ENV_VAR}, or a literal"},
	fldDatabase: {label: "Database", hint: "optional"},
	fldProtocol: {label: "Protocol", choices: []string{
		string(config.ProtocolNative), string(config.ProtocolHTTP),
	}, hint: "clickhouse only; the port follows it and the TLS mode"},
	fldTLS: {label: "TLS", choices: []string{
		tlsDefaultChoice, string(config.TLSDisable), string(config.TLSRequire),
		string(config.TLSVerifyCA), string(config.TLSVerifyFull),
	}, hint: "default is off behind a bastion or a unix socket, require otherwise"},
	fldSSHBastion: {label: "SSH bastion", hint: "optional; blank means a direct connection"},
	fldSSHUser:    {label: "SSH user", hint: "key comes from the 1Password SSH agent"},
	fldSSHPort:    {label: "SSH port", hint: "blank uses 22"},
}

// editorConfirmKind is the question a confirmation prompt is asking.
type editorConfirmKind int

const (
	// editorConfirmPlaintext warns that a literal password will be written to disk.
	editorConfirmPlaintext editorConfirmKind = iota
	// editorConfirmDelete asks before removing a connection from the file.
	editorConfirmDelete
)

// editorConfirm is a yes/no question shown over the editor.
type editorConfirm struct {
	kind editorConfirmKind
	name string // the connection the question is about
}

// connForm holds the edited values of one connection.
type connForm struct {
	mode     formMode
	original string // the name being edited; empty when adding

	// base is the connection the form started from. Saving copies it and
	// overwrites only the edited fields, so a tls block, redis mode, protocol,
	// or allow_write that the form does not show survives an edit.
	base config.Connection

	values [fieldCount]string
	sel    formFieldID

	// plaintextOK records that the user has already accepted storing a literal
	// password, so the warning is asked once rather than on every save.
	plaintextOK bool
}

// connEditor is the overlay the connection list puts up to add, edit, or
// delete a connection. Its parts stack: the picker sits over the form, and a
// confirmation sits over both, which is also the order keys are offered to
// them.
type connEditor struct {
	form    *connForm
	confirm *editorConfirm
	picker  *opPicker

	busy   string // what is in flight; empty when idle
	err    string
	notice string

	// gen rises whenever the editor stops waiting for something, so a result
	// that arrives after a cancel is recognised as stale and dropped.
	gen int
}

// newConnForm returns a form for adding a connection, pre-filled with the
// engine default so the first field the user meets is the one that matters.
func newConnForm() *connForm {
	f := &connForm{mode: formAdd}
	f.values[fldType] = string(config.Postgres)
	f.values[fldProtocol] = string(config.ProtocolNative)
	f.values[fldTLS] = tlsDefaultChoice
	return f
}

// editConnForm returns a form loaded with an existing connection.
func editConnForm(conn config.Connection) *connForm {
	f := &connForm{mode: formEdit, original: conn.Name, base: conn}
	f.values[fldName] = conn.Name
	f.values[fldType] = string(conn.Type)
	f.values[fldProtocol] = string(conn.EffectiveProtocol())
	f.values[fldTLS] = tlsDefaultChoice
	if conn.TLS != nil && conn.TLS.Mode != "" {
		f.values[fldTLS] = string(conn.TLS.Mode)
	}
	f.values[fldHost] = conn.Host
	f.values[fldPort] = itoaOrBlank(conn.Port)
	f.values[fldUser] = conn.User
	f.values[fldPassword] = conn.Password
	f.values[fldDatabase] = conn.Database
	if conn.SSH != nil {
		f.values[fldSSHBastion] = conn.SSH.Bastion
		f.values[fldSSHUser] = conn.SSH.User
		f.values[fldSSHPort] = itoaOrBlank(conn.SSH.Port)
	}
	return f
}

// itoaOrBlank renders a port, showing zero as an empty field.
func itoaOrBlank(n int) string {
	if n == 0 {
		return ""
	}
	return strconv.Itoa(n)
}

// title names the form for its heading.
func (f *connForm) title() string {
	if f.mode == formAdd {
		return "Add connection"
	}
	return "Edit connection " + f.original
}

// value returns the trimmed contents of a field.
func (f *connForm) value(id formFieldID) string {
	return strings.TrimSpace(f.values[id])
}

// move walks the selection by delta, stopping at the ends rather than wrapping
// so holding a key cannot silently cycle past the field being aimed at.
func (f *connForm) move(delta int) {
	if delta == 0 {
		return
	}
	step := 1
	if delta < 0 {
		step = -1
	}
	next := int(f.sel)
	for range abs(delta) {
		// Skip past whatever the current engine has no use for, rather than
		// stopping on a field the form will not show.
		for {
			next += step
			if next < 0 || next >= int(fieldCount) {
				return
			}
			if f.applies(formFieldID(next)) {
				break
			}
		}
		f.sel = formFieldID(next)
	}
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// applies reports whether a field means anything for the engine the form is
// currently set to, so a postgres connection is not asked about a ClickHouse
// wire protocol.
func (f *connForm) applies(id formFieldID) bool {
	if id == fldProtocol {
		return f.value(fldType) == string(config.ClickHouse)
	}
	return true
}

// cycle steps a choice field through its options.
func (f *connForm) cycle(delta int) {
	choices := formFields[f.sel].choices
	if len(choices) == 0 {
		return
	}
	at := 0
	for i, c := range choices {
		if c == f.values[f.sel] {
			at = i
			break
		}
	}
	f.values[f.sel] = choices[((at+delta)%len(choices)+len(choices))%len(choices)]
}

// insert types a rune into the focused field. Choice fields are cycled, never
// typed into, so a stray keystroke cannot put an unknown engine in the file.
func (f *connForm) insert(s string) {
	if len(formFields[f.sel].choices) > 0 {
		return
	}
	f.values[f.sel] += s
}

// backspace deletes the last rune of the focused field.
func (f *connForm) backspace() {
	if len(formFields[f.sel].choices) > 0 {
		return
	}
	r := []rune(f.values[f.sel])
	if len(r) > 0 {
		f.values[f.sel] = string(r[:len(r)-1])
	}
}

// connection builds the connection the form describes, starting from the one
// it was opened on so unedited fields are carried through untouched.
func (f *connForm) connection() (config.Connection, error) {
	conn := f.base
	conn.Name = f.value(fldName)
	conn.Type = config.EngineType(f.value(fldType))
	conn.Host = f.value(fldHost)
	conn.User = f.value(fldUser)
	conn.Password = f.value(fldPassword)
	conn.Database = f.value(fldDatabase)
	conn.TLS = tlsFromField(f.base.TLS, f.value(fldTLS))
	if conn.Type == config.ClickHouse {
		conn.Protocol = config.Protocol(f.value(fldProtocol))
	} else {
		conn.Protocol = ""
	}

	port, err := parsePortField("port", f.value(fldPort))
	if err != nil {
		return config.Connection{}, err
	}
	conn.Port = port

	bastion := f.value(fldSSHBastion)
	if bastion == "" {
		conn.SSH = nil
		return conn, nil
	}
	sshPort, err := parsePortField("ssh port", f.value(fldSSHPort))
	if err != nil {
		return config.Connection{}, err
	}
	ssh := config.SSH{Bastion: bastion, User: f.value(fldSSHUser), Port: sshPort}
	if f.base.SSH != nil {
		// agent_socket is not on the form; keep whatever the file had.
		ssh.AgentSocket = f.base.SSH.AgentSocket
	}
	conn.SSH = &ssh
	return conn, nil
}

// parsePortField reads a port field, where blank means "let the loader pick".
func parsePortField(name, s string) (int, error) {
	if s == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("%s must be a number", name)
	}
	if n < 1 || n > 65535 {
		return 0, fmt.Errorf("%s must be between 1 and 65535", name)
	}
	return n, nil
}

// validate reports the first problem worth showing while the user types, or an
// empty string when the form looks savable. It is deliberately a subset of the
// configuration's own rules: config validates again on save and is the
// authority, so the two cannot drift into disagreeing about what is legal.
func (f *connForm) validate() string {
	if f.value(fldName) == "" {
		return "name is required"
	}
	if f.value(fldHost) == "" {
		return "host is required"
	}
	if _, err := parsePortField("port", f.value(fldPort)); err != nil {
		return err.Error()
	}
	if _, err := parsePortField("ssh port", f.value(fldSSHPort)); err != nil {
		return err.Error()
	}
	if f.value(fldSSHBastion) == "" && (f.value(fldSSHUser) != "" || f.value(fldSSHPort) != "") {
		return "an ssh user or port needs a bastion host"
	}
	if ref := f.value(fldPassword); config.IsOpRef(ref) && !completeOpRef(ref) {
		return "an op:// reference needs vault/item/field"
	}
	return ""
}

// completeOpRef reports whether an op:// reference names all three of vault,
// item, and field. A short one parses as a reference but can never resolve.
func completeOpRef(ref string) bool {
	parts := strings.Split(strings.TrimPrefix(ref, "op://"), "/")
	if len(parts) != 3 {
		return false
	}
	for _, p := range parts {
		if strings.TrimSpace(p) == "" {
			return false
		}
	}
	return true
}

// literalPassword reports whether the password would be stored as a plaintext
// secret rather than as a reference to one.
func (f *connForm) literalPassword() bool {
	pw := f.value(fldPassword)
	return pw != "" && !config.IsOpRef(pw) && !config.IsEnvRef(pw)
}

// --- key handling ---------------------------------------------------------

// updateEditor handles a key while the connection editor is open. The picker
// takes keys first, then a confirmation, then the form itself.
func (m *model) updateEditor(msg tea.KeyMsg) tea.Cmd {
	e := m.editor
	key := msg.String()

	if e.busy != "" {
		// Only give up waiting; a late result is dropped by its generation.
		if key == "esc" {
			e.gen++
			e.busy = ""
			e.notice = ""
			e.err = "cancelled"
		}
		return nil
	}

	switch {
	case e.picker != nil:
		return m.updatePicker(msg)
	case e.confirm != nil:
		return m.updateEditorConfirm(key)
	case e.form != nil:
		return m.updateForm(msg)
	}
	m.closeEditor()
	return nil
}

// updateEditorConfirm answers the yes/no question on top of the editor.
func (m *model) updateEditorConfirm(key string) tea.Cmd {
	e := m.editor
	switch key {
	case "y", "Y":
		c := e.confirm
		e.confirm = nil
		switch c.kind {
		case editorConfirmDelete:
			return m.removeConn(c.name)
		case editorConfirmPlaintext:
			e.form.plaintextOK = true
			return m.saveForm()
		}
	case "n", "N", "esc":
		e.confirm = nil
		if e.form == nil {
			m.closeEditor()
		}
	case "p", "P":
		// The plaintext warning offers 1Password as the way out.
		if e.confirm.kind == editorConfirmPlaintext && e.form != nil {
			e.confirm = nil
			e.form.sel = fldPassword
			return m.openPicker()
		}
	}
	return nil
}

// updateForm handles a key aimed at the form fields.
func (m *model) updateForm(msg tea.KeyMsg) tea.Cmd {
	e := m.editor
	f := e.form
	switch msg.String() {
	case "esc":
		m.closeEditor()
	case "enter", "ctrl+s":
		return m.saveForm()
	case "tab", "down":
		f.move(1)
	case "shift+tab", "up":
		f.move(-1)
	case "left":
		f.cycle(-1)
	case "right", " ":
		f.cycle(1)
	case "ctrl+t":
		return m.testForm()
	case "ctrl+p":
		return m.openPicker()
	case "ctrl+k":
		return m.validateRef()
	case "backspace":
		f.backspace()
		e.err, e.notice = "", ""
	default:
		if msg.Type == tea.KeyRunes {
			f.insert(string(msg.Runes))
			e.err, e.notice = "", ""
		}
	}
	return nil
}

// --- actions --------------------------------------------------------------

// saveForm validates the form and writes it to the configuration file, asking
// first when the password would be stored in plaintext.
func (m *model) saveForm() tea.Cmd {
	e := m.editor
	f := e.form
	e.err, e.notice = "", ""

	if problem := f.validate(); problem != "" {
		e.err = problem
		return nil
	}
	conn, err := f.connection()
	if err != nil {
		e.err = err.Error()
		return nil
	}
	if f.literalPassword() && !f.plaintextOK {
		e.confirm = &editorConfirm{kind: editorConfirmPlaintext, name: conn.Name}
		return nil
	}

	action, path, original, mode := "added", m.cfgPath, f.original, f.mode
	if mode == formEdit {
		action = "updated"
	}
	e.busy = "saving " + conn.Name
	gen := e.gen
	return tea.Batch(m.spinner.Tick, func() tea.Msg {
		var err error
		if mode == formAdd {
			err = config.AddConnection(path, conn)
		} else {
			err = config.UpdateConnection(path, original, conn)
		}
		return configSavedMsg{gen: gen, action: action, name: conn.Name, err: err}
	})
}

// removeConn deletes a connection from the configuration file.
func (m *model) removeConn(name string) tea.Cmd {
	e := m.editor
	e.err, e.notice = "", ""
	e.busy = "removing " + name
	gen, path := e.gen, m.cfgPath
	return tea.Batch(m.spinner.Tick, func() tea.Msg {
		return configSavedMsg{gen: gen, action: "removed", name: name,
			err: config.RemoveConnection(path, name)}
	})
}

// testForm opens a throwaway session to the connection as edited and closes it
// again, so the user can check a change before saving it.
//
// The session owns everything it creates, its own SSH tunnel included, and
// closing it tears that tunnel down. It must never be handed a tunnel the
// connection list owns: sshtunnel.Close is terminal, so closing a borrowed one
// would leave every other session on that connection unable to dial.
func (m *model) testForm() tea.Cmd {
	e := m.editor
	f := e.form
	e.err, e.notice = "", ""

	if problem := f.validate(); problem != "" {
		e.err = problem
		return nil
	}
	conn, err := f.connection()
	if err != nil {
		e.err = err.Error()
		return nil
	}

	e.busy = "connecting to " + conn.Name
	gen, res := e.gen, m.resolver
	return tea.Batch(m.spinner.Tick, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
		defer cancel()

		s, err := session.Open(ctx, res, conn, "")
		if err != nil {
			return connTestMsg{gen: gen, name: conn.Name, err: err}
		}
		// The test is whether it connects; a teardown failure on a session
		// nothing else uses is not worth reporting.
		_ = s.Close()
		return connTestMsg{gen: gen, name: conn.Name}
	})
}

// --- message handling -----------------------------------------------------

// handleConnFormMsg dispatches every asynchronous result the editor asked for.
func (m *model) handleConnFormMsg(msg connFormMsg) tea.Cmd {
	if m.editor == nil {
		return nil
	}
	switch msg := msg.(type) {
	case configSavedMsg:
		return m.handleConfigSaved(msg)
	case connTestMsg:
		if msg.gen != m.editor.gen {
			return nil
		}
		m.editor.busy = ""
		if msg.err != nil {
			m.editor.err = clip(msg.err.Error(), 200)
			return nil
		}
		m.editor.notice = msg.name + " connects"
		return nil
	case opVaultsMsg, opItemsMsg, opFieldsMsg, refValidatedMsg:
		return m.handlePickerMsg(msg)
	}
	return nil
}

// handleConfigSaved closes the editor and reloads the file once a write lands.
func (m *model) handleConfigSaved(msg configSavedMsg) tea.Cmd {
	e := m.editor
	if msg.gen != e.gen {
		return nil
	}
	e.busy = ""
	if msg.err != nil {
		e.err = clip(msg.err.Error(), 200)
		return nil
	}
	m.closeEditor()
	if err := m.reloadConfig(); err != nil {
		m.status = stErr.Render("saved, but re-reading " + m.cfgPath + " failed: " + err.Error())
		return nil
	}
	m.status = stOK.Render(fmt.Sprintf("%s %s in %s", msg.action, msg.name, m.cfgPath))
	return nil
}

// closeEditor dismisses the overlay and stops waiting for anything in flight.
func (m *model) closeEditor() {
	if m.editor != nil {
		m.editor.gen++
	}
	m.editor = nil
}

// --- rendering ------------------------------------------------------------

// editorView renders whichever part of the editor is on top.
func (m *model) editorView() string {
	e := m.editor
	switch {
	case e.confirm != nil:
		return m.confirmView()
	case e.picker != nil:
		return m.pickerView()
	default:
		return m.formView()
	}
}

// confirmView renders the yes/no question over the editor.
func (m *model) confirmView() string {
	c := m.editor.confirm
	var b strings.Builder
	switch c.kind {
	case editorConfirmDelete:
		b.WriteString(stSection.Render("Delete connection "+c.name) + "\n\n")
		fmt.Fprintf(&b, "Remove %s from %s?\n", stHeader.Render(c.name), m.cfgPath)
		b.WriteString(stDim.Render("The file's other entries and comments are left alone.") + "\n\n")
	case editorConfirmPlaintext:
		b.WriteString(stWarn.Render("Store this password in plaintext?") + "\n\n")
		fmt.Fprintf(&b, "It will be written to %s as readable text,\n", m.cfgPath)
		b.WriteString("and anything that can read that file can read the password.\n\n")
		b.WriteString(stDim.Render("A reference keeps the secret in 1Password or the environment:") + "\n")
		b.WriteString(stDim.Render("  op://vault/item/field    ${ENV_VAR}") + "\n\n")
	}
	b.WriteString(stOK.Render("y") + " yes   " + stErr.Render("n") + " no")
	if c.kind == editorConfirmPlaintext {
		b.WriteString("   " + stBadge.Render("p") + " pick from 1Password")
	}
	return stModal.Render(b.String())
}

// formView renders the add/edit form.
func (m *model) formView() string {
	e := m.editor
	f := e.form

	var b strings.Builder
	b.WriteString(stSection.Render(f.title()) + "\n")
	b.WriteString(stDim.Render(m.cfgPath) + "\n\n")

	width := 0
	for _, fd := range formFields {
		if n := len(fd.label); n > width {
			width = n
		}
	}

	for id := formFieldID(0); id < fieldCount; id++ {
		if !f.applies(id) {
			continue
		}
		fd := formFields[id]
		cursor := "  "
		label := pad(fd.label, width)
		if id == f.sel {
			cursor = stSelected.Render("> ")
			label = stSelected.Render(label)
		}
		fmt.Fprintf(&b, "%s%s  %s\n", cursor, label, f.renderValue(id))
	}

	if hint := formFields[f.sel].hint; hint != "" {
		b.WriteString("\n" + stDim.Render(hint) + "\n")
	} else {
		b.WriteString("\n")
	}
	if note := f.passwordNote(); note != "" {
		b.WriteString(note + "\n")
	}

	switch {
	case e.busy != "":
		b.WriteString("\n" + m.spinner.View() + " " + e.busy + stDim.Render(" (esc to give up)") + "\n")
	case e.err != "":
		b.WriteString("\n" + stErr.Render("✗ "+e.err) + "\n")
	case e.notice != "":
		b.WriteString("\n" + stOK.Render("✓ "+e.notice) + "\n")
	default:
		b.WriteString("\n")
	}

	b.WriteString(stDim.Render(formHints))
	return stHelpBox.Render(b.String())
}

// formHints is the key legend at the foot of the form.
const formHints = "tab/↑↓ field · ←/→ choose · enter save · ctrl+t test · " +
	"ctrl+p 1Password · ctrl+k check ref · esc cancel"

// renderValue renders one field's value, masking nothing: the field holds a
// reference or a literal the user just typed, and hiding it would make a typo
// impossible to spot.
func (f *connForm) renderValue(id formFieldID) string {
	v := f.values[id]
	if len(formFields[id].choices) > 0 {
		return stBadge.Render("‹ " + v + " ›")
	}
	if v == "" {
		return stDim.Render("—")
	}
	return v
}

// passwordNote says how the password field will be stored, so the consequence
// is visible before the save asks about it.
func (f *connForm) passwordNote() string {
	pw := f.value(fldPassword)
	switch {
	case pw == "":
		return stDim.Render("no password: the engine must accept the user without one")
	case config.IsOpRef(pw):
		if !completeOpRef(pw) {
			return stErr.Render("incomplete reference: op://vault/item/field")
		}
		return stOK.Render("1Password reference, resolved at connect time")
	case config.IsEnvRef(pw):
		return stOK.Render("environment reference, read at connect time")
	default:
		return stWarn.Render("⚠ literal password: it will be stored in plaintext")
	}
}

// tlsDefaultChoice is how the form spells "no tls block", so the connection
// keeps following the per-shape default rather than pinning a mode.
const tlsDefaultChoice = "default"

// tlsFromField turns the form's TLS choice into a config block. Blank means
// "no block", so the connection keeps following the default, and the rest of
// the block — CA, client certificate, server name — survives an edit, since
// the form does not show those fields.
func tlsFromField(base *config.TLS, mode string) *config.TLS {
	if mode == tlsDefaultChoice || mode == "" {
		if base == nil {
			return nil
		}
		out := *base
		out.Mode = ""
		if out == (config.TLS{}) {
			return nil
		}
		return &out
	}
	out := config.TLS{}
	if base != nil {
		out = *base
	}
	out.Mode = config.TLSMode(mode)
	return &out
}
