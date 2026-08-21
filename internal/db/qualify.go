package db

import "strings"

// qualify renders a schema-qualified table name, dropping the schema when it
// is the engine's default one.
func qualify(schema, name string) string {
	if schema == "" || schema == "public" || schema == "default" {
		return name
	}
	return schema + "." + name
}

// splitQualified reverses qualify: it separates "schema.table" into its parts,
// falling back to def when the name carries no schema.
func splitQualified(table, def string) (schema, name string) {
	if s, n, ok := strings.Cut(table, "."); ok {
		return s, n
	}
	return def, table
}
