package secrets

import (
	"errors"
	"os/exec"
	"regexp"
	"strings"
)

// OpErrorKind classifies why a 1Password CLI call failed, so callers can act
// on the cause instead of matching on message text.
type OpErrorKind string

// The failure states worth telling apart. Anything unrecognised stays
// KindUnknown and carries the CLI's own message through.
const (
	// KindUnknown is a failure with no more specific cause identified.
	KindUnknown OpErrorKind = "unknown"
	// KindNotInstalled means the op binary is not on PATH.
	KindNotInstalled OpErrorKind = "not-installed"
	// KindNotSignedIn means no 1Password account session is available.
	KindNotSignedIn OpErrorKind = "not-signed-in"
	// KindLocked means 1Password is installed and signed in but locked, or
	// the desktop app declined to authorize the request.
	KindLocked OpErrorKind = "locked"
	// KindNotFound means the named vault, item, or field does not exist.
	KindNotFound OpErrorKind = "not-found"
)

// OpError is a failed 1Password CLI call. Its message states what the user can
// do about it and appends whatever the CLI reported. Detail never holds a
// secret: it comes from stderr, which carries diagnostics, not values.
type OpError struct {
	Kind    OpErrorKind
	Command string // the command that failed, e.g. "op vault list"
	Detail  string // the CLI's own message, cleaned of its log prefix
	cause   error
}

// Error renders the advice for the failure, followed by the CLI's message.
func (e *OpError) Error() string {
	msg := e.advice()
	if e.Detail == "" {
		return msg
	}
	return msg + " (op: " + e.Detail + ")"
}

// Unwrap exposes the underlying exec error.
func (e *OpError) Unwrap() error { return e.cause }

// advice is the actionable half of the message, chosen by kind.
func (e *OpError) advice() string {
	switch e.Kind {
	case KindNotInstalled:
		return "1Password CLI (op) not found on PATH; install it and enable " +
			`"Integrate with 1Password CLI" in the desktop app`
	case KindNotSignedIn:
		return `not signed in to 1Password; run "op signin", or enable ` +
			`"Integrate with 1Password CLI" in the desktop app`
	case KindLocked:
		return "1Password is locked; unlock the desktop app and try again"
	case KindNotFound:
		return "1Password has no such vault, item, or field"
	default:
		return e.Command + " failed"
	}
}

// ErrorKind reports how a 1Password call failed, or KindUnknown for an error
// that did not come from the CLI at all.
func ErrorKind(err error) OpErrorKind {
	var opErr *OpError
	if errors.As(err, &opErr) {
		return opErr.Kind
	}
	return KindUnknown
}

// opLogPrefix matches the "[ERROR] 2026/08/21 10:04:05 " stamp the CLI puts in
// front of every diagnostic line.
var opLogPrefix = regexp.MustCompile(`(?im)^\[[a-z]+\]\s+\d{4}/\d{2}/\d{2}\s+\d{2}:\d{2}:\d{2}\s+`)

// classifyOpError turns a failed exec into an OpError, reading the cause out
// of whatever the CLI printed on stderr.
func classifyOpError(command string, err error) error {
	detail := ""
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		detail = cleanOpMessage(string(exitErr.Stderr))
	}
	return &OpError{
		Kind:    classifyOpMessage(detail),
		Command: command,
		Detail:  detail,
		cause:   err,
	}
}

// cleanOpMessage strips the CLI's log stamps and folds the remaining lines
// into one, so the message reads as a sentence inside a larger error.
func cleanOpMessage(stderr string) string {
	stripped := opLogPrefix.ReplaceAllString(stderr, "")
	var lines []string
	for _, line := range strings.Split(stripped, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "; ")
}

// classifyOpMessage matches the CLI's wording for the states worth handling
// differently. The checks are deliberately loose and ordered from most to
// least specific, because the exact phrasing varies between op releases; an
// unrecognised message still reaches the user verbatim as KindUnknown.
func classifyOpMessage(detail string) OpErrorKind {
	msg := strings.ToLower(detail)
	switch {
	case msg == "":
		return KindUnknown
	case containsAny(msg,
		"not currently signed in",
		"not signed in",
		"no account found",
		"op signin",
		"session expired",
		"no saved sign-in"):
		return KindNotSignedIn
	case containsAny(msg,
		"is locked",
		"app is locked",
		"authorization prompt",
		"authorization was denied",
		"connecting to desktop app",
		"cli integration",
		"connection refused"):
		return KindLocked
	case containsAny(msg,
		"isn't an item",
		"isn't a vault",
		"isn't a field",
		"no item matches",
		"no vault matches",
		"doesn't exist",
		"not found",
		"could not find"):
		return KindNotFound
	default:
		return KindUnknown
	}
}

func containsAny(s string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}
