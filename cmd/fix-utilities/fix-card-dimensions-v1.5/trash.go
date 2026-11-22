package main

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// trashPageSize — лимит страницы корзины; максимум по сваггеру — 100
// (02-items.yaml, /content/v2/get/cards/trash: cursor.limit maximum: 100).
const trashPageSize = 100

// Лимиты категории «Контент» по сваггеру (02-items.yaml): 100 req/min,
// интервал 600 мс, всплеск 5. toolID get_cards_trash — как в каноническом
// методе pkg/wb (ToolID-mismatch создаёт отдельный лимитер без состояния).
const (
	trashRatePerMin = 100
	trashRateBurst  = 5
)

// trashClient — узкое отверстие к WB для sync-trash (прецедент: wbUpdateClient).
// Сигнатура = страница канонического метода pkg/wb.GetCardsTrash;
// вторая реализация — фейк в тестах.
type trashClient interface {
	GetCardsTrash(ctx context.Context, settings wb.TrashCardsSettings, rateLimit, burst int) ([]wb.TrashCard, *wb.TrashCursorResponse, error)
}

// trashRow — строка кеш-таблицы cards_trash.
type trashRow struct {
	NmID       int
	VendorCode string
	TrashedAt  string
}

// cardsTrashDDL — кэш-таблица «карточки в корзине WB». Наполняет --sync-trash
// (полная перезаливка); пустая/несуществующая таблица = фильтр выключен.
const cardsTrashDDL = `
CREATE TABLE IF NOT EXISTS cards_trash (
    nm_id       INTEGER PRIMARY KEY,
    vendor_code TEXT NOT NULL DEFAULT '',
    trashed_at  TEXT NOT NULL DEFAULT '',
    synced_at   TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
`

// runSyncTrash обновляет cards_trash полным проходом корзины WB (read-only).
// Сначала выбираются все страницы, затем одна транзакция DELETE+INSERT: ошибка
// выборки оставляет вчерашнюю таблицу (консервативно — лучше лишний skip_basket
// на ночь, чем запись в WB вслепую), ошибка транзакции откатывает всё.
func runSyncTrash(ctx context.Context, db *sql.DB, client trashClient) error {
	fmt.Printf("=== fix-card-dimensions: sync trash ===\n\n")

	if _, err := db.ExecContext(ctx, cardsTrashDDL); err != nil {
		return fmt.Errorf("create cards_trash: %w", err)
	}

	rows, pages, err := fetchAllTrash(ctx, client)
	if err != nil {
		return fmt.Errorf("таблица оставлена прежней: %w", err)
	}

	if err := replaceTrashTable(ctx, db, rows); err != nil {
		return err
	}
	fmt.Printf("trash-sync: %d страниц, %d карточек в корзине\n", pages, len(rows))
	return nil
}

// emptyPageRetries — повторы «пустого мерцания»: аг-content реплики иногда
// отвечают 200 с пустыми cards и нулевым курсором на валидное продолжение
// (наблюдение 22.11: обрывы на 2-й и 22-й страницах при живом списке).
const emptyPageRetries = 3

// fetchAllTrash листает корзину до конца. Условие остановки — как в
// каноническом pkg/wb.GetAllTrashNmIDs: незаполненная страница (Total < limit).
func fetchAllTrash(ctx context.Context, client trashClient) ([]trashRow, int, error) {
	var all []trashRow
	pages := 0
	settings := wb.TrashCardsSettings{Cursor: wb.TrashCardsCursor{Limit: trashPageSize}}
	for {
		cards, cursor, err := fetchTrashPage(ctx, client, settings, pages)
		if err != nil {
			return nil, pages, err
		}
		pages++
		for _, c := range cards {
			all = append(all, trashRow{NmID: c.NmID, VendorCode: c.VendorCode, TrashedAt: c.TrashedAt})
		}
		if cursor == nil || cursor.Total < trashPageSize {
			return all, pages, nil
		}
		settings.Cursor = wb.TrashCardsCursor{
			Limit:     trashPageSize,
			TrashedAt: cursor.TrashedAt,
			NmID:      cursor.NmID,
		}
	}
}

// fetchTrashPage — одна страница с гардом от пустого мерцания. Пустая ПЕРВАЯ
// страница = корзина пуста (норма); пустая после полной — до emptyPageRetries
// повторов, затем ошибка: усечённая таблица хуже вчерашней полной (синк
// повторится следующей ночью, фаза в fill-dims.sh нефатальна).
func fetchTrashPage(ctx context.Context, client trashClient, settings wb.TrashCardsSettings, pagesBefore int) ([]wb.TrashCard, *wb.TrashCursorResponse, error) {
	for attempt := 1; ; attempt++ {
		cards, cursor, err := client.GetCardsTrash(ctx, settings, trashRatePerMin, trashRateBurst)
		if err != nil {
			return nil, nil, fmt.Errorf("страница %d: %w", pagesBefore+1, err)
		}
		if len(cards) > 0 || pagesBefore == 0 {
			return cards, cursor, nil
		}
		if attempt >= emptyPageRetries {
			return nil, nil, fmt.Errorf("страница %d: пустой ответ %d раз подряд при непустом списке (мерцание реплик?)",
				pagesBefore+1, attempt)
		}
		time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
	}
}

// replaceTrashTable атомарно заменяет содержимое cards_trash (батчами по 500).
func replaceTrashTable(ctx context.Context, db *sql.DB, rows []trashRow) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, "DELETE FROM cards_trash"); err != nil {
		return fmt.Errorf("clear cards_trash: %w", err)
	}

	const batchSize = 500
	for i := 0; i < len(rows); i += batchSize {
		end := min(i+batchSize, len(rows))
		var placeholders []string
		var args []any
		for _, r := range rows[i:end] {
			placeholders = append(placeholders, "(?, ?, ?)")
			args = append(args, r.NmID, r.VendorCode, r.TrashedAt)
		}
		q := fmt.Sprintf(
			"INSERT INTO cards_trash (nm_id, vendor_code, trashed_at) VALUES %s",
			strings.Join(placeholders, ", "))
		if _, err := tx.ExecContext(ctx, q, args...); err != nil {
			return fmt.Errorf("insert cards_trash batch at offset %d: %w", i, err)
		}
	}

	return tx.Commit()
}

// markBasketCards помечает staged-кандидатов, находящихся в корзине WB: тем
// cards/update отвечает 400 «in the basket and are prohibited for editing»
// на весь батч (инцидент 21–22.11: 4 карточки топили ночной apply и не давали
// доехать не-корзинным кандидатам). Помеченные не попадают в apply — тот берёт
// только status='pending'. Видимая отсечка с причиной, по образцу skipped_hollow.
func markBasketCards(ctx context.Context, db *sql.DB) (int64, error) {
	res, err := db.ExecContext(ctx, `
		UPDATE fix_card_dimensions_staging
		SET status = 'skipped_basket',
		    error_msg = 'в корзине (перенесена ' || COALESCE(NULLIF(
		        (SELECT t.trashed_at FROM cards_trash t
		          WHERE t.nm_id = fix_card_dimensions_staging.nm_id), ''), '?') || ')'
		WHERE status = 'pending'
		  AND EXISTS (SELECT 1 FROM cards_trash t
		               WHERE t.nm_id = fix_card_dimensions_staging.nm_id)
	`)
	if err != nil {
		return 0, fmt.Errorf("update staging: %w", err)
	}
	return res.RowsAffected()
}
