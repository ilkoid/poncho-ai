package supplies

import (
	"context"
	"fmt"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

const (
	suppliesPageSize = 1000
	goodsPageSize    = 1000
)

// Downloader implements the supply download pipeline.
// Depends only on Source and Writer interfaces — no direct API or DB dependencies.
type Downloader struct {
	source Source
	writer Writer
	opts   DownloadOptions
}

// NewDownloader creates a new supply downloader.
func NewDownloader(source Source, writer Writer, opts DownloadOptions) *Downloader {
	return &Downloader{
		source: source,
		writer: writer,
		opts:   opts,
	}
}

// Run executes the full supply download pipeline:
//  1. Reference data (warehouses + tariffs)
//  2. Supplies list (paginated via POST)
//  3. Per-supply details (details + goods + packages)
//
// Errors for individual supplies are non-fatal (logged via OnProgress, counted in result.Errors).
func (d *Downloader) Run(ctx context.Context) (*DownloadResult, error) {
	start := time.Now()
	result := &DownloadResult{}

	// Phase 1: Reference data (warehouses + tariffs)
	if !d.opts.SkipReference {
		if err := d.downloadReference(ctx, result); err != nil {
			return result, err
		}
	}

	// Phase 2: Supplies list (paginated)
	allSupplies, err := d.downloadSuppliesList(ctx, result)
	if err != nil {
		return result, err
	}

	// Phase 3: Per-supply details (details + goods + packages)
	d.downloadSupplyDetails(ctx, result, allSupplies)

	result.Duration = time.Since(start)
	return result, nil
}

// downloadReference downloads and saves warehouse and tariff reference data.
func (d *Downloader) downloadReference(ctx context.Context, result *DownloadResult) error {
	// Warehouses
	warehouses, err := d.source.GetWarehouses(ctx)
	result.APICalls++
	if err != nil {
		return fmt.Errorf("get warehouses: %w", err)
	}
	if !d.opts.DryRun && len(warehouses) > 0 {
		saved, err := d.writer.SaveWarehouses(ctx, warehouses)
		if err != nil {
			return fmt.Errorf("save warehouses: %w", err)
		}
		result.Warehouses = saved
	}
	d.progress("warehouses: %d saved", result.Warehouses)

	// Transit tariffs
	tariffs, err := d.source.GetTransitTariffs(ctx)
	result.APICalls++
	if err != nil {
		return fmt.Errorf("get transit tariffs: %w", err)
	}
	if !d.opts.DryRun && len(tariffs) > 0 {
		saved, err := d.writer.SaveTransitTariffs(ctx, tariffs)
		if err != nil {
			return fmt.Errorf("save transit tariffs: %w", err)
		}
		result.Tariffs = saved
	}
	d.progress("tariffs: %d saved", result.Tariffs)

	return nil
}

// downloadSuppliesList downloads all supplies with pagination and saves them.
func (d *Downloader) downloadSuppliesList(ctx context.Context, result *DownloadResult) ([]wb.Supply, error) {
	filter := wb.SuppliesFilterRequest{
		Dates: []wb.DateFilter{
			{
				From: d.opts.Begin,
				Till: d.opts.End,
				Type: d.opts.DateFilterType,
			},
		},
		StatusIDs: []int{1, 2, 3, 4, 5, 6},
	}

	var allSupplies []wb.Supply
	offset := 0

	for {
		select {
		case <-ctx.Done():
			return allSupplies, ctx.Err()
		default:
		}

		supplies, err := d.source.GetSupplies(ctx, filter, suppliesPageSize, offset)
		result.APICalls++
		if err != nil {
			return allSupplies, fmt.Errorf("get supplies (offset=%d): %w", offset, err)
		}

		if len(supplies) == 0 {
			break
		}

		allSupplies = append(allSupplies, supplies...)
		d.progress("page: offset=%d received=%d total=%d", offset, len(supplies), len(allSupplies))

		if len(supplies) < suppliesPageSize {
			break
		}
		offset += suppliesPageSize
	}

	// Save supplies from list
	if len(allSupplies) > 0 && !d.opts.DryRun {
		now := time.Now().Format("2006-01-02 15:04:05")
		rows := make([]SupplyRow, 0, len(allSupplies))
		for i := range allSupplies {
			rows = append(rows, SupplyFromAPI(&allSupplies[i], now))
		}
		saved, err := d.writer.SaveSupplies(ctx, rows)
		if err != nil {
			return allSupplies, fmt.Errorf("save supplies: %w", err)
		}
		result.Supplies = saved
	}

	return allSupplies, nil
}

// downloadSupplyDetails downloads details, goods and packages for each supply.
// Errors for individual supplies are non-fatal — logged and counted.
func (d *Downloader) downloadSupplyDetails(ctx context.Context, result *DownloadResult, allSupplies []wb.Supply) {
	now := time.Now().Format("2006-01-02 15:04:05")

	// Collect detail rows for batch upsert after loop
	var detailRows []SupplyRow

	for i, s := range allSupplies {
		select {
		case <-ctx.Done():
			// Save collected details before exiting
			if len(detailRows) > 0 && !d.opts.DryRun {
				d.writer.SaveSupplies(ctx, detailRows)
			}
			return
		default:
		}

		supplyID := int64(0)
		if s.SupplyID != nil {
			supplyID = *s.SupplyID
		}
		preorderID := s.PreorderID

		// Skip unplanned supplies (supply_id=0) — they have no goods/packages
		if supplyID == 0 {
			continue
		}

		// Download details (warehouse info) for this supply
		details, err := d.source.GetSupplyDetails(ctx, supplyID)
		result.APICalls++
		if err != nil {
			result.Errors++
			d.progress("details supply_id=%d: %v", supplyID, err)
		} else if details != nil {
			row := SupplyFromAPIDetail(details, now)
			row.SupplyID = supplyID
			row.PreorderID = preorderID
			detailRows = append(detailRows, row)
		}

		// Download goods (paginated)
		var allGoods []wb.GoodInSupply
		goodsOffset := 0
		for {
			goods, err := d.source.GetSupplyGoods(ctx, supplyID, goodsPageSize, goodsOffset)
			result.APICalls++
			if err != nil {
				result.Errors++
				d.progress("goods supply_id=%d: %v", supplyID, err)
				break
			}
			allGoods = append(allGoods, goods...)
			if len(goods) < goodsPageSize {
				break
			}
			goodsOffset += goodsPageSize
		}

		if len(allGoods) > 0 && !d.opts.DryRun {
			saved, err := d.writer.SaveSupplyGoods(ctx, supplyID, preorderID, allGoods)
			if err != nil {
				result.Errors++
				d.progress("save goods supply_id=%d: %v", supplyID, err)
			} else {
				result.Goods += saved
			}
		}

		// Download packages
		boxes, err := d.source.GetSupplyPackages(ctx, supplyID)
		result.APICalls++
		if err != nil {
			result.Errors++
			d.progress("packages supply_id=%d: %v", supplyID, err)
		} else if len(boxes) > 0 && !d.opts.DryRun {
			saved, err := d.writer.SaveSupplyPackages(ctx, supplyID, preorderID, boxes)
			if err != nil {
				result.Errors++
				d.progress("save packages supply_id=%d: %v", supplyID, err)
			} else {
				result.Packages += saved
			}
		}

		// Progress every 10 supplies
		if (i+1)%10 == 0 || i+1 == len(allSupplies) {
			d.progress("supplies: %d/%d (goods: %d, packages: %d)", i+1, len(allSupplies), result.Goods, result.Packages)
		}
	}

	// Batch save all detail rows (INSERT OR REPLACE / ON CONFLICT updates warehouse fields)
	if len(detailRows) > 0 && !d.opts.DryRun {
		saved, err := d.writer.SaveSupplies(ctx, detailRows)
		if err != nil {
			result.Errors++
			d.progress("save supply details: %v", err)
		} else {
			d.progress("supply details updated: %d", saved)
		}
	}
}

// progress calls the OnProgress callback if set.
func (d *Downloader) progress(format string, args ...any) {
	if d.opts.OnProgress != nil {
		d.opts.OnProgress(fmt.Sprintf(format, args...))
	}
}
