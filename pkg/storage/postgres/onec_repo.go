package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ilkoid/poncho-ai/pkg/onec"
)

// Compile-time assertion: PgOneCRepo implements onec.Writer.
var _ onec.Writer = (*PgOneCRepo)(nil)

const pgOneCChunkSize = 500

// PgOneCRepo implements onec.Writer for PostgreSQL.
type PgOneCRepo struct {
	pool *pgxpool.Pool
}

// NewPgOneCRepo creates a new PostgreSQL 1C data repository.
func NewPgOneCRepo(pool *pgxpool.Pool) *PgOneCRepo {
	return &PgOneCRepo{pool: pool}
}

// InitSchema creates 1C/PIM tables if they don't exist.
func (r *PgOneCRepo) InitSchema(ctx context.Context) error {
	return initOneCSchema(ctx, r.pool)
}

// ---------------------------------------------------------------------------
// Writer interface: Save methods
// ---------------------------------------------------------------------------

// SaveGoods saves a batch of 1C goods using ON CONFLICT upsert.
func (r *PgOneCRepo) SaveGoods(ctx context.Context, goods []onec.Good) (int, error) {
	if len(goods) == 0 {
		return 0, nil
	}

	total := 0
	for i := 0; i < len(goods); i += pgOneCChunkSize {
		end := min(i+pgOneCChunkSize, len(goods))
		n, err := r.saveGoodsChunk(ctx, goods[i:end])
		if err != nil {
			return total, fmt.Errorf("save goods chunk at offset %d: %w", i, err)
		}
		total += n
	}
	return total, nil
}

// SaveSKUs saves a batch of 1C SKUs using ON CONFLICT upsert.
func (r *PgOneCRepo) SaveSKUs(ctx context.Context, skus []onec.SKU) (int, error) {
	if len(skus) == 0 {
		return 0, nil
	}

	total := 0
	for i := 0; i < len(skus); i += pgOneCChunkSize {
		end := min(i+pgOneCChunkSize, len(skus))
		n, err := r.saveSKUsChunk(ctx, skus[i:end])
		if err != nil {
			return total, fmt.Errorf("save SKUs chunk at offset %d: %w", i, err)
		}
		total += n
	}
	return total, nil
}

// SaveDimensions saves a batch of dimension rows using ON CONFLICT upsert.
func (r *PgOneCRepo) SaveDimensions(ctx context.Context, dims []onec.DimensionRow) (int, error) {
	if len(dims) == 0 {
		return 0, nil
	}

	total := 0
	for i := 0; i < len(dims); i += pgOneCChunkSize {
		end := min(i+pgOneCChunkSize, len(dims))
		n, err := r.saveDimensionsChunk(ctx, dims[i:end])
		if err != nil {
			return total, fmt.Errorf("save dimensions chunk at offset %d: %w", i, err)
		}
		total += n
	}
	return total, nil
}

// SaveOneCPrices saves a batch of price rows using ON CONFLICT upsert.
func (r *PgOneCRepo) SaveOneCPrices(ctx context.Context, prices []onec.PriceRow, snapshotDate string) (int, error) {
	if len(prices) == 0 {
		return 0, nil
	}

	total := 0
	for i := 0; i < len(prices); i += pgOneCChunkSize {
		end := min(i+pgOneCChunkSize, len(prices))
		n, err := r.savePricesChunk(ctx, prices[i:end], snapshotDate)
		if err != nil {
			return total, fmt.Errorf("save prices chunk at offset %d: %w", i, err)
		}
		total += n
	}
	return total, nil
}

// SavePIMGoods saves a batch of PIM goods using ON CONFLICT upsert.
func (r *PgOneCRepo) SavePIMGoods(ctx context.Context, items []onec.PIMGoods) (int, error) {
	if len(items) == 0 {
		return 0, nil
	}

	total := 0
	for i := 0; i < len(items); i += pgOneCChunkSize {
		end := min(i+pgOneCChunkSize, len(items))
		n, err := r.savePIMGoodsChunk(ctx, items[i:end])
		if err != nil {
			return total, fmt.Errorf("save PIM goods chunk at offset %d: %w", i, err)
		}
		total += n
	}
	return total, nil
}

// CleanAll deletes all data from 1C/PIM tables (for --clean flag).
// Order respects foreign key constraints.
func (r *PgOneCRepo) CleanAll(ctx context.Context) error {
	for _, table := range []string{
		"onec_prices",
		"onec_goods_sku",
		"onec_dimensions",
		"onec_goods",
		"pim_goods",
	} {
		if _, err := r.pool.Exec(ctx, "DELETE FROM "+table); err != nil {
			return fmt.Errorf("clean table %s: %w", table, err)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Multi-row INSERT SQL fragments
// ---------------------------------------------------------------------------

//nolint:lll // 69-column INSERT is unavoidably long
const insertGoodPrefixSQL = `INSERT INTO onec_goods (
    guid, article, name, name_im, description,
    brand, type, category, category_level1, category_level2,
    sex, season, composition, composition_lining, color,
    collection, country_of_origin, weight, size_range,
    tnved_codes, business_line,
    is_sale, is_new, model_status, date,
    length, wideness, height, weight_sku_g,
    certificate, certificate_type, has_certificate,
    certificate_begin, certificate_end, certificate_number,
    approval_date, date_of_production, date_of_receipt, pps_date,
    collection_season, collection_year, look_season,
    opt_collection_season, opt_collection_year,
    production_season, production_year,
    category_level1_name, category_level2_name,
    age, figure_features, licensor, main_capture,
    markirovka, model_height, ratio_heat, recommendations,
    size_on_model, tag, quantity_bar_code,
    is_adult, is_article_blocked, is_exclude_from_site, is_exclusive,
    is_genuine_leather, is_model_cancelled, is_new_collection,
    is_not_require_ironing, is_pps, is_ya_price_list_opt,
    downloaded_at
) VALUES `

const insertGoodOnConflictSQL = `
ON CONFLICT (guid) DO UPDATE SET
    article = EXCLUDED.article,
    name = EXCLUDED.name,
    name_im = EXCLUDED.name_im,
    description = EXCLUDED.description,
    brand = EXCLUDED.brand,
    type = EXCLUDED.type,
    category = EXCLUDED.category,
    category_level1 = EXCLUDED.category_level1,
    category_level2 = EXCLUDED.category_level2,
    sex = EXCLUDED.sex,
    season = EXCLUDED.season,
    composition = EXCLUDED.composition,
    composition_lining = EXCLUDED.composition_lining,
    color = EXCLUDED.color,
    collection = EXCLUDED.collection,
    country_of_origin = EXCLUDED.country_of_origin,
    weight = EXCLUDED.weight,
    size_range = EXCLUDED.size_range,
    tnved_codes = EXCLUDED.tnved_codes,
    business_line = EXCLUDED.business_line,
    is_sale = EXCLUDED.is_sale,
    is_new = EXCLUDED.is_new,
    model_status = EXCLUDED.model_status,
    date = EXCLUDED.date,
    length = EXCLUDED.length,
    wideness = EXCLUDED.wideness,
    height = EXCLUDED.height,
    weight_sku_g = EXCLUDED.weight_sku_g,
    certificate = EXCLUDED.certificate,
    certificate_type = EXCLUDED.certificate_type,
    has_certificate = EXCLUDED.has_certificate,
    certificate_begin = EXCLUDED.certificate_begin,
    certificate_end = EXCLUDED.certificate_end,
    certificate_number = EXCLUDED.certificate_number,
    approval_date = EXCLUDED.approval_date,
    date_of_production = EXCLUDED.date_of_production,
    date_of_receipt = EXCLUDED.date_of_receipt,
    pps_date = EXCLUDED.pps_date,
    collection_season = EXCLUDED.collection_season,
    collection_year = EXCLUDED.collection_year,
    look_season = EXCLUDED.look_season,
    opt_collection_season = EXCLUDED.opt_collection_season,
    opt_collection_year = EXCLUDED.opt_collection_year,
    production_season = EXCLUDED.production_season,
    production_year = EXCLUDED.production_year,
    category_level1_name = EXCLUDED.category_level1_name,
    category_level2_name = EXCLUDED.category_level2_name,
    age = EXCLUDED.age,
    figure_features = EXCLUDED.figure_features,
    licensor = EXCLUDED.licensor,
    main_capture = EXCLUDED.main_capture,
    markirovka = EXCLUDED.markirovka,
    model_height = EXCLUDED.model_height,
    ratio_heat = EXCLUDED.ratio_heat,
    recommendations = EXCLUDED.recommendations,
    size_on_model = EXCLUDED.size_on_model,
    tag = EXCLUDED.tag,
    quantity_bar_code = EXCLUDED.quantity_bar_code,
    is_adult = EXCLUDED.is_adult,
    is_article_blocked = EXCLUDED.is_article_blocked,
    is_exclude_from_site = EXCLUDED.is_exclude_from_site,
    is_exclusive = EXCLUDED.is_exclusive,
    is_genuine_leather = EXCLUDED.is_genuine_leather,
    is_model_cancelled = EXCLUDED.is_model_cancelled,
    is_new_collection = EXCLUDED.is_new_collection,
    is_not_require_ironing = EXCLUDED.is_not_require_ironing,
    is_pps = EXCLUDED.is_pps,
    is_ya_price_list_opt = EXCLUDED.is_ya_price_list_opt,
    downloaded_at = EXCLUDED.downloaded_at`

// insertGoodCols = 69 data columns + 1 downloaded_at (pre-computed in Go).
const insertGoodCols = 70

// insertGoodFullChunkSQL is pre-built for the common case (full 500-row chunk).
var insertGoodFullChunkSQL = BuildMultiRowInsert(insertGoodPrefixSQL, insertGoodOnConflictSQL, pgOneCChunkSize, insertGoodCols)

// --- SKUs ---

const insertSKUPrefixSQL = `INSERT INTO onec_goods_sku (sku_guid, guid, barcode, size, nds, length, wideness, height, weight_sku_g) VALUES `

const insertSKUOnConflictSQL = `
ON CONFLICT (sku_guid, guid) DO UPDATE SET
    barcode = EXCLUDED.barcode,
    size = EXCLUDED.size,
    nds = EXCLUDED.nds,
    length = EXCLUDED.length,
    wideness = EXCLUDED.wideness,
    height = EXCLUDED.height,
    weight_sku_g = EXCLUDED.weight_sku_g`

const insertSKUCols = 9

var insertSKUFullChunkSQL = BuildMultiRowInsert(insertSKUPrefixSQL, insertSKUOnConflictSQL, pgOneCChunkSize, insertSKUCols)

// --- Dimensions ---

const insertDimensionPrefixSQL = `INSERT INTO onec_dimensions (good_guid, sku_guid, good_name, size_name, length_dm, width_dm, height_dm, weight_kg, volume_cm3, source, created_at) VALUES `

const insertDimensionOnConflictSQL = `
ON CONFLICT (good_guid, sku_guid) DO UPDATE SET
    good_name = EXCLUDED.good_name,
    size_name = EXCLUDED.size_name,
    length_dm = EXCLUDED.length_dm,
    width_dm = EXCLUDED.width_dm,
    height_dm = EXCLUDED.height_dm,
    weight_kg = EXCLUDED.weight_kg,
    volume_cm3 = EXCLUDED.volume_cm3,
    source = EXCLUDED.source,
    created_at = EXCLUDED.created_at`

// insertDimensionCols = 10 data columns + 1 created_at (pre-computed in Go).
const insertDimensionCols = 11

var insertDimensionFullChunkSQL = BuildMultiRowInsert(insertDimensionPrefixSQL, insertDimensionOnConflictSQL, pgOneCChunkSize, insertDimensionCols)

// --- Prices ---

const insertPricePrefixSQL = `INSERT INTO onec_prices (good_guid, snapshot_date, type_guid, type_name, price, spec_price) VALUES `

const insertPriceOnConflictSQL = `
ON CONFLICT (good_guid, snapshot_date, type_guid) DO UPDATE SET
    type_name = EXCLUDED.type_name,
    price = EXCLUDED.price,
    spec_price = EXCLUDED.spec_price`

const insertPriceCols = 6

var insertPriceFullChunkSQL = BuildMultiRowInsert(insertPricePrefixSQL, insertPriceOnConflictSQL, pgOneCChunkSize, insertPriceCols)

// --- PIM Goods ---

const insertPIMGoodsPrefixSQL = `INSERT INTO pim_goods (
    identifier, enabled, family, categories, product_type,
    sex, season, color, filter_color, wb_nm_id,
    year_collection, menu_product_type, menu_age, age_category,
    composition, naznacenie, minicollection,
    brand_country, country_manufacture, size_table,
    features_care, description, name, updated,
    wildberries_length, wildberries_width, wildberries_height,
    downloaded_at
) VALUES `

const insertPIMGoodsOnConflictSQL = `
ON CONFLICT (identifier) DO UPDATE SET
    enabled = EXCLUDED.enabled,
    family = EXCLUDED.family,
    categories = EXCLUDED.categories,
    product_type = EXCLUDED.product_type,
    sex = EXCLUDED.sex,
    season = EXCLUDED.season,
    color = EXCLUDED.color,
    filter_color = EXCLUDED.filter_color,
    wb_nm_id = EXCLUDED.wb_nm_id,
    year_collection = EXCLUDED.year_collection,
    menu_product_type = EXCLUDED.menu_product_type,
    menu_age = EXCLUDED.menu_age,
    age_category = EXCLUDED.age_category,
    composition = EXCLUDED.composition,
    naznacenie = EXCLUDED.naznacenie,
    minicollection = EXCLUDED.minicollection,
    brand_country = EXCLUDED.brand_country,
    country_manufacture = EXCLUDED.country_manufacture,
    size_table = EXCLUDED.size_table,
    features_care = EXCLUDED.features_care,
    description = EXCLUDED.description,
    name = EXCLUDED.name,
    updated = EXCLUDED.updated,
    wildberries_length = EXCLUDED.wildberries_length,
    wildberries_width = EXCLUDED.wildberries_width,
    wildberries_height = EXCLUDED.wildberries_height,
    downloaded_at = EXCLUDED.downloaded_at`

// insertPIMGoodsCols = 27 data columns + 1 downloaded_at (pre-computed in Go).
const insertPIMGoodsCols = 28

var insertPIMGoodsFullChunkSQL = BuildMultiRowInsert(insertPIMGoodsPrefixSQL, insertPIMGoodsOnConflictSQL, pgOneCChunkSize, insertPIMGoodsCols)

// ---------------------------------------------------------------------------
// Chunk-level save methods (multi-row INSERT per chunk)
// ---------------------------------------------------------------------------

func (r *PgOneCRepo) saveGoodsChunk(ctx context.Context, chunk []onec.Good) (int, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	now := time.Now().UTC().Format("2006-01-02 15:04:05")

	args := make([]any, 0, len(chunk)*insertGoodCols)
	for _, g := range chunk {
		args = append(args,
			g.GUID, g.Article, g.Name, g.NameIM, g.Description,        // $1-$5
			g.Brand, g.Type, g.Category, g.CategoryLevel1, g.CategoryLevel2, // $6-$10
			g.Sex, g.Season, g.Composition, g.CompositionLining, g.Color,     // $11-$15
			g.Collection, g.CountryOfOrigin, g.Weight, g.SizeRange,            // $16-$19
			g.TnvedCodes, g.BusinessLine,                                      // $20-$21
			g.IsSale, g.IsNew, g.ModelStatus, g.Date,                          // $22-$25
			g.Length, g.Wideness, g.Height, g.WeightSKUG,                      // $26-$29
			g.Certificate, g.CertificateType, g.HasCertificate,                 // $30-$32
			g.CertificateBegin, g.CertificateEnd, g.CertificateNumber,         // $33-$35
			g.ApprovalDate, g.DateOfProduction, g.DateOfReceipt, g.PPSDate,    // $36-$39
			g.CollectionSeason, g.CollectionYear, g.LookSeason,                // $40-$42
			g.OptCollectionSeason, g.OptCollectionYear,                        // $43-$44
			g.ProductionSeason, g.ProductionYear,                              // $45-$46
			g.CategoryLevel1Name, g.CategoryLevel2Name,                        // $47-$48
			g.Age, g.FigureFeatures, g.Licensor, g.MainCapture,               // $49-$52
			g.Markirovka, g.ModelHeight, g.RatioHeat, g.Recommendations,       // $53-$56
			g.SizeOnModel, g.Tag, g.QuantityBarCode,                           // $57-$59
			g.IsAdult, g.IsArticleBlocked, g.IsExcludeFromSite, g.IsExclusive, // $60-$63
			g.IsGenuineLeather, g.IsModelCancelled, g.IsNewCollection,         // $64-$66
			g.IsNotRequireIroning, g.IsPPS, g.IsYaPriceListOpt,                // $67-$69
			now, // $70 = downloaded_at
		)
	}

	query := insertGoodFullChunkSQL
	if len(chunk) < pgOneCChunkSize {
		query = BuildMultiRowInsert(insertGoodPrefixSQL, insertGoodOnConflictSQL, len(chunk), insertGoodCols)
	}

	tag, err := tx.Exec(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("save goods batch (size %d): %w", len(chunk), err)
	}
	return int(tag.RowsAffected()), tx.Commit(ctx)
}

func (r *PgOneCRepo) saveSKUsChunk(ctx context.Context, chunk []onec.SKU) (int, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	args := make([]any, 0, len(chunk)*insertSKUCols)
	for _, s := range chunk {
		args = append(args,
			s.SKUGUID, s.GUID, s.Barcode, s.Size, s.NDS,
			s.Length, s.Wideness, s.Height, s.WeightSKUG,
		)
	}

	query := insertSKUFullChunkSQL
	if len(chunk) < pgOneCChunkSize {
		query = BuildMultiRowInsert(insertSKUPrefixSQL, insertSKUOnConflictSQL, len(chunk), insertSKUCols)
	}

	tag, err := tx.Exec(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("save SKUs batch (size %d): %w", len(chunk), err)
	}
	return int(tag.RowsAffected()), tx.Commit(ctx)
}

func (r *PgOneCRepo) saveDimensionsChunk(ctx context.Context, chunk []onec.DimensionRow) (int, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	now := time.Now().UTC().Format("2006-01-02 15:04:05")

	args := make([]any, 0, len(chunk)*insertDimensionCols)
	for _, d := range chunk {
		args = append(args,
			d.GoodGUID, d.SKUGUID, d.GoodName, d.SizeName,
			d.LengthDM, d.WidthDM, d.HeightDM, d.WeightKG,
			d.VolumeCM3, d.Source,
			now, // created_at
		)
	}

	query := insertDimensionFullChunkSQL
	if len(chunk) < pgOneCChunkSize {
		query = BuildMultiRowInsert(insertDimensionPrefixSQL, insertDimensionOnConflictSQL, len(chunk), insertDimensionCols)
	}

	tag, err := tx.Exec(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("save dimensions batch (size %d): %w", len(chunk), err)
	}
	return int(tag.RowsAffected()), tx.Commit(ctx)
}

func (r *PgOneCRepo) savePricesChunk(ctx context.Context, chunk []onec.PriceRow, snapshotDate string) (int, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	args := make([]any, 0, len(chunk)*insertPriceCols)
	for _, p := range chunk {
		args = append(args,
			p.GoodGUID, snapshotDate, p.TypeGUID, p.TypeName, p.Price, p.SpecPrice,
		)
	}

	query := insertPriceFullChunkSQL
	if len(chunk) < pgOneCChunkSize {
		query = BuildMultiRowInsert(insertPricePrefixSQL, insertPriceOnConflictSQL, len(chunk), insertPriceCols)
	}

	tag, err := tx.Exec(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("save prices batch (size %d): %w", len(chunk), err)
	}
	return int(tag.RowsAffected()), tx.Commit(ctx)
}

func (r *PgOneCRepo) savePIMGoodsChunk(ctx context.Context, chunk []onec.PIMGoods) (int, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	now := time.Now().UTC().Format("2006-01-02 15:04:05")

	args := make([]any, 0, len(chunk)*insertPIMGoodsCols)
	for _, p := range chunk {
		args = append(args,
			p.Identifier, p.Enabled, p.Family, p.Categories, p.ProductType, // $1-$5
			p.Sex, p.Season, p.Color, p.FilterColor, p.WbNmID, // $6-$10
			p.YearCollection, p.MenuProductType, p.MenuAge, p.AgeCategory, // $11-$14
			p.Composition, p.Naznacenie, p.Minicollection, // $15-$17
			p.BrandCountry, p.CountryManufacture, p.SizeTable, // $18-$20
			p.FeaturesCare, p.Description, p.Name, p.Updated, // $21-$24
			p.WildberriesLength, p.WildberriesWidth, p.WildberriesHeight, // $25-$27
			now, // $28 = downloaded_at
		)
	}

	query := insertPIMGoodsFullChunkSQL
	if len(chunk) < pgOneCChunkSize {
		query = BuildMultiRowInsert(insertPIMGoodsPrefixSQL, insertPIMGoodsOnConflictSQL, len(chunk), insertPIMGoodsCols)
	}

	tag, err := tx.Exec(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("save PIM goods batch (size %d): %w", len(chunk), err)
	}
	return int(tag.RowsAffected()), tx.Commit(ctx)
}
