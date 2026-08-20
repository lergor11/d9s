// Package secrets resolves connection passwords: op:// references via the
// 1Password CLI, ${ENV} references from the environment, literals as-is.
// Resolved values live only in process memory.
package secrets

import (
	"context"
	"fmt"
	"os"
	"os/exec"
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

func runOpCLI(ctx context.Context, ref string) (string, error) {
	path, err := exec.LookPath("op")
	if err != nil {
		return "", fmt.Errorf("1Password CLI (op) not found on PATH; install it and enable 'Integrate with 1Password CLI' in the desktop app")
	}
	cmd := exec.CommandContext(ctx, path, "read", "--no-newline", ref)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return "", fmt.Errorf("op read %s: %s", ref, strings.TrimSpace(string(ee.Stderr)))
		}
		return "", fmt.Errorf("op read %s: %w", ref, err)
	}
	return strings.TrimRight(string(out), "\r\n"), nil
}
