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
