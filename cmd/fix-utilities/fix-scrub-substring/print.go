package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// out is the output sink (os.Stdout in production, overrideable in tests).
var out io.Writer = os.Stdout

// printBanner prints the mode header line.
func printBanner(mode, schema, read, write string) {
	if write == "" {
		fmt.Fprintf(out, "=== fix-scrub-substring — %s ===\n", mode)
		fmt.Fprintf(out, "schema: %s | read_substring: %q (case-insensitive)\n", schema, read)
	} else {
		fmt.Fprintf(out, "=== fix-scrub-substring — %s ===\n", mode)
		fmt.Fprintf(out, "schema: %s | %q → %q (case-insensitive search, verbatim replacement)\n",
			schema, read, write)
	}
}

// restrictDesc renders the effective table filter for the header line.
func restrictDesc(include, selectTables, exclude []string) string {
	switch {
	case len(selectTables) > 0 && len(include) > 0:
		return "tables: include ∩ --select_tables = " + strings.Join(selectTables, ",")
	case len(selectTables) > 0:
		return "--select_tables: " + strings.Join(selectTables, ",")
	case len(include) > 0:
		return "include_tables: " + strings.Join(include, ",")
	default:
		return "tables: all"
	}
}

// printShow prints the read-only match report.
// printShow prints the read-only match report. The banner is printed up-front by
// printScanBegin (so the user sees it before the long scan), not here.
func printShow(schema, read string, include, selectTables, exclude []string, updates []Update) {
	fmt.Fprintf(out, "%s\n", restrictDesc(include, selectTables, exclude))

	if len(updates) == 0 {
		fmt.Fprintln(out, "No matching columns found.")
		return
	}

	tw, cw := maxTableWidth(updates), maxColumnWidth(updates)
	fmt.Fprintf(out, "%-*s  %-*s  %-8s  %s\n", tw, "TABLE", cw, "COLUMN", "TYPE", "MATCHES")
	var rows, tables int
	seen := map[string]bool{}
	for _, u := range updates {
		fmt.Fprintf(out, "%-*s  %-*s  %-8s  %d\n", tw, u.Target.Table, cw, u.Target.Column, u.Target.Type, u.Matches)
		rows += u.Matches
		if !seen[u.Target.Table] {
			seen[u.Target.Table] = true
			tables++
		}
	}
	fmt.Fprintf(out, "Total: %d columns in %d tables, %d matching rows.\n", len(updates), tables, rows)
}

// printDryRun prints the planned UPDATEs with before→after samples. No writes. The
// banner is printed up-front by printScanBegin, not here.
func printDryRun(schema, read, write string, updates []Update) {
	if len(updates) == 0 {
		fmt.Fprintln(out, "No matching columns found — nothing to do.")
		return
	}
	fmt.Fprintf(out, "Planned UPDATEs: %d  (single transaction, all-or-nothing)\n\n", len(updates))

	var totalRows int
	for i, u := range updates {
		totalRows += u.Matches
		fmt.Fprintf(out, "[%d/%d] %s.%s  →  %d rows\n",
			i+1, len(updates), u.Target.Table, u.Target.Column, u.Matches)
		fmt.Fprintf(out, "      regexp_replace(%s, %q → %q, 'gi')\n", u.Target.Column, read, write)
		for _, sm := range u.Samples {
			fmt.Fprintf(out, "      sample:  %q  →  %q\n", sm.Before, sm.After)
		}
	}
	fmt.Fprintf(out, "\nTotal affected rows: %d across %d columns.\n", totalRows, len(updates))
	fmt.Fprintln(out, "DRY-RUN: no rows were modified. Re-run with --apply to execute.")
}

// printConnect prints a one-line connection attempt BEFORE openPool, so a hang on
// the network/connect is distinguishable from a hang during the scan.
func printConnect(host string, port int, db, user string) {
	fmt.Fprintf(out, "connecting to %s:%d/%s as %s…\n", host, port, db, user)
}

// printScanBegin prints the mode banner plus the scan plan and a wall-clock start
// timestamp. Fired after discovery so the table/column counts are accurate. The
// banner is printed here (not in printShow/printDryRun) so it appears BEFORE the scan.
func printScanBegin(mode, schema, read, write string, nTables, nCols int, startedAt time.Time) {
	printBanner(mode, schema, read, write)
	fmt.Fprintf(out, "Scanning %d table(s), %d column(s) for matches…  [started %s]\n",
		nTables, nCols, startedAt.Format("2006-01-02 15:04:05"))
}

// printScanProgress fires BEFORE each table's count scan. Because stdout is unbuffered
// (os.Stdout), the last printed line is always the table currently being scanned — a
// hang shows up as a table name that never advances to the next index. elapsed is
// measured from the scan-wide start.
func printScanProgress(idx, total int, g TableGroup, elapsed time.Duration) {
	cols := make([]string, len(g.Cols))
	for i, c := range g.Cols {
		cols[i] = c.Column
	}
	fmt.Fprintf(out, "  [%d/%d] scanning %s (%s)…  [elapsed %s]\n",
		idx, total, g.Table, strings.Join(cols, ", "), elapsed.Round(time.Millisecond))
}

// printScanDone prints the total scan duration and wall-clock finish time after the
// report, mirroring printApplyDone for the read-only modes.
func printScanDone(elapsed time.Duration, finishedAt time.Time) {
	fmt.Fprintf(out, "Done in %s  [finished %s]\n",
		elapsed.Round(time.Millisecond), finishedAt.Format("2006-01-02 15:04:05"))
}

// printApplyBegin prints the start banner for the destructive run, with a wall-clock
// start timestamp so a long --apply is anchored in time.
func printApplyBegin(schema, read, write string, startedAt time.Time) {
	printBanner("APPLY (destructive)", schema, read, write)
	fmt.Fprintf(out, "Beginning single transaction…  [started %s]\n", startedAt.Format("2006-01-02 15:04:05"))
}

// printApplyProgress prints one line per updated table group inside the tx, with
// the columns touched and elapsed-since-start so a long multi-million-row scrub is
// observably progressing.
func printApplyProgress(idx, total int, g TableGroup, rows int, elapsed time.Duration) {
	cols := make([]string, len(g.Cols))
	for i, c := range g.Cols {
		cols[i] = c.Column
	}
	fmt.Fprintf(out, "  [%d/%d] %s (%s)  →  %d rows  [elapsed %s]\n",
		idx, total, g.Table, strings.Join(cols, ", "), rows, elapsed.Round(time.Millisecond))
}

// printApplyDone prints the commit summary. rows = updated rows (a row matching in
// several columns counts once); tablesTouched = tables that had ≥1 match. Includes
// total elapsed and wall-clock finish time.
func printApplyDone(totalRows, tablesTouched int, elapsed time.Duration, finishedAt time.Time) {
	fmt.Fprintf(out, "COMMIT. Total: %d rows updated across %d tables.  [%s, finished %s]\n",
		totalRows, tablesTouched, elapsed.Round(time.Millisecond), finishedAt.Format("2006-01-02 15:04:05"))
}

func maxTableWidth(updates []Update) int {
	const min = 5 // len("TABLE")
	w := min
	for _, u := range updates {
		if len(u.Target.Table) > w {
			w = len(u.Target.Table)
		}
	}
	return w
}

func maxColumnWidth(updates []Update) int {
	const min = 6 // len("COLUMN")
	w := min
	for _, u := range updates {
		if len(u.Target.Column) > w {
			w = len(u.Target.Column)
		}
	}
	return w
}
