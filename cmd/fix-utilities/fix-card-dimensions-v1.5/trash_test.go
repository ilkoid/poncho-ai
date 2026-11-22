package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// ============================================================================
// sync-trash: пагинация, идемпотентность, сохранность таблицы при ошибке
// ============================================================================

// fakeTrashClient — фейк канонического pkg/wb.GetCardsTrash (страница корзины).
type fakeTrashClient struct {
	pages     [][]wb.TrashCard // страницы ответов по порядку
	failAt    int              // номер страницы (с 1), на которой вернуть ошибку
	emptyFlap map[int]int      // номер страницы → сколько пустых ответов выдать прежде настоящей (мерцание)
	failedOn  bool
}

func (f *fakeTrashClient) GetCardsTrash(_ context.Context, _ wb.TrashCardsSettings, _, _ int) ([]wb.TrashCard, *wb.TrashCursorResponse, error) {
	n := len(f.pages)
	if f.failAt > 0 && f.failAt == n+1 {
		f.failedOn = true
		return nil, nil, errors.New("wb api error: status 429")
	}
	if f.emptyFlap[n] > 0 {
		f.emptyFlap[n]--
		return nil, &wb.TrashCursorResponse{}, nil
	}
	page := f.pages[0]
	f.pages = f.pages[1:]
	cursor := &wb.TrashCursorResponse{
		TrashedAt: fmt.Sprintf("2026-11-%02dT00:00:00Z", n-len(f.pages)+1),
		NmID:      page[len(page)-1].NmID,
		Total:     len(page),
	}
	return page, cursor, nil
}

func makeTrashPage(firstNmID, count int) []wb.TrashCard {
	page := make([]wb.TrashCard, 0, count)
	for i := 0; i < count; i++ {
		page = append(page, wb.TrashCard{
			ProductCard: wb.ProductCard{NmID: firstNmID + i, VendorCode: fmt.Sprintf("%d", firstNmID+i)},
			TrashedAt:   "2026-11-13T09:33:58Z",
		})
	}
	return page
}

func trashCount(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM cards_trash").Scan(&n); err != nil {
		t.Fatalf("count cards_trash: %v", err)
	}
	return n
}

func TestSyncCardsTrashPagination(t *testing.T) {
	db := newApplyTestDB(t)
	client := &fakeTrashClient{pages: [][]wb.TrashCard{
		makeTrashPage(1001, 100), // полная страница — продолжаем
		makeTrashPage(2001, 100), // полная страница — продолжаем
		makeTrashPage(3001, 37),  // короткая — стоп
	}}

	if err := runSyncTrash(context.Background(), db, client); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if got := trashCount(t, db); got != 237 {
		t.Fatalf("строк в cards_trash = %d, хочу 237 (100+100+37)", got)
	}

	// Идемпотентность: повторный sync той же корзины не растит таблицу.
	client2 := &fakeTrashClient{pages: [][]wb.TrashCard{
		makeTrashPage(1001, 100),
		makeTrashPage(2001, 100),
		makeTrashPage(3001, 37),
	}}
	if err := runSyncTrash(context.Background(), db, client2); err != nil {
		t.Fatalf("повторный sync: %v", err)
	}
	if got := trashCount(t, db); got != 237 {
		t.Fatalf("после повторного sync строк = %d, хочу 237 (замена, не дубли)", got)
	}
}

func TestSyncTrashKeepsOldTableOnFetchError(t *testing.T) {
	db := newApplyTestDB(t)
	if _, err := db.Exec(cardsTrashDDL); err != nil {
		t.Fatalf("ddl: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO cards_trash (nm_id, vendor_code, trashed_at) VALUES (7, '7', 'старая')`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	client := &fakeTrashClient{pages: [][]wb.TrashCard{makeTrashPage(1001, 100)}, failAt: 2}
	if err := runSyncTrash(context.Background(), db, client); err == nil {
		t.Fatal("ожидал ошибку выборки")
	}
	if got := trashCount(t, db); got != 1 {
		t.Fatalf("таблица перезаписана при ошибке: %d строк, хочу прежние 1", got)
	}
	var trashedAt string
	if err := db.QueryRow("SELECT trashed_at FROM cards_trash WHERE nm_id=7").Scan(&trashedAt); err != nil || trashedAt != "старая" {
		t.Fatalf("вчерашняя строка затёрта: %q / %v", trashedAt, err)
	}
}

func TestSyncTrashRetriesEmptyPageFlap(t *testing.T) {
	t.Run("мерцание зажило повтором — полный проход", func(t *testing.T) {
		db := newApplyTestDB(t)
		// страница 2 один раз отвечает пустышкой (мерцание реплики);
		// в момент 2-го запроса len(pages)=2 → ключ флапа = 2
		client := &fakeTrashClient{
			pages: [][]wb.TrashCard{
				makeTrashPage(1001, 100),
				makeTrashPage(2001, 100),
				makeTrashPage(3001, 37),
			},
			emptyFlap: map[int]int{2: 1},
		}
		if err := runSyncTrash(context.Background(), db, client); err != nil {
			t.Fatalf("sync: %v", err)
		}
		if got := trashCount(t, db); got != 237 {
			t.Fatalf("строк = %d, хочу 237 (пустышка не оборвала проход)", got)
		}
	})

	t.Run("мерцание стойко 3 раза — ошибка, вчерашняя таблица цела", func(t *testing.T) {
		db := newApplyTestDB(t)
		if _, err := db.Exec(cardsTrashDDL); err != nil {
			t.Fatalf("ddl: %v", err)
		}
		if _, err := db.Exec(`INSERT INTO cards_trash (nm_id, vendor_code, trashed_at) VALUES (7, '7', 'старая')`); err != nil {
			t.Fatalf("seed: %v", err)
		}
		client := &fakeTrashClient{
			// 2 страницы: в момент 2-го запроса len(pages)=1 → ключ флапа = 1
			pages:     [][]wb.TrashCard{makeTrashPage(1001, 100), makeTrashPage(2001, 100)},
			emptyFlap: map[int]int{1: emptyPageRetries},
		}
		if err := runSyncTrash(context.Background(), db, client); err == nil {
			t.Fatal("ожидал ошибку стойкого мерцания")
		}
		if got := trashCount(t, db); got != 1 {
			t.Fatalf("таблица перезаписана: %d строк, хочу прежние 1", got)
		}
	})
}

// ============================================================================
// stage-разметка: корзинные → skipped_basket, apply их не видит
// ============================================================================

func TestMarkBasketCards(t *testing.T) {
	db := newApplyTestDB(t)
	if _, err := db.Exec(cardsTrashDDL); err != nil {
		t.Fatalf("ddl: %v", err)
	}

	for _, row := range []struct{ nm, trash int }{
		{nm: 1001, trash: 1}, // корзинная (трэшнута 13.11)
		{nm: 1002, trash: 0}, // чистая
		{nm: 1003, trash: 1}, // корзинная без даты (проверка COALESCE '?')
	} {
		seedStaging(t, db, row.nm, "")
		if row.trash == 1 {
			at := "2026-11-13T09:33:58Z"
			if row.nm == 1003 {
				at = ""
			}
			if _, err := db.Exec(`INSERT INTO cards_trash (nm_id, vendor_code, trashed_at) VALUES (?, ?, ?)`,
				row.nm, fmt.Sprintf("%d", row.nm), at); err != nil {
				t.Fatalf("seed trash %d: %v", row.nm, err)
			}
		}
	}

	marked, err := markBasketCards(context.Background(), db)
	if err != nil {
		t.Fatalf("mark: %v", err)
	}
	if marked != 2 {
		t.Fatalf("помечено %d, хочу 2", marked)
	}

	st := stagingStatuses(t, db)
	if st[1001][0] != "skipped_basket" || st[1001][1] != "в корзине (перенесена 2026-11-13T09:33:58Z)" {
		t.Fatalf("1001: %v", st[1001])
	}
	if st[1003][0] != "skipped_basket" || st[1003][1] != "в корзине (перенесена ?)" {
		t.Fatalf("1003 (без даты): %v", st[1003])
	}
	if st[1002][0] != "pending" {
		t.Fatalf("чистая 1002 помечена ошибочно: %v", st[1002])
	}

	// Инвариант применения (OCP): apply берёт только pending — корзинные
	// в батч не попадают вовсе, без единой правки apply.go.
	pending, err := loadPendingStaging(context.Background(), db)
	if err != nil {
		t.Fatalf("load pending: %v", err)
	}
	if len(pending) != 1 || pending[0].NmID != 1002 {
		t.Fatalf("pending = %v, хочу только чистую 1002", pending)
	}
}
