# Change: Add schema browser (tables and columns)

## Why
Seeing structure without hand-writing catalog queries is core DataGrip value;
the MVP only lists databases.

## What Changes
- Database list gains a drill-in schema level: tables (with row estimates) and
  their columns (name, type, nullable, default/key info)
- Redis: key browser by pattern with type/TTL instead of tables
- Selecting a table inserts a ready `SELECT ... LIMIT 100` into the editor

## Impact
- Affected specs: schema-browser (new), engine-adapters (driver interface
  gains ListTables/ListColumns), tui-shell (navigation)
- Affected code: internal/db (all adapters), internal/ui
