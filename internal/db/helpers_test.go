package db

import (
	"database/sql/driver"
	"reflect"
	"testing"
	"time"
)

type nullString struct {
	value string
	valid bool
}

func (n nullString) Value() (driver.Value, error) {
	if !n.valid {
		return nil, nil
	}
	return n.value, nil
}

func TestStringify(t *testing.T) {
	ts := time.Date(2026, 8, 20, 15, 4, 5, 0, time.UTC)
	answer := 42

	tests := []struct {
		name string
		in   any
		want string
	}{
		{name: "nil renders as NULL", in: nil, want: "NULL"},
		{name: "string passes through", in: "hello", want: "hello"},
		{name: "bytes become text", in: []byte("bytes"), want: "bytes"},
		{name: "time uses RFC3339", in: ts, want: "2026-08-20T15:04:05Z"},
		{name: "int", in: 7, want: "7"},
		{name: "float", in: 1.5, want: "1.5"},
		{name: "bool", in: true, want: "true"},
		{name: "pointer is dereferenced", in: &answer, want: "42"},
		{name: "nil pointer renders as NULL", in: (*int)(nil), want: "NULL"},
		{name: "valuer is unwrapped", in: nullString{value: "v", valid: true}, want: "v"},
		{name: "null valuer renders as NULL", in: nullString{}, want: "NULL"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stringify(tt.in); got != tt.want {
				t.Errorf("stringify(%#v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestSplitCommandLine(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    []string
		wantErr bool
	}{
		{name: "plain words", in: "GET key", want: []string{"GET", "key"}},
		{name: "collapses whitespace", in: "  SET   k   v  ", want: []string{"SET", "k", "v"}},
		{name: "double quotes keep spaces", in: `SET k "hello world"`, want: []string{"SET", "k", "hello world"}},
		{name: "single quotes keep spaces", in: `SET k 'hello world'`, want: []string{"SET", "k", "hello world"}},
		{name: "quotes inside a word", in: `SET k pre"fix post"`, want: []string{"SET", "k", `prefix post`}},
		{name: "backslash escape", in: `SET k a\ b`, want: []string{"SET", "k", "a b"}},
		{name: "empty quoted argument", in: `SET k ""`, want: []string{"SET", "k", ""}},
		{name: "empty input", in: "   ", want: nil},
		{name: "unterminated double quote", in: `SET k "oops`, wantErr: true},
		{name: "unterminated single quote", in: `SET k 'oops`, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := splitCommandLine(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("splitCommandLine(%q) succeeded with %v, want an error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("splitCommandLine(%q): %v", tt.in, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("splitCommandLine(%q) = %#v, want %#v", tt.in, got, tt.want)
			}
		})
	}
}

func TestRedisReplyRows(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want [][]string
	}{
		{name: "nil", in: nil, want: [][]string{{"(nil)"}}},
		{name: "scalar", in: "OK", want: [][]string{{"OK"}}},
		{name: "integer", in: int64(3), want: [][]string{{"3"}}},
		{name: "empty array", in: []any{}, want: [][]string{{"(empty array)"}}},
		{
			name: "flat array is numbered",
			in:   []any{"a", "b"},
			want: [][]string{{"1) a"}, {"2) b"}},
		},
		{
			name: "nested array keeps the outer index",
			in:   []any{"0", []any{"k1", "k2"}},
			want: [][]string{{"1) 0"}, {"2) 1) k1"}, {"2) 2) k2"}},
		},
		{
			name: "map entries are sorted",
			in:   map[any]any{"b": 2, "a": 1},
			want: [][]string{{"a: 1"}, {"b: 2"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := redisReplyRows(tt.in, ""); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("redisReplyRows(%#v) = %#v, want %#v", tt.in, got, tt.want)
			}
		})
	}
}

func TestQualifyAndSplitQualified(t *testing.T) {
	tests := []struct {
		schema, name, qualified string
	}{
		{schema: "public", name: "users", qualified: "users"},
		{schema: "default", name: "events", qualified: "events"},
		{schema: "", name: "users", qualified: "users"},
		{schema: "billing", name: "invoices", qualified: "billing.invoices"},
	}

	for _, tt := range tests {
		t.Run(tt.qualified, func(t *testing.T) {
			if got := qualify(tt.schema, tt.name); got != tt.qualified {
				t.Errorf("qualify(%q, %q) = %q, want %q", tt.schema, tt.name, got, tt.qualified)
			}
		})
	}

	if schema, name := splitQualified("billing.invoices", "public"); schema != "billing" || name != "invoices" {
		t.Errorf(`splitQualified("billing.invoices") = (%q, %q), want ("billing", "invoices")`, schema, name)
	}
	if schema, name := splitQualified("users", "public"); schema != "public" || name != "users" {
		t.Errorf(`splitQualified("users") = (%q, %q), want ("public", "users")`, schema, name)
	}
}
