package sales

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Downloader is a reusable sales downloader.
// Depends on SalesSource (WB API) and SalesWriter (persistence) — both are interfaces.
type Downloader struct {
	source SalesSource
	writer SalesWriter
	opts   DownloadOptions
}

const statsAPIURL = "https://statistics-api.wildberries.ru"

// NewDownloader creates a downloader from a SalesSource and a SalesWriter.
func NewDownloader(source SalesSource, writer SalesWriter, opts DownloadOptions) *Downloader {
	if opts.MaxDaysPerPeriod > 0 {
		wb.MaxDaysPerPeriod = opts.MaxDaysPerPeriod
	}
	return &Downloader{
		source: source,
		writer: writer,
		opts:   opts,
	}
}

// Run downloads sales data for the given date ranges.
func (d *Downloader) Run(ctx context.Context, ranges []wb.DateRange, resume, rewrite bool) (*DownloadResult, error) {
	start := time.Now()

	result := &DownloadResult{
		PeriodsCount: len(ranges),
	}

	if rewrite && resume {
		d.progress("⚠️  rewrite + resume: rewrite приоритетнее, resume выключен")
		resume = false
	}
	d.opts.Rewrite = rewrite

	var lastDT, firstDT time.Time
	if resume {
		var err error
		lastDT, err = d.writer.GetLastSaleDT(ctx)
		if err != nil {
			return result, fmt.Errorf("get last sale_dt: %w", err)
		}
		firstDT, err = d.writer.GetFirstSaleDT(ctx)
		if err != nil {
			return result, fmt.Errorf("get first sale_dt: %w", err)
		}
		if !lastDT.IsZero() {
			d.progress("📊 Найдена последняя запись: %s", lastDT.Format("2006-01-02 15:04:05"))
		} else {
			d.progress("📥 База пуста — загружаем с начала")
		}
	}

	adjusted := make([]wb.DateRange, 0, len(ranges))
	skipped := 0

	for _, dr := range ranges {
		if resume {
			newFrom, shouldSkip, skippedUntil := wb.AdjustPeriodForResume(dr.From, dr.To, firstDT, lastDT)
			if shouldSkip {
				skipped++
				lastDT = skippedUntil
				continue
			}
			if newFrom.After(dr.From) {
				d.progress("  🔄 %s → %s", dr.String(), wb.DateRange{From: newFrom, To: dr.To}.String())
				dr = wb.DateRange{From: newFrom, To: dr.To}
			}
		}
		adjusted = append(adjusted, dr)
	}

	result.PeriodsCount = len(adjusted)
	if skipped > 0 {
		d.progress("⏭️  Пропущено периодов: %d", skipped)
	}

	if len(adjusted) == 0 {
		d.progress("✅ Все периоды уже загружены!")
		return result, nil
	}

	for i, dr := range adjusted {
		pr, err := d.downloadPeriod(ctx, dr, i, len(adjusted), resume)
		if err != nil {
			return result, err
		}
		result.TotalRows += pr.Rows
		result.TotalPages += pr.Pages
	}

	result.Duration = time.Since(start)
	return result, nil
}

// downloadPeriod handles a single period.
func (d *Downloader) downloadPeriod(ctx context.Context, dr wb.DateRange, periodNum, total int, resume bool) (*periodResult, error) {
	periodStart := time.Now()
	d.progress("\n=== Период %d/%d: %s ===", periodNum+1, total, dr.String())
	d.progress("  🕐 Начало: %s", time.Now().Format("2006-01-02 15:04:05"))

	if d.opts.Rewrite && !d.opts.DryRun {
		fromStr := dr.From.Format("2006-01-02T15:04:05Z07:00")
		toStr := dr.To.Format("2006-01-02T15:04:05Z07:00")

		deleted, err := d.writer.DeleteSalesByDateRange(ctx, fromStr, toStr)
		if err != nil {
			return nil, fmt.Errorf("delete sales for %s: %w", dr.String(), err)
		}

		var svcDeleted int64
		if !d.opts.SkipServiceRecords {
			svcDeleted, err = d.writer.DeleteServiceRecordsByDateRange(ctx, fromStr, toStr)
			if err != nil {
				return nil, fmt.Errorf("delete service records for %s: %w", dr.String(), err)
			}
		}

		if deleted > 0 || svcDeleted > 0 {
			d.progress("  🗑️  Rewrote %s: deleted %d sales + %d service records",
				dr.String(), deleted, svcDeleted)
		}
	}

	res := &periodResult{}

	if dr.HasTime() {
		d.progress("  🔧 Time-based mode: %s → %s", dr.FromRFC3339(), dr.ToRFC3339())
		_, err := d.source.ReportDetailByPeriodIteratorWithTime(
			ctx,
			statsAPIURL,
			d.opts.RateLimit,
			d.opts.Burst,
			dr.FromRFC3339(),
			dr.ToRFC3339(),
			func(rows []wb.RealizationReportRow) error {
				return d.saveRows(ctx, rows, resume, res)
			},
		)
		if err != nil {
			return res, fmt.Errorf("iterator error: %w", err)
		}
	} else {
		d.progress("  🔧 Date-based mode: %d → %d", dr.FromInt(), dr.ToInt())
		_, err := d.source.ReportDetailByPeriodIterator(
			ctx,
			statsAPIURL,
			d.opts.RateLimit,
			d.opts.Burst,
			dr.FromInt(),
			dr.ToInt(),
			func(rows []wb.RealizationReportRow) error {
				return d.saveRows(ctx, rows, resume, res)
			},
		)
		if err != nil {
			return res, fmt.Errorf("iterator error: %w", err)
		}
	}

	res.TotalDuration = time.Since(periodStart)

	d.progress("  ✅ Готово: %d строк, %d страниц", res.Rows, res.Pages)
	return res, nil
}

// periodResult tracks internal per-period counters.
type periodResult struct {
	Rows           int
	Pages          int
	TotalDuration  time.Duration
	APIWaitTime    time.Duration
	DBWriteTime    time.Duration
	ProcessingTime time.Duration
}

// saveRows filters and persists a batch of rows.
func (d *Downloader) saveRows(ctx context.Context, rows []wb.RealizationReportRow, resume bool, res *periodResult) error {
	procStart := time.Now()

	if len(rows) == 0 {
		return nil
	}

	rows = d.filterRows(rows)

	var salesRows, serviceRows []wb.RealizationReportRow
	for _, row := range rows {
		if row.NmID > 0 && row.DocTypeName != "" {
			salesRows = append(salesRows, row)
		} else {
			serviceRows = append(serviceRows, row)
		}
	}


	if d.opts.DryRun {
		d.progress("  🏜️  [DRY-RUN] %d sales + %d service records (skip)", len(salesRows), len(serviceRows))
		res.Rows += len(salesRows)
		res.Pages++
		return nil
	}
	if d.opts.SkipServiceRecords && len(serviceRows) > 0 {
		d.progress("  ⏭️  Пропущено %d служебных записей", len(serviceRows))
	} else if len(serviceRows) > 0 {
		// В rewrite-режиме диапазон уже удалён (downloadPeriod:115-136),
		// конфликты по rrd_id невозможны → plain INSERT без ON CONFLICT
		// даёт ~1.3–2x на write-пути. В resume-режиме нужен upsert.
		var err error
		if d.opts.Rewrite {
			err = d.writer.SaveServiceRecordsPlain(ctx, serviceRows)
		} else {
			err = d.writer.SaveServiceRecords(ctx, serviceRows)
		}
		if err != nil {
			return fmt.Errorf("save service records: %w", err)
		}
	}

	var toSave []wb.RealizationReportRow
	if resume && len(salesRows) > 0 {
		skipped := 0
		for _, r := range salesRows {
			exists, err := d.writer.Exists(ctx, r.RrdID)
			if err != nil {
				return fmt.Errorf("exists rrd_id=%d: %w", r.RrdID, err)
			}
			if !exists {
				toSave = append(toSave, r)
			} else {
				skipped++
			}
		}
		if skipped > 0 {
			d.progress("  🔄 Resume: отфильтровано %d дублей", skipped)
		}
	} else {
		toSave = salesRows
	}

	if len(toSave) > 0 {
		// В rewrite-режиме диапазон уже удалён (downloadPeriod:115-136),
		// конфликты по rrd_id невозможны → plain INSERT без ON CONFLICT.
		var err error
		if d.opts.Rewrite {
			err = d.writer.SavePlain(ctx, toSave)
		} else {
			err = d.writer.Save(ctx, toSave)
		}
		if err != nil {
			return fmt.Errorf("save sales: %w", err)
		}
	}

	res.Rows += len(toSave)
	res.Pages++

	res.ProcessingTime += time.Since(procStart)

	return nil
}

// filterRows applies article filtering based on config.
func (d *Downloader) filterRows(rows []wb.RealizationReportRow) []wb.RealizationReportRow {
	hasFilter := len(d.opts.Filter.ExcludeLengths) > 0 || len(d.opts.Filter.AllowedYears) > 0
	if !hasFilter {
		return rows
	}

	var filtered []wb.RealizationReportRow
	for _, r := range rows {
		if !shouldFilterArticle(r.SupplierArticle, d.opts.Filter.ExcludeLengths, d.opts.Filter.AllowedYears) {
			filtered = append(filtered, r)
		}
	}
	return filtered
}

// shouldFilterArticle checks if an article should be excluded.
func shouldFilterArticle(article string, excludeLengths, allowedYears []int) bool {
	if len(excludeLengths) > 0 {
		if slices.Contains(excludeLengths, len(article)) {
			return true
		}
	}
	if len(allowedYears) > 0 && len(article) >= 3 {
		year, err := strconv.Atoi(article[1:3])
		if err == nil {
			if slices.Contains(allowedYears, year) {
				return false
			}
			return true
		}
	}
	return false
}

// progress emits a progress message via the OnProgress callback if set.
func (d *Downloader) progress(format string, args ...any) {
	if d.opts.OnProgress != nil {
		d.opts.OnProgress(fmt.Sprintf(format, args...))
	}
}
