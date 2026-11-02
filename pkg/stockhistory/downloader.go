package stockhistory

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Downloader handles the async CSV stock history report lifecycle.
type Downloader struct {
	source StockHistorySource
	writer StockHistoryWriter
	opts   DownloadOptions
}

// NewDownloader creates a stock-history downloader from source and writer.
func NewDownloader(source StockHistorySource, writer StockHistoryWriter, opts DownloadOptions) *Downloader {
	return &Downloader{
		source: source,
		writer: writer,
		opts:   opts,
	}
}

// Run executes the full async CSV download pipeline:
//  1. resolve dates
//  2. check resume (if enabled)
//  3. create report
//  4. poll until ready
//  5. download ZIP
//  6. extract + parse CSV
//  7. save to DB
func (d *Downloader) Run(ctx context.Context) (*DownloadResult, error) {
	start := time.Now()
	result := &DownloadResult{}

	from, to := d.resolveDates()
	d.progress("Period: %s -> %s", from, to)

	reportType := d.reportType()

	// Resume: check if we already have a successful report for this period
	if d.opts.Resume {
		existing, err := d.writer.GetReport(ctx, reportType, from, to, d.opts.StockType)
		if err != nil {
			d.progress("Resume check failed: %v", err)
		} else if existing != nil && existing.Status == wb.StockHistoryStatusSuccess {
			d.progress("Resumed: report %s already completed (%d rows)", existing.ID, existing.RowsCount)
			result.DownloadID = existing.ID
			result.RowsCount = existing.RowsCount
			result.Status = "RESUMED"
			result.Duration = time.Since(start)
			return result, nil
		}
	}

	// Adopt from list: этот период могла создать другая машина нашего
	// аккаунта (квота созданий общая — 20/сутки). Если в бесплатном
	// GET-списке есть свежий отчёт того же периода — забираем его вместо
	// создания нового слота. Источники без листинга путь пропускают.
	var adoptedID string
	if d.opts.Resume {
		if lister, ok := d.source.(StockHistoryLister); ok {
			id, err := d.adoptFromList(ctx, lister, from, to)
			if err != nil {
				d.progress("Adopt check failed: %v (fallback to create)", err)
			} else {
				adoptedID = id
			}
		}
	}

	id := adoptedID
	if id == "" {
		req := wb.StockHistoryReportRequest{
			ID:             uuid.New().String(),
			ReportType:     reportType,
			UserReportName: stockReportName(d.opts.ReportType, from, to),
			Params: wb.StockHistoryReportParams{
				CurrentPeriod: wb.StockHistoryPeriod{
					Start: from,
					End:   to,
				},
				StockType:     d.opts.StockType,
				SkipDeletedNm: true,
			},
		}

		// Metrics-specific params
		if d.opts.ReportType == "metrics" {
			req.Params.AvailabilityFilters = []string{"deficient", "actual", "balanced"}
			req.Params.OrderBy = &wb.StockHistoryOrderBy{
				Field: "avgOrders",
				Mode:  "desc",
			}
		}

		created, err := d.source.CreateReport(ctx, req)
		if err != nil {
			return result, fmt.Errorf("create report: %w", err)
		}
		id = created
		d.progress("Report created: %s", id)
	} else {
		d.progress("Adopted from list: %s (period already built by another machine, no quota slot spent)", id)
	}
	result.DownloadID = id

	// Poll until ready
	d.progress("Polling status (interval=%ds, timeout=%dm)...", d.opts.PollIntervalSec, d.opts.PollTimeoutMin)
	status, err := d.poll(ctx, id)
	if err != nil {
		return result, fmt.Errorf("poll report %s: %w", id, err)
	}
	d.progress("Report ready: status=%s size=%d", status.Status, status.Size)

	// Download ZIP
	zipBytes, err := d.source.DownloadFile(ctx, id)
	if err != nil {
		return result, fmt.Errorf("download report %s: %w", id, err)
	}
	d.progress("Downloaded: %d bytes", len(zipBytes))

	// Extract CSV from ZIP
	csvReader, err := extractCSVFromZip(zipBytes)
	if err != nil {
		return result, fmt.Errorf("extract CSV: %w", err)
	}

	// Parse CSV and save based on report type
	if d.opts.ReportType == "metrics" {
		rows, err := ParseMetricsCSV(csvReader)
		if err != nil {
			return result, fmt.Errorf("parse metrics CSV: %w", err)
		}
		result.RowsCount = len(rows)
		d.progress("Parsed %d metrics rows", len(rows))

		if d.opts.DryRun {
			result.Status = "SUCCESS"
			result.Duration = time.Since(start)
			return result, nil
		}

		// Save report metadata (FK constraint: report must exist before data rows)
		if err := d.writer.SaveReport(ctx, ReportRecord{
			ID:         id,
			ReportType: reportType,
			StartDate:  from,
			EndDate:    to,
			StockType:  d.opts.StockType,
			Status:     wb.StockHistoryStatusWaiting,
			FileSize:   int64(len(zipBytes)),
			RowsCount:  len(rows),
			CreatedAt:  status.CreatedAt,
		}); err != nil {
			d.progress("Warning: save report metadata: %v", err)
		}

		// Set ReportID on each row
		for i := range rows {
			rows[i].ReportID = id
		}

		// Save metrics data
		saved, err := d.writer.SaveMetrics(ctx, rows)
		if err != nil {
			return result, fmt.Errorf("save metrics: %w", err)
		}
		d.progress("Saved %d metrics rows", saved)

	} else { // daily
		rows, err := ParseDailyCSV(csvReader)
		if err != nil {
			return result, fmt.Errorf("parse daily CSV: %w", err)
		}
		result.RowsCount = len(rows)
		d.progress("Parsed %d daily rows", len(rows))

		if d.opts.DryRun {
			result.Status = "SUCCESS"
			result.Duration = time.Since(start)
			return result, nil
		}

		// Save report metadata
		if err := d.writer.SaveReport(ctx, ReportRecord{
			ID:         id,
			ReportType: reportType,
			StartDate:  from,
			EndDate:    to,
			StockType:  d.opts.StockType,
			Status:     wb.StockHistoryStatusWaiting,
			FileSize:   int64(len(zipBytes)),
			RowsCount:  len(rows),
			CreatedAt:  status.CreatedAt,
		}); err != nil {
			d.progress("Warning: save report metadata: %v", err)
		}

		// Set ReportID on each row
		for i := range rows {
			rows[i].ReportID = id
		}

		// Save daily data
		saved, err := d.writer.SaveDaily(ctx, rows)
		if err != nil {
			return result, fmt.Errorf("save daily: %w", err)
		}
		d.progress("Saved %d daily rows", saved)
	}

	// Update report status to SUCCESS
	if err := d.writer.UpdateReportStatus(ctx, id, wb.StockHistoryStatusSuccess, result.RowsCount); err != nil {
		d.progress("Warning: update report status: %v", err)
	}

	result.Status = "SUCCESS"
	result.Duration = time.Since(start)
	return result, nil
}

// stockReportName — userReportName сток-отчётов: ставится при создании и по
// нему же вторая машина аккаунта находит отчёт в GET-списке для adopt.
func stockReportName(reportKind, from, to string) string {
	return fmt.Sprintf("Stock History %s %s-%s", reportKind, from, to)
}

// adoptFromList ищет в GET-списке свежий отчёт того же периода, созданный
// другой машиной аккаунта. Готовые файлы WB хранит 48 ч — берём не старше
// 40 ч; при дублях периода выигрывает свежейший. "" → обычное создание.
func (d *Downloader) adoptFromList(ctx context.Context, lister StockHistoryLister, from, to string) (string, error) {
	items, err := lister.ListReports(ctx)
	if err != nil {
		return "", err
	}
	want := stockReportName(d.opts.ReportType, from, to)
	var bestID string
	var bestAt time.Time
	for i := range items {
		it := &items[i]
		// Период полностью закодирован в имени (мы же его и построили);
		// endDate в списке не сверяем строго — WB эхает его иначе для
		// metrics (наблюдение 02.11: имя «…-2026-11-02», endDate=2026-11-01).
		if it.Name != want || it.StartDate != from {
			continue
		}
		if it.Status == wb.StockHistoryStatusFailed {
			continue // слот creation потрачен, файла нет — не подбираем
		}
		at, ok := parseListedAt(it.CreatedAt)
		if !ok || time.Since(at) > 40*time.Hour {
			continue
		}
		if bestID == "" || at.After(bestAt) {
			bestID, bestAt = it.ID, at
		}
	}
	return bestID, nil
}

// parseListedAt парсит createdAt из списка отчётов («2024-06-26 20:05:32», UTC).
func parseListedAt(s string) (time.Time, bool) {
	t, err := time.Parse("2006-01-02 15:04:05", s)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// resolveDates returns (from, to) as YYYY-MM-DD strings.
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

// reportType returns the WB API report type string.
func (d *Downloader) reportType() string {
	if d.opts.ReportType == "daily" {
		return "STOCK_HISTORY_DAILY_CSV"
	}
	return "STOCK_HISTORY_REPORT_CSV"
}

// poll waits for the report to reach a terminal state.
func (d *Downloader) poll(ctx context.Context, downloadID string) (*wb.StockHistoryReportItem, error) {
	interval := time.Duration(d.opts.PollIntervalSec) * time.Second
	if interval <= 0 {
		interval = 30 * time.Second
	}
	timeout := time.Duration(d.opts.PollTimeoutMin) * time.Minute
	if timeout <= 0 {
		timeout = 30 * time.Minute
	}

	deadline := time.Now().Add(timeout)
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		status, err := d.source.PollStatus(ctx, downloadID)
		if err != nil {
			return nil, fmt.Errorf("poll status: %w", err)
		}

		switch status.Status {
		case wb.StockHistoryStatusSuccess:
			return status, nil
		case wb.StockHistoryStatusFailed:
			return nil, fmt.Errorf("report failed")
		case wb.StockHistoryStatusRetry:
			d.progress("Report status: RETRY, will re-request")
		}

		if time.Now().After(deadline) {
			return nil, fmt.Errorf("poll timeout after %v", timeout)
		}

		time.Sleep(interval)
	}
}

// extractCSVFromZip finds the first .csv file in a ZIP archive.
func extractCSVFromZip(zipBytes []byte) (*bytes.Reader, error) {
	r, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		return nil, fmt.Errorf("open zip: %w", err)
	}

	for _, f := range r.File {
		if f.Method == zip.Deflate || f.Method == zip.Store {
			rc, err := f.Open()
			if err != nil {
				return nil, fmt.Errorf("open %s: %w", f.Name, err)
			}
			defer rc.Close()

			var buf bytes.Buffer
			if _, err := buf.ReadFrom(rc); err != nil {
				return nil, fmt.Errorf("read %s: %w", f.Name, err)
			}
			return bytes.NewReader(buf.Bytes()), nil
		}
	}

	return nil, fmt.Errorf("no files found in ZIP archive")
}

// progress emits a progress message via the OnProgress callback.
func (d *Downloader) progress(format string, args ...any) {
	if d.opts.OnProgress != nil {
		d.opts.OnProgress(fmt.Sprintf(format, args...))
	}
}
