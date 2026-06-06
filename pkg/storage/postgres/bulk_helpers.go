package postgres

import (
	"fmt"
	"strconv"
	"strings"
)

// pgMaxParams is the maximum number of parameters per query in PostgreSQL.
const pgMaxParams = 65535

// BuildMultiRowInsert constructs a multi-row INSERT query with PostgreSQL
// positional parameters ($1, $2, ..., $N).
//
// Parameters:
//   - prefix:     SQL up to and including the column list, e.g. "INSERT INTO t (a,b) VALUES "
//   - onConflict: ON CONFLICT clause, e.g. "ON CONFLICT (id) DO UPDATE SET ..."
//   - rowCount:   number of value tuples
//   - colCount:   number of parameters per tuple
//
// Panics if rowCount*colCount > 65535 (PostgreSQL parameter limit).
//
// Example output for rowCount=2, colCount=3:
//
//	"INSERT INTO t (a,b,c) VALUES ($1,$2,$3), ($4,$5,$6) ON CONFLICT ..."
func BuildMultiRowInsert(prefix, onConflict string, rowCount, colCount int) string {
	if rowCount <= 0 || colCount <= 0 {
		panic(fmt.Sprintf("BuildMultiRowInsert: invalid rowCount=%d colCount=%d", rowCount, colCount))
	}
	total := rowCount * colCount
	if total > pgMaxParams {
		panic(fmt.Sprintf("BuildMultiRowInsert: %d rows × %d cols = %d params exceeds PG limit %d",
			rowCount, colCount, total, pgMaxParams))
	}

	// Estimate ~8 bytes per placeholder ("$12345, ") + 4 bytes per row ("(), ")
	estimated := len(prefix) + len(onConflict) + rowCount*(colCount*8+4)
	var sb strings.Builder
	sb.Grow(estimated)
	sb.WriteString(prefix)

	idx := 1
	for row := 0; row < rowCount; row++ {
		if row > 0 {
			sb.WriteString(", ")
		}
		sb.WriteByte('(')
		for col := 0; col < colCount; col++ {
			if col > 0 {
				sb.WriteString(", ")
			}
			sb.WriteByte('$')
			sb.WriteString(strconv.Itoa(idx))
			idx++
		}
		sb.WriteByte(')')
	}

	sb.WriteByte(' ')
	sb.WriteString(onConflict)
	return sb.String()
}
