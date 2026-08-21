package session

import (
	"strings"
	"testing"

	"github.com/lergor11/d9s/internal/config"
)

func TestFind(t *testing.T) {
	cfg := &config.Config{Connections: []config.Connection{
		{Name: "prod-pg", Type: config.Postgres},
		{Name: "cache", Type: config.Redis},
	}}

	tests := []struct {
		name    string
		cfg     *config.Config
		lookup  string
		wantErr []string
	}{
		{name: "the first connection", cfg: cfg, lookup: "prod-pg"},
		{name: "a later connection", cfg: cfg, lookup: "cache"},
		{
			name: "a typo names what is configured", cfg: cfg, lookup: "prod_pg",
			wantErr: []string{`"prod_pg"`, "prod-pg, cache"},
		},
		{
			name: "matching is exact", cfg: cfg, lookup: "PROD-PG",
			wantErr: []string{`"PROD-PG"`},
		},
		{
			name: "nothing configured at all", cfg: &config.Config{}, lookup: "prod-pg",
			wantErr: []string{"no connections"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn, err := Find(tt.cfg, tt.lookup)
			if len(tt.wantErr) == 0 {
				if err != nil {
					t.Fatalf("Find(%q): %v", tt.lookup, err)
				}
				if conn.Name != tt.lookup {
					t.Errorf("Find(%q) returned %q", tt.lookup, conn.Name)
				}
				return
			}
			if err == nil {
				t.Fatalf("Find(%q) succeeded, want an error", tt.lookup)
			}
			for _, want := range tt.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %q, want it to mention %q", err, want)
				}
			}
		})
	}
}
