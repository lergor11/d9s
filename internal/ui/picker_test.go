package ui

import (
	"errors"
	"strings"
	"testing"

	"github.com/andreim/d9s/internal/secrets"
)

// openPickerWith puts a picker on screen already loaded with vaults, skipping
// the CLI call the real one makes.
func openPickerWith(t *testing.T, vaults []secrets.Vault) *model {
	t.Helper()
	m := testModel(t, twoConnections)
	m.updateConnections(key("a"))
	m.editor.picker = &opPicker{vaults: vaults}
	return m
}

var testVaults = []secrets.Vault{
	{ID: "v1", Name: "Infra"},
	{ID: "v2", Name: "Personal"},
	{ID: "v3", Name: "Shared Infra"},
}

var testItems = []secrets.Item{
	{ID: "i1", Title: "prod-pg", Category: "LOGIN"},
	{ID: "i2", Title: "staging-pg", Category: "LOGIN"},
	{ID: "i3", Title: "wifi", Category: "PASSWORD"},
}

var testFields = []secrets.Field{
	{ID: "f1", Label: "username", Type: "STRING", Purpose: "USERNAME"},
	{ID: "f2", Label: "password", Type: "CONCEALED", Purpose: "PASSWORD"},
	{ID: "f3", Label: "notes", Type: "STRING"},
	{ID: "f4", Label: "api-key", Type: "CONCEALED", Section: "tokens"},
}

func TestPickerWalksVaultItemField(t *testing.T) {
	m := openPickerWith(t, testVaults)
	p := m.editor.picker

	if p.step != stepVaults {
		t.Fatalf("step = %v, want vaults", p.step)
	}
	if got := len(p.rows()); got != 3 {
		t.Fatalf("got %d vault rows, want 3", got)
	}

	// Down to Personal, then back up and into Infra.
	m.updateConnections(key("down"))
	if p.sel != 1 {
		t.Errorf("sel = %d, want 1", p.sel)
	}
	m.updateConnections(key("up"))

	if cmd := m.updateConnections(key("enter")); cmd == nil {
		t.Fatal("choosing a vault did not start the item listing")
	}
	if p.step != stepItems {
		t.Fatalf("step = %v, want items", p.step)
	}
	if p.vault.ID != "v1" {
		t.Errorf("vault = %#v, want Infra", p.vault)
	}
	if m.editor.busy == "" {
		t.Error("the editor is not busy while listing items")
	}

	// The listing arrives.
	m.handleConnFormMsg(opItemsMsg{gen: m.editor.gen, items: testItems})
	if m.editor.busy != "" {
		t.Error("still busy after the items arrived")
	}
	if got := len(p.rows()); got != 3 {
		t.Fatalf("got %d item rows, want 3", got)
	}

	if cmd := m.updateConnections(key("enter")); cmd == nil {
		t.Fatal("choosing an item did not start the field listing")
	}
	if p.step != stepFields || p.item.ID != "i1" {
		t.Fatalf("step = %v item = %#v, want fields of prod-pg", p.step, p.item)
	}

	m.handleConnFormMsg(opFieldsMsg{gen: m.editor.gen, fields: testFields})

	// Concealed fields come first, so the password is the default choice.
	rows := p.rows()
	if len(rows) != 4 {
		t.Fatalf("got %d field rows, want 4", len(rows))
	}
	if rows[0].label != "password" && rows[0].label != "api-key" {
		t.Errorf("first field = %q, want a concealed one", rows[0].label)
	}

	// Choosing the password writes the reference into the form.
	for p.rows()[p.sel].label != "password" {
		m.updateConnections(key("down"))
	}
	m.updateConnections(key("enter"))

	if m.editor.picker != nil {
		t.Error("the picker stayed open after a field was chosen")
	}
	want := "op://Infra/prod-pg/password"
	if got := m.editor.form.values[fldPassword]; got != want {
		t.Errorf("password field = %q, want %q", got, want)
	}
	if m.editor.form.sel != fldPassword {
		t.Errorf("selection = %v, want the password field", m.editor.form.sel)
	}
}

func TestPickerFilters(t *testing.T) {
	tests := []struct {
		name   string
		typing string
		want   []string
	}{
		{name: "no filter", want: []string{"Infra", "Personal", "Shared Infra"}},
		{name: "prefix", typing: "Inf", want: []string{"Infra", "Shared Infra"}},
		{name: "case insensitive", typing: "inf", want: []string{"Infra", "Shared Infra"}},
		{name: "substring", typing: "hared", want: []string{"Shared Infra"}},
		{name: "no match", typing: "zzz", want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := openPickerWith(t, testVaults)
			typeText(m, tt.typing)

			var got []string
			for _, r := range m.editor.picker.rows() {
				got = append(got, r.label)
			}
			if strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Errorf("rows = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPickerFilterBackspaceAndSelection(t *testing.T) {
	m := openPickerWith(t, testVaults)
	p := m.editor.picker

	typeText(m, "Personal")
	if len(p.rows()) != 1 {
		t.Fatalf("got %d rows, want 1", len(p.rows()))
	}
	// The selection cannot point past the filtered list.
	m.updateConnections(key("down"))
	if p.sel != 0 {
		t.Errorf("sel = %d, want it clamped to 0", p.sel)
	}

	for i := 0; i < len("Personal"); i++ {
		m.updateConnections(key("backspace"))
	}
	if p.filter != "" {
		t.Errorf("filter = %q, want it emptied", p.filter)
	}
	if len(p.rows()) != 3 {
		t.Errorf("got %d rows, want all 3 back", len(p.rows()))
	}
	// A backspace on an empty filter is harmless.
	m.updateConnections(key("backspace"))
	if p.filter != "" {
		t.Errorf("filter = %q, want it still empty", p.filter)
	}
}

// TestPickerChoosesTheHighlightedRowWhenFiltered guards the mapping from the
// filtered row back to the underlying entry.
func TestPickerChoosesTheHighlightedRowWhenFiltered(t *testing.T) {
	m := openPickerWith(t, testVaults)
	typeText(m, "Inf") // Infra, Shared Infra
	m.updateConnections(key("down"))
	m.updateConnections(key("enter"))

	if got := m.editor.picker.vault.ID; got != "v3" {
		t.Errorf("chose vault %q, want v3 (Shared Infra)", got)
	}
}

func TestPickerEscapeWalksBack(t *testing.T) {
	m := openPickerWith(t, testVaults)
	p := m.editor.picker
	p.step, p.vault, p.item = stepFields, testVaults[0], testItems[0]
	p.items, p.fields = testItems, testFields
	typeText(m, "pass")

	m.updateConnections(key("esc"))
	if p.step != stepItems {
		t.Fatalf("step = %v, want items", p.step)
	}
	if p.filter != "" {
		t.Errorf("filter = %q, want it cleared on the way back", p.filter)
	}

	m.updateConnections(key("esc"))
	if p.step != stepVaults {
		t.Fatalf("step = %v, want vaults", p.step)
	}

	m.updateConnections(key("esc"))
	if m.editor.picker != nil {
		t.Error("esc at the top did not close the picker")
	}
	if m.editor.form == nil {
		t.Error("closing the picker also closed the form")
	}
}

func TestPickerCrumbs(t *testing.T) {
	p := &opPicker{}
	if got := p.crumbs(); got != "1Password" {
		t.Errorf("crumbs = %q, want 1Password", got)
	}
	p.step, p.vault = stepItems, testVaults[0]
	if got := p.crumbs(); got != "1Password › Infra" {
		t.Errorf("crumbs = %q", got)
	}
	p.step, p.item = stepFields, testItems[0]
	if got := p.crumbs(); got != "1Password › Infra › prod-pg" {
		t.Errorf("crumbs = %q", got)
	}
}

// TestPickerSurfacesErrorKinds checks a failure reads as advice rather than as
// a CLI exit status, which is what the spec asks for.
func TestPickerSurfacesErrorKinds(t *testing.T) {
	tests := []struct {
		name string
		kind secrets.OpErrorKind
		want string
	}{
		{name: "locked", kind: secrets.KindLocked, want: "unlock"},
		{name: "not signed in", kind: secrets.KindNotSignedIn, want: "signin"},
		{name: "not installed", kind: secrets.KindNotInstalled, want: "PATH"},
		{name: "not found", kind: secrets.KindNotFound, want: "no such vault"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := openPickerWith(t, nil)
			m.editor.busy = "listing 1Password vaults"
			err := &secrets.OpError{Kind: tt.kind, Command: "op vault list"}

			m.handleConnFormMsg(opVaultsMsg{gen: m.editor.gen, err: err})

			if m.editor.busy != "" {
				t.Error("still busy after the failure")
			}
			if m.editor.picker != nil {
				t.Error("the picker stayed open after failing to list vaults")
			}
			if !strings.Contains(m.editor.err, tt.want) {
				t.Errorf("err = %q, want it to mention %q", m.editor.err, tt.want)
			}
			if strings.Contains(m.editor.err, "exit status") {
				t.Errorf("err = %q, want advice rather than an exit status", m.editor.err)
			}
		})
	}
}

// TestPickerErrorMidWalkStepsBack checks a failure deeper in keeps the picker
// usable instead of dropping the user out of it.
func TestPickerErrorMidWalkStepsBack(t *testing.T) {
	m := openPickerWith(t, testVaults)
	p := m.editor.picker
	p.step, p.vault = stepItems, testVaults[0]
	m.editor.busy = "listing items"

	m.handleConnFormMsg(opItemsMsg{gen: m.editor.gen,
		err: &secrets.OpError{Kind: secrets.KindLocked, Command: "op item list"}})

	if m.editor.picker == nil {
		t.Fatal("the picker closed on an item-listing failure")
	}
	if p.step != stepVaults {
		t.Errorf("step = %v, want it back at the vault list", p.step)
	}
	if !strings.Contains(m.editor.err, "unlock") {
		t.Errorf("err = %q, want the advice", m.editor.err)
	}
}

func TestPickerEmptyVaultList(t *testing.T) {
	m := openPickerWith(t, nil)
	m.editor.busy = "listing 1Password vaults"
	m.handleConnFormMsg(opVaultsMsg{gen: m.editor.gen, vaults: nil})

	if m.editor.picker == nil {
		t.Fatal("the picker closed on an empty but successful listing")
	}
	if !strings.Contains(m.editor.err, "no vaults") {
		t.Errorf("err = %q, want it to say there are no vaults", m.editor.err)
	}
}

func TestPickerStaleResultsAreDropped(t *testing.T) {
	m := openPickerWith(t, nil)
	stale := m.editor.gen
	m.editor.gen++

	m.handleConnFormMsg(opVaultsMsg{gen: stale, vaults: testVaults})
	if len(m.editor.picker.vaults) != 0 {
		t.Error("a stale vault listing was applied")
	}
}

func TestValidateRefRejectsWhatItCannotCheck(t *testing.T) {
	tests := []struct {
		name string
		pw   string
		want string
	}{
		{name: "empty", pw: "", want: "empty"},
		{name: "literal", pw: "hunter2", want: "only an op:// reference"},
		{name: "env reference", pw: "${PGPASSWORD}", want: "only an op:// reference"},
		{name: "incomplete", pw: "op://Infra/db", want: "vault/item/field"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := testModel(t, twoConnections)
			m.updateConnections(key("a"))
			m.editor.form.values[fldPassword] = tt.pw

			if cmd := m.validateRef(); cmd != nil {
				t.Fatal("validateRef() called the CLI for something it cannot check")
			}
			if !strings.Contains(m.editor.err, tt.want) {
				t.Errorf("err = %q, want it to mention %q", m.editor.err, tt.want)
			}
		})
	}
}

func TestValidateRefReportsTheOutcome(t *testing.T) {
	const ref = "op://Infra/prod-pg/password"

	t.Run("resolves", func(t *testing.T) {
		m := testModel(t, twoConnections)
		m.updateConnections(key("a"))
		m.editor.busy = "checking " + ref

		m.handleConnFormMsg(refValidatedMsg{gen: m.editor.gen, ref: ref})

		if m.editor.err != "" {
			t.Errorf("err = %q, want none", m.editor.err)
		}
		if !strings.Contains(m.editor.notice, "resolves") {
			t.Errorf("notice = %q, want it to confirm the reference resolves", m.editor.notice)
		}
		if strings.Contains(m.editor.notice, "hunter2") {
			t.Error("the notice leaked a value")
		}
	})

	t.Run("does not resolve", func(t *testing.T) {
		m := testModel(t, twoConnections)
		m.updateConnections(key("a"))
		m.editor.busy = "checking " + ref

		m.handleConnFormMsg(refValidatedMsg{gen: m.editor.gen, ref: ref,
			err: &secrets.OpError{Kind: secrets.KindNotFound, Command: "op read"}})

		if m.editor.notice != "" {
			t.Errorf("notice = %q, want none on a failure", m.editor.notice)
		}
		if !strings.Contains(m.editor.err, "no such vault") {
			t.Errorf("err = %q, want the CLI's reason", m.editor.err)
		}
	})
}

func TestValidateRefStartsTheCheck(t *testing.T) {
	m := testModel(t, twoConnections)
	m.updateConnections(key("a"))
	m.editor.form.values[fldPassword] = "op://Infra/prod-pg/password"

	if cmd := m.validateRef(); cmd == nil {
		t.Fatal("validateRef() did not start a check for a complete reference")
	}
	if m.editor.busy == "" {
		t.Error("the editor is not busy while checking")
	}
}

func TestConcealedFirstKeepsOrderWithinGroups(t *testing.T) {
	got := concealedFirst(testFields)
	var labels []string
	for _, f := range got {
		labels = append(labels, f.Label)
	}
	want := []string{"password", "api-key", "username", "notes"}
	if strings.Join(labels, ",") != strings.Join(want, ",") {
		t.Errorf("order = %v, want %v", labels, want)
	}
	// The input is left alone.
	if testFields[0].Label != "username" {
		t.Error("concealedFirst sorted its argument in place")
	}
}

func TestFieldDetailNeverShowsAValue(t *testing.T) {
	tests := []struct {
		name  string
		field secrets.Field
		want  string
	}{
		{name: "concealed", field: secrets.Field{Label: "password", Type: "CONCEALED"}, want: "concealed"},
		{name: "plain", field: secrets.Field{Label: "username", Type: "STRING"}, want: "string"},
		{
			name:  "sectioned",
			field: secrets.Field{Label: "api-key", Type: "CONCEALED", Section: "tokens"},
			want:  "tokens · concealed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := fieldDetail(tt.field); got != tt.want {
				t.Errorf("fieldDetail() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPickerViewShowsTheStep(t *testing.T) {
	m := openPickerWith(t, testVaults)
	view := m.pickerView()
	for _, want := range []string{"1Password", "choose a vault", "Infra", "Personal", "esc back"} {
		if !strings.Contains(view, want) {
			t.Errorf("the picker view is missing %q:\n%s", want, view)
		}
	}

	m.editor.picker.step = stepFields
	m.editor.picker.fields = testFields
	m.editor.picker.vault, m.editor.picker.item = testVaults[0], testItems[0]
	view = m.pickerView()
	if !strings.Contains(view, "choose the field") {
		t.Errorf("the field step does not say what it wants:\n%s", view)
	}
	if !strings.Contains(view, "concealed") {
		t.Errorf("the field step does not mark concealed fields:\n%s", view)
	}
}

func TestOpFailureOnANonCLIError(t *testing.T) {
	got := opFailure(errors.New("something else broke"))
	if !strings.Contains(got, "something else broke") {
		t.Errorf("opFailure() = %q, want the original message", got)
	}
	if strings.Contains(got, "ctrl+p again") {
		t.Errorf("opFailure() = %q, want no retry advice for an unrelated error", got)
	}
}

func TestCompleteOpRef(t *testing.T) {
	tests := []struct {
		ref  string
		want bool
	}{
		{"op://Infra/prod-pg/password", true},
		{"op://Shared Infra/prod pg/password", true},
		{"op://Infra/prod-pg", false},
		{"op://Infra", false},
		{"op://", false},
		{"op://Infra//password", false},
		{"op://Infra/prod-pg/password/extra", false},
	}
	for _, tt := range tests {
		t.Run(tt.ref, func(t *testing.T) {
			if got := completeOpRef(tt.ref); got != tt.want {
				t.Errorf("completeOpRef(%q) = %v, want %v", tt.ref, got, tt.want)
			}
		})
	}
}
