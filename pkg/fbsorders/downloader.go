package fbsorders

import (
	"context"
	"fmt"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// statusBatchSize — максимум ID сборочных заданий в одном запросе статусов
// (swagger /api/v3/orders/status: maxItems 1000).
const statusBatchSize = 1000

// Downloader is a reusable FBS assembly tasks downloader.
//
// Usage:
//
//	dl := fbsorders.NewDownloader(source, writer, opts)
//	result, err := dl.Run(ctx)
type Downloader struct {
	source Source
	writer Writer
	opts   DownloadOptions
}

// NewDownloader creates an FBS downloader from source, writer and options.
func NewDownloader(source Source, writer Writer, opts DownloadOptions) *Downloader {
	if opts.Days <= 0 {
		opts.Days = 90
	}
	if opts.StatusWindowDays <= 0 {
		opts.StatusWindowDays = 90
	}
	if opts.FeedDays <= 0 {
		opts.FeedDays = 7
	}
	return &Downloader{source: source, writer: writer, opts: opts}
}

// Run выполняет три фазы:
//
//  1. Задания: /api/v3/orders за [from, to] → fbs_orders (идемпотентный upsert).
//  2. Статусы: все кандидаты свежего окна + незакрытые → fbs_orders_status +
//     fbs_orders_status_log. Ошибка батча ФАТАЛЬНА — прогон прерывается,
//     пропусков переходов не бывает.
//  3. Лента заказов (если не отключена): order-feed за FeedDays суток →
//     order_feed. Сбой нефатален (1 req/min на стороне WB хрупок), фиксируется
//     в DownloadResult.FeedErr.
func (d *Downloader) Run(ctx context.Context) (*DownloadResult, error) {
	start := time.Now()
	res := &DownloadResult{}

	from, to, err := d.resolveRange()
	if err != nil {
		return res, err
	}

	// ---- Фаза 1: сборочные задания -------------------------------------
	d.progress("фаза 1/3: задания FBS за %s .. %s (окна по 30 дней)",
		from.Format("2006-01-02"), to.Format("2006-01-02"))

	_, err = d.source.FBSOrdersIterator(ctx, from, to, func(page []wb.FBSOrder) error {
		if d.opts.DryRun {
			res.TotalOrders += len(page)
			d.progress("dry-run: страница %d заданий пропущена", len(page))
			return nil
		}
		n, err := d.writer.SaveOrders(ctx, page)
		if err != nil {
			return fmt.Errorf("save orders: %w", err)
		}
		res.TotalOrders += n
		return nil
	})
	if err != nil {
		res.Duration = time.Since(start)
		return res, fmt.Errorf("fbs orders download: %w", err)
	}
	d.progress("задания: %d сохранено", res.TotalOrders)

	// ---- Фаза 2: статусы (фатальные ошибки) ----------------------------
	if d.opts.DryRun {
		d.progress("dry-run: фаза статусов пропущена (кандидаты читаются из БД)")
	} else if err := d.runStatusPhase(ctx, start, res); err != nil {
		res.Duration = time.Since(start)
		return res, err
	}

	// ---- Фаза 3: лента заказов (нефатально) ----------------------------
	if !d.opts.DisableFeed {
		feedFrom := time.Now().UTC().AddDate(0, 0, -d.opts.FeedDays)
		d.progress("фаза 3/3: лента заказов с %s (по дате текущего статуса)", feedFrom.Format("2006-01-02"))

		_, err := d.source.OrderFeedIterator(ctx, feedFrom, func(page []wb.OrderFeedItem) error {
			if d.opts.DryRun {
				res.FeedRows += len(page)
				return nil
			}
			n, err := d.writer.SaveOrderFeed(ctx, page)
			if err != nil {
				return fmt.Errorf("save order feed: %w", err)
			}
			res.FeedRows += n
			return nil
		})
		if err != nil {
			// Нефатально: статусы — обязательная часть, лента — аддитивная.
			res.FeedErr = err.Error()
			d.progress("warning: лента заказов не загружена (прогон успешен): %v", err)
		} else {
			d.progress("лента: %d строк", res.FeedRows)
		}
	}

	res.Duration = time.Since(start)
	return res, nil
}

// runStatusPhase обновляет статусы всех кандидатов батчами по 1000.
func (d *Downloader) runStatusPhase(ctx context.Context, start time.Time, res *DownloadResult) error {
	cutoff := time.Now().UTC().AddDate(0, 0, -d.opts.StatusWindowDays)
	ids, err := d.writer.LoadStatusCandidateIDs(ctx, cutoff)
	if err != nil {
		return fmt.Errorf("load status candidates: %w", err)
	}
	res.StatusCandidates = len(ids)

	d.progress("фаза 2/3: статусы %d заданий (окно %d дн + незакрытые), батчи по %d",
		len(ids), d.opts.StatusWindowDays, statusBatchSize)

	for i := 0; i < len(ids); i += statusBatchSize {
		end := i + statusBatchSize
		if end > len(ids) {
			end = len(ids)
		}
		batch := ids[i:end]

		statuses, err := d.source.GetFBSOrdersStatus(ctx, batch)
		if err != nil {
			// Фатально: молчаливый пропуск батча ломает полноту журнала статусов.
			return fmt.Errorf("status batch %d..%d of %d: %w (прогон прерван — перезапустите)",
				i, end, len(ids), err)
		}

		n, err := d.writer.SaveStatuses(ctx, statuses)
		if err != nil {
			return fmt.Errorf("save statuses batch %d..%d of %d: %w", i, end, len(ids), err)
		}
		res.StatusBatches++
		res.TotalStatuses += n

		if res.StatusBatches%10 == 0 || end == len(ids) {
			d.progress("статусы: %d/%d заданий", end, len(ids))
		}
	}
	return nil
}

// resolveRange вычисляет [from, to] из From/To/Days. to — всегда now (UTC).
func (d *Downloader) resolveRange() (time.Time, time.Time, error) {
	to := time.Now().UTC()
	if d.opts.To != "" {
		t, err := time.ParseInLocation("2006-01-02", d.opts.To, time.UTC)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("parse to %q: %w", d.opts.To, err)
		}
		to = t.Add(24 * time.Hour) // включительно
	}

	var from time.Time
	if d.opts.From != "" {
		f, err := time.ParseInLocation("2006-01-02", d.opts.From, time.UTC)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("parse from %q: %w", d.opts.From, err)
		}
		from = f
	} else {
		from = to.AddDate(0, 0, -d.opts.Days)
	}

	if !from.Before(to) {
		return time.Time{}, time.Time{}, fmt.Errorf("empty period: from=%s to=%s", from.Format("2006-01-02"), to.Format("2006-01-02"))
	}
	return from, to, nil
}

// progress calls the OnProgress callback if set.
func (d *Downloader) progress(format string, args ...any) {
	if d.opts.OnProgress != nil {
		d.opts.OnProgress(fmt.Sprintf(format, args...))
	}
}
