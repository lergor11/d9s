package mcp

import (
	"context"
	"io"
	"strings"
	"sync"

	"github.com/andreim/d9s/internal/secrets"
)

// redactedMarker replaces a secret wherever one is found in outgoing text.
const redactedMarker = "[redacted]"

// minRedactLen is the shortest secret worth scrubbing. A one- or two-character
// value occurs by chance in ordinary output, so replacing it would corrupt
// every response without protecting anything an attacker could not guess.
const minRedactLen = 4

// redactor scrubs resolved secrets out of everything the server writes. Values
// are registered as they are resolved, and every tool response, tool error and
// log line passes through scrub, so a driver that quotes a password in a
// handshake error cannot leak it into the agent's context.
type redactor struct {
	mu     sync.RWMutex
	values []string
}

// add registers a resolved secret. Values too short to scrub safely, and the
// empty value of a connection with no password, are ignored.
func (r *redactor) add(v string) {
	if len(v) < minRedactLen {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, known := range r.values {
		if known == v {
			return
		}
	}
	r.values = append(r.values, v)
}

// scrub replaces every registered secret found in s. A secret that happens to
// spell an ordinary word takes an innocent occurrence with it; losing a word is
// the cheaper mistake.
func (r *redactor) scrub(s string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, v := range r.values {
		s = strings.ReplaceAll(s, v, redactedMarker)
	}
	return s
}

// redactWriter scrubs each record on its way to the log. slog hands a whole
// record to Write, so scrubbing per call cannot cut a secret in half and miss
// it.
type redactWriter struct {
	w   io.Writer
	red *redactor
}

// Write implements io.Writer.
func (rw *redactWriter) Write(p []byte) (int, error) {
	if _, err := io.WriteString(rw.w, rw.red.scrub(string(p))); err != nil {
		return 0, err
	}
	// Report the caller's own length: the scrubbed form has a different one,
	// and a short write would look like an error to slog.
	return len(p), nil
}

// recordingResolver registers every value it resolves before handing it back,
// which is what extends redaction to TLS key material an engine adapter
// resolves for itself, deep inside a connect.
type recordingResolver struct {
	inner *secrets.Resolver
	red   *redactor
}

// Resolve implements db.SecretResolver.
func (r *recordingResolver) Resolve(ctx context.Context, ref string) (string, error) {
	v, err := r.inner.Resolve(ctx, ref)
	if err != nil {
		return "", err
	}
	r.red.add(v)
	return v, nil
}
