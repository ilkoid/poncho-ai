package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ilkoid/poncho-ai/pkg/opsales"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Compile-time assertion: PgOpsalesRepo implements opsales.OpsalesWriter.
var _ opsales.OpsalesWriter = (*PgOpsalesRepo)(nil)

// PgOpsalesRepo implements opsales.OpsalesWriter for PostgreSQL.
// Focused repository (ISP) — only operational sales persistence methods.
type PgOpsalesRepo struct {
	pool *pgxpool.Pool
}

// NewPgOpsalesRepo creates a new PostgreSQL operational sales repository.
func NewPgOpsalesRepo(pool *pgxpool.Pool) *PgOpsalesRepo {
	return &PgOpsalesRepo{pool: pool}
}

// InitSchema creates operational_sales table if it doesn't exist.
func (r *PgOpsalesRepo) InitSchema(ctx context.Context) error {
	return initOpsalesSchema(ctx, r.pool)
}

// SaveSales saves a batch of operational sales using ON CONFLICT for upsert.
// Chunk size: 500 sales per transaction.
// Returns count of saved sales.
func (r *PgOpsalesRepo) SaveSales(ctx context.Context, sales []wb.SalesItem) (int, error) {
	if len(sales) == 0 {
		return 0, nil
	}

	total := 0
	for i := 0; i < len(sales); i += pgOpsalesChunkSize {
		end := i + pgOpsalesChunkSize
		if end > len(sales) {
			end = len(sales)
		}
		chunk := sales[i:end]

		n, err := r.saveSalesChunk(ctx, chunk)
		if err != nil {
			return 0, fmt.Errorf("save sales chunk at offset %d: %w", i, err)
		}
		total += n
	}
	return total, nil
}

// DeleteSalesOlderThan removes sales with sale_date before the given time.
func (r *PgOpsalesRepo) DeleteSalesOlderThan(ctx context.Context, before time.Time) (int64, error) {
	tag, err := r.pool.Exec(ctx, pgDeleteSalesOlderThanSQL, before.Format("2006-01-02"))
	if err != nil {
		return 0, fmt.Errorf("delete old sales: %w", err)
	}
	return tag.RowsAffected(), nil
}

const pgOpsalesChunkSize = 500

// saveSalesChunk saves up to 500 sales in a single transaction.
func (r *PgOpsalesRepo) saveSalesChunk(ctx context.Context, chunk []wb.SalesItem) (int, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	for _, s := range chunk {
		_, err := tx.Exec(ctx, pgInsertSaleSQL,
			s.SaleID,
			s.Date, s.LastChangeDate,
			s.WarehouseName, s.WarehouseType, s.CountryName, s.OblastOkrugName, s.RegionName,
			s.SupplierArticle, s.NmID, s.Barcode, s.Category, s.Subject, s.Brand, s.TechSize,
			s.IncomeID, s.IsSupply, s.IsRealization,
			s.TotalPrice, s.DiscountPercent, s.Spp, s.PaymentSaleAmount, s.ForPay, s.FinishedPrice, s.PriceWithDisc,
			s.Sticker, s.GNumber, s.Srid,
		)
		if err != nil {
			return 0, fmt.Errorf("upsert sale sale_id=%s: %w", s.SaleID, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}

	return len(chunk), nil
}

var (
	// PostgreSQL upsert — update all fields on conflict (sale_id).
	pgInsertSaleSQL = `
INSERT INTO operational_sales (
    sale_id,
    sale_date, last_change_date,
    warehouse_name, warehouse_type, country_name, oblast_okrug_name, region_name,
    supplier_article, nm_id, barcode, category, subject, brand, tech_size,
    income_id, is_supply, is_realization,
    total_price, discount_percent, spp, payment_sale_amount, for_pay, finished_price, price_with_disc,
    sticker, g_number, srid,
    downloaded_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'))
ON CONFLICT (sale_id) DO UPDATE SET
    sale_date = EXCLUDED.sale_date,
    last_change_date = EXCLUDED.last_change_date,
    warehouse_name = EXCLUDED.warehouse_name,
    warehouse_type = EXCLUDED.warehouse_type,
    country_name = EXCLUDED.country_name,
    oblast_okrug_name = EXCLUDED.oblast_okrug_name,
    region_name = EXCLUDED.region_name,
    supplier_article = EXCLUDED.supplier_article,
    nm_id = EXCLUDED.nm_id,
    barcode = EXCLUDED.barcode,
    category = EXCLUDED.category,
    subject = EXCLUDED.subject,
    brand = EXCLUDED.brand,
    tech_size = EXCLUDED.tech_size,
    income_id = EXCLUDED.income_id,
    is_supply = EXCLUDED.is_supply,
    is_realization = EXCLUDED.is_realization,
    total_price = EXCLUDED.total_price,
    discount_percent = EXCLUDED.discount_percent,
    spp = EXCLUDED.spp,
    payment_sale_amount = EXCLUDED.payment_sale_amount,
    for_pay = EXCLUDED.for_pay,
    finished_price = EXCLUDED.finished_price,
    price_with_disc = EXCLUDED.price_with_disc,
    sticker = EXCLUDED.sticker,
    g_number = EXCLUDED.g_number,
    srid = EXCLUDED.srid,
    downloaded_at = EXCLUDED.downloaded_at`

	pgDeleteSalesOlderThanSQL = `DELETE FROM operational_sales WHERE sale_date < $1`
)
