package onec

import (
	"context"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Rests domain — warehouse stock levels from 1C RESTs API
//
// Separate sub-domain within pkg/onec/: RestsWriter and RestsSource are
// independent from the goods/prices/PIM Writer and Source interfaces (ISP).
// A rests-only downloader should not need SaveGoods, SaveSKUs, etc.
// ---------------------------------------------------------------------------

// RestsRow maps to onec_rests table (storage-facing DTO, no JSON tags).
// snapshot_date is provided by caller, not embedded.
type RestsRow struct {
	GoodGUID    string
	SKUGUID     string
	StorageGUID string
	StorageName string
	Stock       int
	Reserv      int
	Free        int
	FirstStage  bool
}

// ---------------------------------------------------------------------------
// Storage filter
// ---------------------------------------------------------------------------

// RestsStorageFilter defines warehouse filtering rules.
// A storage row passes if it matches GUID OR name pattern (union).
// If both lists are empty, all storages are accepted.
type RestsStorageFilter struct {
	GUIDs        []string // exact storage_guid match (case-insensitive)
	NamePatterns []string // case-insensitive substring match on storage_name
}

// Matches returns true if the storage row passes the filter.
// Union logic: passes if GUID matches OR name matches.
func (f RestsStorageFilter) Matches(guid, name string) bool {
	if len(f.GUIDs) == 0 && len(f.NamePatterns) == 0 {
		return true
	}
	for _, g := range f.GUIDs {
		if strings.EqualFold(g, guid) {
			return true
		}
	}
	lower := strings.ToLower(name)
	for _, p := range f.NamePatterns {
		if strings.Contains(lower, strings.ToLower(p)) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Interfaces
// ---------------------------------------------------------------------------

// RestsSource fetches warehouse stock levels from 1C RESTs API.
// Implemented by HTTPRestsSource (real API) and MockRestsSource (--mock).
//
// FetchRests takes a RestsWriter parameter to enable batch writes during
// streaming decode — constant memory regardless of payload size (~414K rows).
type RestsSource interface {
	// FetchRests streams from the 1C RESTs API, applies storage filter during
	// parse, and writes rows to the writer in batches of 500.
	FetchRests(ctx context.Context, url string, filter RestsStorageFilter,
		writer RestsWriter, snapshotDate string) (goodsCount, totalSaved, filteredOut int, err error)
}

// RestsWriter is the persistence interface for 1C rests data.
// Focused (ISP) — separate from the goods/prices/PIM Writer.
type RestsWriter interface {
	// SaveRests saves a batch of rests rows for a given snapshot date.
	SaveRests(ctx context.Context, rows []RestsRow, snapshotDate string) (int, error)
	// CountRests returns total number of rests rows.
	CountRests(ctx context.Context) (int, error)
	// CleanRests deletes all rows from onec_rests table.
	CleanRests(ctx context.Context) error
	// PurgeOldRestsSnapshots deletes snapshots older than retentionDays from yesterday.
	PurgeOldRestsSnapshots(ctx context.Context, retentionDays int) (int, error)
}

// ---------------------------------------------------------------------------
// Options & Result
// ---------------------------------------------------------------------------

// RestsDownloadOptions configures a RestsDownloader run.
type RestsDownloadOptions struct {
	// RestURL is the 1C RESTs API endpoint (with basic auth in URL).
	RestURL string

	// SnapshotDate for rests rows (YYYY-MM-DD), set by caller.
	SnapshotDate string

	// StorageFilter selects which warehouses to keep (empty = all).
	StorageFilter RestsStorageFilter

	// RetentionDays keeps N daily snapshots counting from yesterday.
	// 0 = no retention (keep forever).
	RetentionDays int

	// Clean wipes onec_rests table before loading.
	Clean bool

	// DryRun skips all DB writes (HTTP fetch still happens).
	DryRun bool

	// OnProgress callback for status messages (nil = silent).
	OnProgress func(msg string)
}

// RestsDownloadResult holds the outcome of a rests download run.
type RestsDownloadResult struct {
	GoodsCount  int
	TotalSaved  int
	FilteredOut int
	TotalInDB   int
	Duration    time.Duration
}
