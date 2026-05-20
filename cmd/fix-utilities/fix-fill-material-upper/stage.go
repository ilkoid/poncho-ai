package main

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

const stagingTableDDL = `
CREATE TABLE IF NOT EXISTS fix_material_upper (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    nm_id            INTEGER NOT NULL UNIQUE,
    vendor_code      TEXT    NOT NULL,
    title            TEXT    NOT NULL DEFAULT '',
    subject_name     TEXT    NOT NULL DEFAULT '',
    wb_material_upper TEXT   NOT NULL DEFAULT '',
    onec_article     TEXT    NOT NULL DEFAULT '',
    onec_composition TEXT    NOT NULL DEFAULT '',
    mapped_value     TEXT    NOT NULL DEFAULT '',
    char_id          INTEGER NOT NULL DEFAULT 15003971,
    status           TEXT    NOT NULL DEFAULT 'new',
    error_msg        TEXT    NOT NULL DEFAULT '',
    created_at       TEXT    DEFAULT CURRENT_TIMESTAMP
);
`

type stagingRow struct {
	nmID            int
	vendorCode      string
	title           string
	subjectName     string
	wbMaterialUpper string
	onecArticle     string
	onecComposition string
	mappedValue     string
}

// mappingRule defines a keyword→WB-value rule. First match wins.
var mappingRules = []struct {
	keyword string
	wbValue string
}{
	{"натуральная кожа", "натуральная кожа"},
	{"искусственная кожа", "искусственная кожа"},
	{"натуральный мех", "натуральный мех"},
	{"искусственный мех", "искусственный мех"},
	{"текстиль", "текстиль"},
	{"полиуретан", "полиуретан"},
	{"резина", "резина"},
	{"эва", "ЭВА"},
	{"пвх", "ПВХ"},
	{"полиэстер", "полиэстер"},
}

func runStage(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, stagingTableDDL); err != nil {
		return fmt.Errorf("create staging table: %w", err)
	}

	if _, err := db.ExecContext(ctx, "DELETE FROM fix_material_upper"); err != nil {
		return fmt.Errorf("clear staging table: %w", err)
	}

	rows, err := db.QueryContext(ctx, `
		SELECT c.nm_id, c.vendor_code, COALESCE(c.title, ''), COALESCE(c.subject_name, ''),
		       COALESCE(ch.json_value, ''),
		       COALESCE(g.article, ''),
		       COALESCE(g.composition, '')
		FROM cards c
		LEFT JOIN card_characteristics ch ON c.nm_id = ch.nm_id AND ch.name = 'Материал верха'
		LEFT JOIN onec_goods g ON c.vendor_code = g.article
		WHERE c.subject_id = 105
		  AND LENGTH(c.vendor_code) = 8
		  AND SUBSTR(c.vendor_code, 2, 2) IN ('23','24','25','26')
		  AND (ch.nm_id IS NULL OR ch.json_value IN ('[]', ''))
		ORDER BY c.nm_id
	`)
	if err != nil {
		return fmt.Errorf("query missing cards: %w", err)
	}
	defer rows.Close()

	var staged []stagingRow
	for rows.Next() {
		var r stagingRow
		if err := rows.Scan(&r.nmID, &r.vendorCode, &r.title, &r.subjectName, &r.wbMaterialUpper, &r.onecArticle, &r.onecComposition); err != nil {
			return fmt.Errorf("scan row: %w", err)
		}
		r.mappedValue = mapComposition(r.onecComposition)
		staged = append(staged, r)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("rows: %w", err)
	}

	if len(staged) == 0 {
		fmt.Println("No sneakers without 'Материал верха' found.")
		return nil
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	stmt, err := tx.PrepareContext(ctx, `
		INSERT OR REPLACE INTO fix_material_upper (nm_id, vendor_code, title, subject_name, wb_material_upper, onec_article, onec_composition, mapped_value)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("prepare insert: %w", err)
	}
	defer stmt.Close()

	for _, r := range staged {
		if _, err := stmt.ExecContext(ctx, r.nmID, r.vendorCode, r.title, r.subjectName, r.wbMaterialUpper, r.onecArticle, r.onecComposition, r.mappedValue); err != nil {
			tx.Rollback()
			return fmt.Errorf("insert nm_id=%d: %w", r.nmID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	printStats(ctx, db)
	return nil
}

func printStats(ctx context.Context, db *sql.DB) {
	var total, mapped, unmapped, noOneC int
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM fix_material_upper").Scan(&total)
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM fix_material_upper WHERE mapped_value != 'UNMAPPED'").Scan(&mapped)
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM fix_material_upper WHERE mapped_value = 'UNMAPPED'").Scan(&unmapped)
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM fix_material_upper WHERE onec_composition = ''").Scan(&noOneC)

	fmt.Printf("Staged %d cards:\n", total)
	fmt.Printf("  mapped:     %d\n", mapped)
	fmt.Printf("  unmapped:   %d\n", unmapped)
	fmt.Printf("  no 1C data: %d\n", noOneC)
	fmt.Println()

	distRows, _ := db.QueryContext(ctx, `
		SELECT mapped_value, COUNT(*) FROM fix_material_upper GROUP BY mapped_value ORDER BY COUNT(*) DESC
	`)
	if distRows != nil {
		defer distRows.Close()
		fmt.Println("Value distribution:")
		for distRows.Next() {
			var val string
			var cnt int
			distRows.Scan(&val, &cnt)
			fmt.Printf("  %-25s %d\n", val, cnt)
		}
	}

	fmt.Println()
	fmt.Println("Review:  SELECT * FROM fix_material_upper;")
	fmt.Println("Fix:     UPDATE fix_material_upper SET mapped_value = '...' WHERE ...;")
	fmt.Println("Apply:   fix-fill-material-upper --apply [--dry-run]")
}

func mapComposition(composition string) string {
	if composition == "" {
		return "UNMAPPED"
	}
	lower := strings.ToLower(composition)
	for _, rule := range mappingRules {
		if strings.Contains(lower, rule.keyword) {
			return rule.wbValue
		}
	}
	return "UNMAPPED"
}
