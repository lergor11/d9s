// Package secrets resolves connection passwords: op:// references via the
// 1Password CLI, ${ENV} references from the environment, literals as-is.
// Resolved values live only in process memory.
package secrets

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/andreim/d9s/internal/config"
)

// Resolver resolves secret references with in-memory caching.
type Resolver struct {
	mu    sync.Mutex
	cache map[string]string
	// runOp is swappable in tests.
	runOp func(ctx context.Context, ref string) (string, error)
}

// NewResolver returns a resolver backed by the 1Password CLI.
func NewResolver() *Resolver {
	return &Resolver{cache: map[string]string{}, runOp: runOpCLI}
}

// Resolve turns a config password value into the actual secret.
func (r *Resolver) Resolve(ctx context.Context, v string) (string, error) {
	switch {
	case v == "":
		return "", nil
	case config.IsEnvRef(v):
		name := v[2 : len(v)-1]
		val, ok := os.LookupEnv(name)
		if !ok {
			return "", fmt.Errorf("environment variable %s is not set", name)
		}
		return val, nil
	case config.IsOpRef(v):
		r.mu.Lock()
		cached, ok := r.cache[v]
		r.mu.Unlock()
		if ok {
			return cached, nil
		}
		val, err := r.runOp(ctx, v)
		if err != nil {
			return "", err
		}
		r.mu.Lock()
		r.cache[v] = val
		r.mu.Unlock()
		return val, nil
	default:
		return v, nil // literal (config.Load already warned)
	}
}

// Validate reports whether ref resolves, without revealing what it names. The
// secret is read and discarded: it is never returned, logged, or quoted in the
// error, which carries the reference and the CLI's reason instead.
//
// Validation always asks the CLI, bypassing the cache, so it answers whether
// the reference resolves now rather than whether it once did.
func (r *Resolver) Validate(ctx context.Context, ref string) error {
	if !config.IsOpRef(ref) {
		return fmt.Errorf("%q is not a 1Password reference (want op://vault/item/field)", ref)
	}
	if _, err := r.runOp(ctx, ref); err != nil {
		return fmt.Errorf("%s does not resolve: %w", ref, err)
	}
	return nil
}

func runOpCLI(ctx context.Context, ref string) (string, error) {
	out, err := runOpJSON(ctx, "read", "--no-newline", ref)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(out), "\r\n"), nil
}
