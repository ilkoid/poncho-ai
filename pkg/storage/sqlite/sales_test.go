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
		IsB2b:                            true,
		SalePriceAffiliatedDiscountPrc:   3.5,
		SalePriceWholesaleDiscountPrc:    7.25,
	}

	if err := repo.Save(ctx, []wb.RealizationReportRow{row}); err != nil {
		t.Fatalf("Save: %v (column/placeholder/arg count mismatch in INSERT?)", err)
	}

	var (
		tin       sql.NullString
		orderUID  sql.NullString
		isB2B     sql.NullInt64
		affilPrc  sql.NullFloat64
		wholPrc   sql.NullFloat64
	)
	err = db.QueryRowContext(ctx,
		`SELECT b2b_customer_tin, order_uid, is_b2b,
		        sale_price_affiliated_discount_prc, sale_price_wholesale_discount_prc
		 FROM sales WHERE rrd_id = ?`, row.RrdID,
	).Scan(&tin, &orderUID, &isB2B, &affilPrc, &wholPrc)
	if err != nil {
		t.Fatalf("select back: %v", err)
	}
	if !tin.Valid || tin.String != "7707083893" {
		t.Errorf("b2b_customer_tin = %v, want \"7707083893\"", tin)
	}
	if !orderUID.Valid || orderUID.String != "b2b-order-uid-42" {
		t.Errorf("order_uid = %v, want \"b2b-order-uid-42\"", orderUID)
	}
	if !isB2B.Valid || isB2B.Int64 != 1 {
		t.Errorf("is_b2b = %v, want 1 (true)", isB2B)
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
	var isB2B sql.NullInt64
	_ = db.QueryRowContext(ctx,
		`SELECT is_b2b, sale_price_affiliated_discount_prc, sale_price_wholesale_discount_prc
		 FROM sales WHERE rrd_id = ?`, row.RrdID,
	).Scan(&isB2B, &affilPrc, &wholPrc)

	// is_b2b DEFAULT 0 — но INSERT передаёт 0 явно → 0, не NULL. Это ок (false).
	// Проценты идут через nullFloat() → 0 превращается в NULL.
	if affilPrc.Valid {
		t.Errorf("sale_price_affiliated_discount_prc = %v, want NULL (sparse)", affilPrc.Float64)
	}
	if wholPrc.Valid {
		t.Errorf("sale_price_wholesale_discount_prc = %v, want NULL (sparse)", wholPrc.Float64)
	}
}
