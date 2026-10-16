package cardupdate

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ilkoid/poncho-ai/pkg/storage/sqlite"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// TestToUpdateItem_DocumentsSerialization — guard формата payload'а documents
// (спека 2026-10-11: объект перезаписывается целиком; документ с тем же id
// и теми же данными = «без изменений»). Nil-семантика: нет документов и нет
// флага exclude_documents → поле опускается (легаси-БД без card_documents).
func TestToUpdateItem_DocumentsSerialization(t *testing.T) {
	marshal := func(card FullCardData) string {
		b, err := json.Marshal(ToUpdateItem(card))
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return string(b)
	}

	t.Run("documents_with_ids_emitted", func(t *testing.T) {
		exclude := false
		out := marshal(FullCardData{
			NmID: 1, VendorCode: "VC", Brand: "B", Title: "T",
			Documents: []wb.CardDocument{
				{ID: "doc-uuid-1", Type: 2, Number: "ЕАЭС RU D-RU.AB12.B.05001/21", StartDate: "2026-01-01T00:00:00Z", EndDate: "2031-01-01T00:00:00Z"},
			},
			ExcludeDocuments: &exclude,
		})
		for _, want := range []string{
			`"documents":{"items":[{"id":"doc-uuid-1","type":2,"number":"ЕАЭС RU D-RU.AB12.B.05001/21"`,
			`"excludeDocuments":false`,
		} {
			if !strings.Contains(out, want) {
				t.Errorf("payload не содержит %s\npayload: %s", want, out)
			}
		}
	})

	t.Run("no_documents_omitted", func(t *testing.T) {
		out := marshal(FullCardData{NmID: 1, VendorCode: "VC", Brand: "B", Title: "T"})
		if strings.Contains(out, `"documents"`) {
			t.Errorf("payload без документов не должен содержать поле documents: %s", out)
		}
	})

	t.Run("exclude_only_flag_emitted", func(t *testing.T) {
		exclude := true
		out := marshal(FullCardData{
			NmID: 1, VendorCode: "VC", Brand: "B", Title: "T",
			ExcludeDocuments: &exclude,
		})
		if !strings.Contains(out, `"documents":{"items":[],"excludeDocuments":true}`) {
			t.Errorf("payload должен содержать documents с пустым items и excludeDocuments:true: %s", out)
		}
	})
}

// TestLoadFullCard_ToUpdateItem_DocumentsRoundTrip — сквозной тест канала:
// SaveCards (загрузчик) → card_documents → LoadFullCard → ToUpdateItem.
// Доказывает, что документы доезжают в payload 1:1 (id, тип, номер, даты, isEndless),
// а карточка без документов даёт nil (поле опускается).
func TestLoadFullCard_ToUpdateItem_DocumentsRoundTrip(t *testing.T) {
	ctx := context.Background()
	repo, err := sqlite.NewSQLiteSalesRepository(":memory:")
	if err != nil {
		t.Fatalf("open mem repo: %v", err)
	}
	defer repo.Close()

	exclude := false
	in := []wb.ProductCard{
		{
			NmID: 100, VendorCode: "VC-100", Brand: "B", Title: "T",
			Documents: &wb.CardDocuments{
				Items: []wb.CardDocument{
					{ID: "doc-uuid-1", Type: 2, Number: "N1", StartDate: "2026-01-01T00:00:00Z", EndDate: "2031-01-01T00:00:00Z"},
					{ID: "doc-uuid-2", Type: 1, Number: "N2", IsEndless: true},
				},
				ExcludeDocuments: &exclude,
			},
		},
		{NmID: 200, VendorCode: "VC-200", Brand: "B", Title: "T"},
	}
	if _, err := repo.SaveCards(ctx, in); err != nil {
		t.Fatalf("SaveCards: %v", err)
	}

	u := NewCardUpdater(repo.DB())

	card, err := u.LoadFullCard(ctx, 100)
	if err != nil {
		t.Fatalf("LoadFullCard nm_id=100: %v", err)
	}
	item := ToUpdateItem(card)
	if item.Documents == nil {
		t.Fatal("nm_id=100: documents == nil в payload, want round-trip")
	}
	if len(item.Documents.Items) != 2 ||
		item.Documents.Items[0].ID != "doc-uuid-1" ||
		item.Documents.Items[1].ID != "doc-uuid-2" {
		t.Errorf("nm_id=100: id не round-trip'нулись: %+v", item.Documents.Items)
	}
	if item.Documents.Items[1].IsEndless != true || item.Documents.Items[0].StartDate != "2026-01-01T00:00:00Z" {
		t.Errorf("nm_id=100: поля документов исказились: %+v", item.Documents.Items)
	}
	if item.Documents.ExcludeDocuments {
		t.Errorf("nm_id=100: excludeDocuments должен быть false")
	}

	card2, err := u.LoadFullCard(ctx, 200)
	if err != nil {
		t.Fatalf("LoadFullCard nm_id=200: %v", err)
	}
	if item2 := ToUpdateItem(card2); item2.Documents != nil {
		t.Errorf("nm_id=200 (без документов): documents != nil, want nil (поле опускается)")
	}
}
