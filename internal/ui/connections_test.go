package ui

import (
	"testing"

	"github.com/andreim/d9s/internal/config"
)

func TestTLSLabel(t *testing.T) {
	tests := []struct {
		name string
		conn config.Connection
		want string
	}{
		{
			name: "tunneled connection shows nothing",
			conn: config.Connection{SSH: &config.SSH{Bastion: "b"}},
		},
		{
			name: "explicit disable shows nothing",
			conn: config.Connection{TLS: &config.TLS{Mode: config.TLSDisable}},
		},
		{
			name: "direct connection defaults to an unverified badge",
			conn: config.Connection{},
			want: "unverified",
		},
		{
			name: "require is flagged as unverified",
			conn: config.Connection{TLS: &config.TLS{Mode: config.TLSRequire}},
			want: "unverified",
		},
		{
			name: "verify-ca names its mode",
			conn: config.Connection{TLS: &config.TLS{Mode: config.TLSVerifyCA}},
			want: "verify-ca",
		},
		{
			name: "verify-full names its mode",
			conn: config.Connection{TLS: &config.TLS{Mode: config.TLSVerifyFull}},
			want: "verify-full",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tlsLabel(tt.conn); got != tt.want {
				t.Errorf("tlsLabel() = %q, want %q", got, tt.want)
			}
			if got := len([]rune(stripANSI(tlsBadge(tt.conn, 11)))); got != 11 {
				t.Errorf("tlsBadge() renders %d visible columns, want the requested 11", got)
			}
		})
	}
}

// stripANSI drops escape sequences so a styled cell can be measured.
func stripANSI(s string) string {
	var out []rune
	inEscape := false
	for _, r := range s {
		switch {
		case r == '\x1b':
			inEscape = true
		case inEscape && r == 'm':
			inEscape = false
		case !inEscape:
			out = append(out, r)
		}
	}
	return string(out)
}
