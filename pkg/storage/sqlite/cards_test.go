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

// TestSaveCards_DocumentsRoundTrip — guard гэпа documents (спека 2026-10-11):
// WB перезаписывает объект documents при /content/v2/cards/update целиком, поэтому
// загрузчик обязан складывать документы в card_documents (с doc_id), а карточный
// флаг — в cards.exclude_documents. Без этого cardupdate.LoadFullCard не может
// round-trip'нуть документы → каждый card-rewrite сшибал бы их на перепроверку.
//
// NULL-семантика exclude_documents: nil объект/поле → NULL (снапшот без documents),
// явное true/false → 1/0.
func TestSaveCards_DocumentsRoundTrip(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open mem db: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec(CardsSchemaSQL); err != nil {
		t.Fatalf("apply CardsSchemaSQL: %v", err)
	}

	exclude := false
	repo := &SQLiteSalesRepository{db: db}
	cards := []wb.ProductCard{
		{
			NmID: 100, VendorCode: "VC-100",
			Documents: &wb.CardDocuments{
				Items: []wb.CardDocument{
					{ID: "doc-uuid-1", Type: 2, Number: "ЕАЭС RU D-RU.AB12.B.05001/21", StartDate: "2026-01-01T00:00:00Z", EndDate: "2031-01-01T00:00:00Z"},
					{ID: "doc-uuid-2", Type: 1, Number: "ЕАЭС RU С-RU.AB12.B.05002/21", IsEndless: true},
				},
				ExcludeDocuments: &exclude,
			},
		},
		{NmID: 200, VendorCode: "VC-200"}, // без объекта documents → NULL-флаг, 0 строк
	}
	if _, err := repo.SaveCards(ctx, cards); err != nil {
		t.Fatalf("SaveCards: %v", err)
	}

	// Карточка с документами: обе строки на месте с их doc_id.
	rows, err := db.QueryContext(ctx, `
		SELECT doc_id, type, number, start_date, end_date, is_endless
		FROM card_documents WHERE nm_id = ? ORDER BY doc_id`, 100)
	if err != nil {
		t.Fatalf("select documents: %v", err)
	}
	defer rows.Close()
	var got []wb.CardDocument
	for rows.Next() {
		var d wb.CardDocument
		var isEndless int
		if err := rows.Scan(&d.ID, &d.Type, &d.Number, &d.StartDate, &d.EndDate, &isEndless); err != nil {
			t.Fatalf("scan document: %v", err)
		}
		d.IsEndless = isEndless != 0
		got = append(got, d)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("nm_id=100: документов %d, want 2", len(got))
	}
	if got[0].ID != "doc-uuid-1" || got[1].ID != "doc-uuid-2" {
		t.Errorf("doc_id round-trip: got %s/%s, want doc-uuid-1/doc-uuid-2", got[0].ID, got[1].ID)
	}
	if got[0].Type != 2 || got[0].Number != "ЕАЭС RU D-RU.AB12.B.05001/21" || got[0].EndDate != "2031-01-01T00:00:00Z" {
		t.Errorf("doc-uuid-1 поля не round-trip'нулись: %+v", got[0])
	}
	if !got[1].IsEndless {
		t.Errorf("doc-uuid-2 is_endless: got false, want true")
	}

	// Карточный флаг.
	var ex100, ex200 sql.NullInt64
	if err := db.QueryRowContext(ctx, "SELECT exclude_documents FROM cards WHERE nm_id = ?", 100).Scan(&ex100); err != nil {
		t.Fatalf("select exclude_documents nm_id=100: %v", err)
	}
	if err := db.QueryRowContext(ctx, "SELECT exclude_documents FROM cards WHERE nm_id = ?", 200).Scan(&ex200); err != nil {
		t.Fatalf("select exclude_documents nm_id=200: %v", err)
	}
	if !ex100.Valid || ex100.Int64 != 0 {
		t.Errorf("nm_id=100 exclude_documents=%v, want 0 (явный false)", ex100)
	}
	if ex200.Valid {
		t.Errorf("nm_id=200 exclude_documents=%v, want NULL (объекта documents не было)", ex200)
	}

	// Re-save той же карточки с одним документом: старые строки не накапливаются.
	cards[0].Documents.Items = cards[0].Documents.Items[:1]
	if _, err := repo.SaveCards(ctx, cards); err != nil {
		t.Fatalf("re-SaveCards: %v", err)
	}
	var n int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM card_documents WHERE nm_id = ?", 100).Scan(&n); err != nil {
		t.Fatalf("count after re-save: %v", err)
	}
	if n != 1 {
		t.Errorf("после re-save документов %d, want 1 (DELETE+INSERT, не накопление)", n)
	}
}
