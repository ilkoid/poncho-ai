package main

import (
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// capturePrint swaps the package-level output sink for a buffer for the duration of
// fn, returning whatever fn wrote. Restores os.Stdout via defer so tests stay isolated.
func capturePrint(fn func()) string {
	var b bytes.Buffer
	orig := out
	out = &b
	defer func() { out = orig }()
	fn()
	return b.String()
}

func TestPrintConnect(t *testing.T) {
	s := capturePrint(func() { printConnect("h", 5432, "db", "u") })
	assert.Contains(t, s, "connecting to h:5432/db as u")
}

func TestPrintScanBegin(t *testing.T) {
	when := time.Date(2026, 6, 18, 14, 32, 1, 0, time.UTC)
	s := capturePrint(func() {
		printScanBegin("SHOW (read-only)", "public", "PlayToday", "", 5, 12, when)
	})
	assert.Contains(t, s, "SHOW (read-only)")
	assert.Contains(t, s, "Scanning 5 table(s), 12 column(s)")
	assert.Contains(t, s, "[started 2026-06-18 14:32:01]")
}

func TestPrintScanProgress(t *testing.T) {
	g := TableGroup{Table: "cards", Cols: []Target{
		{Column: "brand"}, {Column: "title"},
	}}
	s := capturePrint(func() {
		printScanProgress(1, 3, g, 2500*time.Millisecond)
	})
	assert.Contains(t, s, "[1/3] scanning cards (brand, title)")
	assert.Contains(t, s, "2.5s")
}

func TestPrintScanDone(t *testing.T) {
	when := time.Date(2026, 6, 18, 14, 34, 15, 0, time.UTC)
	s := capturePrint(func() {
		printScanDone(73 * time.Second, when)
	})
	assert.Contains(t, s, "Done in")
	assert.Contains(t, s, "[finished 2026-06-18 14:34:15]")
}

func TestPrintApplyBegin_Timestamp(t *testing.T) {
	when := time.Date(2026, 6, 18, 14, 0, 0, 0, time.UTC)
	s := capturePrint(func() {
		printApplyBegin("public", "PlayToday", "[X]", when)
	})
	assert.Contains(t, s, "Beginning single transaction")
	assert.Contains(t, s, "[started 2026-06-18 14:00:00]")
}

func TestPrintApplyDone_Timestamp(t *testing.T) {
	when := time.Date(2026, 6, 18, 14, 0, 5, 0, time.UTC)
	s := capturePrint(func() {
		printApplyDone(100, 2, 5*time.Second, when)
	})
	assert.Contains(t, s, "100 rows updated across 2 tables")
	assert.Contains(t, s, "[5s, finished 2026-06-18 14:00:05]")
}

// TestShowReport_NoDoubleBanner — printShow no longer prints its own banner; the
// banner is printScanBegin's job. So a raw printShow call starts with the filter line,
// not the "===" banner.
func TestShowReport_NoDoubleBanner(t *testing.T) {
	s := capturePrint(func() {
		printShow("public", "PlayToday", nil, []string{"cards"}, nil, nil)
	})
	assert.NotContains(t, s, "fix-scrub-substring")
	assert.Contains(t, s, "--select_tables: cards")
}
