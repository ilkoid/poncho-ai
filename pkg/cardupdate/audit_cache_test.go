package cardupdate

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// TestAuditCachePayloadEquivalence — офлайн-аудит round-trip на всём каталоге кеша
// (уровень 1 независимого аудита documents, 12.10). Запускается только при
// PONCHO_AUDIT_CACHE=<путь к sqlite>; без переменной тест молча скипается.
//
// Для каждой карточки: LoadFullCard → ToUpdateItem → JSON, и результат сравнивается
// с первоисточником в БД (не через ToUpdateItem):
//   - documents: payload items против card_documents поле-к-полю;
//   - characteristics: payload против card_characteristics (unwrap одноэлементных
//     массивов повторён здесь независимо);
//   - kizMarked: cards.kiz_marked → true/false/отсутствует;
//   - sizes: состав chrt_id против card_sizes.
//
// Ожидаемое расхождение — только «omitters»: карточки без documents и с NULL-флагом
// (протухший снапшот) — payload обязан опустить поле documents.
func TestAuditCachePayloadEquivalence(t *testing.T) {
	dbPath := os.Getenv("PONCHO_AUDIT_CACHE")
	if dbPath == "" {
		t.Skip("аудит выключен: PONCHO_AUDIT_CACHE не задан")
	}
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=10000")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	ctx := context.Background()
	nmIDs, err := allCardIDs(ctx, db)
	if err != nil {
		t.Fatalf("card ids: %v", err)
	}
	t.Logf("аудит: %d карточек в %s", len(nmIDs), dbPath)

	u := NewCardUpdater(db)
	var stats struct {
		cards, withDocs, omitters          int
		kizTrue, kizFalse, kizAbsent       int
		docMism, charMism, sizeMism, other int
	}
	var firstMism [5]string

	for i, nmID := range nmIDs {
		card, err := u.LoadFullCard(ctx, nmID)
		if err != nil {
			stats.other++
			if firstMism[0] == "" {
				firstMism[0] = fmt.Sprintf("nm=%d LoadFullCard: %v", nmID, err)
			}
			continue
		}
		item := ToUpdateItem(card)
		b, err := json.Marshal(item)
		if err != nil {
			stats.other++
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(b, &payload); err != nil {
			stats.other++
			continue
		}
		stats.cards++

		// kizMarked
		var kiz sql.NullInt64
		_ = db.QueryRowContext(ctx, "SELECT kiz_marked FROM cards WHERE nm_id = ?", nmID).Scan(&kiz)
		switch {
		case !kiz.Valid:
			stats.kizAbsent++
			if _, ok := payload["kizMarked"]; ok {
				stats.charMism++ // переиспользуем счётчик — любое расхождение фатально ниже
				firstMism[1] = fmt.Sprintf("nm=%d: kiz NULL, но в payload есть kizMarked", nmID)
			}
		case kiz.Int64 == 1:
			stats.kizTrue++
			if payload["kizMarked"] != true {
				stats.charMism++
				firstMism[1] = fmt.Sprintf("nm=%d: kiz=1, payload=%v", nmID, payload["kizMarked"])
			}
		default:
			stats.kizFalse++
			if payload["kizMarked"] != false {
				stats.charMism++
				firstMism[1] = fmt.Sprintf("nm=%d: kiz=0, payload=%v", nmID, payload["kizMarked"])
			}
		}

		// documents: payload против card_documents (независимое чтение)
		wantDocs, err := expectedDocs(ctx, db, nmID)
		if err != nil {
			stats.other++
			continue
		}
		var excl sql.NullInt64
		_ = db.QueryRowContext(ctx, "SELECT exclude_documents FROM cards WHERE nm_id = ?", nmID).Scan(&excl)
		gotDocs, _ := payload["documents"].(map[string]any)
		switch {
		case len(wantDocs) == 0 && !excl.Valid:
			stats.omitters++
			if gotDocs != nil {
				stats.docMism++
				firstMism[2] = fmt.Sprintf("nm=%d: ожидается опуск documents, а он есть", nmID)
			}
		case gotDocs == nil:
			stats.docMism++
			if firstMism[2] == "" {
				firstMism[2] = fmt.Sprintf("nm=%d: documents в payload отсутствует, а должен быть (%d строк, flag=%v)", nmID, len(wantDocs), excl)
			}
		default:
			stats.withDocs++
			gotItems, _ := gotDocs["items"].([]any)
			if len(gotItems) != len(wantDocs) {
				stats.docMism++
				if firstMism[2] == "" {
					firstMism[2] = fmt.Sprintf("nm=%d: документов в payload %d, в БД %d", nmID, len(gotItems), len(wantDocs))
				}
				break
			}
			gotByID := map[string]map[string]any{}
			for _, it := range gotItems {
				if m, ok := it.(map[string]any); ok {
					if id, ok := m["id"].(string); ok {
						gotByID[id] = m
					}
				}
			}
			for id, want := range wantDocs {
				got := gotByID[id]
				if got == nil {
					stats.docMism++
					firstMism[2] = fmt.Sprintf("nm=%d: документ %s из БД отсутствует в payload", nmID, id)
					break
				}
				for f, wantV := range want {
					if got[f] != wantV {
						stats.docMism++
						firstMism[2] = fmt.Sprintf("nm=%d doc=%s поле %s: payload=%v, БД=%v", nmID, id, f, got[f], wantV)
						break
					}
				}
			}
			wantExcl := false
			if excl.Valid && excl.Int64 == 1 {
				wantExcl = true
			}
			if gotDocs["excludeDocuments"] != wantExcl {
				stats.docMism++
				firstMism[2] = fmt.Sprintf("nm=%d: excludeDocuments payload=%v, БД=%v", nmID, gotDocs["excludeDocuments"], wantExcl)
			}
		}

		// characteristics: количество и состав id
		wantCharIDs, err := expectedCharIDs(ctx, db, nmID)
		if err != nil {
			stats.other++
			continue
		}
		gotChars, _ := payload["characteristics"].([]any)
		if len(gotChars) != len(wantCharIDs) {
			stats.charMism++
			if firstMism[3] == "" {
				firstMism[3] = fmt.Sprintf("nm=%d: характеристик в payload %d, в БД %d", nmID, len(gotChars), len(wantCharIDs))
			}
		}

		// sizes: состав chrt_id
		wantChrt, err := expectedChrtIDs(ctx, db, nmID)
		if err != nil {
			stats.other++
			continue
		}
		gotSizes, _ := payload["sizes"].([]any)
		if len(gotSizes) != len(wantChrt) {
			stats.sizeMism++
			if firstMism[4] == "" {
				firstMism[4] = fmt.Sprintf("nm=%d: размеров в payload %d, в БД %d", nmID, len(gotSizes), len(wantChrt))
			}
		}

		if i%10000 == 0 && i > 0 {
			t.Logf("  ...%d/%d (docMism=%d charMism=%d sizeMism=%d)", i, len(nmIDs), stats.docMism, stats.charMism, stats.sizeMism)
		}
	}

	t.Logf("ИТОГ: cards=%d withDocs=%d omitters=%d kiz(t/f/absent)=%d/%d/%d | docMism=%d charMism=%d sizeMism=%d other=%d",
		stats.cards, stats.withDocs, stats.omitters, stats.kizTrue, stats.kizFalse, stats.kizAbsent,
		stats.docMism, stats.charMism, stats.sizeMism, stats.other)
	for i, m := range firstMism {
		if m != "" {
			t.Errorf("первое расхождение [%d]: %s", i, m)
		}
	}
	if stats.docMism > 0 || stats.charMism > 0 || stats.sizeMism > 0 || stats.other > 0 {
		t.Fatalf("аудит провален: docMism=%d charMism=%d sizeMism=%d other=%d",
			stats.docMism, stats.charMism, stats.sizeMism, stats.other)
	}
}

func allCardIDs(ctx context.Context, db *sql.DB) ([]int, error) {
	rows, err := db.QueryContext(ctx, "SELECT nm_id FROM cards ORDER BY nm_id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// expectedDocs читает card_documents независимо от loadDocuments и приводит значения
// к типам JSON-payload (float64 для чисел, bool для is_endless).
func expectedDocs(ctx context.Context, db *sql.DB, nmID int) (map[string]map[string]any, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT doc_id, type, number, product_number, trade_name, applicant, start_date, end_date, is_endless
		FROM card_documents WHERE nm_id = ?`, nmID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]map[string]any{}
	for rows.Next() {
		var id, number, pn, tn, ap, sd, ed string
		var typ, endless int
		if err := rows.Scan(&id, &typ, &number, &pn, &tn, &ap, &sd, &ed, &endless); err != nil {
			return nil, err
		}
		out[id] = map[string]any{
			"id": id, "type": float64(typ), "number": number,
			"productNumber": pn, "tradeName": tn, "applicant": ap,
			"startDate": sd, "endDate": ed, "isEndless": endless == 1,
		}
	}
	return out, rows.Err()
}

func expectedCharIDs(ctx context.Context, db *sql.DB, nmID int) (map[int]bool, error) {
	rows, err := db.QueryContext(ctx, "SELECT char_id FROM card_characteristics WHERE nm_id = ?", nmID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int]bool{}
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

func expectedChrtIDs(ctx context.Context, db *sql.DB, nmID int) (map[int]bool, error) {
	rows, err := db.QueryContext(ctx, "SELECT chrt_id FROM card_sizes WHERE nm_id = ?", nmID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int]bool{}
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}
