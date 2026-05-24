package funnel

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"time"
)

// Downloader is a reusable funnel history downloader.
// Depends on FunnelSource (WB API) and FunnelWriter (persistence) — both are interfaces.
type Downloader struct {
	source FunnelSource
	writer FunnelWriter
	opts   DownloadOptions
}

// NewDownloader creates a funnel downloader from a FunnelSource and FunnelWriter.
func NewDownloader(source FunnelSource, writer FunnelWriter, opts DownloadOptions) *Downloader {
	if opts.BatchSize <= 0 {
		opts.BatchSize = MaxBatchSize
	}
	return &Downloader{
		source: source,
		writer: writer,
		opts:   opts,
	}
}

// Run executes the full funnel download pipeline.
func (d *Downloader) Run(ctx context.Context) (*DownloadResult, error) {
	start := time.Now()
	result := &DownloadResult{}

	from, to := d.resolveDates()
	d.progress("📅 Period: %s → %s", from, to)

	nmIDs, err := d.prepareNmIDs(ctx)
	if err != nil {
		return result, fmt.Errorf("prepare nmIDs: %w", err)
	}
	if len(nmIDs) == 0 {
		d.progress("✅ No products to load!")
		result.Duration = time.Since(start)
		return result, nil
	}

	batches := chunkInts(nmIDs, d.opts.BatchSize)
	if d.opts.MaxBatches > 0 && len(batches) > d.opts.MaxBatches {
		batches = batches[:d.opts.MaxBatches]
		d.progress("🔢 Limited to %d/%d batches", d.opts.MaxBatches, len(batches))
	}

	d.progress("📦 %d products in %d batches", len(nmIDs), len(batches))

	for i, batch := range batches {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		default:
		}

		br, err := d.source.LoadBatch(ctx, batch, from, to)
		if err != nil {
			d.progress("  ❌ Batch %d/%d error: %v", i+1, len(batches), err)
			continue
		}

		for _, item := range br {
			if d.opts.DryRun {
				result.ProductsLoaded++
				result.MetricsLoaded += len(item.Rows)
				continue
			}

			if err := d.writer.SaveFunnelHistoryWithWindow(ctx, item.Product, item.Rows, d.opts.RefreshWindow); err != nil {
				d.progress("  ❌ Save nmID=%d: %v", item.Product.NmID, err)
				continue
			}
			result.ProductsLoaded++
			result.MetricsLoaded += len(item.Rows)
		}

		result.BatchesTotal++
		d.progress("  ✅ Batch %d/%d: %d products, %d metrics", i+1, len(batches), len(br), countRows(br))
	}

	result.Duration = time.Since(start)
	return result, nil
}

// resolveDates returns (from, to) as YYYY-MM-DD strings.
// Funnel includes today (unlike sales which uses yesterday).
func (d *Downloader) resolveDates() (string, string) {
	if d.opts.From != "" && d.opts.To != "" {
		return d.opts.From, d.opts.To
	}
	days := d.opts.Days
	if days <= 0 {
		days = 7
	}
	now := time.Now()
	from := now.AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	to := now.Format("2006-01-02")
	return from, to
}

// prepareNmIDs runs the full preparation pipeline:
// GetDistinctNmIDs → filterByArticle → FilterActiveNmIDs → incrementalSkip.
func (d *Downloader) prepareNmIDs(ctx context.Context) ([]int, error) {
	nmIDs, err := d.writer.GetDistinctNmIDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("get distinct nmIDs: %w", err)
	}
	d.progress("📊 Found %d distinct nmIDs", len(nmIDs))

	nmIDs, err = d.filterByArticle(ctx, nmIDs)
	if err != nil {
		return nil, err
	}

	if d.opts.Filter.ActiveDays > 0 {
		before := len(nmIDs)
		nmIDs, err = d.writer.FilterActiveNmIDs(ctx, nmIDs, d.opts.Filter.ActiveDays)
		if err != nil {
			return nil, fmt.Errorf("filter active nmIDs: %w", err)
		}
		d.progress("🔍 Active filter (%d days): %d → %d", d.opts.Filter.ActiveDays, before, len(nmIDs))
	}

	nmIDs = d.incrementalSkip(ctx, nmIDs)

	return nmIDs, nil
}

// filterByArticle removes nmIDs whose supplier article matches exclusion rules.
func (d *Downloader) filterByArticle(ctx context.Context, nmIDs []int) ([]int, error) {
	if len(d.opts.Filter.ExcludeLengths) == 0 && len(d.opts.Filter.AllowedYears) == 0 {
		return nmIDs, nil
	}

	articlesMap, err := d.writer.GetSupplierArticlesByNmIDs(ctx, nmIDs)
	if err != nil {
		return nil, fmt.Errorf("get supplier articles: %w", err)
	}

	var filtered []int
	for _, id := range nmIDs {
		article, ok := articlesMap[id]
		if !ok || !shouldFilterArticle(article, d.opts.Filter.ExcludeLengths, d.opts.Filter.AllowedYears) {
			filtered = append(filtered, id)
		}
	}

	d.progress("🔎 Article filter: %d → %d", len(nmIDs), len(filtered))
	return filtered, nil
}

// incrementalSkip removes nmIDs that were recently loaded.
func (d *Downloader) incrementalSkip(ctx context.Context, nmIDs []int) []int {
	if d.opts.IncrementalHours <= 0 {
		return nmIDs
	}

	recent, err := d.writer.GetRecentlyLoadedNmIDs(ctx, d.opts.IncrementalHours)
	if err != nil {
		d.progress("⚠️  Incremental skip failed: %v", err)
		return nmIDs
	}

	var filtered []int
	for _, id := range nmIDs {
		if !recent[id] {
			filtered = append(filtered, id)
		}
	}
	d.progress("⏩ Incremental skip (%dh): %d → %d", d.opts.IncrementalHours, len(nmIDs), len(filtered))
	return filtered
}

// shouldFilterArticle returns true if the article should be excluded.
func shouldFilterArticle(article string, excludeLengths, allowedYears []int) bool {
	if len(excludeLengths) > 0 && slices.Contains(excludeLengths, len(article)) {
		return true
	}
	if len(allowedYears) > 0 && len(article) >= 3 {
		year, err := strconv.Atoi(article[1:3])
		if err == nil && !slices.Contains(allowedYears, year) {
			return true
		}
	}
	return false
}

// chunkInts splits a slice into chunks of size n.
func chunkInts(ids []int, n int) [][]int {
	var chunks [][]int
	for i := 0; i < len(ids); i += n {
		end := i + n
		if end > len(ids) {
			end = len(ids)
		}
		chunks = append(chunks, ids[i:end])
	}
	return chunks
}

// countRows counts total rows across batch results.
func countRows(br []BatchResult) int {
	total := 0
	for _, r := range br {
		total += len(r.Rows)
	}
	return total
}

// progress emits a progress message via the OnProgress callback if set.
func (d *Downloader) progress(format string, args ...any) {
	if d.opts.OnProgress != nil {
		d.opts.OnProgress(fmt.Sprintf(format, args...))
	}
}
