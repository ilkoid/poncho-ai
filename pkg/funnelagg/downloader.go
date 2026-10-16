package funnelagg

import (
	"context"
	"fmt"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Downloader is a reusable aggregated funnel downloader.
// Depends on Source (WB API) and Writer (persistence) — both are interfaces.
type Downloader struct {
	source Source
	writer Writer
	opts   DownloadOptions
}

// NewDownloader creates an aggregated funnel downloader from a Source and Writer.
func NewDownloader(source Source, writer Writer, opts DownloadOptions) *Downloader {
	if opts.PageSize <= 0 {
		opts.PageSize = 100
	}
	if opts.RateLimit <= 0 {
		opts.RateLimit = 3
	}
	if opts.Burst <= 0 {
		opts.Burst = 3
	}
	if opts.MaxPageRetries <= 0 {
		opts.MaxPageRetries = 3
	}
	if opts.PageRetryBaseSleep <= 0 {
		opts.PageRetryBaseSleep = 2 * time.Minute
	}
	return &Downloader{source: source, writer: writer, opts: opts}
}

// Run executes the full aggregated funnel download pipeline.
//
// Algorithm:
//  1. Estimate progress via writer.GetDistinctNmIDCount() (best-effort).
//  2. Offset resume: skip already-downloaded pages via writer.GetFunnelAggregatedCount().
//  3. Pagination loop: offset += pageSize, page retry on API errors.
//  4. Exit on empty response OR len(products) < pageSize.
func (d *Downloader) Run(ctx context.Context) (*DownloadResult, error) {
	start := time.Now()
	result := &DownloadResult{}

	// Progress estimation (best-effort — may be 0 if no sales data).
	totalProducts := 0
	if count, err := d.writer.GetDistinctNmIDCount(ctx); err == nil && count > 0 {
		totalProducts = count
	}
	// Оценка числа страниц для [N/M % ETA] в префиксе dllog. API тотал не
	// отдаёт (11-analytics.yaml: data = products + currency), единственный
	// источник — distinct nmID из sales; на пустой БД оценка 0 = линейный счёт.
	pagesTotal := 0
	if totalProducts > 0 && d.opts.PageSize > 0 {
		pagesTotal = (totalProducts + d.opts.PageSize - 1) / d.opts.PageSize
	}

	d.progress(0, 0, "📅 Period: %s → %s", d.opts.SelectedStart, d.opts.SelectedEnd)
	if d.opts.PastStart != "" && d.opts.PastEnd != "" {
		d.progress(0, 0, "📅 Past:   %s → %s", d.opts.PastStart, d.opts.PastEnd)
	}

	// Offset resume: skip already-downloaded pages.
	offset := 0
	existingCount, resumeErr := d.writer.GetFunnelAggregatedCount(ctx, d.opts.SelectedStart, d.opts.SelectedEnd)
	if resumeErr == nil && existingCount > 0 {
		offset = max(0, existingCount-d.opts.PageSize) // overlap by one page for safety
		d.progress(0, 0, "📎 Resume: %d products already in DB, starting at offset=%d", existingCount, offset)
	}

	totalLoaded := offset

	var fatalErr error

	for {
		select {
		case <-ctx.Done():
			result.Duration = time.Since(start)
			return result, ctx.Err()
		default:
		}

		req := d.buildRequest(offset)
		pageNum := offset/d.opts.PageSize + 1

		// Progress info: [N/M % ETA] даёт dllog-префикс; в сообщении — товары.
		if totalProducts > 0 {
			d.progress(pageNum, pagesTotal, "📄 Page %d (offset=%d, %d/%d)",
				pageNum, offset, totalLoaded, totalProducts)
		} else {
			d.progress(pageNum, pagesTotal, "📄 Page %d (offset=%d, loaded=%d)", pageNum, offset, totalLoaded)
		}

		// Fetch with page-level retry for global limiter recovery.
		products, currency, err := d.fetchWithRetry(ctx, req)
		if err != nil {
			d.progress(pageNum, pagesTotal, "❌ API error after retries: %v", err)
			fatalErr = fmt.Errorf("API error page %d: %w", pageNum, err)
			result.Errors++
			break
		}

		apiCount := len(products)
		if apiCount == 0 {
			if pageNum == 1 {
				d.progress(0, 0, "ℹ️  No data for the specified period")
			} else {
				d.progress(pageNum, pagesTotal, "✅ All data loaded")
			}
			break
		}

		// Save batch.
		saved := apiCount
		if !d.opts.DryRun {
			saved, err = d.writer.SaveFunnelAggregatedBatch(ctx, products,
				d.opts.SelectedStart, d.opts.SelectedEnd, currency)
			if err != nil {
				d.progress(pageNum, pagesTotal, "❌ Save error: %v", err)
				fatalErr = fmt.Errorf("save cards page %d: %w", pageNum, err)
				result.Errors++
				break
			}
		}

		totalLoaded += saved
		result.ProductsLoaded = totalLoaded
		result.PagesLoaded++

		if saved != apiCount {
			d.progress(pageNum, pagesTotal, "⚠️  API: %d, saved: %d", apiCount, saved)
		}

		// Last page check.
		if apiCount < d.opts.PageSize {
			d.progress(pageNum, pagesTotal, "✅ Last page loaded (%d products)", apiCount)
			break
		}

		offset += d.opts.PageSize
	}

	result.Duration = time.Since(start)
	return result, fatalErr
}

// buildRequest constructs the API request for the given offset.
func (d *Downloader) buildRequest(offset int) wb.FunnelAggregatedRequest {
	req := wb.FunnelAggregatedRequest{
		SelectedPeriod: wb.PeriodRange{
			Start: d.opts.SelectedStart,
			End:   d.opts.SelectedEnd,
		},
		NmIDs:         d.opts.NmIDs,
		BrandNames:    d.opts.BrandNames,
		SubjectIDs:    d.opts.SubjectIDs,
		TagIDs:        d.opts.TagIDs,
		SkipDeletedNm: d.opts.SkipDeletedNm,
		Limit:         d.opts.PageSize,
		Offset:        offset,
	}

	if d.opts.PastStart != "" && d.opts.PastEnd != "" {
		req.PastPeriod = &wb.PeriodRange{
			Start: d.opts.PastStart,
			End:   d.opts.PastEnd,
		}
	}

	if d.opts.OrderByField != "" {
		req.OrderBy = &wb.OrderBy{
			Field: d.opts.OrderByField,
			Mode:  d.opts.OrderByMode,
		}
	}

	return req
}

// pageRetryCyclePause — пауза между циклами попыток в budget-режиме. Внутри
// цикла клиент сам ждёт по подсказке сервера (кулдаун «подсказка + ε»), поэтому
// между циклами достаточно короткой паузы, чтобы не крутить холостые запросы.
// var (а не const) — только ради override в тестах.
var pageRetryCyclePause = 2 * time.Second

// fetchWithRetry fetches one page with retries on API errors.
//
// PageRetryBudget > 0 — budget-режим (fetchWithBudget): циклы попыток подряд
// до исхода wall-clock бюджета на страницу. Иначе — прежняя лестница
// MaxPageRetries × линейный сон PageRetryBaseSleep×n (backcompat).
func (d *Downloader) fetchWithRetry(ctx context.Context, req wb.FunnelAggregatedRequest) ([]wb.FunnelAggregatedProduct, string, error) {
	if d.opts.PageRetryBudget > 0 {
		return d.fetchWithBudget(ctx, req)
	}

	var products []wb.FunnelAggregatedProduct
	var currency string
	var lastErr error

	for retry := range d.opts.MaxPageRetries {
		select {
		case <-ctx.Done():
			return nil, "", ctx.Err()
		default:
		}

		products, currency, lastErr = d.source.LoadAggregatedPage(ctx, req)
		if lastErr == nil {
			return products, currency, nil
		}

		if retry < d.opts.MaxPageRetries-1 {
			sleepDur := d.opts.PageRetryBaseSleep * time.Duration(retry+1)
			d.progress(0, 0, "⏳ Retry %d/%d after %v: %v", retry+2, d.opts.MaxPageRetries, sleepDur, lastErr)

			select {
			case <-ctx.Done():
				return nil, "", ctx.Err()
			case <-time.After(sleepDur):
			}
		}
	}

	return nil, "", fmt.Errorf("all %d retries exhausted: %w", d.opts.MaxPageRetries, lastErr)
}

// fetchWithBudget — budget-режим: страницы воюют за ведро, а не ждут длинную
// лестницу. Каждая итерация — полный цикл попыток клиента (retry_attempts
// hint-гонок с кулдауном «подсказка + ε»); между циклами короткая пауза.
// Конкурент, непрерывно вычерпывающий ведро, выбивает нас из длинных снов —
// выигрывает тот, кто присутствует на большем числе раздач токена, поэтому
// ограничение по ВРЕМЕНИ (а не по числу попыток) честнее: исход — либо страница
// взята, либо бюджет исчерпан и прогон падает с offset-resume на следующий раз.
func (d *Downloader) fetchWithBudget(ctx context.Context, req wb.FunnelAggregatedRequest) ([]wb.FunnelAggregatedProduct, string, error) {
	deadline := time.Now().Add(d.opts.PageRetryBudget)
	var products []wb.FunnelAggregatedProduct
	var currency string
	var lastErr error

	for cycle := 1; ; cycle++ {
		select {
		case <-ctx.Done():
			return nil, "", ctx.Err()
		default:
		}

		products, currency, lastErr = d.source.LoadAggregatedPage(ctx, req)
		if lastErr == nil {
			return products, currency, nil
		}

		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, "", fmt.Errorf("page budget %v exhausted after %d cycles: %w",
				d.opts.PageRetryBudget.Truncate(time.Second), cycle, lastErr)
		}

		d.progress(0, 0, "⏳ page-budget %v: cycle %d failed, %v left: %v",
			d.opts.PageRetryBudget.Truncate(time.Second), cycle, remaining.Truncate(time.Second), lastErr)

		select {
		case <-ctx.Done():
			return nil, "", ctx.Err()
		case <-time.After(pageRetryCyclePause):
		}
	}
}

// progress emits a progress message via the OnProgress callback if set.
// cur/total — страница/оценка страниц для [N/M % ETA] (0/0 = инфо-строка).
func (d *Downloader) progress(cur, total int, format string, args ...any) {
	if d.opts.OnProgress != nil {
		d.opts.OnProgress(cur, total, fmt.Sprintf(format, args...))
	}
}
