package ui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lergor11/d9s/internal/config"
	"github.com/lergor11/d9s/internal/secrets"
)

// opTimeout bounds one 1Password CLI call. It is generous because unlocking
// can wait on a biometric prompt the user has to answer.
const opTimeout = 60 * time.Second

// opVaultsMsg carries the vault listing.
type opVaultsMsg struct {
	gen    int
	vaults []secrets.Vault
	err    error
}

// opItemsMsg carries the items of one vault.
type opItemsMsg struct {
	gen   int
	items []secrets.Item
	err   error
}

// opFieldsMsg carries the fields of one item.
type opFieldsMsg struct {
	gen    int
	fields []secrets.Field
	err    error
}

// refValidatedMsg reports whether a typed reference resolves. It never carries
// the value: secrets.Validate reads and discards it.
type refValidatedMsg struct {
	gen int
	ref string
	err error
}

func (opVaultsMsg) isConnFormMsg()     {}
func (opItemsMsg) isConnFormMsg()      {}
func (opFieldsMsg) isConnFormMsg()     {}
func (refValidatedMsg) isConnFormMsg() {}

// opStep is how far into vault → item → field the picker has walked.
type opStep int

const (
	stepVaults opStep = iota
	stepItems
	stepFields
)

// opPicker browses 1Password to build an op:// reference. It shows names only:
// no field value is ever fetched, so there is nothing secret on screen.
type opPicker struct {
	step   opStep
	filter string
	sel    int

	vaults []secrets.Vault
	items  []secrets.Item
	fields []secrets.Field

	vault secrets.Vault
	item  secrets.Item
}

// pickerRow is one line of the picker: what to match on, and what to show.
type pickerRow struct {
	label  string
	detail string
}

// rows returns the entries of the current step that match the filter.
func (p *opPicker) rows() []pickerRow {
	var all []pickerRow
	switch p.step {
	case stepVaults:
		for _, v := range p.vaults {
			all = append(all, pickerRow{label: v.Name})
		}
	case stepItems:
		for _, it := range p.items {
			all = append(all, pickerRow{label: it.Title, detail: strings.ToLower(it.Category)})
		}
	case stepFields:
		for _, fl := range p.fields {
			all = append(all, pickerRow{label: fl.Label, detail: fieldDetail(fl)})
		}
	}
	q := strings.ToLower(strings.TrimSpace(p.filter))
	if q == "" {
		return all
	}
	var out []pickerRow
	for _, r := range all {
		if strings.Contains(strings.ToLower(r.label), q) {
			out = append(out, r)
		}
	}
	return out
}

// fieldDetail describes a field without revealing anything about its value.
func fieldDetail(f secrets.Field) string {
	var parts []string
	if f.Section != "" {
		parts = append(parts, f.Section)
	}
	if f.Concealed() {
		parts = append(parts, "concealed")
	} else if f.Type != "" {
		parts = append(parts, strings.ToLower(f.Type))
	}
	return strings.Join(parts, " · ")
}

// clampSel keeps the selection inside the filtered list.
func (p *opPicker) clampSel() {
	n := len(p.rows())
	if p.sel >= n {
		p.sel = n - 1
	}
	if p.sel < 0 {
		p.sel = 0
	}
}

// selectedIndex maps the highlighted row back to its position in the unfiltered
// list of the current step, or -1 when nothing is selectable.
func (p *opPicker) selectedIndex() int {
	rows := p.rows()
	if p.sel < 0 || p.sel >= len(rows) {
		return -1
	}
	want := rows[p.sel]
	var labels []string
	switch p.step {
	case stepVaults:
		for _, v := range p.vaults {
			labels = append(labels, v.Name)
		}
	case stepItems:
		for _, it := range p.items {
			labels = append(labels, it.Title)
		}
	case stepFields:
		for _, fl := range p.fields {
			labels = append(labels, fl.Label)
		}
	}
	// Walk the filtered rows again so duplicate labels resolve to the same
	// entry the user is looking at rather than to the first match.
	seen := 0
	q := strings.ToLower(strings.TrimSpace(p.filter))
	for i, label := range labels {
		if q != "" && !strings.Contains(strings.ToLower(label), q) {
			continue
		}
		if seen == p.sel && label == want.label {
			return i
		}
		seen++
	}
	return -1
}

// crumbs names the path walked so far, for the picker heading.
func (p *opPicker) crumbs() string {
	parts := []string{"1Password"}
	if p.step > stepVaults {
		parts = append(parts, p.vault.Name)
	}
	if p.step > stepItems {
		parts = append(parts, p.item.Title)
	}
	return strings.Join(parts, " › ")
}

// --- actions --------------------------------------------------------------

// openPicker starts the picker on the vault list.
func (m *model) openPicker() tea.Cmd {
	e := m.editor
	e.err, e.notice = "", ""
	e.picker = &opPicker{}
	e.busy = "listing 1Password vaults"
	gen := e.gen
	return tea.Batch(m.spinner.Tick, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
		defer cancel()
		vaults, err := secrets.NewClient().Vaults(ctx)
		return opVaultsMsg{gen: gen, vaults: vaults, err: err}
	})
}

// loadItems fetches the items of the chosen vault.
func (m *model) loadItems(vault secrets.Vault) tea.Cmd {
	e := m.editor
	e.busy = "listing items in " + vault.Name
	gen := e.gen
	return tea.Batch(m.spinner.Tick, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
		defer cancel()
		items, err := secrets.NewClient().Items(ctx, vault.ID)
		return opItemsMsg{gen: gen, items: items, err: err}
	})
}

// loadFields fetches the fields of the chosen item.
func (m *model) loadFields(vault secrets.Vault, item secrets.Item) tea.Cmd {
	e := m.editor
	e.busy = "listing fields of " + item.Title
	gen := e.gen
	return tea.Batch(m.spinner.Tick, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
		defer cancel()
		fields, err := secrets.NewClient().Fields(ctx, vault.ID, item.ID)
		return opFieldsMsg{gen: gen, fields: fields, err: err}
	})
}

// validateRef checks the reference in the password field against 1Password.
func (m *model) validateRef() tea.Cmd {
	e := m.editor
	ref := e.form.value(fldPassword)
	e.err, e.notice = "", ""

	switch {
	case ref == "":
		e.err = "nothing to check: the password field is empty"
		return nil
	case !config.IsOpRef(ref):
		e.err = "only an op:// reference can be checked against 1Password"
		return nil
	case !completeOpRef(ref):
		e.err = "an op:// reference needs vault/item/field"
		return nil
	}

	e.busy = "checking " + ref
	gen, res := e.gen, m.resolver
	return tea.Batch(m.spinner.Tick, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
		defer cancel()
		return refValidatedMsg{gen: gen, ref: ref, err: res.Validate(ctx, ref)}
	})
}

// --- key handling ---------------------------------------------------------

// updatePicker handles a key while the picker is open.
func (m *model) updatePicker(msg tea.KeyMsg) tea.Cmd {
	e := m.editor
	p := e.picker

	switch msg.String() {
	case "esc":
		return m.pickerBack()
	case "up":
		p.sel--
		p.clampSel()
	case "down":
		p.sel++
		p.clampSel()
	case "enter":
		return m.pickerChoose()
	case "backspace":
		r := []rune(p.filter)
		if len(r) > 0 {
			p.filter = string(r[:len(r)-1])
			p.sel = 0
		}
	default:
		if msg.Type == tea.KeyRunes {
			p.filter += string(msg.Runes)
			p.sel = 0
		}
	}
	return nil
}

// pickerBack steps out of the current level, closing the picker at the top.
func (m *model) pickerBack() tea.Cmd {
	e := m.editor
	p := e.picker
	e.err = ""
	switch p.step {
	case stepFields:
		p.step, p.filter, p.sel = stepItems, "", 0
	case stepItems:
		p.step, p.filter, p.sel = stepVaults, "", 0
	default:
		e.picker = nil
	}
	return nil
}

// pickerChoose descends into the highlighted entry, or writes the reference
// once a field is picked.
func (m *model) pickerChoose() tea.Cmd {
	e := m.editor
	p := e.picker
	i := p.selectedIndex()
	if i < 0 {
		return nil
	}
	switch p.step {
	case stepVaults:
		p.vault = p.vaults[i]
		p.step, p.filter, p.sel = stepItems, "", 0
		return m.loadItems(p.vault)
	case stepItems:
		p.item = p.items[i]
		p.step, p.filter, p.sel = stepFields, "", 0
		return m.loadFields(p.vault, p.item)
	case stepFields:
		ref, err := secrets.Reference(p.vault, p.item, p.fields[i])
		if err != nil {
			e.err = err.Error()
			return nil
		}
		e.form.values[fldPassword] = ref
		e.form.plaintextOK = false // a reference is not a literal
		e.form.sel = fldPassword
		e.picker = nil
		e.notice = "reference set to " + ref
		return nil
	}
	return nil
}

// --- message handling -----------------------------------------------------

// handlePickerMsg applies a 1Password result to the picker.
func (m *model) handlePickerMsg(msg connFormMsg) tea.Cmd {
	e := m.editor
	switch msg := msg.(type) {
	case opVaultsMsg:
		if msg.gen != e.gen {
			return nil
		}
		e.busy = ""
		if e.picker == nil {
			return nil
		}
		if msg.err != nil {
			e.picker = nil
			e.err = opFailure(msg.err)
			return nil
		}
		e.picker.vaults = msg.vaults
		e.picker.clampSel()
		if len(msg.vaults) == 0 {
			e.err = "1Password returned no vaults for this account"
		}

	case opItemsMsg:
		if msg.gen != e.gen || e.picker == nil {
			return nil
		}
		e.busy = ""
		if msg.err != nil {
			e.err = opFailure(msg.err)
			e.picker.step = stepVaults
			return nil
		}
		e.picker.items = msg.items
		e.picker.clampSel()

	case opFieldsMsg:
		if msg.gen != e.gen || e.picker == nil {
			return nil
		}
		e.busy = ""
		if msg.err != nil {
			e.err = opFailure(msg.err)
			e.picker.step = stepItems
			return nil
		}
		e.picker.fields = concealedFirst(msg.fields)
		e.picker.clampSel()

	case refValidatedMsg:
		if msg.gen != e.gen {
			return nil
		}
		e.busy = ""
		if msg.err != nil {
			e.err = opFailure(msg.err)
			return nil
		}
		e.notice = msg.ref + " resolves"
	}
	return nil
}

// concealedFirst puts the hidden fields at the top, since a password picker is
// nearly always after one, keeping the CLI's order within each group.
func concealedFirst(fields []secrets.Field) []secrets.Field {
	out := append([]secrets.Field(nil), fields...)
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Concealed() && !out[j].Concealed()
	})
	return out
}

// opFailure renders a 1Password failure as the advice it carries. OpError's
// own message already leads with what to do about it, so the kind only decides
// whether there is anything more worth adding.
func opFailure(err error) string {
	msg := clip(err.Error(), 200)
	switch secrets.ErrorKind(err) {
	case secrets.KindNotInstalled, secrets.KindNotSignedIn, secrets.KindLocked:
		// The advice is the message; retrying is the whole remedy.
		return msg + " — then press ctrl+p again"
	default:
		return msg
	}
}

// --- rendering ------------------------------------------------------------

// pickerRows is how many entries the picker shows at once.
const pickerRows = 12

// pickerView renders the vault, item, or field list.
func (m *model) pickerView() string {
	e := m.editor
	p := e.picker

	var b strings.Builder
	b.WriteString(stSection.Render(p.crumbs()) + "\n")
	b.WriteString(stDim.Render(pickerPrompt[p.step]) + "\n\n")

	if p.filter != "" {
		b.WriteString("filter: " + stHeader.Render(p.filter) + "\n\n")
	}

	rows := p.rows()
	switch {
	case e.busy != "":
		b.WriteString(m.spinner.View() + " " + e.busy + stDim.Render(" (esc to give up)") + "\n")
	case len(rows) == 0:
		b.WriteString(stDim.Render("nothing matches") + "\n")
	default:
		start := 0
		if p.sel >= pickerRows {
			start = p.sel - pickerRows + 1
		}
		end := start + pickerRows
		if end > len(rows) {
			end = len(rows)
		}
		for i := start; i < end; i++ {
			cursor, label := "  ", rows[i].label
			if i == p.sel {
				cursor = stSelected.Render("> ")
				label = stSelected.Render(label)
			}
			line := cursor + label
			if rows[i].detail != "" {
				line += "  " + stDim.Render(rows[i].detail)
			}
			b.WriteString(line + "\n")
		}
		if len(rows) > end {
			fmt.Fprintf(&b, "%s\n", stDim.Render(fmt.Sprintf("  … %d more", len(rows)-end)))
		}
	}

	if e.err != "" {
		b.WriteString("\n" + stErr.Render("✗ "+e.err) + "\n")
	}
	b.WriteString("\n" + stDim.Render("type to filter · ↑/↓ select · enter choose · esc back"))
	return stHelpBox.Render(b.String())
}

// pickerPrompt says what the current step is asking for.
var pickerPrompt = map[opStep]string{
	stepVaults: "choose a vault",
	stepItems:  "choose an item",
	stepFields: "choose the field holding the password",
}
