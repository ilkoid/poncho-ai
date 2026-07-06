package sqlite

import (
	"context"
	"database/sql"
	"testing"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// TestSaveCards_KizMarkedRoundTrip — guard регрессии парсинг-гэпа kizMarked
// (этап 2-correction, 2026-07-07).
//
// До фикса ProductCard НЕ парсил kizMarked из ответа /content/v2/get/cards/list, а INSERT
// cards НЕ писал cards.kiz_marked → колонка всегда NULL. Следствие: cardupdate.LoadFullCard
// получал *bool=nil → ToUpdateItem опускал поле (omitempty) → WB /content/v2/cards/update
// применял default false → подтверждение маркировки «Честный ЗНАК» сбрасывалось при любом
// card-rewrite (провал модерации для need_kiz=1).
//
// Тест доказывает, что SaveCards пишёт реальное значение kizMarked в cards.kiz_marked.
// Парсинг ProductCard проверяется неявно: поле выставлено в mock-объекте, как его выставил бы
// json.Unmarshal из ответа WB.
func TestSaveCards_KizMarkedRoundTrip(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open mem db: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec(CardsSchemaSQL); err != nil {
		t.Fatalf("apply CardsSchemaSQL: %v", err)
	}

	repo := &SQLiteSalesRepository{db: db}
	cards := []wb.ProductCard{
		{NmID: 100, VendorCode: "VC-100", SubjectName: "Кроссовки", NeedKiz: true, KizMarked: true},
		{NmID: 200, VendorCode: "VC-200", SubjectName: "Футболка", NeedKiz: false, KizMarked: false},
	}
	if _, err := repo.SaveCards(ctx, cards); err != nil {
		t.Fatalf("SaveCards: %v", err)
	}

	var k100, k200 sql.NullInt64
	if err := db.QueryRowContext(ctx, "SELECT kiz_marked FROM cards WHERE nm_id = ?", 100).Scan(&k100); err != nil {
		t.Fatalf("select nm_id=100: %v", err)
	}
	if err := db.QueryRowContext(ctx, "SELECT kiz_marked FROM cards WHERE nm_id = ?", 200).Scan(&k200); err != nil {
		t.Fatalf("select nm_id=200: %v", err)
	}

	if !k100.Valid || k100.Int64 != 1 {
		t.Errorf("nm_id=100 (маркированный, подтверждён) kiz_marked=%v, want 1", k100)
	}
	if !k200.Valid || k200.Int64 != 0 {
		t.Errorf("nm_id=200 (немаркированный) kiz_marked=%v, want 0", k200)
	}
}
