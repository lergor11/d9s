package db

import (
	"context"
	"fmt"
)

// IndexDef is one index of a table, as the engine defines it.
type IndexDef struct {
	// Name is the index's name, or a role like "PRIMARY KEY" where the engine
	// has no separate name.
	Name string
	// Definition is the engine's own definition text.
	Definition string
}

// TableDetail is the verbose description `\d+` adds to a table's columns.
// Fields the engine does not report stay empty.
type TableDetail struct {
	// Size is the table's on-disk size, human-readable.
	Size string
	// Comment is the table's comment.
	Comment string
	// Indexes are the table's indexes.
	Indexes []IndexDef
}

// Describer is implemented by drivers that can add the detail `\d+` shows.
// It is kept out of Driver the way Streamer is, so an engine without a richer
// catalog is unaffected.
type Describer interface {
	// DescribeTable reports a table's size, comment and indexes.
	DescribeTable(ctx context.Context, table string) (TableDetail, error)
}

// prettyBytes renders a byte count the way catalogs usually do: 1536 → "1.5 kB".
func prettyBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d bytes", n)
	}
	div, exp := uint64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	v := float64(n) / float64(div)
	return fmt.Sprintf("%.1f %cB", v, "kMGTPE"[exp])
}
