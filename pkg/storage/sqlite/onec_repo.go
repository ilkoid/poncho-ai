package sqlite

import (
	"context"
	"fmt"
	"strings"
)

// SaveOneCGoods saves a batch of 1C goods using INSERT OR REPLACE.
// Returns number of rows inserted/replaced.
func (r *SQLiteSalesRepository) SaveOneCGoods(ctx context.Context, goods []OneCGood) (int, error) {
	if len(goods) == 0 {
		return 0, nil
	}

	r.db.Exec("PRAGMA synchronous = OFF")
	defer r.db.Exec("PRAGMA synchronous = NORMAL")

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	// SQLite default limit: 999 variables per statement. 69 cols × 14 = 966 < 999.
	const batchSize = 14
	totalSaved := 0

	for i := 0; i < len(goods); i += batchSize {
		end := min(i+batchSize, len(goods))
		batch := goods[i:end]

		var placeholders []string
		var args []any

		for _, g := range batch {
			placeholders = append(placeholders, "(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)")
			args = append(args,
				g.GUID, g.Article, g.Name, g.NameIM, g.Description,
				g.Brand, g.Type, g.Category, g.CategoryLevel1, g.CategoryLevel2,
				g.Sex, g.Season, g.Composition, g.CompositionLining, g.Color,
				g.Collection, g.CountryOfOrigin, g.Weight, g.SizeRange,
				g.TnvedCodes, g.BusinessLine,
				g.IsSale, g.IsNew, g.ModelStatus, g.Date,
				// Dimensions & Weight
				g.Length, g.Wideness, g.Height, g.WeightSKUG,
				// Certificate
				g.Certificate, g.CertificateType, g.HasCertificate, g.CertificateBegin, g.CertificateEnd, g.CertificateNumber,
				// Dates
				g.ApprovalDate, g.DateOfProduction, g.DateOfReceipt, g.PPSDate,
				// Seasons & Collections
				g.CollectionSeason, g.CollectionYear, g.LookSeason,
				g.OptCollectionSeason, g.OptCollectionYear,
				g.ProductionSeason, g.ProductionYear,
				// Categories
				g.CategoryLevel1Name, g.CategoryLevel2Name,
				// Product attributes
				g.Age, g.FigureFeatures, g.Licensor, g.MainCapture,
				g.Markirovka, g.ModelHeight, g.RatioHeat, g.Recommendations,
				g.SizeOnModel, g.Tag, g.QuantityBarCode,
				// Boolean flags
				g.IsAdult, g.IsArticleBlocked, g.IsExcludeFromSite, g.IsExclusive,
				g.IsGenuineLeather, g.IsModelCancelled, g.IsNewCollection,
				g.IsNotRequireIroning, g.IsPPS, g.IsYaPriceListOpt,
			)
		}

		query := fmt.Sprintf(`
			INSERT OR REPLACE INTO onec_goods (
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
				is_not_require_ironing, is_pps, is_ya_price_list_opt
			) VALUES %s
		`, strings.Join(placeholders, ", "))

		result, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			return totalSaved, fmt.Errorf("save onec_goods batch (offset %d): %w", i, err)
		}

		affected, _ := result.RowsAffected()
		totalSaved += int(affected)
	}

	return totalSaved, tx.Commit()
}

// SaveOneCSKUs saves a batch of 1C SKUs using INSERT OR REPLACE.
// Returns number of rows inserted/replaced.
func (r *SQLiteSalesRepository) SaveOneCSKUs(ctx context.Context, skus []OneCSKU) (int, error) {
	if len(skus) == 0 {
		return 0, nil
	}

	r.db.Exec("PRAGMA synchronous = OFF")
	defer r.db.Exec("PRAGMA synchronous = NORMAL")

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	// SQLite default limit: 999 variables per statement. 9 cols × 111 = 999.
	const batchSize = 111
	totalSaved := 0

	for i := 0; i < len(skus); i += batchSize {
		end := min(i+batchSize, len(skus))
		batch := skus[i:end]

		var placeholders []string
		var args []any

		for _, s := range batch {
			placeholders = append(placeholders, "(?, ?, ?, ?, ?, ?, ?, ?, ?)")
			args = append(args, s.SKUGUID, s.GUID, s.Barcode, s.Size, s.NDS,
				s.Length, s.Wideness, s.Height, s.WeightSKUG)
		}

		query := fmt.Sprintf(`
			INSERT OR REPLACE INTO onec_goods_sku (sku_guid, guid, barcode, size, nds, length, wideness, height, weight_sku_g)
			VALUES %s
		`, strings.Join(placeholders, ", "))

		result, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			return totalSaved, fmt.Errorf("save onec_goods_sku batch (offset %d): %w", i, err)
		}

		affected, _ := result.RowsAffected()
		totalSaved += int(affected)
	}

	return totalSaved, tx.Commit()
}

// SaveOneCPrices saves a batch of 1C price rows using INSERT OR REPLACE.
// snapshotDate is applied to all rows (YYYY-MM-DD, set by caller).
// Returns number of rows inserted/replaced.
func (r *SQLiteSalesRepository) SaveOneCPrices(ctx context.Context, prices []OneCPriceRow, snapshotDate string) (int, error) {
	if len(prices) == 0 {
		return 0, nil
	}

	r.db.Exec("PRAGMA synchronous = OFF")
	defer r.db.Exec("PRAGMA synchronous = NORMAL")

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	const batchSize = 500
	totalSaved := 0

	for i := 0; i < len(prices); i += batchSize {
		end := min(i+batchSize, len(prices))
		batch := prices[i:end]

		var placeholders []string
		var args []any

		for _, p := range batch {
			placeholders = append(placeholders, "(?, ?, ?, ?, ?, ?)")
			args = append(args, p.GoodGUID, snapshotDate, p.TypeGUID, p.TypeName, p.Price, p.SpecPrice)
		}

		query := fmt.Sprintf(`
			INSERT OR REPLACE INTO onec_prices (good_guid, snapshot_date, type_guid, type_name, price, spec_price)
			VALUES %s
		`, strings.Join(placeholders, ", "))

		result, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			return totalSaved, fmt.Errorf("save onec_prices batch (offset %d): %w", i, err)
		}

		affected, _ := result.RowsAffected()
		totalSaved += int(affected)
	}

	return totalSaved, tx.Commit()
}

// SavePIMGoods saves a batch of PIM goods using INSERT OR REPLACE.
// Returns number of rows inserted/replaced.
func (r *SQLiteSalesRepository) SavePIMGoods(ctx context.Context, items []PIMGoodsRow) (int, error) {
	if len(items) == 0 {
		return 0, nil
	}

	r.db.Exec("PRAGMA synchronous = OFF")
	defer r.db.Exec("PRAGMA synchronous = NORMAL")

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	const batchSize = 500
	totalSaved := 0

	for i := 0; i < len(items); i += batchSize {
		end := min(i+batchSize, len(items))
		batch := items[i:end]

		var placeholders []string
		var args []any

		for _, p := range batch {
			placeholders = append(placeholders, "(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)")
			args = append(args,
				p.Identifier, p.Enabled, p.Family, p.Categories, p.ProductType,
				p.Sex, p.Season, p.Color, p.FilterColor, p.WbNmID,
				p.YearCollection, p.MenuProductType, p.MenuAge, p.AgeCategory,
				p.Composition, p.Naznacenie, p.Minicollection,
				p.BrandCountry, p.CountryManufacture, p.SizeTable,
				p.FeaturesCare, p.Description, p.Name, p.Updated,
				p.WildberriesLength, p.WildberriesWidth, p.WildberriesHeight,
			)
		}

		query := fmt.Sprintf(`
			INSERT OR REPLACE INTO pim_goods (
				identifier, enabled, family, categories, product_type,
				sex, season, color, filter_color, wb_nm_id,
				year_collection, menu_product_type, menu_age, age_category,
				composition, naznacenie, minicollection,
				brand_country, country_manufacture, size_table,
				features_care, description, name, updated,
				wildberries_length, wildberries_width, wildberries_height
			) VALUES %s
		`, strings.Join(placeholders, ", "))

		result, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			return totalSaved, fmt.Errorf("save pim_goods batch (offset %d): %w", i, err)
		}

		affected, _ := result.RowsAffected()
		totalSaved += int(affected)
	}

	return totalSaved, tx.Commit()
}

// CountOneCGoods returns total number of 1C goods in the database.
func (r *SQLiteSalesRepository) CountOneCGoods(ctx context.Context) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM onec_goods").Scan(&count)
	return count, err
}

// CountOneCSKUs returns total number of 1C SKUs in the database.
func (r *SQLiteSalesRepository) CountOneCSKUs(ctx context.Context) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM onec_goods_sku").Scan(&count)
	return count, err
}

// CountOneCPrices returns total number of 1C price rows in the database.
func (r *SQLiteSalesRepository) CountOneCPrices(ctx context.Context) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM onec_prices").Scan(&count)
	return count, err
}

// CountPIMGoods returns total number of PIM goods in the database.
func (r *SQLiteSalesRepository) CountPIMGoods(ctx context.Context) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM pim_goods").Scan(&count)
	return count, err
}

// CleanOneCData deletes all data from 1C/PIM tables.
func (r *SQLiteSalesRepository) CleanOneCData(ctx context.Context) error {
	for _, table := range []string{"onec_prices", "onec_goods_sku", "onec_goods", "pim_goods", "onec_rests"} {
		if _, err := r.db.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			return fmt.Errorf("clean table %s: %w", table, err)
		}
	}
	// Clean API-sourced dimension rows (preserve XLS rows)
	if _, err := r.db.ExecContext(ctx, "DELETE FROM onec_dimensions WHERE source = 'api'"); err != nil {
		return fmt.Errorf("clean api dimensions: %w", err)
	}
	return nil
}

// SaveOneCRests saves a batch of 1C rests rows using INSERT OR REPLACE.
// snapshotDate is applied to all rows (YYYY-MM-DD, set by caller).
// Returns number of rows inserted/replaced.
func (r *SQLiteSalesRepository) SaveOneCRests(ctx context.Context, rests []OneCRestsRow, snapshotDate string) (int, error) {
	if len(rests) == 0 {
		return 0, nil
	}

	r.db.Exec("PRAGMA synchronous = OFF")
	defer r.db.Exec("PRAGMA synchronous = NORMAL")

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	const batchSize = 500
	totalSaved := 0

	for i := 0; i < len(rests); i += batchSize {
		end := min(i+batchSize, len(rests))
		batch := rests[i:end]

		var placeholders []string
		var args []any

		for _, r := range batch {
			placeholders = append(placeholders, "(?, ?, ?, ?, ?, ?, ?, ?, ?)")
			args = append(args,
				r.GoodGUID, r.SKUGUID, r.StorageGUID, snapshotDate,
				r.StorageName, r.Stock, r.Reserv, r.Free, boolToInt(r.FirstStage),
			)
		}

		query := fmt.Sprintf(`
			INSERT OR REPLACE INTO onec_rests (
				good_guid, sku_guid, storage_guid, snapshot_date,
				storage_name, stock, reserv, free, first_stage
			) VALUES %s
		`, strings.Join(placeholders, ", "))

		result, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			return totalSaved, fmt.Errorf("save onec_rests batch (offset %d): %w", i, err)
		}

		affected, _ := result.RowsAffected()
		totalSaved += int(affected)
	}

	return totalSaved, tx.Commit()
}

// CountOneCRests returns total number of 1C rests rows in the database.
func (r *SQLiteSalesRepository) CountOneCRests(ctx context.Context) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM onec_rests").Scan(&count)
	return count, err
}

// CleanOneCRests deletes all data from onec_rests table.
func (r *SQLiteSalesRepository) CleanOneCRests(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, "DELETE FROM onec_rests")
	return err
}

// PurgeOldRestsSnapshots deletes snapshots older than retentionDays counting from yesterday.
// retentionDays=7 → keep snapshots from date('now','-1 day') through date('now','-7 days').
// Today's snapshot is always kept (day still in progress).
func (r *SQLiteSalesRepository) PurgeOldRestsSnapshots(ctx context.Context, retentionDays int) (int, error) {
	if retentionDays <= 0 {
		return 0, nil
	}

	cutoff := fmt.Sprintf("-%d days", retentionDays+1)
	result, err := r.db.ExecContext(ctx,
		"DELETE FROM onec_rests WHERE snapshot_date < date('now', ?)", cutoff,
	)
	if err != nil {
		return 0, fmt.Errorf("purge old rests snapshots: %w", err)
	}
	affected, _ := result.RowsAffected()
	return int(affected), nil
}

// ImportDimensions saves a batch of per-SKU dimension rows using INSERT OR REPLACE.
// Returns number of rows inserted/replaced.
func (r *SQLiteSalesRepository) ImportDimensions(ctx context.Context, rows []OneCDimensionRow) (int, error) {
	if len(rows) == 0 {
		return 0, nil
	}

	r.db.Exec("PRAGMA synchronous = OFF")
	defer r.db.Exec("PRAGMA synchronous = NORMAL")

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	const batchSize = 500
	totalSaved := 0

	for i := 0; i < len(rows); i += batchSize {
		end := min(i+batchSize, len(rows))
		batch := rows[i:end]

		var placeholders []string
		var args []any

		for _, d := range batch {
			placeholders = append(placeholders, "(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)")
			args = append(args,
				d.GoodGUID, d.SKUGUID, d.GoodName, d.SizeName,
				d.LengthDM, d.WidthDM, d.HeightDM, d.WeightKG,
				d.VolumeCM3, d.Source,
			)
		}

		query := fmt.Sprintf(`
			INSERT OR REPLACE INTO onec_dimensions (
				good_guid, sku_guid, good_name, size_name,
				length_dm, width_dm, height_dm, weight_kg,
				volume_cm3, source
			) VALUES %s
		`, strings.Join(placeholders, ", "))

		result, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			return totalSaved, fmt.Errorf("save onec_dimensions batch (offset %d): %w", i, err)
		}

		affected, _ := result.RowsAffected()
		totalSaved += int(affected)
	}

	return totalSaved, tx.Commit()
}

// CountDimensions returns total number of rows in onec_dimensions.
func (r *SQLiteSalesRepository) CountDimensions(ctx context.Context) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM onec_dimensions").Scan(&count)
	return count, err
}

// DimensionAggRow is the aggregated dimension result (max-volume SKU per good_guid).
type DimensionAggRow struct {
	NmID       int
	VendorCode string
	OldLength  float64
	OldWidth   float64
	OldHeight  float64
	OldWeight  float64
	NewLength  float64
	NewWidth   float64
	NewHeight  float64
	NewWeight  float64
}

// GetAggregatedDimensions joins onec_dimensions with cards via onec_goods,
// selects the SKU with the largest volume per good_guid, and returns only
// cards with missing dims.
func (r *SQLiteSalesRepository) GetAggregatedDimensions(ctx context.Context) ([]DimensionAggRow, error) {
	query := `
		SELECT
			c.nm_id,
			c.vendor_code,
			COALESCE(c.dim_length, 0),
			COALESCE(c.dim_width, 0),
			COALESCE(c.dim_height, 0),
			COALESCE(c.dim_weight_brutto, 0),
			d.length_dm * 10,
			d.width_dm * 10,
			d.height_dm * 10,
			d.weight_kg
		FROM onec_dimensions d
		JOIN onec_goods og ON og.guid = d.good_guid
		JOIN cards c ON c.vendor_code = og.article
		WHERE (c.dim_length = 0 OR c.dim_width = 0 OR c.dim_height = 0 OR c.dim_weight_brutto = 0)
		  AND CASE WHEN d.volume_cm3 > 0 THEN d.volume_cm3
		           ELSE (d.length_dm * 10) * (d.width_dm * 10) * (d.height_dm * 10)
		      END = (
			  SELECT MAX(CASE WHEN d2.volume_cm3 > 0 THEN d2.volume_cm3
			                  ELSE (d2.length_dm * 10) * (d2.width_dm * 10) * (d2.height_dm * 10)
			             END)
			  FROM onec_dimensions d2 WHERE d2.good_guid = d.good_guid
		  )
		GROUP BY c.nm_id ORDER BY c.vendor_code
	`

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query aggregated dimensions: %w", err)
	}
	defer rows.Close()

	var result []DimensionAggRow
	for rows.Next() {
		var r DimensionAggRow
		if err := rows.Scan(
			&r.NmID, &r.VendorCode,
			&r.OldLength, &r.OldWidth, &r.OldHeight, &r.OldWeight,
			&r.NewLength, &r.NewWidth, &r.NewHeight, &r.NewWeight,
		); err != nil {
			return nil, fmt.Errorf("scan dimension agg row: %w", err)
		}
		result = append(result, r)
	}

	return result, rows.Err()
}
