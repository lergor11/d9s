package ui

import (
	"strings"
	"testing"

	"github.com/lergor11/d9s/internal/config"
)

func TestFormTLSFieldRoundTrips(t *testing.T) {
	f := editConnForm(config.Connection{
		Name: "ch", Type: config.ClickHouse, Host: "h",
		TLS: &config.TLS{Mode: config.TLSDisable, CA: "/etc/ca.pem"},
	})
	if got := f.value(fldTLS); got != "disable" {
		t.Fatalf("prefilled TLS = %q, want disable", got)
	}
	conn, err := f.connection()
	if err != nil {
		t.Fatal(err)
	}
	if conn.TLS == nil || conn.TLS.Mode != config.TLSDisable {
		t.Errorf("saved TLS = %+v, want disable", conn.TLS)
	}
	if conn.TLS.CA != "/etc/ca.pem" {
		t.Errorf("CA = %q, want the field the form does not show to survive", conn.TLS.CA)
	}

	// Blank means "no block", so the connection follows the default again.
	f2 := newConnForm()
	f2.values[fldName], f2.values[fldHost] = "x", "h"
	conn2, err := f2.connection()
	if err != nil {
		t.Fatal(err)
	}
	if conn2.TLS != nil {
		t.Errorf("TLS = %+v, want nil when the field is blank", conn2.TLS)
	}
}

func TestFormShowsTLSField(t *testing.T) {
	m := &model{width: 100, height: 30, editor: &connEditor{form: newConnForm()}}
	if got := m.editorView(); !strings.Contains(got, "TLS") {
		t.Errorf("the form does not show a TLS field:\n%s", got)
	}
}

func TestProtocolFieldOnlyForClickHouse(t *testing.T) {
	f := newConnForm()
	if f.applies(fldProtocol) {
		t.Error("the protocol field is offered on postgres, where it means nothing")
	}

	f.values[fldType] = string(config.ClickHouse)
	if !f.applies(fldProtocol) {
		t.Error("the protocol field is hidden on clickhouse, where it is the point")
	}

	// Moving down from Database must land on Protocol here, and skip it on
	// an engine that has none.
	f.sel = fldDatabase
	f.move(1)
	if f.sel != fldProtocol {
		t.Errorf("selection = %v, want the protocol field on clickhouse", f.sel)
	}
	f.values[fldType] = string(config.Postgres)
	f.sel = fldDatabase
	f.move(1)
	if f.sel != fldTLS {
		t.Errorf("selection = %v, want the protocol field skipped on postgres", f.sel)
	}
}

func TestProtocolSavedOnlyForClickHouse(t *testing.T) {
	f := editConnForm(config.Connection{Name: "ch", Type: config.ClickHouse, Host: "h"})
	f.values[fldProtocol] = string(config.ProtocolHTTP)
	conn, err := f.connection()
	if err != nil {
		t.Fatal(err)
	}
	if conn.Protocol != config.ProtocolHTTP {
		t.Errorf("protocol = %q, want http", conn.Protocol)
	}

	f2 := editConnForm(config.Connection{Name: "pg", Type: config.Postgres, Host: "h"})
	f2.values[fldProtocol] = string(config.ProtocolHTTP)
	conn2, err := f2.connection()
	if err != nil {
		t.Fatal(err)
	}
	if conn2.Protocol != "" {
		t.Errorf("protocol = %q on postgres, want it left unset so the file stays valid", conn2.Protocol)
	}
}
