package secrets

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// newTestResolver returns a resolver whose op:// backend is the given stub,
// plus a pointer to the call counter so tests can assert on caching.
func newTestResolver(t *testing.T, value string, err error) (*Resolver, *int) {
	t.Helper()
	calls := 0
	r := NewResolver()
	r.runOp = func(context.Context, string) (string, error) {
		calls++
		return value, err
	}
	return r, &calls
}

func TestResolveLiteralAndEmpty(t *testing.T) {
	r, calls := newTestResolver(t, "", nil)

	for _, in := range []string{"", "hunter2"} {
		got, err := r.Resolve(context.Background(), in)
		if err != nil {
			t.Fatalf("Resolve(%q): %v", in, err)
		}
		if got != in {
			t.Errorf("Resolve(%q) = %q, want it unchanged", in, got)
		}
	}
	if *calls != 0 {
		t.Errorf("op invoked %d times for non-op values, want 0", *calls)
	}
}

func TestResolveEnvReference(t *testing.T) {
	t.Setenv("D9S_TEST_PASS", "s3cret")
	r, _ := newTestResolver(t, "", nil)

	got, err := r.Resolve(context.Background(), "${D9S_TEST_PASS}")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != "s3cret" {
		t.Errorf("Resolve = %q, want %q", got, "s3cret")
	}
}

func TestResolveEnvReferenceUnset(t *testing.T) {
	r, _ := newTestResolver(t, "", nil)

	_, err := r.Resolve(context.Background(), "${D9S_TEST_ABSENT}")
	if err == nil {
		t.Fatal("Resolve succeeded, want an error naming the missing variable")
	}
	if !strings.Contains(err.Error(), "D9S_TEST_ABSENT") {
		t.Errorf("error = %q, want it to name the variable", err)
	}
}

func TestResolveOpReferenceIsCached(t *testing.T) {
	const ref = "op://Infra/pg/password"
	r, calls := newTestResolver(t, "from-1password", nil)

	for i := range 3 {
		got, err := r.Resolve(context.Background(), ref)
		if err != nil {
			t.Fatalf("Resolve #%d: %v", i+1, err)
		}
		if got != "from-1password" {
			t.Errorf("Resolve #%d = %q, want %q", i+1, got, "from-1password")
		}
	}
	if *calls != 1 {
		t.Errorf("op invoked %d times, want 1 (later reads served from cache)", *calls)
	}
}

func TestResolveOpFailureIsNotCached(t *testing.T) {
	wantErr := errors.New("vault is locked")
	r, calls := newTestResolver(t, "", wantErr)

	for i := range 2 {
		if _, err := r.Resolve(context.Background(), "op://Infra/pg/password"); !errors.Is(err, wantErr) {
			t.Fatalf("Resolve #%d error = %v, want %v", i+1, err, wantErr)
		}
	}
	if *calls != 2 {
		t.Errorf("op invoked %d times, want 2 (a failure must not be cached)", *calls)
	}
}

func TestValidateReportsWhetherAReferenceResolves(t *testing.T) {
	const ref = "op://Infra/pg/password"
	locked := &OpError{Kind: KindLocked, Command: "op read"}

	tests := []struct {
		name    string
		ref     string
		opErr   error
		wantErr string
		wantOps int
	}{
		{
			name: "a reference that resolves", ref: ref, wantOps: 1,
		},
		{
			name: "a reference the cli rejects", ref: "op://Infra/typo/password",
			opErr: locked, wantErr: "op://Infra/typo/password does not resolve", wantOps: 1,
		},
		{
			name: "something that is not a reference", ref: "${PGPASS}",
			wantErr: "is not a 1Password reference",
		},
		{
			name: "a literal password", ref: "hunter2",
			wantErr: "is not a 1Password reference",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, calls := newTestResolver(t, theSecret, tt.opErr)

			err := r.Validate(context.Background(), tt.ref)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate: %v", err)
				}
			} else {
				if err == nil {
					t.Fatalf("Validate succeeded, want an error mentioning %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error = %q, want it to mention %q", err, tt.wantErr)
				}
				// Whatever went wrong, the value must not travel with it.
				if strings.Contains(err.Error(), theSecret) {
					t.Errorf("error leaks the secret: %v", err)
				}
			}
			if *calls != tt.wantOps {
				t.Errorf("op invoked %d times, want %d", *calls, tt.wantOps)
			}
		})
	}
}

func TestValidateKeepsTheCLIReasonReachable(t *testing.T) {
	locked := &OpError{Kind: KindLocked, Command: "op read"}
	r, _ := newTestResolver(t, "", locked)

	err := r.Validate(context.Background(), "op://Infra/pg/password")
	if got := ErrorKind(err); got != KindLocked {
		t.Errorf("ErrorKind = %q, want %q: the cause must survive wrapping", got, KindLocked)
	}
	if !strings.Contains(err.Error(), "1Password is locked") {
		t.Errorf("error = %q, want it to carry the CLI's advice", err)
	}
}

func TestValidateDoesNotPopulateTheCache(t *testing.T) {
	const ref = "op://Infra/pg/password"
	r, calls := newTestResolver(t, theSecret, nil)

	if err := r.Validate(context.Background(), ref); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if _, err := r.Resolve(context.Background(), ref); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if *calls != 2 {
		t.Errorf("op invoked %d times, want 2: validation must not seed the cache", *calls)
	}
}

func TestResolveCachesPerReference(t *testing.T) {
	r := NewResolver()
	r.runOp = func(_ context.Context, ref string) (string, error) {
		return "value-of:" + ref, nil
	}

	first, err := r.Resolve(context.Background(), "op://Infra/a/password")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	second, err := r.Resolve(context.Background(), "op://Infra/b/password")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if first == second {
		t.Errorf("distinct references both resolved to %q, want per-reference values", first)
	}
}
