package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

const stagingTableDDL = `
CREATE TABLE IF NOT EXISTS fix_card_fields_staging (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    nm_id          INTEGER NOT NULL UNIQUE,
    vendor_code    TEXT    NOT NULL,
    title          TEXT    NOT NULL DEFAULT '',
    subject_id     INTEGER NOT NULL DEFAULT 0,
    subject_name   TEXT    NOT NULL DEFAULT '',
    changes_json   TEXT    NOT NULL DEFAULT '[]',
    all_chars_json TEXT    NOT NULL DEFAULT '[]',
    sizes_json     TEXT    NOT NULL DEFAULT '[]',
    status         TEXT    NOT NULL DEFAULT 'new',
    error_msg      TEXT    NOT NULL DEFAULT '',
    created_at     TEXT    DEFAULT CURRENT_TIMESTAMP
);
`

func runStage(ctx context.Context, db *sql.DB, cfg *Config) error {
	if _, err := db.ExecContext(ctx, stagingTableDDL); err != nil {
		return fmt.Errorf("create staging table: %w", err)
	}
	if _, err := db.ExecContext(ctx, "DELETE FROM fix_card_fields_staging"); err != nil {
		return fmt.Errorf("clear staging table: %w", err)
	}

	cards, err := loadFilteredCards(ctx, db, cfg.Filters)
	if err != nil {
		return fmt.Errorf("load cards: %w", err)
	}
	if len(cards) == 0 {
		fmt.Println("No cards match the filters.")
		return nil
	}
	fmt.Printf("Filtered: %d cards\n", len(cards))

	nmIDs := make([]int, len(cards))
	for i, c := range cards {
		nmIDs[i] = c.NmID
	}

	charsMap, err := loadCharacteristics(ctx, db, nmIDs)
	if err != nil {
		return fmt.Errorf("load characteristics: %w", err)
	}

	var staged []stagedCard
	for _, card := range cards {
		chars := charsMap[card.NmID]
		changes := matchRules(cfg.FixRules, chars)
		if len(changes) == 0 {
			continue
		}
		staged = append(staged, stagedCard{
			CardData: card,
			Changes:  changes,
			AllChars: chars,
		})
	}

	if len(staged) == 0 {
		fmt.Println("No cards matched any fix rules.")
		return nil
	}

	// Load sizes only for cards that have changes.
	stagedNmIDs := make([]int, len(staged))
	for i, s := range staged {
		stagedNmIDs[i] = s.NmID
	}
	sizesMap, err := loadSizes(ctx, db, stagedNmIDs)
	if err != nil {
		return fmt.Errorf("load sizes: %w", err)
	}
	for i, s := range staged {
		staged[i].Sizes = sizesMap[s.NmID]
	}

	// Write to staging table in a transaction.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT OR REPLACE INTO fix_card_fields_staging
			(nm_id, vendor_code, title, subject_id, subject_name,
			 changes_json, all_chars_json, sizes_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("prepare insert: %w", err)
	}
	defer stmt.Close()

	for _, s := range staged {
		changesJSON, _ := json.Marshal(s.Changes)
		allCharsJSON, _ := json.Marshal(s.AllChars)
		sizesJSON, _ := json.Marshal(s.Sizes)

		if _, err := stmt.ExecContext(ctx,
			s.NmID, s.VendorCode, s.Title, s.SubjectID, s.SubjectName,
			string(changesJSON), string(allCharsJSON), string(sizesJSON),
		); err != nil {
			tx.Rollback()
			return fmt.Errorf("insert nm_id=%d: %w", s.NmID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	printStageStats(ctx, db, cfg)
	return nil
}

func printStageStats(ctx context.Context, db *sql.DB, cfg *Config) {
	var total, withChanges, skipped int
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM fix_card_fields_staging").Scan(&total)
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM fix_card_fields_staging WHERE changes_json != '[]'").Scan(&withChanges)
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM fix_card_fields_staging WHERE status = 'skipped'").Scan(&skipped)

	fmt.Printf("\nStaged %d cards:\n", total)
	fmt.Printf("  with changes: %d\n", withChanges)
	fmt.Printf("  skipped:      %d\n", skipped)

	// Per-rule distribution.
	for _, rule := range cfg.FixRules {
		var cnt int
		db.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM fix_card_fields_staging
			WHERE changes_json LIKE ?
		`, fmt.Sprintf(`%%"char_id":%d%%`, rule.CharID)).Scan(&cnt)
		fmt.Printf("  rule char_id=%d (%s→%s): %d cards\n",
			rule.CharID, fmtVal(rule.SearchValue), fmtVal(rule.ReplaceValue), cnt)
	}

	fmt.Println()
	fmt.Println("Next steps:")
	fmt.Println("  Review:  sqlite3 <db> \"SELECT * FROM fix_card_fields_staging\"")
	fmt.Println("  Diff:    fix-card-fields --config config.yaml --diff")
	fmt.Println("  Apply:   fix-card-fields --config config.yaml --apply [--dry-run]")
}

// runDiff prints a before/after diff for staged cards.
func runDiff(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, `
		SELECT nm_id, vendor_code, changes_json, all_chars_json
		FROM fix_card_fields_staging
		WHERE status = 'new' AND changes_json != '[]'
		ORDER BY nm_id
	`)
	if err != nil {
		return fmt.Errorf("query staging: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var nmID int
		var vc, changesJSON, allCharsJSON string
		if err := rows.Scan(&nmID, &vc, &changesJSON, &allCharsJSON); err != nil {
			return fmt.Errorf("scan: %w", err)
		}

		var changes []changeEntry
		json.Unmarshal([]byte(changesJSON), &changes)
		var allChars []CardChar
		json.Unmarshal([]byte(allCharsJSON), &allChars)

		fmt.Printf("\n--- nm_id=%d vendor_code=%s ---\n", nmID, vc)
		charNames := make(map[int]string)
		for _, c := range allChars {
			charNames[c.CharID] = c.Name
		}
		for _, ch := range changes {
			name := charNames[ch.CharID]
			if name == "" {
				name = fmt.Sprintf("char_%d", ch.CharID)
			}
			fmt.Printf("  %s (id=%d): %s → %s\n", name, ch.CharID, ch.Old, ch.New)
		}
	}
	return nil
}
