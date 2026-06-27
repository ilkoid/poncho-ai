package main

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// CSV audit row "kind" values. The CSV is a change log: FIX (rewritten), ERROR
// (per-card failure), WB_*/RUN (batch + run outcomes). Skipped cards are NOT logged
// — "skipped" means nothing changed; their count lives in the RUN summary line and
// their per-row status in the staging table.
const (
	kindFix     = "FIX"      // card rewritten: old→new dimensions
	kindError   = "ERROR"    // build/send failure for one card
	kindWBError = "WB_ERROR" // WB validation error line (per affected vendor_code)
	kindWBOK    = "WB_OK"    // batch passed the read-after-write check
	kindWBStop  = "WB_STOP"  // run halted after a batch produced validation errors
	kindRun     = "RUN"      // run-level summary line
)

// auditHeader is the CSV header. No weight column (weight is preserved, not logged).
var auditHeader = []string{
	"date", "time", "kind", "nm_id", "vendor_code",
	"old_l", "old_w", "old_h", "new_l", "new_w", "new_h",
	"status", "detail",
}

// Auditor appends audit rows to a daily-rotated CSV file under dir.
// It is the durable, append-only history; the staging table is working state.
type Auditor struct {
	dir    string
	mu     sync.Mutex
	f      *os.File
	w      *csv.Writer
	curDay string
}

// NewAuditor creates an Auditor and ensures the log directory exists.
func NewAuditor(dir string) (*Auditor, error) {
	if dir == "" {
		dir = "logs"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create audit dir %q: %w", dir, err)
	}
	return &Auditor{dir: dir}, nil
}

// Close flushes and closes the current file (if any).
func (a *Auditor) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.closeLocked()
}

func (a *Auditor) closeLocked() error {
	if a.w != nil {
		a.w.Flush()
	}
	if a.f != nil {
		err := a.f.Close()
		a.f = nil
		a.w = nil
		return err
	}
	return nil
}

// openDay opens (or rotates to) today's CSV file, writing the header on creation.
func (a *Auditor) openDay() error {
	day := time.Now().Format("2006-01-02")
	if a.f != nil && day == a.curDay {
		return nil
	}
	if err := a.closeLocked(); err != nil {
		return fmt.Errorf("rotate audit file: %w", err)
	}
	path := filepath.Join(a.dir, "penalties-dims-"+day+".csv")
	newFile := false
	if _, err := os.Stat(path); os.IsNotExist(err) {
		newFile = true
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open audit file %q: %w", path, err)
	}
	a.f = f
	a.w = csv.NewWriter(f)
	a.curDay = day
	if newFile {
		if err := a.w.Write(auditHeader); err != nil {
			return fmt.Errorf("write audit header: %w", err)
		}
	}
	return nil
}

// write appends one row. dims (old/new) and nmID/vendorCode are filled for FIX/SKIP.
func (a *Auditor) write(kind string, r stagedRow, detail string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.openDay(); err != nil {
		fmt.Fprintf(os.Stderr, "audit open failed: %v\n", err)
		return
	}
	now := time.Now()
	row := []string{
		now.Format("2006-01-02"), now.Format("15:04:05"), kind,
		fmt.Sprintf("%d", r.NmID), r.VendorCode,
		fmt.Sprintf("%g", r.OldLength), fmt.Sprintf("%g", r.OldWidth), fmt.Sprintf("%g", r.OldHeight),
		fmt.Sprintf("%g", r.NewLength), fmt.Sprintf("%g", r.NewWidth), fmt.Sprintf("%g", r.NewHeight),
		r.Status, detail,
	}
	if err := a.w.Write(row); err != nil {
		fmt.Fprintf(os.Stderr, "audit write failed: %v\n", err)
		return
	}
	a.w.Flush()
}

// Fix logs a successfully rewritten card (status='applied').
func (a *Auditor) Fix(r stagedRow) {
	r.Status = "applied"
	a.write(kindFix, r, "")
}

// Error logs a per-card build/send failure.
func (a *Auditor) Error(r stagedRow, msg string) {
	r.Status = "error"
	a.write(kindError, r, msg)
}

// WBError logs one WB validation error line for a vendor_code in a batch.
func (a *Auditor) WBError(batchNo int, vc string, nmID int, errText string) {
	a.write(kindWBError, stagedRow{NmID: nmID, VendorCode: vc},
		fmt.Sprintf("batch %d: %s", batchNo, errText))
}

// WBOK logs a clean read-after-write check for a batch.
func (a *Auditor) WBOK(batchNo, count int) {
	a.write(kindWBOK, stagedRow{}, fmt.Sprintf("batch %d: %d cards, no validation errors", batchNo, count))
}

// WBStop logs that the run halted after a batch produced validation errors.
func (a *Auditor) WBStop(batchNo, errorCount int) {
	a.write(kindWBStop, stagedRow{},
		fmt.Sprintf("halted after batch %d: %d validation errors", batchNo, errorCount))
}

// Run logs a run-level summary line.
func (a *Auditor) Run(msg string) {
	a.write(kindRun, stagedRow{}, msg)
}
