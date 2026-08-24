// Package fbsorders provides a reusable FBS assembly tasks downloader.
//
// Architecture follows the v2 downloader pattern (dev_v2_postgres.md):
//   - Source — API abstraction (*wb.Client via WBSource adapter)
//   - Writer — persistence abstraction (PostgreSQL adapter; домен PG-only)
//   - Downloader — business logic depends only on interfaces
//
// Данные (RAW-слой, схема public):
//   - fbs_orders            — сборочные задания (GET /api/v3/orders)
//   - fbs_orders_status     — последний статус задания (1:1)
//   - fbs_orders_status_log — журнал уникальных состояний (воронка/тайминги ЖЦ)
//   - order_feed            — лента заказов: cancelType, география, возвраты
//
// Гарантия полноты статусов: каждый прогон обновляет статусы всех заданий
// свежего окна + всех незакрытых заданий любого возраста; ошибка батча
// статусов прерывает прогон (молчаливые пропуски исключены).
package fbsorders

import (
	"context"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Source is the data source interface for FBS order downloads.
// Implemented by WBSource (real API) and MockSource (--mock).
type Source interface {
	// FBSOrdersIterator итерирует по всем страницам /api/v3/orders за период.
	// Период [from, to] внутри режется на окна ≤ 30 дней (лимит API).
	FBSOrdersIterator(ctx context.Context, from, to time.Time, callback func([]wb.FBSOrder) error) (int, error)

	// GetFBSOrdersStatus возвращает текущие статусы по ID заданий (батч ≤ 1000).
	GetFBSOrdersStatus(ctx context.Context, ids []int64) ([]wb.FBSOrderStatus, error)

	// OrderFeedIterator итерирует по ленте заказов с даты from (≤ 31 сутки).
	OrderFeedIterator(ctx context.Context, from time.Time, callback func([]wb.OrderFeedItem) error) (int, error)
}

// Writer is the persistence interface (declared here, consumer — Rule 6).
// ISP: 4 метода — ровно то, что нужно Downloader.
type Writer interface {
	// SaveOrders сохраняет батч заданий с upsert-семантикой по id.
	SaveOrders(ctx context.Context, orders []wb.FBSOrder) (int, error)

	// SaveStatuses обновляет последний статус и вплетает состояния в журнал.
	SaveStatuses(ctx context.Context, statuses []wb.FBSOrderStatus) (int, error)

	// SaveOrderFeed сохраняет строки ленты заказов с upsert по srid.
	SaveOrderFeed(ctx context.Context, items []wb.OrderFeedItem) (int, error)

	// LoadStatusCandidateIDs возвращает ID заданий для обновления статусов:
	// свежее окно created_at >= createdSince + задания без статуса + незакрытые.
	LoadStatusCandidateIDs(ctx context.Context, createdSince time.Time) ([]int64, error)
}

// DownloadOptions configures the FBS download behavior.
type DownloadOptions struct {
	// Days — сколько дней назад качать задания (default: 90, глубина API).
	Days int

	// From/To переопределяют Days точными датами (YYYY-MM-DD).
	From string
	To   string

	// StatusWindowDays — окно безусловного обновления статусов (default: 90).
	// Незакрытые задания обновляются всегда, независимо от окна.
	StatusWindowDays int

	// DisableFeed отключает фазу ленты заказов (default: включена).
	DisableFeed bool

	// FeedDays — глубина ленты заказов в сутках (default: 7, максимум 31).
	FeedDays int

	// FeedMpOnly — сохранять из ленты только заказы склада продавца
	// (is_mp=true: FBS/DBS). API не умеет фильтровать по модели выполнения,
	// поэтому страницы качаются целиком, а не-FBS (FBW) отбрасываются до записи.
	// nil/default = true (утилита FBS-доменная). false = писать все модели.
	FeedMpOnly *bool

	// DryRun пропускает все записи в БД.
	DryRun bool

	// OnProgress callback для сообщений (nil = молча).
	OnProgress func(msg string)
}

// DownloadResult holds the outcome of an FBS download run.
type DownloadResult struct {
	TotalOrders      int
	StatusCandidates int
	StatusBatches    int
	TotalStatuses    int
	FeedRows         int
	FeedErr          string // нефатальная ошибка ленты (прогон завершён успешно)
	Duration         time.Duration
}
