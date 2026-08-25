package ui

import (
	"strings"
	"testing"

	"github.com/lergor11/d9s/internal/db"
)

func TestFmtCount(t *testing.T) {
	tests := []struct {
		n    uint64
		want string
	}{
		{0, "0"},
		{950, "950"},
		{1500, "1.5k"},
		{12300, "12.3k"},
		{1230000, "1.23M"},
		{2000000, "2M"},
		{4560000000, "4.56B"},
	}
	for _, tt := range tests {
		if got := fmtCount(tt.n); got != tt.want {
			t.Errorf("fmtCount(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

func TestFmtBytes(t *testing.T) {
	tests := []struct {
		n    uint64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1 KB"},
		{1536, "1.5 KB"},
		{350 * 1024 * 1024, "350 MB"},
		{3 * 1024 * 1024 * 1024, "3 GB"},
	}
	for _, tt := range tests {
		if got := fmtBytes(tt.n); got != tt.want {
			t.Errorf("fmtBytes(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

func TestRunProgressTakeResets(t *testing.T) {
	r := &runProgress{}
	r.Progress(db.Progress{Rows: 10, Bytes: 100})
	r.Progress(db.Progress{Rows: 20, Bytes: 200, TotalRows: 40})
	r.ProfileEvents([]db.ProfileEvent{{Name: "SelectedRows", Value: 20}})
	r.Log(db.LogLine{Level: "information", Text: "hello"})

	if p, ok := r.snapshot(); !ok || p.Rows != 20 {
		t.Fatalf("snapshot = %+v, %v; want the latest totals", p, ok)
	}

	read, events, logs := r.take()
	if read == nil || read.Rows != 20 || read.TotalRows != 40 {
		t.Errorf("take read = %+v, want the final counters", read)
	}
	if len(events) != 1 || len(logs) != 1 {
		t.Errorf("take kept %d events and %d logs, want 1 and 1", len(events), len(logs))
	}

	// The next statement starts clean: nothing seen, nothing carried over.
	if read, events, logs := r.take(); read != nil || events != nil || logs != nil {
		t.Errorf("second take = %+v, %v, %v; want everything cleared", read, events, logs)
	}
	if _, ok := r.snapshot(); ok {
		t.Error("snapshot still reports progress after take")
	}
}

func TestProfileBody(t *testing.T) {
	tests := []struct {
		name string
		res  db.Result
		want []string // substrings the body must carry
	}{
		{
			name: "no events says so instead of an empty table",
			res:  db.Result{},
			want: []string{"no profile events"},
		},
		{
			name: "events render one per row, aligned",
			res: db.Result{ProfileEvents: []db.ProfileEvent{
				{Name: "SelectedRows", Value: 1000},
				{Name: "NetworkSendBytes", Value: 42},
			}},
			want: []string{"SelectedRows     1000", "NetworkSendBytes 42"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := profileBody(tt.res)
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Errorf("profileBody = %q, want it to contain %q", got, want)
				}
			}
		})
	}
}

func TestReadNote(t *testing.T) {
	if got := readNote(db.Result{}); got != "" {
		t.Errorf("readNote without counters = %q, want empty", got)
	}
	res := db.Result{Read: &db.Progress{Rows: 1500000, Bytes: 350 * 1024 * 1024}}
	got := readNote(res)
	for _, want := range []string{"1.5M rows", "350 MB"} {
		if !strings.Contains(got, want) {
			t.Errorf("readNote = %q, want it to mention %q", got, want)
		}
	}
}
