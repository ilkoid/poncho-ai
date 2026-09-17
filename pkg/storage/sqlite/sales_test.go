package sqlite

import (
	"context"
	"database/sql"
	"testing"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// TestSave_NewB2BFieldsRoundTrip проверяет, что 5 новых B2B-полей (swagger 13-finances.yaml,
// июль 2026) корректно пишутся в sales и читаются обратно. Главный guard против рассинхрона
// column count / placeholder count / arg count: если INSERT в sales.go имеет ≠47 '?' или
// ≠47 args — Save упадёт с runtime SQLite-ошибкой «N values for M columns».
func TestSave_NewB2BFieldsRoundTrip(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open mem db: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec(SchemaSQL); err != nil {
		t.Fatalf("apply SchemaSQL: %v", err)
	}

	repo := &SQLiteSalesRepository{db: db}
	row := wb.RealizationReportRow{
		RrdID:                            777,
		NmID:                             12345,
		Quantity:                         1,
		B2BCustomerTin:                   "7707083893",
		OrderUID:                         "b2b-order-uid-42",
		IsLegalEntity:                    true,
		SalePriceAffiliatedDiscountPrc:   3.5,
		SalePriceWholesaleDiscountPrc:    7.25,
	}

	if err := repo.Save(ctx, []wb.RealizationReportRow{row}); err != nil {
		t.Fatalf("Save: %v (column/placeholder/arg count mismatch in INSERT?)", err)
	}

	var (
		tin          sql.NullString
		orderUID     sql.NullString
		isLegalEnt   sql.NullInt64
		affilPrc     sql.NullFloat64
		wholPrc      sql.NullFloat64
	)
	err = db.QueryRowContext(ctx,
		`SELECT b2b_customer_tin, order_uid, is_legal_entity,
		        sale_price_affiliated_discount_prc, sale_price_wholesale_discount_prc
		 FROM sales WHERE rrd_id = ?`, row.RrdID,
	).Scan(&tin, &orderUID, &isLegalEnt, &affilPrc, &wholPrc)
	if err != nil {
		t.Fatalf("select back: %v", err)
	}
	if !tin.Valid || tin.String != "7707083893" {
		t.Errorf("b2b_customer_tin = %v, want \"7707083893\"", tin)
	}
	if !orderUID.Valid || orderUID.String != "b2b-order-uid-42" {
		t.Errorf("order_uid = %v, want \"b2b-order-uid-42\"", orderUID)
	}
	if !isLegalEnt.Valid || isLegalEnt.Int64 != 1 {
		t.Errorf("is_legal_entity = %v, want 1 (true)", isLegalEnt)
	}
	if !affilPrc.Valid || affilPrc.Float64 != 3.5 {
		t.Errorf("sale_price_affiliated_discount_prc = %v, want 3.5", affilPrc)
	}
	if !wholPrc.Valid || wholPrc.Float64 != 7.25 {
		t.Errorf("sale_price_wholesale_discount_prc = %v, want 7.25", wholPrc)
	}
}

// TestSave_ZeroB2BFieldsNullable проверяет, что B2B-поля со значением zero/empty
// хранятся как NULL (sparse-оптимизация для процентов; empty string → NULL через
// отсутствие NOT NULL). Гарантирует что «поле отсутствует в ответе WB» ≠ «0».
func TestSave_ZeroB2BFieldsNullable(t *testing.T) {
	ctx := context.Background()
	db, _ := sql.Open("sqlite3", ":memory:")
	defer db.Close()
	db.Exec(SchemaSQL)
	repo := &SQLiteSalesRepository{db: db}

	row := wb.RealizationReportRow{RrdID: 1, NmID: 2, Quantity: 1} // все B2B-поля zero
	if err := repo.Save(ctx, []wb.RealizationReportRow{row}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	var affilPrc, wholPrc sql.NullFloat64
	var isLegalEnt sql.NullInt64
	_ = db.QueryRowContext(ctx,
		`SELECT is_legal_entity, sale_price_affiliated_discount_prc, sale_price_wholesale_discount_prc
		 FROM sales WHERE rrd_id = ?`, row.RrdID,
	).Scan(&isLegalEnt, &affilPrc, &wholPrc)

	// is_legal_entity DEFAULT 0 — но INSERT передаёт 0 явно → 0, не NULL. Это ок (false).
	// Проценты идут через nullFloat() → 0 превращается в NULL.
	if affilPrc.Valid {
		t.Errorf("sale_price_affiliated_discount_prc = %v, want NULL (sparse)", affilPrc.Float64)
	}
	if wholPrc.Valid {
		t.Errorf("sale_price_wholesale_discount_prc = %v, want NULL (sparse)", wholPrc.Float64)
	}
}

// TestDeleteSalesByDateRange_MixedRRDTFormats — регрессия инцидента 04.09.2026
// (nightly download-wb-sales-v2 падал на duplicate key sales_rrd_id_key):
// finance-API отдаёт rr_dt датой без времени ('2026-08-28'), статистик-эра —
// RFC3339. Дата-онли строка первого дня окна лексикографически меньше нижней
// RFC3339-границы ('2026-08-28' < '2026-08-28T00:00:00+03:00') и переживала
// DELETE → rewrite-INSERT конфликтовал по rrd_id. DELETE обязан матчить оба
// формата по всем дням окна, включая первый.
func TestDeleteSalesByDateRange_MixedRRDTFormats(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open mem db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(SchemaSQL); err != nil {
		t.Fatalf("apply SchemaSQL: %v", err)
	}
	repo := &SQLiteSalesRepository{db: db}

	rows := []wb.RealizationReportRow{
		{RrdID: 1, NmID: 10, DocTypeName: "Продажа", Quantity: 1, RRDT: "2026-08-27"},                 // до окна, дата-онли
		{RrdID: 2, NmID: 10, DocTypeName: "Продажа", Quantity: 1, RRDT: "2026-08-28"},                 // первый день, дата-онли ← баг
		{RrdID: 3, NmID: 10, DocTypeName: "Продажа", Quantity: 1, RRDT: "2026-09-01"},                 // середина окна, дата-онли
		{RrdID: 4, NmID: 10, DocTypeName: "Продажа", Quantity: 1, RRDT: "2026-08-28T14:23:05+03:00"},  // первый день, RFC3339
		{RrdID: 5, NmID: 10, DocTypeName: "Продажа", Quantity: 1, RRDT: "2026-08-27T23:59:59+03:00"},  // до окна, RFC3339
	}
	if err := repo.Save(ctx, rows); err != nil {
		t.Fatalf("Save: %v", err)
	}

	deleted, err := repo.DeleteSalesByDateRange(ctx,
		"2026-08-28T00:00:00+03:00", "2026-09-03T23:59:59+03:00")
	if err != nil {
		t.Fatalf("DeleteSalesByDateRange: %v", err)
	}
	if deleted != 3 {
		t.Errorf("deleted = %d, want 3 (rrd_id 2,3,4 — все строки окна в обоих форматах, включая первый день)", deleted)
	}

	var remaining []int
	rs, err := db.QueryContext(ctx, "SELECT rrd_id FROM sales ORDER BY rrd_id")
	if err != nil {
		t.Fatalf("select remaining: %v", err)
	}
	defer rs.Close()
	for rs.Next() {
		var id int
		if err := rs.Scan(&id); err != nil {
			t.Fatalf("scan: %v", err)
		}
		remaining = append(remaining, id)
	}
	want := []int{1, 5} // обе до-оконные строки, в обоих форматах
	if len(remaining) != len(want) {
		t.Errorf("remaining rrd_id = %v, want %v", remaining, want)
	} else {
		for i := range want {
			if remaining[i] != want[i] {
				t.Errorf("remaining rrd_id = %v, want %v", remaining, want)
				break
			}
		}
	}
}
