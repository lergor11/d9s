package secrets

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

// theSecret stands in for a real password everywhere below. No result, error,
// or rendered struct in these tests is allowed to contain it.
const theSecret = "correct-horse-battery-staple"

// stubClient returns a client whose CLI is a canned reply per command, keyed
// by the joined arguments, plus the commands it was asked to run.
func stubClient(t *testing.T, replies map[string]string) (*Client, *[]string) {
	t.Helper()
	var calls []string
	c := &Client{run: func(_ context.Context, args ...string) ([]byte, error) {
		line := strings.Join(args, " ")
		calls = append(calls, line)
		reply, ok := replies[line]
		if !ok {
			t.Errorf("unexpected command %q; stubbed: %v", line, keys(replies))
			return nil, errors.New("unstubbed command")
		}
		return []byte(reply), nil
	}}
	return c, &calls
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestVaults(t *testing.T) {
	const reply = `[
	  {"id":"vaultid1","name":"Infra","content_version":7},
	  {"id":"vaultid2","name":"Shared Infra"}
	]`
	c, calls := stubClient(t, map[string]string{"vault list --format json": reply})

	got, err := c.Vaults(context.Background())
	if err != nil {
		t.Fatalf("Vaults: %v", err)
	}
	want := []Vault{{ID: "vaultid1", Name: "Infra"}, {ID: "vaultid2", Name: "Shared Infra"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Vaults() = %+v, want %+v", got, want)
	}
	if len(*calls) != 1 || (*calls)[0] != "vault list --format json" {
		t.Errorf("commands = %v, want one JSON-formatted vault list", *calls)
	}
}

func TestItems(t *testing.T) {
	const reply = `[
	  {"id":"itemid1","title":"prod-pg","category":"LOGIN",
	   "vault":{"id":"vaultid1","name":"Infra"}},
	  {"id":"itemid2","title":"analytics/ch","category":"PASSWORD"}
	]`
	c, calls := stubClient(t, map[string]string{
		"item list --vault vaultid1 --format json": reply,
	})

	got, err := c.Items(context.Background(), "vaultid1")
	if err != nil {
		t.Fatalf("Items: %v", err)
	}
	want := []Item{
		{ID: "itemid1", Title: "prod-pg", Category: "LOGIN"},
		{ID: "itemid2", Title: "analytics/ch", Category: "PASSWORD"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Items() = %+v, want %+v", got, want)
	}
	if len(*calls) != 1 {
		t.Errorf("commands = %v, want exactly one", *calls)
	}
}

// itemGetReply is a trimmed `op item get --format json` body. The values are
// present exactly as the real CLI sends them, so the parser is proven to drop
// them rather than merely never seeing them.
const itemGetReply = `{
  "id":"itemid1",
  "title":"prod-pg",
  "vault":{"id":"vaultid1","name":"Infra"},
  "category":"LOGIN",
  "fields":[
    {"id":"username","type":"STRING","purpose":"USERNAME","label":"username",
     "value":"app","reference":"op://Infra/prod-pg/username"},
    {"id":"password","type":"CONCEALED","purpose":"PASSWORD","label":"password",
     "value":"` + theSecret + `","reference":"op://Infra/prod-pg/password"},
    {"id":"abc123","type":"CONCEALED","label":"access key",
     "section":{"id":"sec1","label":"Access Keys"},
     "value":"` + theSecret + `","reference":"op://Infra/prod-pg/Access Keys/access key"}
  ]
}`

func TestFields(t *testing.T) {
	c, _ := stubClient(t, map[string]string{
		"item get itemid1 --vault vaultid1 --format json": itemGetReply,
	})

	got, err := c.Fields(context.Background(), "vaultid1", "itemid1")
	if err != nil {
		t.Fatalf("Fields: %v", err)
	}
	want := []Field{
		{ID: "username", Label: "username", Type: "STRING", Purpose: "USERNAME",
			Reference: "op://Infra/prod-pg/username"},
		{ID: "password", Label: "password", Type: "CONCEALED", Purpose: "PASSWORD",
			Reference: "op://Infra/prod-pg/password"},
		{ID: "abc123", Label: "access key", Type: "CONCEALED", Section: "Access Keys",
			Reference: "op://Infra/prod-pg/Access Keys/access key"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Fields() = %+v, want %+v", got, want)
	}
}

func TestFieldsNeverCarryTheValue(t *testing.T) {
	c, _ := stubClient(t, map[string]string{
		"item get itemid1 --vault vaultid1 --format json": itemGetReply,
	})

	got, err := c.Fields(context.Background(), "vaultid1", "itemid1")
	if err != nil {
		t.Fatalf("Fields: %v", err)
	}
	// Rendering the whole result catches a value smuggled into any field,
	// including one added later.
	if rendered := fmt.Sprintf("%+v", got); strings.Contains(rendered, theSecret) {
		t.Errorf("the parsed fields contain the secret value: %s", rendered)
	}
	if len(got) != 3 {
		t.Fatalf("got %d fields, want 3", len(got))
	}
	if !got[1].Concealed() || got[0].Concealed() {
		t.Errorf("Concealed() = %v for a password and %v for a username, want true and false",
			got[1].Concealed(), got[0].Concealed())
	}
}

func TestFieldsRejectsUnreadableOutputWithoutQuotingIt(t *testing.T) {
	// A body the decoder chokes on, holding a secret: the error must describe
	// the failure without echoing what it was reading.
	broken := `{"fields":[{"id":"password","value":"` + theSecret + `"` // truncated
	c, _ := stubClient(t, map[string]string{
		"item get itemid1 --vault vaultid1 --format json": broken,
	})

	_, err := c.Fields(context.Background(), "vaultid1", "itemid1")
	if err == nil {
		t.Fatal("Fields succeeded on truncated JSON, want an error")
	}
	if strings.Contains(err.Error(), theSecret) {
		t.Errorf("error quotes the secret: %v", err)
	}
	if !strings.Contains(err.Error(), "op item get") {
		t.Errorf("error = %q, want it to name the command", err)
	}
}

func TestListingsRejectUnreadableOutput(t *testing.T) {
	tests := []struct {
		name    string
		command string
		list    func(*Client) error
		want    string
	}{
		{
			name:    "vaults",
			command: "vault list --format json",
			list: func(c *Client) error {
				_, err := c.Vaults(context.Background())
				return err
			},
			want: "parsing op vault list output",
		},
		{
			name:    "items",
			command: "item list --vault vaultid1 --format json",
			list: func(c *Client) error {
				_, err := c.Items(context.Background(), "vaultid1")
				return err
			},
			want: "parsing op item list output",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := stubClient(t, map[string]string{tt.command: "not json at all"})
			err := tt.list(c)
			if err == nil {
				t.Fatalf("listing succeeded on junk output, want an error mentioning %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want it to mention %q", err, tt.want)
			}
		})
	}
}

func TestClientPropagatesCLIFailures(t *testing.T) {
	want := &OpError{Kind: KindLocked, Command: "op vault list"}
	c := &Client{run: func(context.Context, ...string) ([]byte, error) { return nil, want }}

	_, err := c.Vaults(context.Background())
	if !errors.Is(err, want) {
		t.Fatalf("Vaults error = %v, want the CLI error passed through", err)
	}
	if got := ErrorKind(err); got != KindLocked {
		t.Errorf("ErrorKind = %q, want %q", got, KindLocked)
	}
}

func TestReference(t *testing.T) {
	infra := Vault{ID: "vaultid1", Name: "Infra"}
	shared := Vault{ID: "vaultid2", Name: "Shared Infra"}
	slashed := Vault{ID: "vaultid3", Name: "Infra/EU"}
	item := Item{ID: "itemid1", Title: "prod-pg"}

	tests := []struct {
		name    string
		vault   Vault
		item    Item
		field   Field
		want    string
		wantErr string
	}{
		{
			name:  "the reference the cli reported wins",
			vault: infra, item: item,
			field: Field{ID: "password", Label: "password", Reference: "op://Infra/prod-pg/password"},
			want:  "op://Infra/prod-pg/password",
		},
		{
			name:  "built from names",
			vault: infra, item: item,
			field: Field{ID: "password", Label: "password"},
			want:  "op://Infra/prod-pg/password",
		},
		{
			// Spaces are legal in a reference and must survive verbatim.
			name:  "a vault name with a space keeps the space",
			vault: shared, item: item,
			field: Field{ID: "password", Label: "password"},
			want:  "op://Shared Infra/prod-pg/password",
		},
		{
			name:  "a section becomes a path segment",
			vault: infra, item: item,
			field: Field{ID: "abc123", Label: "access key", Section: "Access Keys"},
			want:  "op://Infra/prod-pg/Access Keys/access key",
		},
		{
			// A slash would split the path, so the id stands in for the name.
			name:  "a slash in the vault name falls back to its id",
			vault: slashed, item: item,
			field: Field{ID: "password", Label: "password"},
			want:  "op://vaultid3/prod-pg/password",
		},
		{
			name:  "a slash in the item title falls back to its id",
			vault: infra, item: Item{ID: "itemid2", Title: "analytics/ch"},
			field: Field{ID: "password", Label: "password"},
			want:  "op://Infra/itemid2/password",
		},
		{
			name:  "a slash in the field label falls back to its id",
			vault: infra, item: item,
			field: Field{ID: "abc123", Label: "add/more"},
			want:  "op://Infra/prod-pg/abc123",
		},
		{
			name:  "every awkward name at once",
			vault: slashed, item: Item{ID: "itemid2", Title: "analytics/ch"},
			field: Field{ID: "abc123", Label: "add/more"},
			want:  "op://vaultid3/itemid2/abc123",
		},
		{
			name:  "an unusable field name with no id is refused",
			vault: infra, item: item,
			field:   Field{Label: "add/more"},
			wantErr: `1Password field "add/more" contains a character`,
		},
		{
			// A section has no identifier to fall back on.
			name:  "an unusable section name is refused",
			vault: infra, item: item,
			field:   Field{ID: "abc123", Label: "access key", Section: "keys/aws"},
			wantErr: `1Password section "keys/aws" contains a character`,
		},
		{
			name:  "a field with neither name nor id is refused",
			vault: infra, item: item,
			field:   Field{},
			wantErr: "1Password field has neither a name nor an id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Reference(tt.vault, tt.item, tt.field)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("Reference() = %q, want an error mentioning %q", got, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error = %q, want it to mention %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Reference: %v", err)
			}
			if got != tt.want {
				t.Errorf("Reference() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestClassifyOpError(t *testing.T) {
	tests := []struct {
		name       string
		stderr     string
		want       OpErrorKind
		wantAdvice string
	}{
		{
			name:       "not signed in",
			stderr:     `[ERROR] 2026/08/21 10:04:05 you are not currently signed in. Please run "op signin --help" for instructions`,
			want:       KindNotSignedIn,
			wantAdvice: "not signed in to 1Password",
		},
		{
			name:       "locked desktop app",
			stderr:     "[ERROR] 2026/08/21 10:04:05 error initializing client: the 1Password app is locked",
			want:       KindLocked,
			wantAdvice: "1Password is locked; unlock the desktop app",
		},
		{
			name:       "authorization dismissed",
			stderr:     "[ERROR] 2026/08/21 10:04:05 authorization prompt dismissed, please try again",
			want:       KindLocked,
			wantAdvice: "1Password is locked",
		},
		{
			name:       "unknown item",
			stderr:     `[ERROR] 2026/08/21 10:04:05 "typo" isn't an item. Specify the item with its UUID, name, or domain.`,
			want:       KindNotFound,
			wantAdvice: "1Password has no such vault, item, or field",
		},
		{
			name:       "unknown vault",
			stderr:     `[ERROR] 2026/08/21 10:04:05 "Nope" isn't a vault in this account`,
			want:       KindNotFound,
			wantAdvice: "1Password has no such vault",
		},
		{
			name:       "anything else keeps the cli wording",
			stderr:     "[ERROR] 2026/08/21 10:04:05 something nobody predicted",
			want:       KindUnknown,
			wantAdvice: "op vault list failed",
		},
		{
			name:   "no stderr at all",
			stderr: "",
			want:   KindUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := classifyOpError("op vault list", &exec.ExitError{Stderr: []byte(tt.stderr)})
			if got := ErrorKind(err); got != tt.want {
				t.Errorf("ErrorKind = %q, want %q", got, tt.want)
			}
			if tt.wantAdvice != "" && !strings.Contains(err.Error(), tt.wantAdvice) {
				t.Errorf("error = %q, want it to advise %q", err, tt.wantAdvice)
			}
			// The log stamp is noise; the CLI's sentence is not.
			if strings.Contains(err.Error(), "[ERROR]") {
				t.Errorf("error = %q, want the CLI log prefix stripped", err)
			}
			if tt.stderr != "" && !strings.Contains(err.Error(), "op: ") {
				t.Errorf("error = %q, want it to carry the CLI's own message", err)
			}
		})
	}
}

func TestErrorKindOfAForeignError(t *testing.T) {
	if got := ErrorKind(errors.New("something else entirely")); got != KindUnknown {
		t.Errorf("ErrorKind of a non-CLI error = %q, want %q", got, KindUnknown)
	}
	if got := ErrorKind(nil); got != KindUnknown {
		t.Errorf("ErrorKind(nil) = %q, want %q", got, KindUnknown)
	}
}

func TestNotInstalledIsItsOwnState(t *testing.T) {
	// LookPath failures never reach classifyOpError, so check the message the
	// runner builds directly.
	err := &OpError{Kind: KindNotInstalled, Command: "op vault list", cause: exec.ErrNotFound}
	if got := ErrorKind(err); got != KindNotInstalled {
		t.Errorf("ErrorKind = %q, want %q", got, KindNotInstalled)
	}
	for _, want := range []string{"not found on PATH", "Integrate with 1Password CLI"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to mention %q", err, want)
		}
	}
	if !errors.Is(err, exec.ErrNotFound) {
		t.Error("the underlying lookup error is not unwrappable")
	}
}
