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

	const batchSize = 500
	totalSaved := 0

	for i := 0; i < len(goods); i += batchSize {
		end := min(i+batchSize, len(goods))
		batch := goods[i:end]

		var placeholders []string
		var args []any

		for _, g := range batch {
			placeholders = append(placeholders, "(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)")
			args = append(args,
				g.GUID, g.Article, g.Name, g.NameIM, g.Description,
				g.Brand, g.Type, g.Category, g.CategoryLevel1, g.CategoryLevel2,
				g.Sex, g.Season, g.Composition, g.CompositionLining, g.Color,
				g.Collection, g.CountryOfOrigin, g.Weight, g.SizeRange,
				g.TnvedCodes, g.BusinessLine,
				g.IsSale, g.IsNew, g.ModelStatus, g.Date,
			)
		}

		query := fmt.Sprintf(`
			INSERT OR REPLACE INTO onec_goods (
				guid, article, name, name_im, description,
				brand, type, category, category_level1, category_level2,
				sex, season, composition, composition_lining, color,
				collection, country_of_origin, weight, size_range,
				tnved_codes, business_line,
				is_sale, is_new, model_status, date
			) VALUES %s
		`, strings.Join(placeholders, ", "))

		result, err := r.db.ExecContext(ctx, query, args...)
		if err != nil {
			return totalSaved, fmt.Errorf("save onec_goods batch (offset %d): %w", i, err)
		}

		affected, _ := result.RowsAffected()
		totalSaved += int(affected)
	}

	return totalSaved, nil
}

// SaveOneCSKUs saves a batch of 1C SKUs using INSERT OR REPLACE.
// Returns number of rows inserted/replaced.
func (r *SQLiteSalesRepository) SaveOneCSKUs(ctx context.Context, skus []OneCSKU) (int, error) {
	if len(skus) == 0 {
		return 0, nil
	}

	const batchSize = 500
	totalSaved := 0

	for i := 0; i < len(skus); i += batchSize {
		end := min(i+batchSize, len(skus))
		batch := skus[i:end]

		var placeholders []string
		var args []any

		for _, s := range batch {
			placeholders = append(placeholders, "(?, ?, ?, ?, ?)")
			args = append(args, s.SKUGUID, s.GUID, s.Barcode, s.Size, s.NDS)
		}

		query := fmt.Sprintf(`
			INSERT OR REPLACE INTO onec_goods_sku (sku_guid, guid, barcode, size, nds)
			VALUES %s
		`, strings.Join(placeholders, ", "))

		result, err := r.db.ExecContext(ctx, query, args...)
		if err != nil {
			return totalSaved, fmt.Errorf("save onec_goods_sku batch (offset %d): %w", i, err)
		}

		affected, _ := result.RowsAffected()
		totalSaved += int(affected)
	}

	return totalSaved, nil
}

// SaveOneCPrices saves a batch of 1C price rows using INSERT OR REPLACE.
// snapshotDate is applied to all rows (YYYY-MM-DD, set by caller).
// Returns number of rows inserted/replaced.
func (r *SQLiteSalesRepository) SaveOneCPrices(ctx context.Context, prices []OneCPriceRow, snapshotDate string) (int, error) {
	if len(prices) == 0 {
		return 0, nil
	}

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

		result, err := r.db.ExecContext(ctx, query, args...)
		if err != nil {
			return totalSaved, fmt.Errorf("save onec_prices batch (offset %d): %w", i, err)
		}

		affected, _ := result.RowsAffected()
		totalSaved += int(affected)
	}

	return totalSaved, nil
}

// SavePIMGoods saves a batch of PIM goods using INSERT OR REPLACE.
// Returns number of rows inserted/replaced.
func (r *SQLiteSalesRepository) SavePIMGoods(ctx context.Context, items []PIMGoodsRow) (int, error) {
	if len(items) == 0 {
		return 0, nil
	}

	const batchSize = 500
	totalSaved := 0

	for i := 0; i < len(items); i += batchSize {
		end := min(i+batchSize, len(items))
		batch := items[i:end]

		var placeholders []string
		var args []any

		for _, p := range batch {
			placeholders = append(placeholders, "(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)")
			args = append(args,
				p.Identifier, p.Enabled, p.Family, p.Categories, p.ProductType,
				p.Sex, p.Season, p.Color, p.FilterColor, p.WbNmID,
				p.YearCollection, p.MenuProductType, p.MenuAge, p.AgeCategory,
				p.Composition, p.Naznacenie, p.Minicollection,
				p.BrandCountry, p.CountryManufacture, p.SizeTable,
				p.FeaturesCare, p.Description, p.Name, p.Updated,
				p.ValuesJSON,
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
				values_json
			) VALUES %s
		`, strings.Join(placeholders, ", "))

		result, err := r.db.ExecContext(ctx, query, args...)
		if err != nil {
			return totalSaved, fmt.Errorf("save pim_goods batch (offset %d): %w", i, err)
		}

		affected, _ := result.RowsAffected()
		totalSaved += int(affected)
	}

	return totalSaved, nil
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
	for _, table := range []string{"onec_prices", "onec_goods_sku", "onec_goods", "pim_goods"} {
		if _, err := r.db.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			return fmt.Errorf("clean table %s: %w", table, err)
		}
	}
	return nil
}
