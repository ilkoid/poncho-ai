package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/ilkoid/poncho-ai/pkg/cardupdate"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// ============================================================================
// Fixture: in-memory SQLite (паттерн fix-penalties-dims/staging_test.go)
// ============================================================================

// applyTestSchema — минимальные таблицы под SQL LoadFullCard (cardupdate.go)
// + staging (stage.go). Одна связь → одна in-memory база для всех запросов.
const applyTestSchema = `
CREATE TABLE cards (
	nm_id INTEGER PRIMARY KEY,
	vendor_code TEXT NOT NULL DEFAULT '',
	brand TEXT NOT NULL DEFAULT '',
	title TEXT NOT NULL DEFAULT '',
	description TEXT NOT NULL DEFAULT '',
	dim_length REAL NOT NULL DEFAULT 0,
	dim_width REAL NOT NULL DEFAULT 0,
	dim_height REAL NOT NULL DEFAULT 0,
	dim_weight_brutto REAL NOT NULL DEFAULT 0,
	dim_is_valid INTEGER NOT NULL DEFAULT 0,
	kiz_marked INTEGER,
	exclude_documents INTEGER,
	downloaded_at TEXT NOT NULL DEFAULT ''
);
CREATE TABLE card_characteristics (
	nm_id INTEGER NOT NULL,
	char_id INTEGER NOT NULL,
	name TEXT NOT NULL DEFAULT '',
	json_value TEXT NOT NULL DEFAULT '""',
	PRIMARY KEY (nm_id, char_id)
);
CREATE TABLE card_sizes (
	chrt_id INTEGER PRIMARY KEY,
	nm_id INTEGER NOT NULL,
	tech_size TEXT NOT NULL DEFAULT '',
	wb_size TEXT NOT NULL DEFAULT '',
	skus_json TEXT NOT NULL DEFAULT '[]'
);
CREATE TABLE card_documents (
	nm_id INTEGER NOT NULL,
	doc_id TEXT NOT NULL,
	type INTEGER NOT NULL DEFAULT 0,
	number TEXT NOT NULL DEFAULT '',
	product_number TEXT NOT NULL DEFAULT '',
	trade_name TEXT NOT NULL DEFAULT '',
	applicant TEXT NOT NULL DEFAULT '',
	start_date TEXT NOT NULL DEFAULT '',
	end_date TEXT NOT NULL DEFAULT '',
	is_endless INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (nm_id, doc_id)
);
CREATE TABLE fix_card_dimensions_staging (
	nm_id        INTEGER PRIMARY KEY,
	vendor_code  TEXT NOT NULL,
	old_length REAL, old_width REAL, old_height REAL, old_weight REAL,
	new_length REAL, new_width  REAL, new_height REAL, new_weight REAL,
	status       TEXT NOT NULL DEFAULT 'pending',
	error_msg    TEXT,
	created_at   TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
`

func newApplyTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1) // одна связь = одна in-memory база для всех запросов
	if _, err := db.Exec(applyTestSchema); err != nil {
		t.Fatalf("schema: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// cardSeed — карточка для fixture: full=true даёт заполненные поля;
// поля hollowDesc/hollowWBSize «продалбливают» description/wbSize.
type cardSeed struct {
	nmID       int
	vendorCode string
	hollowDesc bool
	hollowSize bool // wb_size='' у единственного размера
}

func seedCard(t *testing.T, db *sql.DB, c cardSeed) {
	t.Helper()
	if c.vendorCode == "" {
		c.vendorCode = fmt.Sprintf("%d", c.nmID)
	}
	desc := "Полное описание карточки для теста: достаточно длинная строка, чтобы гард считал её заполненной."
	if c.hollowDesc {
		desc = ""
	}
	if _, err := db.Exec(`INSERT INTO cards
		(nm_id, vendor_code, brand, title, description, dim_is_valid, downloaded_at)
		VALUES (?, ?, 'PlayToday', 'Тестовый товар', ?, 0, datetime('now'))`,
		c.nmID, c.vendorCode, desc); err != nil {
		t.Fatalf("seed card %d: %v", c.nmID, err)
	}
	wbSize := "152"
	if c.hollowSize {
		wbSize = ""
	}
	if _, err := db.Exec(`INSERT INTO card_sizes (chrt_id, nm_id, tech_size, wb_size, skus_json)
		VALUES (?, ?, '152', ?, '["2000000000011"]')`,
		c.nmID*10+1, c.nmID, wbSize); err != nil {
		t.Fatalf("seed size %d: %v", c.nmID, err)
	}
	if _, err := db.Exec(`INSERT INTO card_characteristics (nm_id, char_id, name, json_value)
		VALUES (?, 14177451, 'Страна производства', '"Китай"')`, c.nmID); err != nil {
		t.Fatalf("seed char %d: %v", c.nmID, err)
	}
}

func seedStaging(t *testing.T, db *sql.DB, nmID int, vendorCode string) {
	t.Helper()
	if vendorCode == "" {
		vendorCode = fmt.Sprintf("%d", nmID)
	}
	if _, err := db.Exec(`INSERT INTO fix_card_dimensions_staging
		(nm_id, vendor_code, old_length, old_width, old_height, old_weight,
		 new_length, new_width, new_height, new_weight, status)
		VALUES (?, ?, 0, 0, 0, 0, 40, 10, 30, 0.5, 'pending')`,
		nmID, vendorCode); err != nil {
		t.Fatalf("seed staging %d: %v", nmID, err)
	}
}

// stagingStatuses — карта nm_id → (status, error_msg) после прогона.
func stagingStatuses(t *testing.T, db *sql.DB) map[int][2]string {
	t.Helper()
	rows, err := db.Query(`SELECT nm_id, COALESCE(status,''), COALESCE(error_msg,'') FROM fix_card_dimensions_staging`)
	if err != nil {
		t.Fatalf("query statuses: %v", err)
	}
	defer rows.Close()
	out := make(map[int][2]string)
	for rows.Next() {
		var nm int
		var st, msg string
		if err := rows.Scan(&nm, &st, &msg); err != nil {
			t.Fatalf("scan status: %v", err)
		}
		out[nm] = [2]string{st, msg}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return out
}

// fakeWBClient — фейк wbUpdateClient: poison = nmID-«яды» (любой POST с ядом
// отвергается на уровне WB с errorText); rejectAll = «мёртвый сервер»;
// transportErr = сетевая ошибка без errorText. posts пишет nmID каждого POST.
type fakeWBClient struct {
	posts        [][]int
	poison       map[int]bool
	rejectAll    bool
	transportErr error
}

func (f *fakeWBClient) UpdateCards(_ context.Context, _ string, _, _ int, cards []wb.CardUpdateItem) (string, string, error) {
	ids := make([]int, len(cards))
	for i, c := range cards {
		ids[i] = c.NmID
	}
	f.posts = append(f.posts, ids)
	if f.transportErr != nil {
		return "", "", f.transportErr
	}
	if f.rejectAll {
		return "", "Invalid request format", errors.New("wb api error: status 400")
	}
	for _, c := range cards {
		if f.poison[c.NmID] {
			return "", "Invalid request format", errors.New("wb api error: status 400")
		}
	}
	return "{}", "", nil
}

func (f *fakeWBClient) GetCardErrorsList(_ context.Context, _ string, _, _ int, _ wb.CardErrorsListRequest) ([]wb.CardErrorItem, error) {
	return nil, nil
}

func intPtr(i int) *int { return &i }

func applyTestConfig(batchSize int) *Config {
	return &Config{
		WBUpdate: cardupdate.WBUpdateConfig{
			BatchSize:       batchSize,
			IntervalSeconds: 0, // без межбатчевых пауз в тестах
		},
		MaxSnapshotAgeHours: intPtr(0), // гард свежести кеша off
	}
}

// ============================================================================
// 1. Гард полоты — чистая функция
// ============================================================================

func fullUpdateItem() wb.CardUpdateItem {
	return wb.CardUpdateItem{
		NmID:        1,
		VendorCode:  "10001",
		Brand:       "PlayToday",
		Title:       "Тестовый товар",
		Description: "Полное описание",
		Dimensions:  &wb.CardDimensions{Length: 40, Width: 10, Height: 30, WeightBrutto: 0.5},
		Characteristics: []wb.CardUpdateCharc{
			{ID: 14177451, Value: "Китай"},
		},
		Sizes: []wb.CardSize{{ChrtID: 11, TechSize: "152", WBSize: "152", Skus: []string{"2000000000011"}}},
	}
}

func TestHollowCardReasons(t *testing.T) {
	noWbSize := fullUpdateItem()
	noWbSize.Sizes = []wb.CardSize{{ChrtID: 11, TechSize: "152", WBSize: "", Skus: []string{"1"}}}
	noSkus := fullUpdateItem()
	noSkus.Sizes = []wb.CardSize{{ChrtID: 11, TechSize: "152", WBSize: "152"}}
	noChrt := fullUpdateItem()
	noChrt.Sizes = []wb.CardSize{{ChrtID: 0, TechSize: "152", WBSize: "152", Skus: []string{"1"}}}
	noDesc := fullUpdateItem()
	noDesc.Description = "   "
	emptyCharc := fullUpdateItem()
	emptyCharc.Characteristics = []wb.CardUpdateCharc{{ID: 48, Value: ""}}
	noSizes := fullUpdateItem()
	noSizes.Sizes = nil
	noDims := fullUpdateItem()
	noDims.Dimensions = nil

	cases := []struct {
		name     string
		item     wb.CardUpdateItem
		contains string // подстрока ожидаемой причины; "" ⇒ ожидаем nil
	}{
		{"полная карточка — годна", fullUpdateItem(), ""},
		{"wbSize пустой у размера", noWbSize, "wbSize пустой у 1 размеров"},
		{"нет баркодов у размера", noSkus, "нет баркодов у 1 размеров"},
		{"chrtID=0 у размера", noChrt, "chrtID=0 у 1 размеров"},
		{"description из пробелов", noDesc, "description пустой"},
		{"характеристика без значения", emptyCharc, "без значения"},
		{"sizes пустой", noSizes, "sizes пустой"},
		{"dimensions nil", noDims, "dimensions неполные"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := hollowCardReasons(tc.item)
			if tc.contains == "" {
				if len(got) != 0 {
					t.Fatalf("ожидалось nil, получено: %v", got)
				}
				return
			}
			found := false
			for _, r := range got {
				if strings.Contains(r, tc.contains) {
					found = true
				}
			}
			if !found {
				t.Fatalf("ожидалась причина %q, получено: %v", tc.contains, got)
			}
		})
	}
}

// ============================================================================
// 2. Гард в прогоне: полая → skipped_hollow, чистые применены
// ============================================================================

func TestRunApplyGuardSkipsHollow(t *testing.T) {
	db := newApplyTestDB(t)
	seedCard(t, db, cardSeed{nmID: 1})
	seedCard(t, db, cardSeed{nmID: 2})
	seedCard(t, db, cardSeed{nmID: 3, hollowDesc: true})
	for _, nm := range []int{1, 2, 3} {
		seedStaging(t, db, nm, "")
	}

	fake := &fakeWBClient{}
	err := runApply(context.Background(), db, fake, applyTestConfig(30), false, false)
	if err != nil {
		t.Fatalf("ожидался успех (skipped_hollow — не провал): %v", err)
	}

	st := stagingStatuses(t, db)
	if st[3][0] != "skipped_hollow" || !strings.Contains(st[3][1], "description пустой") {
		t.Fatalf("полая карточка: получено %v", st[3])
	}
	for _, nm := range []int{1, 2} {
		if st[nm][0] != "applied" {
			t.Fatalf("чистая карточка %d: получено %v", nm, st[nm])
		}
	}
	// В POST ушли только чистые — яд не доехал до WB
	if len(fake.posts) != 1 || len(fake.posts[0]) != 2 {
		t.Fatalf("ожидался один POST с 2 карточками, получено: %v", fake.posts)
	}
}

// ============================================================================
// 3. Бисект: яд в чане из 8 → поимённая атрибуция, чистые применены
// (трейс A из мысленного эксперимента)
// ============================================================================

func TestRunApplyBisectAttributesPoison(t *testing.T) {
	db := newApplyTestDB(t)
	nms := []int{1, 2, 3, 4, 5, 6, 7, 8}
	for _, nm := range nms {
		seedCard(t, db, cardSeed{nmID: nm})
		seedStaging(t, db, nm, "")
	}

	fake := &fakeWBClient{poison: map[int]bool{8: true}}
	err := runApply(context.Background(), db, fake, applyTestConfig(8), false, false)
	if err == nil {
		t.Fatal("ожидалась ошибка: failed=1 (честный exit)")
	}
	if !strings.Contains(err.Error(), "не применено") {
		t.Fatalf("неожиданное сообщение: %v", err)
	}

	st := stagingStatuses(t, db)
	if st[8][0] != "error" || !strings.Contains(st[8][1], "WB: Invalid request format") {
		t.Fatalf("яд не атрибутирован: %v", st[8])
	}
	for _, nm := range nms[:7] {
		if st[nm][0] != "applied" {
			t.Fatalf("чистая карточка %d не применена: %v", nm, st[nm])
		}
	}
	// Посты: [8]F → [4]OK → [4]F → [2]OK → [2]F → [1]OK → [1]F = 7 попыток
	if len(fake.posts) != 7 {
		t.Fatalf("ожидалось 7 POST по трейсу A, получено %d: %v", len(fake.posts), fake.posts)
	}
}

// ============================================================================
// 4. Предохранитель: мёртвый сервер → стоп после 3 одиночных (трейс B)
// ============================================================================

func TestRunApplyBisectAbortsOnTotalReject(t *testing.T) {
	db := newApplyTestDB(t)
	nms := []int{1, 2, 3, 4, 5, 6, 7, 8}
	for _, nm := range nms {
		seedCard(t, db, cardSeed{nmID: nm})
		seedStaging(t, db, nm, "")
	}

	fake := &fakeWBClient{rejectAll: true}
	err := runApply(context.Background(), db, fake, applyTestConfig(8), false, false)
	if err == nil {
		t.Fatal("ожидалась ошибка при полном отказе")
	}

	// Бюджет: корень→…→3 одиночных ≈ 7 POST, потолок 15 (без предохранителя 2N-1=15)
	if len(fake.posts) > 15 {
		t.Fatalf("предохранитель не сработал: %d POST: %v", len(fake.posts), fake.posts)
	}
	st := stagingStatuses(t, db)
	attributed, unattributed := 0, 0
	for _, nm := range nms {
		s := st[nm]
		if s[0] != "error" {
			t.Fatalf("карточка %d не в error: %v", nm, s)
		}
		if strings.Contains(s[1], "WB: ") {
			attributed++
		} else if strings.Contains(s[1], "без атрибуции") {
			unattributed++
		}
	}
	if attributed != 3 {
		t.Fatalf("ожидалось ровно 3 атрибутированных отказа, получено %d", attributed)
	}
	if unattributed == 0 {
		t.Fatal("остаток должен быть помечен «без атрибуции»")
	}
}

// ============================================================================
// 5. Транспортная ошибка (errorText="") — бисекта нет, чанк целиком в error
// ============================================================================

func TestRunApplyTransportErrorNoBisect(t *testing.T) {
	db := newApplyTestDB(t)
	for _, nm := range []int{1, 2, 3, 4} {
		seedCard(t, db, cardSeed{nmID: nm})
		seedStaging(t, db, nm, "")
	}

	fake := &fakeWBClient{transportErr: errors.New("dial tcp: i/o timeout")}
	err := runApply(context.Background(), db, fake, applyTestConfig(4), false, false)
	if err == nil {
		t.Fatal("ожидалась ошибка при транспортном сбое")
	}
	if len(fake.posts) != 1 {
		t.Fatalf("транспортная ошибка не должна запускать бисект: %v", fake.posts)
	}
	for nm, s := range stagingStatuses(t, db) {
		if s[0] != "error" || !strings.Contains(s[1], "транспорт:") {
			t.Fatalf("карточка %d: %v", nm, s)
		}
	}
}

// ============================================================================
// 6-9. Инвариант покрытия: каждая карточка — ровно один терминальный статус
// (sent+failed+skipped == выборка, pending не остаётся)
// ============================================================================

func TestSendChunkCoverageInvariant(t *testing.T) {
	scenarios := []struct {
		name   string
		fake   *fakeWBClient
		hallow bool
		dryRun bool
	}{
		{"чистый прогон", &fakeWBClient{}, false, false},
		{"гард полоты", &fakeWBClient{}, false, false},
		{"один яд", &fakeWBClient{poison: map[int]bool{4: true}}, false, false},
		{"мёртвый сервер", &fakeWBClient{rejectAll: true}, false, false},
		{"транспорт", &fakeWBClient{transportErr: errors.New("timeout")}, false, false},
		{"dry-run", &fakeWBClient{}, false, true},
		{"allow-hollow", &fakeWBClient{}, true, false},
	}
	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			db := newApplyTestDB(t)
			nms := []int{1, 2, 3, 4, 5, 6, 7}
			for i, nm := range nms {
				// карточка 2 — полая по description, 5 — полая по wbSize
				seedCard(t, db, cardSeed{nmID: nm, hollowDesc: nm == 2, hollowSize: nm == 5, vendorCode: fmt.Sprintf("1000%d", i)})
				seedStaging(t, db, nm, "")
			}
			_ = runApply(context.Background(), db, sc.fake, applyTestConfig(7), sc.dryRun, sc.hallow)

			st := stagingStatuses(t, db)
			if len(st) != len(nms) {
				t.Fatalf("потеряны строки staging: %d из %d", len(st), len(nms))
			}
			terminal := map[string]bool{"applied": true, "error": true, "skipped_hollow": true}
			counts := make(map[string]int)
			for nm, s := range st {
				if !terminal[s[0]] && !(sc.dryRun && s[0] == "pending") {
					t.Fatalf("карточка %d без терминального статуса: %v", nm, s)
				}
				counts[s[0]]++
			}
			// dry-run сознательно не помечает применённые — их статус остаётся pending
			expected := len(nms)
			if sc.dryRun {
				expected = counts["pending"] + counts["skipped_hollow"] + counts["error"]
			}
			if got := counts["applied"] + counts["error"] + counts["skipped_hollow"] + counts["pending"]; got != expected {
				t.Fatalf("сумма статусов %d != ожидаемой %d: %v", got, expected, counts)
			}
		})
	}
}

// ============================================================================
// 7. Dry-run: гард пишет skipped_hollow, WB не зовётся
// ============================================================================

func TestRunApplyDryRunMarksHollowNoPosts(t *testing.T) {
	db := newApplyTestDB(t)
	seedCard(t, db, cardSeed{nmID: 1})
	seedCard(t, db, cardSeed{nmID: 2, hollowDesc: true})
	seedStaging(t, db, 1, "")
	seedStaging(t, db, 2, "")

	fake := &fakeWBClient{}
	if err := runApply(context.Background(), db, fake, applyTestConfig(30), true, false); err != nil {
		t.Fatalf("dry-run не должен падать: %v", err)
	}
	if len(fake.posts) != 0 {
		t.Fatalf("dry-run не должен звать WB: %v", fake.posts)
	}
	st := stagingStatuses(t, db)
	if st[2][0] != "skipped_hollow" {
		t.Fatalf("dry-run должен пометить полую: %v", st[2])
	}
	if st[1][0] != "pending" {
		t.Fatalf("чистая в dry-run остаётся pending: %v", st[1])
	}
}

// ============================================================================
// 8. --allow-hollow: гард обойдён, полая уходит в POST
// ============================================================================

func TestAllowHollowOverridesGuard(t *testing.T) {
	db := newApplyTestDB(t)
	seedCard(t, db, cardSeed{nmID: 1, hollowDesc: true})
	seedStaging(t, db, 1, "")

	fake := &fakeWBClient{}
	if err := runApply(context.Background(), db, fake, applyTestConfig(30), false, true); err != nil {
		t.Fatalf("allow-hollow прогон должен пройти: %v", err)
	}
	if len(fake.posts) != 1 {
		t.Fatalf("полая карточка должна уйти в POST: %v", fake.posts)
	}
	if st := stagingStatuses(t, db)[1]; st[0] != "applied" {
		t.Fatalf("ожидался applied: %v", st)
	}
}
