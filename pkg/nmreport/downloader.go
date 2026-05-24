package nmreport

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Downloader handles async CSV funnel report lifecycle.
type Downloader struct {
	source NmReportSource
	writer NmReportWriter
	opts   DownloadOptions
}

// NewDownloader creates a nm-report downloader from source and writer.
func NewDownloader(source NmReportSource, writer NmReportWriter, opts DownloadOptions) *Downloader {
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
	downloadID := uuid.New().String()

	// Resume: check if we already have a successful report for this period
	if d.opts.Resume {
		existing, err := d.writer.GetNmReport(ctx, reportType, from, to)
		if err != nil {
			d.progress("Resume check failed: %v", err)
		} else if existing != nil && existing.Status == wb.StockHistoryStatusSuccess {
			d.progress("Resumed: report %s already completed", existing.ID)
			result.DownloadID = existing.ID
			result.Status = "RESUMED"
			result.Duration = time.Since(start)
			return result, nil
		}
	}

	// Create report
	req := wb.NmReportFunnelRequest{
		ID:         downloadID,
		ReportType: reportType,
		Params: wb.NmReportFunnelParams{
			StartDate: from,
			EndDate:   to,
		},
	}

	id, err := d.source.CreateReport(ctx, req)
	if err != nil {
		return result, fmt.Errorf("create report: %w", err)
	}
	result.DownloadID = id
	d.progress("Report created: %s", id)

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

	// Parse CSV
	if reportType == "DETAIL_HISTORY_REPORT" {
		rows, err := ParseDetailCSV(csvReader)
		if err != nil {
			return result, fmt.Errorf("parse detail CSV: %w", err)
		}
		result.RowsCount = len(rows)
		d.progress("Parsed %d detail rows", len(rows))

		if d.opts.DryRun {
			result.Status = "SUCCESS"
			result.Duration = time.Since(start)
			return result, nil
		}

		// Save report metadata
		if err := d.writer.SaveNmReport(ctx, NmReportRecord{
			ID:         id,
			ReportType: reportType,
			StartDate:  from,
			EndDate:    to,
			Status:     wb.StockHistoryStatusWaiting,
			FileSize:   int64(len(zipBytes)),
			RowsCount:  len(rows),
			CreatedAt:  time.Now().Format("2006-01-02 15:04:05"),
		}); err != nil {
			d.progress("Warning: save report metadata: %v", err)
		}

		// Save detail rows
		if err := d.writer.SaveFunnelMetricsDetail(ctx, rows, d.opts.RefreshWindow); err != nil {
			return result, fmt.Errorf("save detail rows: %w", err)
		}
	} else {
		rows, err := ParseGroupedCSV(csvReader)
		if err != nil {
			return result, fmt.Errorf("parse grouped CSV: %w", err)
		}
		result.RowsCount = len(rows)
		d.progress("Parsed %d grouped rows", len(rows))

		if d.opts.DryRun {
			result.Status = "SUCCESS"
			result.Duration = time.Since(start)
			return result, nil
		}

		if err := d.writer.SaveNmReport(ctx, NmReportRecord{
			ID:         id,
			ReportType: reportType,
			StartDate:  from,
			EndDate:    to,
			Status:     wb.StockHistoryStatusWaiting,
			FileSize:   int64(len(zipBytes)),
			RowsCount:  len(rows),
			CreatedAt:  time.Now().Format("2006-01-02 15:04:05"),
		}); err != nil {
			d.progress("Warning: save report metadata: %v", err)
		}

		if err := d.writer.SaveFunnelMetricsGrouped(ctx, rows); err != nil {
			return result, fmt.Errorf("save grouped rows: %w", err)
		}
	}

	// Update report status
	if err := d.writer.UpdateNmReportStatus(ctx, id, wb.StockHistoryStatusSuccess, result.RowsCount); err != nil {
		d.progress("Warning: update report status: %v", err)
	}

	result.Status = "SUCCESS"
	result.Duration = time.Since(start)
	return result, nil
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
	if d.opts.ReportType == "grouped" {
		return "GROUPED_HISTORY_REPORT"
	}
	return "DETAIL_HISTORY_REPORT"
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
