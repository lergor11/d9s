package secrets

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// Vault is one 1Password vault the account can read.
type Vault struct {
	ID   string
	Name string
}

// Item is one entry in a vault. Only what the picker needs to show is kept.
type Item struct {
	ID       string
	Title    string
	Category string // e.g. LOGIN, PASSWORD, SERVER
}

// Field is one field of an item. It deliberately has no value: the picker
// writes references, never secrets, so the value is dropped while parsing and
// never leaves this package.
type Field struct {
	ID        string
	Label     string
	Type      string // e.g. CONCEALED, STRING
	Purpose   string // e.g. PASSWORD, USERNAME, NOTES; empty for custom fields
	Section   string // section label; empty when the field is not in one
	Reference string // op:// reference as reported by the CLI, when it gives one
}

// Concealed reports whether the field holds a hidden value, which is what a
// password picker should offer first.
func (f Field) Concealed() bool {
	return strings.EqualFold(f.Type, "CONCEALED") || strings.EqualFold(f.Purpose, "PASSWORD")
}

// opRunner executes an op subcommand and returns its stdout. It is the seam
// tests replace so no test ever runs the real CLI.
type opRunner func(ctx context.Context, args ...string) ([]byte, error)

// Client browses 1Password through the op command-line tool. It reads only;
// nothing here creates, edits, or unlocks anything.
type Client struct {
	run opRunner
}

// NewClient returns a client backed by the op CLI on PATH.
func NewClient() *Client { return &Client{run: runOpJSON} }

// Vaults lists the vaults the signed-in account can read.
func (c *Client) Vaults(ctx context.Context) ([]Vault, error) {
	out, err := c.run(ctx, "vault", "list", "--format", "json")
	if err != nil {
		return nil, err
	}
	var wire []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(out, &wire); err != nil {
		return nil, parseError("op vault list", err)
	}
	vaults := make([]Vault, 0, len(wire))
	for _, v := range wire {
		vaults = append(vaults, Vault{ID: v.ID, Name: v.Name})
	}
	return vaults, nil
}

// Items lists the items of one vault, named by id or by name.
func (c *Client) Items(ctx context.Context, vault string) ([]Item, error) {
	out, err := c.run(ctx, "item", "list", "--vault", vault, "--format", "json")
	if err != nil {
		return nil, err
	}
	var wire []struct {
		ID       string `json:"id"`
		Title    string `json:"title"`
		Category string `json:"category"`
	}
	if err := json.Unmarshal(out, &wire); err != nil {
		return nil, parseError("op item list", err)
	}
	items := make([]Item, 0, len(wire))
	for _, i := range wire {
		items = append(items, Item{ID: i.ID, Title: i.Title, Category: i.Category})
	}
	return items, nil
}

// Fields lists the fields of one item. The CLI returns the values along with
// them; they are discarded here and never surface in the result or an error.
func (c *Client) Fields(ctx context.Context, vault, item string) ([]Field, error) {
	out, err := c.run(ctx, "item", "get", item, "--vault", vault, "--format", "json")
	if err != nil {
		return nil, err
	}
	var wire struct {
		Fields []struct {
			ID        string `json:"id"`
			Label     string `json:"label"`
			Type      string `json:"type"`
			Purpose   string `json:"purpose"`
			Reference string `json:"reference"`
			Section   *struct {
				Label string `json:"label"`
			} `json:"section"`
		} `json:"fields"`
	}
	if err := json.Unmarshal(out, &wire); err != nil {
		// The body holds secret values, so only the decoder's own message
		// travels with this error, never the input it choked on.
		return nil, parseError("op item get", err)
	}
	fields := make([]Field, 0, len(wire.Fields))
	for _, f := range wire.Fields {
		field := Field{
			ID:        f.ID,
			Label:     f.Label,
			Type:      f.Type,
			Purpose:   f.Purpose,
			Reference: f.Reference,
		}
		if f.Section != nil {
			field.Section = f.Section.Label
		}
		fields = append(fields, field)
	}
	return fields, nil
}

// parseError reports unreadable CLI output without quoting that output, which
// for `op item get` would mean copying secrets into an error message.
func parseError(command string, err error) error {
	return fmt.Errorf("parsing %s output: %w", command, err)
}

// referenceSafe matches the characters 1Password accepts in a secret
// reference: letters, digits, "-", "_", "." and spaces. Everything else — a
// slash above all, which would split the path — forces the component to be
// named by its unique identifier instead.
var referenceSafe = regexp.MustCompile(`^[a-zA-Z0-9._\- ]+$`)

// Reference returns the op:// reference for one field of an item.
//
// It prefers the reference the CLI itself reported, and otherwise assembles
// one, substituting a component's unique identifier whenever its name contains
// a character a reference cannot carry. Names with spaces are kept verbatim:
// the reference is passed to the CLI as a single argument, never through a
// shell, so "op://Shared Infra/prod-pg/password" needs no quoting of its own.
func Reference(vault Vault, item Item, field Field) (string, error) {
	if field.Reference != "" {
		return field.Reference, nil
	}
	parts := make([]string, 0, 4)
	for _, component := range []struct{ kind, name, id string }{
		{"vault", vault.Name, vault.ID},
		{"item", item.Title, item.ID},
	} {
		part, err := referencePart(component.kind, component.name, component.id)
		if err != nil {
			return "", err
		}
		parts = append(parts, part)
	}
	if field.Section != "" {
		// A section can only ever be named, so an awkward label is fatal here
		// rather than something an identifier could paper over.
		part, err := referencePart("section", field.Section, "")
		if err != nil {
			return "", err
		}
		parts = append(parts, part)
	}
	part, err := referencePart("field", field.Label, field.ID)
	if err != nil {
		return "", err
	}
	parts = append(parts, part)
	return "op://" + strings.Join(parts, "/"), nil
}

// referencePart returns name when a reference can express it, the identifier
// when it cannot, and an error when neither works.
func referencePart(kind, name, id string) (string, error) {
	if referenceSafe.MatchString(name) {
		return name, nil
	}
	if referenceSafe.MatchString(id) {
		return id, nil
	}
	if name == "" {
		return "", fmt.Errorf("1Password %s has neither a name nor an id to build a reference from", kind)
	}
	return "", fmt.Errorf("1Password %s %q contains a character a secret reference cannot express, and has no usable id to stand in for it", kind, name)
}

// runOpJSON runs one op subcommand and returns its stdout.
func runOpJSON(ctx context.Context, args ...string) ([]byte, error) {
	path, err := exec.LookPath("op")
	if err != nil {
		return nil, &OpError{Kind: KindNotInstalled, Command: commandLine(args), cause: err}
	}
	cmd := exec.CommandContext(ctx, path, args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, classifyOpError(commandLine(args), err)
	}
	return out, nil
}

func commandLine(args []string) string { return "op " + strings.Join(args, " ") }
