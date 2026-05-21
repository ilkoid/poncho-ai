// Package dllog provides unified console output for downloader utilities.
// It offers consistent formatting: timestamped progress lines with batch
// counts and ETA, colored errors/done messages, and clean startup headers.
//
// ANSI colors are auto-disabled when stdout is piped or redirected.
package dllog

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/utils"
)

// HeaderField is a key-value pair for the startup banner.
type HeaderField struct {
	Key   string // "DB", "Period", "API Key"
	Value string
}

// terminal check — cached on first call
var isTerm bool

func init() {
	fi, _ := os.Stdout.Stat()
	isTerm = fi != nil && fi.Mode()&os.ModeCharDevice != 0
}

const (
	colorRed   = 31
	colorGreen = 32
	sepWidth   = 72
)

// PrintHeader prints the startup banner with title and fields.
//
//	Output:
//	========================================================================
//	WB Promotion V2 Downloader
//	========================================================================
//	DB:       /var/db/wb-sales.db
//	Period:   2026-05-14 -> 2026-05-21
//	API Key:  eyJh...Kx8f
//	========================================================================
func PrintHeader(title string, fields ...HeaderField) {
	sep := strings.Repeat("=", sepWidth)
	fmt.Println(sep)
	fmt.Println(title)
	fmt.Println(sep)

	maxKeyLen := 0
	for _, f := range fields {
		if len(f.Key) > maxKeyLen {
			maxKeyLen = len(f.Key)
		}
	}

	for _, f := range fields {
		pad := strings.Repeat(" ", maxKeyLen-len(f.Key))
		fmt.Printf("%s:%s  %s\n", f.Key, pad, f.Value)
	}

	fmt.Println(sep)
}

// Progress prints a timestamped batch progress line.
//
//	dllog.Progress(5, 12, "bids", "18 saved", start)
//	→ 14:32:15 [5/12 42% ETA ~8m] bids: 18 saved
func Progress(batch, total int, label, msg string, start time.Time) {
	ts := time.Now().Format("15:04:05")
	batchInfo := formatBatch(batch, total, start)
	fmt.Printf("%s [%s] %s: %s\n", ts, batchInfo, label, msg)
}

// Error prints a timestamped error line (red when terminal).
//
//	14:33:45 error: failed to save bids: disk full
func Error(format string, args ...any) {
	ts := time.Now().Format("15:04:05")
	msg := fmt.Sprintf(format, args...)
	fmt.Println(colorize(colorRed, fmt.Sprintf("%s error: %s", ts, msg)))
}

// Done prints a timestamped completion line (green when terminal).
//
//	14:35:22 done: 210 bids from 12 campaigns (3m 7s)
func Done(dur time.Duration, format string, args ...any) {
	ts := time.Now().Format("15:04:05")
	msg := fmt.Sprintf(format, args...)
	fmt.Println(colorize(colorGreen, fmt.Sprintf("%s done: %s (%s)", ts, msg, utils.FormatDuration(dur))))
}

// Log prints a timestamped info line (no color). For single-shot operations.
//
//	14:32:15 balance: net=22035, bonus=1500
func Log(format string, args ...any) {
	ts := time.Now().Format("15:04:05")
	msg := fmt.Sprintf(format, args...)
	fmt.Printf("%s %s\n", ts, msg)
}

// formatBatch returns batch progress string.
// Returns "5/12 42% ETA ~8m" when total > 0 and start is non-zero.
// Returns "5/12" when total > 0 but no start time.
// Returns just "5" when total is 0.
func formatBatch(cur, total int, start time.Time) string {
	if total <= 0 {
		return fmt.Sprintf("%d", cur)
	}

	pct := cur * 100 / total
	s := fmt.Sprintf("%d/%d %d%%", cur, total, pct)

	if !start.IsZero() && cur > 0 {
		elapsed := time.Since(start)
		remaining := time.Duration(float64(elapsed) / float64(cur) * float64(total-cur))
		s += fmt.Sprintf(" ETA ~%s", utils.FormatDuration(remaining))
	}

	return s
}

// colorize wraps s in ANSI escape codes when stdout is a terminal.
func colorize(code int, s string) string {
	if !isTerm {
		return s
	}
	return fmt.Sprintf("\033[%dm%s\033[0m", code, s)
}
