package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/ilkoid/poncho-ai/pkg/cardupdate"
)

// TestBuildMaterialPayload_FullCardInvariant проверяет главный инвариант
// безопасного rewrite: buildMaterialPayload возвращает ПОЛНЫЙ CardUpdateItem
// (vendorCode/brand/title/description/dimensions/sizes), а не частичный
// {NmID, Characteristics}, который обнулил бы карточку (WB делает полную замену).
// char_id берётся из staging-строки (т.к. «Материал верха» разный для разных категорий).
func TestBuildMaterialPayload_FullCardInvariant(t *testing.T) {
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

	const nmID = 778899
	const matCharID = 5029 // «Материал верха» (id varies by category — taken from staging row)
	if _, err := db.Exec(
		`INSERT INTO cards (nm_id, vendor_code, brand, title, description, dim_length, dim_width, dim_height, dim_weight_brutto, dim_is_valid)
		 VALUES (?,?,?,?,?,?,?,?,?,?)`,
		nmID, "300012", "BrandY", "Кроссовки", "Описание", 11.0, 21.0, 31.0, 600.0, 1,
	); err != nil {
		t.Fatalf("insert card: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO card_characteristics (nm_id, char_id, name, json_value) VALUES (?,?,?,?)`,
		nmID, 222, "Цвет", `["белый"]`,
	); err != nil {
		t.Fatalf("insert char: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO card_sizes (nm_id, chrt_id, tech_size, wb_size, skus_json) VALUES (?,?,?,?,?)`,
		nmID, 100, "38", "38", `["sku-x"]`,
	); err != nil {
		t.Fatalf("insert size: %v", err)
	}

	updater := cardupdate.NewCardUpdater(db)
	item, err := buildMaterialPayload(ctx, updater, applyRow{
		nmID:        nmID,
		charID:      matCharID,
		mappedValue: "Искусственная кожа",
	})
	if err != nil {
		t.Fatalf("buildMaterialPayload: %v", err)
	}

	if item.NmID != nmID || item.VendorCode != "300012" {
		t.Errorf("NmID=%d VendorCode=%q, want %d / \"300012\"", item.NmID, item.VendorCode, nmID)
	}
	if item.Brand == "" || item.Title == "" || item.Description == "" {
		t.Errorf("Brand/Title/Description empty: brand=%q title=%q desc=%q", item.Brand, item.Title, item.Description)
	}
	if item.Dimensions == nil || item.Dimensions.Length != 11 {
		t.Errorf("Dimensions = %+v, want L=11", item.Dimensions)
	}
	if len(item.Sizes) != 1 || item.Sizes[0].ChrtID != 100 {
		t.Errorf("Sizes = %+v, want one size chrtID=100", item.Sizes)
	}

	var haveMat, haveColor bool
	for _, c := range item.Characteristics {
		if c.ID == matCharID {
			haveMat = true
			if c.Value != "Искусственная кожа" {
				t.Errorf("material value = %v, want \"Искусственная кожа\"", c.Value)
			}
		}
		if c.ID == 222 {
			haveColor = true
		}
	}
	if !haveMat {
		t.Error("material characteristic missing — append branch failed")
	}
	if !haveColor {
		t.Error("existing Цвет characteristic dropped — full-card preservation broken")
	}

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

	// kiz_marked IS NULL → KizMarked nil → поле опущено в JSON (3-value logic).
	if item.KizMarked != nil {
		t.Errorf("KizMarked = %v, want nil (kiz_marked is NULL)", *item.KizMarked)
	}
	if _, ok := probe["kizMarked"]; ok {
		t.Error("wire JSON contains \"kizMarked\" — must be omitted when kiz_marked is NULL")
	}
}
