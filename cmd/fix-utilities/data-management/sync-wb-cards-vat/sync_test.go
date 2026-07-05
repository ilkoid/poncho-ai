package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/ilkoid/poncho-ai/pkg/cardupdate"
)

// TestBuildNDSPayload_FullCardInvariant проверяет главный инвариант безопасного
// rewrite: buildNDSPayload возвращает ПОЛНЫЙ CardUpdateItem (vendorCode/brand/
// title/description/dimensions/sizes), а не частичный {NmID, Characteristics},
// который обнулил бы карточку (WB делает полную замену). Также проверяется
// append-ветка: НДС-характеристики нет в исходной карточке → она добавляется.
func TestBuildNDSPayload_FullCardInvariant(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open mem db: %v", err)
	}
	defer db.Close()

	for _, q := range []string{
		`CREATE TABLE cards (nm_id INTEGER PRIMARY KEY, vendor_code TEXT, brand TEXT, title TEXT, description TEXT,
			dim_length REAL, dim_width REAL, dim_height REAL, dim_weight_brutto REAL, dim_is_valid INTEGER,
			kiz_marked INTEGER)`,
		`CREATE TABLE card_characteristics (nm_id INTEGER, char_id INTEGER, name TEXT, json_value TEXT)`,
		`CREATE TABLE card_sizes (nm_id INTEGER, chrt_id INTEGER, tech_size TEXT, wb_size TEXT, skus_json TEXT)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("exec %q: %v", q, err)
		}
	}

	const nmID = 12345678
	if _, err := db.Exec(
		`INSERT INTO cards (nm_id, vendor_code, brand, title, description, dim_length, dim_width, dim_height, dim_weight_brutto, dim_is_valid)
		 VALUES (?,?,?,?,?,?,?,?,?,?)`,
		nmID, "126210", "BrandX", "Title", "Desc", 10.0, 20.0, 30.0, 500.0, 1,
	); err != nil {
		t.Fatalf("insert card: %v", err)
	}
	// Существующая характеристика (НЕ НДС) — должна сохраниться как есть.
	if _, err := db.Exec(
		`INSERT INTO card_characteristics (nm_id, char_id, name, json_value) VALUES (?,?,?,?)`,
		nmID, 222, "Цвет", `["синий"]`,
	); err != nil {
		t.Fatalf("insert char: %v", err)
	}
	// Один размер — должен попасть в payload.
	if _, err := db.Exec(
		`INSERT INTO card_sizes (nm_id, chrt_id, tech_size, wb_size, skus_json) VALUES (?,?,?,?,?)`,
		nmID, 99, "42", "42", `["sku1"]`,
	); err != nil {
		t.Fatalf("insert size: %v", err)
	}

	updater := cardupdate.NewCardUpdater(db)
	item, err := buildNDSPayload(ctx, updater, NDSRow{NmID: nmID, OneCNDS: 10})
	if err != nil {
		t.Fatalf("buildNDSPayload: %v", err)
	}

	// 1. Все «шапочные» поля на месте (раньше были пусты → карточка обнулилась бы).
	if item.NmID != nmID {
		t.Errorf("NmID = %d, want %d", item.NmID, nmID)
	}
	if item.VendorCode != "126210" {
		t.Errorf("VendorCode = %q, want \"126210\" (partial payload had \"\")", item.VendorCode)
	}
	if item.Brand == "" || item.Title == "" || item.Description == "" {
		t.Errorf("Brand/Title/Description empty: brand=%q title=%q desc=%q", item.Brand, item.Title, item.Description)
	}
	if item.Dimensions == nil {
		t.Fatal("Dimensions nil — partial payload had no dimensions")
	}
	if item.Dimensions.Length != 10 || item.Dimensions.Width != 20 || item.Dimensions.Height != 30 {
		t.Errorf("Dimensions = %+v, want L=10 W=20 H=30", item.Dimensions)
	}
	if len(item.Sizes) != 1 || item.Sizes[0].ChrtID != 99 {
		t.Errorf("Sizes = %+v, want one size with chrtID=99", item.Sizes)
	}

	// 2. Существующая характеристика сохранена, НДС добавлен (append-ветка).
	var haveVAT, haveColor bool
	for _, c := range item.Characteristics {
		if c.ID == vatCharID {
			haveVAT = true
			if c.Value != "10" {
				t.Errorf("VAT value = %v, want \"10\"", c.Value)
			}
		}
		if c.ID == 222 {
			haveColor = true
		}
	}
	if !haveVAT {
		t.Error("НДС characteristic missing — append branch failed")
	}
	if !haveColor {
		t.Error("existing Цвет characteristic dropped — full-card preservation broken")
	}

	// 3. JSON-сериализация: поле sizes и vendorCode присутствуют в wire-формате.
	raw, _ := json.Marshal(item)
	var probe map[string]any
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("unmarshal probe: %v", err)
	}
	for _, k := range []string{"nmID", "vendorCode", "brand", "title", "description", "dimensions", "characteristics", "sizes"} {
		if _, ok := probe[k]; !ok {
			t.Errorf("wire JSON missing key %q (partial-payload bug)", k)
		}
	}

	// 4. kizMarked: NULL в БД → nil pointer → поле ОПУЩЕНО в JSON (3-value logic).
	//    WB тогда ставит default false; для маркированных карточек это риск (need_kiz=1).
	if item.KizMarked != nil {
		t.Errorf("KizMarked = %v, want nil (kiz_marked is NULL)", *item.KizMarked)
	}
	if _, ok := probe["kizMarked"]; ok {
		t.Error("wire JSON contains \"kizMarked\" — must be omitted when kiz_marked is NULL")
	}
}

// TestBuildNDSPayload_KizMarkedCarry проверяет, что явное значение cards.kiz_marked
// переносится в payload: kiz_marked=1 → KizMarked=*true → "kizMarked":true в JSON.
func TestBuildNDSPayload_KizMarkedCarry(t *testing.T) {
	ctx := context.Background()
	db, _ := sql.Open("sqlite3", ":memory:")
	defer db.Close()
	for _, q := range []string{
		`CREATE TABLE cards (nm_id INTEGER PRIMARY KEY, vendor_code TEXT, brand TEXT, title TEXT, description TEXT,
			dim_length REAL, dim_width REAL, dim_height REAL, dim_weight_brutto REAL, dim_is_valid INTEGER,
			kiz_marked INTEGER)`,
		`CREATE TABLE card_characteristics (nm_id INTEGER, char_id INTEGER, name TEXT, json_value TEXT)`,
		`CREATE TABLE card_sizes (nm_id INTEGER, chrt_id INTEGER, tech_size TEXT, wb_size TEXT, skus_json TEXT)`,
	} {
		db.Exec(q)
	}
	const nmID = 42
	db.Exec(`INSERT INTO cards (nm_id, vendor_code, kiz_marked) VALUES (?,?,?)`, nmID, "X", 1)

	item, err := buildNDSPayload(ctx, cardupdate.NewCardUpdater(db), NDSRow{NmID: nmID, OneCNDS: 22})
	if err != nil {
		t.Fatalf("buildNDSPayload: %v", err)
	}
	if item.KizMarked == nil || !*item.KizMarked {
		t.Fatalf("KizMarked = %v, want *true (kiz_marked=1)", item.KizMarked)
	}
	raw, _ := json.Marshal(item)
	var probe map[string]any
	json.Unmarshal(raw, &probe)
	if v, ok := probe["kizMarked"]; !ok || v != true {
		t.Errorf("wire JSON kizMarked = %v (%T), want true", v, v)
	}
}

// TestBuildNDSPayload_ReplaceExistingVAT проверяет replace-ветку: если карточка
// уже имеет НДС (но не тот) — значение заменяется in-place, дублей нет.
func TestBuildNDSPayload_ReplaceExistingVAT(t *testing.T) {
	ctx := context.Background()
	db, _ := sql.Open("sqlite3", ":memory:")
	defer db.Close()
	for _, q := range []string{
		`CREATE TABLE cards (nm_id INTEGER PRIMARY KEY, vendor_code TEXT, brand TEXT, title TEXT, description TEXT,
			dim_length REAL, dim_width REAL, dim_height REAL, dim_weight_brutto REAL, dim_is_valid INTEGER,
			kiz_marked INTEGER)`,
		`CREATE TABLE card_characteristics (nm_id INTEGER, char_id INTEGER, name TEXT, json_value TEXT)`,
		`CREATE TABLE card_sizes (nm_id INTEGER, chrt_id INTEGER, tech_size TEXT, wb_size TEXT, skus_json TEXT)`,
	} {
		db.Exec(q)
	}
	const nmID = 1
	db.Exec(`INSERT INTO cards (nm_id,vendor_code,dim_is_valid) VALUES (?,?,?)`, nmID, "A", 0)
	db.Exec(`INSERT INTO card_characteristics (nm_id,char_id,name,json_value) VALUES (?,?,?,?)`,
		nmID, vatCharID, "НДС", `["22"]`)

	item, err := buildNDSPayload(ctx, cardupdate.NewCardUpdater(db), NDSRow{NmID: nmID, OneCNDS: 10})
	if err != nil {
		t.Fatalf("buildNDSPayload: %v", err)
	}
	count := 0
	for _, c := range item.Characteristics {
		if c.ID == vatCharID {
			count++
			if c.Value != "10" {
				t.Errorf("VAT = %v, want \"10\"", c.Value)
			}
		}
	}
	if count != 1 {
		t.Errorf("VAT characteristics count = %d, want 1 (no duplicates)", count)
	}
}
