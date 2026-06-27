// Package cardupdate provides a reusable SDK for updating WB card content
// via POST /content/v2/cards/update.
//
// This package enforces full-card updates: WB API fully replaces cards,
// so partial payloads destroy data. All updates go through LoadFullCard()
// which loads all card fields from SQLite before building the API payload.
//
// Typical usage:
//
//	updater := cardupdate.NewCardUpdater(db)
//	card, err := updater.LoadFullCard(ctx, nmID)
//	item := cardupdate.ToUpdateItem(card)
//	// mutate item as needed (e.g., set new dimensions)...
//	cardupdate.ApplyBatch(ctx, client, cfg, items, buildFn)
package cardupdate

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// FullCardData holds all data needed for a safe WB card update.
// WB API fully overwrites cards — every field must be present in the update payload.
// Loading through this struct guarantees no field is accidentally zeroed.
type FullCardData struct {
	NmID            int
	VendorCode      string
	Brand           string
	Title           string
	Description     string
	Dimensions      wb.CardDimensions
	Characteristics []CardChar
	Sizes           []wb.CardSize
}

// CardUpdater loads card data from SQLite and helps build safe WB update payloads.
type CardUpdater struct {
	db *sql.DB
}

// NewCardUpdater creates a new CardUpdater backed by the given database.
func NewCardUpdater(db *sql.DB) *CardUpdater {
	return &CardUpdater{db: db}
}

// LoadFullCard loads all card data from SQLite for a safe WB update.
// Queries brand/title/description, dimensions, characteristics, and sizes
// in a single pass for the given nmID.
func (u *CardUpdater) LoadFullCard(ctx context.Context, nmID int) (FullCardData, error) {
	var card FullCardData
	var dimValid int

	err := u.db.QueryRowContext(ctx, `
		SELECT nm_id, COALESCE(vendor_code,''), COALESCE(brand,''),
		       COALESCE(title,''), COALESCE(description,''),
		       COALESCE(dim_length,0), COALESCE(dim_width,0),
		       COALESCE(dim_height,0), COALESCE(dim_weight_brutto,0),
		       COALESCE(dim_is_valid,0)
		FROM cards WHERE nm_id = ?
	`, nmID).Scan(
		&card.NmID, &card.VendorCode, &card.Brand,
		&card.Title, &card.Description,
		&card.Dimensions.Length, &card.Dimensions.Width,
		&card.Dimensions.Height, &card.Dimensions.WeightBrutto,
		&dimValid,
	)
	if err != nil {
		return FullCardData{}, fmt.Errorf("load card core for nm_id=%d: %w", nmID, err)
	}
	card.Dimensions.IsValid = dimValid != 0

	chars, err := u.loadCharacteristics(ctx, nmID)
	if err != nil {
		return FullCardData{}, fmt.Errorf("load chars: %w", err)
	}
	card.Characteristics = chars

	sizes, err := u.loadSizes(ctx, nmID)
	if err != nil {
		return FullCardData{}, fmt.Errorf("load sizes: %w", err)
	}
	card.Sizes = sizes

	return card, nil
}

// LoadBulkCharacteristics loads characteristics for multiple cards.
// Returns map[nmID][]CardChar.
func (u *CardUpdater) LoadBulkCharacteristics(ctx context.Context, nmIDs []int) (map[int][]CardChar, error) {
	if len(nmIDs) == 0 {
		return nil, nil
	}
	ph := placeholders(len(nmIDs))
	args := intSliceToAny(nmIDs)

	query := fmt.Sprintf(`
		SELECT nm_id, char_id, name, json_value
		FROM card_characteristics
		WHERE nm_id IN (%s)
	`, ph)

	rows, err := u.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query characteristics: %w", err)
	}
	defer rows.Close()

	result := make(map[int][]CardChar)
	for rows.Next() {
		var nmID, charID int
		var name, jsonValue string
		if err := rows.Scan(&nmID, &charID, &name, &jsonValue); err != nil {
			return nil, fmt.Errorf("scan characteristic: %w", err)
		}
		result[nmID] = append(result[nmID], CardChar{
			CharID: charID,
			Name:   name,
			Value:  jsonValue,
		})
	}
	return result, rows.Err()
}

// LoadBulkSizes loads sizes for multiple cards.
// Returns map[nmID][]CardSize.
func (u *CardUpdater) LoadBulkSizes(ctx context.Context, nmIDs []int) (map[int][]wb.CardSize, error) {
	if len(nmIDs) == 0 {
		return nil, nil
	}
	ph := placeholders(len(nmIDs))
	args := intSliceToAny(nmIDs)

	query := fmt.Sprintf(`
		SELECT nm_id, chrt_id, tech_size, wb_size, skus_json
		FROM card_sizes
		WHERE nm_id IN (%s)
	`, ph)

	rows, err := u.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query sizes: %w", err)
	}
	defer rows.Close()

	result := make(map[int][]wb.CardSize)
	for rows.Next() {
		var nmID, chrtID int
		var techSize, wbSize, skusJSON string
		if err := rows.Scan(&nmID, &chrtID, &techSize, &wbSize, &skusJSON); err != nil {
			return nil, fmt.Errorf("scan size: %w", err)
		}
		var skus []string
		if err := json.Unmarshal([]byte(skusJSON), &skus); err != nil {
			skus = []string{}
		}
		result[nmID] = append(result[nmID], wb.CardSize{
			ChrtID:   chrtID,
			TechSize: techSize,
			WBSize:   wbSize,
			Skus:     skus,
		})
	}
	return result, rows.Err()
}

// ToUpdateItem converts FullCardData to wb.CardUpdateItem ready for the WB API.
// Unwraps characteristic values from DB format (JSON arrays) to WB API format (scalars).
//
// KNOWN GAP — kizMarked: POST /content/v2/cards/update also accepts `kizMarked`
// (default false; confirmation of "Честный ЗНАК" marking). This round-trip does NOT
// send it — the field is absent from ProductCard, cards table, and CardUpdateItem —
// so a marking-required card (needKiz=true, e.g. clothing) can have its kizMarked
// reset to false on rewrite. Swagger: docs/wb_api_swagger/02-products.yaml,
// /content/v2/cards/update schema (kizMarked). Fix later by adding KizMarked to
// ProductCard + CardUpdateItem + carrying it through FullCardData/ToUpdateItem
// (source: either a cards.kiz_marked column populated by the downloader, or a live
// GetCardsByNmIDs fetch). Tracked in memory cardupdate_kizmarked_gap. See also the
// penalties-dims fixer README ("Известные ограничения").
func ToUpdateItem(card FullCardData) wb.CardUpdateItem {
	chars := make([]wb.CardUpdateCharc, 0, len(card.Characteristics))
	for _, c := range card.Characteristics {
		var val any
		if err := json.Unmarshal([]byte(c.Value), &val); err != nil {
			val = c.Value
		}
		val = UnwrapValue(val)
		chars = append(chars, wb.CardUpdateCharc{ID: c.CharID, Value: val})
	}

	return wb.CardUpdateItem{
		NmID:            card.NmID,
		VendorCode:      card.VendorCode,
		Brand:           card.Brand,
		Title:           card.Title,
		Description:     card.Description,
		Dimensions:      &card.Dimensions,
		Characteristics: chars,
		Sizes:           card.Sizes,
	}
}

// --- Single-row helpers (used by LoadFullCard) ---

func (u *CardUpdater) loadCharacteristics(ctx context.Context, nmID int) ([]CardChar, error) {
	rows, err := u.db.QueryContext(ctx, `
		SELECT char_id, COALESCE(name,''), json_value FROM card_characteristics WHERE nm_id = ?
	`, nmID)
	if err != nil {
		return nil, fmt.Errorf("query chars: %w", err)
	}
	defer rows.Close()

	var chars []CardChar
	for rows.Next() {
		var c CardChar
		if err := rows.Scan(&c.CharID, &c.Name, &c.Value); err != nil {
			return nil, fmt.Errorf("scan char: %w", err)
		}
		chars = append(chars, c)
	}
	return chars, rows.Err()
}

func (u *CardUpdater) loadSizes(ctx context.Context, nmID int) ([]wb.CardSize, error) {
	rows, err := u.db.QueryContext(ctx, `
		SELECT chrt_id, tech_size, wb_size, skus_json FROM card_sizes WHERE nm_id = ?
	`, nmID)
	if err != nil {
		return nil, fmt.Errorf("query sizes: %w", err)
	}
	defer rows.Close()

	var sizes []wb.CardSize
	for rows.Next() {
		var s wb.CardSize
		var skusJSON string
		if err := rows.Scan(&s.ChrtID, &s.TechSize, &s.WBSize, &skusJSON); err != nil {
			return nil, fmt.Errorf("scan size: %w", err)
		}
		if err := json.Unmarshal([]byte(skusJSON), &s.Skus); err != nil {
			s.Skus = nil
		}
		sizes = append(sizes, s)
	}
	return sizes, rows.Err()
}

// ============================================================================
// Batch processing
// ============================================================================

// BatchItem is a staged item ready for batch processing.
type BatchItem struct {
	NmID       int
	VendorCode string
}

// ApplyBatchResult holds the outcome of a batch apply run.
type ApplyBatchResult struct {
	Sent   int
	Failed int
}

// batchConfig holds optional settings for ApplyBatch.
type batchConfig struct {
	dryRun   bool
	onStatus func(nmID int, status string, errMsg string)
	onDryRun func(batchNum int, items []wb.CardUpdateItem)
}

// BatchOption configures ApplyBatch behavior.
type BatchOption func(*batchConfig)

// WithDryRun enables dry-run mode: payloads are passed to onDryRun instead of being sent.
func WithDryRun() BatchOption {
	return func(c *batchConfig) { c.dryRun = true }
}

// WithStatusCallback sets a callback called for each item after batch processing.
// status is "ok" or "error". errMsg is non-empty only for errors.
func WithStatusCallback(fn func(nmID int, status string, errMsg string)) BatchOption {
	return func(c *batchConfig) { c.onStatus = fn }
}

// WithDryRunCallback sets a callback for dry-run mode to display payloads.
func WithDryRunCallback(fn func(batchNum int, items []wb.CardUpdateItem)) BatchOption {
	return func(c *batchConfig) { c.onDryRun = fn }
}

// ApplyBatch sends card updates to WB API in batches with rate limiting.
//
// The caller provides a buildFn that maps each BatchItem to a CardUpdateItem.
// This function handles:
//   - Chunking into batches of cfg.BatchSize
//   - Context cancellation between batches
//   - Calling wb.Client.UpdateCards per batch
//   - Sleep between batches (cfg.IntervalSeconds)
//   - Error/status tracking via callbacks
func ApplyBatch(
	ctx context.Context,
	client *wb.Client,
	cfg WBUpdateConfig,
	items []BatchItem,
	buildFn func(ctx context.Context, item BatchItem) (wb.CardUpdateItem, error),
	opts ...BatchOption,
) (ApplyBatchResult, error) {
	var bc batchConfig
	for _, o := range opts {
		o(&bc)
	}

	result := ApplyBatchResult{}
	bs := cfg.BatchSize

	for i := 0; i < len(items); i += bs {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		default:
		}

		end := min(i+bs, len(items))
		chunk := items[i:end]

		// Build update items for this batch.
		updateItems := make([]wb.CardUpdateItem, 0, len(chunk))
		for _, r := range chunk {
			item, err := buildFn(ctx, r)
			if err != nil {
				log.Printf("  ERROR build payload nm_id=%d: %v", r.NmID, err)
				if bc.onStatus != nil {
					bc.onStatus(r.NmID, "error", err.Error())
				}
				result.Failed++
				continue
			}
			updateItems = append(updateItems, item)
		}

		if len(updateItems) == 0 {
			continue
		}

		// Dry-run mode: show payloads without sending.
		if bc.dryRun {
			if bc.onDryRun != nil {
				bc.onDryRun(i+1, updateItems)
			}
			result.Sent += len(updateItems)
			continue
		}

		// Send to WB API.
		_, errorText, err := client.UpdateCards(ctx, wb.CardsBaseURL,
			cfg.RatePerMin, cfg.RateBurst, updateItems)
		if err != nil {
			log.Printf("batch %d-%d: %v (WB: %s)", i+1, end, err, errorText)
			for _, r := range chunk {
				if bc.onStatus != nil {
					bc.onStatus(r.NmID, "error", err.Error())
				}
			}
			result.Failed += len(chunk)
		} else {
			for _, r := range chunk {
				if bc.onStatus != nil {
					bc.onStatus(r.NmID, "ok", "")
				}
			}
			result.Sent += len(chunk)
			log.Printf("  batch %d-%d: OK (%d cards)", i+1, end, len(chunk))
		}

		if i+bs < len(items) && cfg.IntervalSeconds > 0 {
			time.Sleep(time.Duration(cfg.IntervalSeconds) * time.Second)
		}
	}

	return result, nil
}

// --- SQL helpers ---

func placeholders(n int) string {
	p := make([]string, n)
	for i := range p {
		p[i] = "?"
	}
	return strings.Join(p, ",")
}

func intSliceToAny(s []int) []any {
	a := make([]any, len(s))
	for i, v := range s {
		a[i] = v
	}
	return a
}
