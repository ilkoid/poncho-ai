package feedbacks

import (
	"context"
	"fmt"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Downloader is a reusable feedbacks + questions downloader.
// Depends on Source (WB API) and Writer (persistence) — both are interfaces.
//
// Usage:
//
//	dl := feedbacks.NewDownloader(source, writer, opts)
//	result, err := dl.Run(ctx)
type Downloader struct {
	source Source
	writer Writer
	opts   DownloadOptions
}

// NewDownloader creates a feedbacks downloader from source, writer, and options.
// Applies defaults: Feedbacks=true, Questions=true, Days=7.
func NewDownloader(source Source, writer Writer, opts DownloadOptions) *Downloader {
	if opts.Days <= 0 {
		opts.Days = 7
	}
	if opts.FeedbacksRate <= 0 {
		opts.FeedbacksRate = 180
	}
	if opts.FeedbacksBurst <= 0 {
		opts.FeedbacksBurst = 6
	}
	if opts.QuestionsRate <= 0 {
		opts.QuestionsRate = 180
	}
	if opts.QuestionsBurst <= 0 {
		opts.QuestionsBurst = 6
	}
	return &Downloader{
		source: source,
		writer: writer,
		opts:   opts,
	}
}

// Run executes the full feedbacks + questions download pipeline.
//
// Flow:
//  1. Resolve date range → Unix timestamps
//  2. If Feedbacks enabled: two passes (isAnswered=false, then true), pagination loop
//  3. If Questions enabled: two passes + recursive period splitting
//  4. Return result with counts and duration
func (d *Downloader) Run(ctx context.Context) (*DownloadResult, error) {
	start := time.Now()
	result := &DownloadResult{}

	dateFrom, dateTo, err := d.resolveDateRange()
	if err != nil {
		return nil, fmt.Errorf("resolve dates: %w", err)
	}

	tsFrom := dateToTimestamp(dateFrom, false)
	tsTo := dateToTimestamp(dateTo, true)

	d.progress("📅 Period: %s → %s", dateFrom, dateTo)

	// Download feedbacks (two passes: unanswered, then answered)
	if d.opts.Feedbacks {
		select {
		case <-ctx.Done():
			return result, fmt.Errorf("context cancelled: %w", ctx.Err())
		default:
		}

		d.progress("📥 Downloading feedbacks...")
		for i, isAnswered := range []bool{false, true} {
			saved, pages, err := d.downloadFeedbacks(ctx, tsFrom, tsTo, isAnswered)
			if err != nil {
				return result, fmt.Errorf("feedbacks pass %d (isAnswered=%t): %w", i+1, isAnswered, err)
			}
			result.FeedbacksSaved += saved
			result.FeedbacksPages += pages
			d.progress("  pass %d (isAnswered=%t): %d feedbacks, %d pages", i+1, isAnswered, saved, pages)
		}
	}

	// Download questions (two passes + period splitting)
	if d.opts.Questions {
		select {
		case <-ctx.Done():
			return result, fmt.Errorf("context cancelled: %w", ctx.Err())
		default:
		}

		d.progress("📥 Downloading questions...")
		for i, isAnswered := range []bool{false, true} {
			saved, pages, err := d.downloadQuestions(ctx, tsFrom, tsTo, isAnswered)
			if err != nil {
				return result, fmt.Errorf("questions pass %d (isAnswered=%t): %w", i+1, isAnswered, err)
			}
			result.QuestionsSaved += saved
			result.QuestionsPages += pages
			d.progress("  pass %d (isAnswered=%t): %d questions, %d pages", i+1, isAnswered, saved, pages)
		}
	}

	result.Duration = time.Since(start)
	return result, nil
}

// downloadFeedbacks downloads all feedbacks for a given isAnswered filter.
// Uses simple pagination: skip 0, 5000, 10000, ... up to FeedbacksMaxSkip.
func (d *Downloader) downloadFeedbacks(ctx context.Context, dateFrom, dateTo int64, isAnswered bool) (saved, pages int, err error) {
	for skip := 0; skip <= FeedbacksMaxSkip; skip += FeedbacksMaxTake {
		select {
		case <-ctx.Done():
			return saved, pages, fmt.Errorf("context cancelled: %w", ctx.Err())
		default:
		}

		items, err := d.source.GetFeedbacksPage(ctx, PageRequest{
			IsAnswered: isAnswered,
			Take:       FeedbacksMaxTake,
			Skip:       skip,
			DateFrom:   dateFrom,
			DateTo:     dateTo,
			RateLimit:  d.opts.FeedbacksRate,
			Burst:      d.opts.FeedbacksBurst,
		})
		if err != nil {
			return saved, pages, fmt.Errorf("skip=%d: %w", skip, err)
		}

		if len(items) == 0 {
			break
		}

		pages++
		if d.opts.DryRun {
			d.progress("    [dry-run] skip=%d: %d feedbacks", skip, len(items))
			saved += len(items)
		} else {
			n, err := d.writer.SaveFeedbacks(ctx, items)
			if err != nil {
				return saved, pages, fmt.Errorf("save at skip=%d: %w", skip, err)
			}
			saved += n
			d.progress("    skip=%d: %d feedbacks saved", skip, n)
		}

		if len(items) < FeedbacksMaxTake {
			break
		}
	}
	return saved, pages, nil
}

// downloadQuestions downloads all questions for a given isAnswered filter.
// Delegates to downloadQuestionsPeriod which handles recursive period splitting.
func (d *Downloader) downloadQuestions(ctx context.Context, dateFrom, dateTo int64, isAnswered bool) (saved, pages int, err error) {
	return d.downloadQuestionsPeriod(ctx, dateFrom, dateTo, isAnswered, 0)
}

// downloadQuestionsPeriod downloads questions for a single time period.
//
// Questions API has a hard limit: take + skip ≤ 10000.
// Strategy:
//  1. First page: take=5000, skip=0
//  2. If full → second page: take=5000, skip=5000
//  3. If second page also full → split period in half, recurse (max depth 10)
//
// This is the proven v1 logic, migrated verbatim.
func (d *Downloader) downloadQuestionsPeriod(
	ctx context.Context,
	dateFrom, dateTo int64,
	isAnswered bool,
	depth int,
) (saved, pages int, err error) {
	if depth > MaxSplitDepth {
		return 0, 0, fmt.Errorf("max split depth reached (%d)", MaxSplitDepth)
	}

	// First page: take=5000, skip=0
	items, err := d.source.GetQuestionsPage(ctx, PageRequest{
		IsAnswered: isAnswered,
		Take:       QuestionsMaxTake,
		Skip:       0,
		DateFrom:   dateFrom,
		DateTo:     dateTo,
		RateLimit:  d.opts.QuestionsRate,
		Burst:      d.opts.QuestionsBurst,
	})
	if err != nil {
		return 0, 0, err
	}

	if len(items) == 0 {
		return 0, 0, nil
	}

	pages++
	if d.opts.DryRun {
		d.progress("    [dry-run] questions skip=0: %d items (depth=%d)", len(items), depth)
		saved += len(items)
	} else {
		n, err := d.writer.SaveQuestions(ctx, items)
		if err != nil {
			return 0, pages, fmt.Errorf("save questions skip=0: %w", err)
		}
		saved += n
	}

	// If first page is full, try second page
	if len(items) == QuestionsMaxTake {
		items2, err := d.source.GetQuestionsPage(ctx, PageRequest{
			IsAnswered: isAnswered,
			Take:       QuestionsMaxTake,
			Skip:       QuestionsMaxSkip,
			DateFrom:   dateFrom,
			DateTo:     dateTo,
			RateLimit:  d.opts.QuestionsRate,
			Burst:      d.opts.QuestionsBurst,
		})
		if err != nil {
			return saved, pages, fmt.Errorf("questions skip=%d: %w", QuestionsMaxSkip, err)
		}

		if len(items2) > 0 {
			pages++
			if d.opts.DryRun {
				d.progress("    [dry-run] questions skip=%d: %d items (depth=%d)", QuestionsMaxSkip, len(items2), depth)
				saved += len(items2)
			} else {
				n, err := d.writer.SaveQuestions(ctx, items2)
				if err != nil {
					return saved, pages, fmt.Errorf("save questions skip=%d: %w", QuestionsMaxSkip, err)
				}
				saved += n
			}
		}

		// If second page also full → split period in half and recurse
		if len(items2) == QuestionsMaxTake {
			d.progress("      splitting period (depth=%d)...", depth)
			mid := (dateFrom + dateTo) / 2

			n1, p1, err := d.downloadQuestionsPeriod(ctx, dateFrom, mid, isAnswered, depth+1)
			if err != nil {
				return saved, pages, fmt.Errorf("first half: %w", err)
			}
			saved += n1
			pages += p1

			n2, p2, err := d.downloadQuestionsPeriod(ctx, mid+1, dateTo, isAnswered, depth+1)
			if err != nil {
				return saved, pages, fmt.Errorf("second half: %w", err)
			}
			saved += n2
			pages += p2
		}
	}

	return saved, pages, nil
}

// resolveDateRange returns (dateFrom, dateTo) as YYYY-MM-DD strings.
// Priority: explicit DateFrom/DateTo → computed from Days.
func (d *Downloader) resolveDateRange() (string, string, error) {
	if d.opts.DateFrom != "" && d.opts.DateTo != "" {
		return d.opts.DateFrom, d.opts.DateTo, nil
	}

	days := d.opts.Days
	if days <= 0 {
		days = 7
	}

	now := time.Now()
	// --days 7 = last 7 days excluding today
	end := now.AddDate(0, 0, -1).Format("2006-01-02")
	begin := now.AddDate(0, 0, -days).Format("2006-01-02")
	return begin, end, nil
}

// dateToTimestamp converts a YYYY-MM-DD string to Unix timestamp.
// For "from" dates (endOfDay=false), returns start of day (00:00:00).
// For "to" dates (endOfDay=true), returns end of day (23:59:59).
func dateToTimestamp(dateStr string, endOfDay bool) int64 {
	layout := "2006-01-02"
	t, err := time.Parse(layout, dateStr)
	if err != nil {
		return 0
	}
	if endOfDay {
		t = t.Add(23*time.Hour + 59*time.Minute + 59*time.Second)
	}
	return t.Unix()
}

// progress emits a progress message via the OnProgress callback if set.
func (d *Downloader) progress(format string, args ...any) {
	if d.opts.OnProgress != nil {
		d.opts.OnProgress(fmt.Sprintf(format, args...))
	}
}

// Compile-time check: wb.FeedbackFull and wb.QuestionFull must be usable by the downloader.
var (
	_ []wb.FeedbackFull = nil
	_ []wb.QuestionFull = nil
)
