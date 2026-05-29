package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/filter"
)

// Certificate characteristic IDs on WB.
const (
	charDeclNumber = 15001135 // Номер декларации соответствия
	charCertNumber = 15001136 // Номер сертификата соответствия
	charCertBegin  = 15001137 // Дата регистрации сертификата/декларации
	charCertEnd    = 15001138 // Дата окончания действия сертификата/декларации
)

// certKind distinguishes 1C certificate types by the prefix of onec_goods.certificate.
type certKind int

const (
	certNone certKind = iota // Отказное письмо или неизвестный тип — пропустить
	certType                 // "Сертификат соответствия" → char 15001136
	certDecl                 // "Декларация о соответствии" → char 15001135
)

func parseCertKind(s string) certKind {
	if strings.HasPrefix(s, "Декларация") {
		return certDecl
	}
	if strings.HasPrefix(s, "Сертификат") {
		return certType
	}
	return certNone
}

// targetCharID returns the WB char_id for the certificate/declaration number.
func (k certKind) targetCharID() int {
	if k == certDecl {
		return charDeclNumber
	}
	return charCertNumber
}

const stagingTableDDL = `
CREATE TABLE IF NOT EXISTS fix_certificates_staging (
    id                      INTEGER PRIMARY KEY AUTOINCREMENT,
    nm_id                   INTEGER NOT NULL UNIQUE,
    vendor_code             TEXT    NOT NULL,
    title                   TEXT    NOT NULL DEFAULT '',
    subject_name            TEXT    NOT NULL DEFAULT '',
    onec_certificate        TEXT    NOT NULL DEFAULT '',
    onec_certificate_number TEXT    NOT NULL DEFAULT '',
    onec_certificate_begin  TEXT    NOT NULL DEFAULT '',
    onec_certificate_end    TEXT    NOT NULL DEFAULT '',
    changes_json            TEXT    NOT NULL DEFAULT '[]',
    all_chars_json          TEXT    NOT NULL DEFAULT '[]',
    sizes_json              TEXT    NOT NULL DEFAULT '[]',
    status                  TEXT    NOT NULL DEFAULT 'new',
    error_msg               TEXT    NOT NULL DEFAULT '',
    created_at              TEXT    DEFAULT CURRENT_TIMESTAMP
);
`

type stageRow struct {
	NmID                 int
	VendorCode           string
	Title                string
	SubjectName          string
	OneCCertificate      string
	OneCCertificateNum   string
	OneCCertificateBegin string
	OneCCertificateEnd   string
}

// stageRowAdapter wraps stageRow to implement filter.Filterable.
type stageRowAdapter struct{ row stageRow }

func (a stageRowAdapter) GetNmID() int          { return a.row.NmID }
func (a stageRowAdapter) GetVendorCode() string  { return a.row.VendorCode }
func (a stageRowAdapter) GetSubjectID() int      { return 0 }
func (a stageRowAdapter) GetSubjectName() string { return a.row.SubjectName }
func (a stageRowAdapter) GetSeasons() []string   { return nil }

type changeEntry struct {
	CharID int    `json:"char_id"`
	Old    string `json:"old"`
	New    string `json:"new"`
}

func runStage(ctx context.Context, db *sql.DB, f *filter.Filter, refTime time.Time) error {
	if _, err := db.ExecContext(ctx, stagingTableDDL); err != nil {
		return fmt.Errorf("create staging table: %w", err)
	}
	if _, err := db.ExecContext(ctx, "DELETE FROM fix_certificates_staging"); err != nil {
		return fmt.Errorf("clear staging table: %w", err)
	}

	fmt.Println("Querying cards with missing certificates and declarations...")

	rows, err := db.QueryContext(ctx, `
		SELECT c.nm_id, c.vendor_code, COALESCE(c.title,''), COALESCE(c.subject_name,''),
		       COALESCE(og.certificate,''), COALESCE(og.certificate_number,''),
		       COALESCE(og.certificate_begin,''), COALESCE(og.certificate_end,'')
		FROM cards c
		JOIN onec_goods og ON c.vendor_code = og.article
		WHERE og.has_certificate = 1
		  AND og.certificate_number != ''
		  AND (
		    NOT EXISTS (
		      SELECT 1 FROM card_characteristics cc
		      WHERE cc.nm_id = c.nm_id AND cc.char_id IN (15001135, 15001136)
		      AND cc.json_value NOT IN ('[]', '')
		    )
		  )
		ORDER BY c.nm_id
	`)
	if err != nil {
		return fmt.Errorf("query cards: %w", err)
	}
	defer rows.Close()

	var cards []stageRow
	for rows.Next() {
		var r stageRow
		if err := rows.Scan(&r.NmID, &r.VendorCode, &r.Title, &r.SubjectName,
			&r.OneCCertificate, &r.OneCCertificateNum,
			&r.OneCCertificateBegin, &r.OneCCertificateEnd); err != nil {
			return fmt.Errorf("scan: %w", err)
		}
		cards = append(cards, r)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("rows: %w", err)
	}

	// Apply in-memory filters (fields available in stageRow).
	cards = applyMemFilters(cards, f)

	// Apply SQL filters (DB-dependent fields: in_stock, subject_ids, seasons, onec_type, etc.).
	cards, err = applySQLFilters(ctx, db, cards, f)
	if err != nil {
		return fmt.Errorf("sql filters: %w", err)
	}

	fmt.Printf("Found %d cards with missing certificate data\n\n", len(cards))

	if len(cards) == 0 {
		return nil
	}

	// Load characteristics and sizes for all cards.
	nmIDs := make([]int, len(cards))
	for i, c := range cards {
		nmIDs[i] = c.NmID
	}
	charsMap, err := loadCharacteristics(ctx, db, nmIDs)
	if err != nil {
		return fmt.Errorf("load characteristics: %w", err)
	}
	sizesMap, err := loadSizes(ctx, db, nmIDs)
	if err != nil {
		return fmt.Errorf("load sizes: %w", err)
	}

	// Build staging rows.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT OR REPLACE INTO fix_certificates_staging
			(nm_id, vendor_code, title, subject_name,
			 onec_certificate, onec_certificate_number, onec_certificate_begin, onec_certificate_end,
			 changes_json, all_chars_json, sizes_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("prepare insert: %w", err)
	}
	defer stmt.Close()

	staged := 0
	for _, c := range cards {
		changes := buildCertChanges(charsMap[c.NmID], c, refTime)
		if len(changes) == 0 {
			continue
		}

		allCharsJSON, _ := json.Marshal(charsMap[c.NmID])
		sizesJSON, _ := json.Marshal(sizesMap[c.NmID])
		changesJSON, _ := json.Marshal(changes)

		if _, err := stmt.ExecContext(ctx,
			c.NmID, c.VendorCode, c.Title, c.SubjectName,
			c.OneCCertificate, c.OneCCertificateNum, c.OneCCertificateBegin, c.OneCCertificateEnd,
			string(changesJSON), string(allCharsJSON), string(sizesJSON),
		); err != nil {
			tx.Rollback()
			return fmt.Errorf("insert nm_id=%d: %w", c.NmID, err)
		}
		staged++
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	printStageStats(ctx, db)
	fmt.Printf("\nNext steps:\n")
	fmt.Printf("  Review:  sqlite3 <db> \"SELECT * FROM fix_certificates_staging LIMIT 20\"\n")
	fmt.Printf("  Diff:    fix-certificates --diff --db <db>\n")
	fmt.Printf("  Apply:   fix-certificates --apply --dry-run --db <db>\n")
	return nil
}

// fixRow holds a card with cert/decl type mismatch for --fix-type staging.
type fixRow struct {
	NmID        int
	VendorCode  string
	Title       string
	SubjectName string
	Certificate string
	CertNum     string
	WrongCharID int
	WrongValue  string
}

// fixRowAdapter wraps fixRow to implement filter.Filterable.
type fixRowAdapter struct{ row fixRow }

func (a fixRowAdapter) GetNmID() int          { return a.row.NmID }
func (a fixRowAdapter) GetVendorCode() string  { return a.row.VendorCode }
func (a fixRowAdapter) GetSubjectID() int      { return 0 }
func (a fixRowAdapter) GetSubjectName() string { return a.row.SubjectName }
func (a fixRowAdapter) GetSeasons() []string   { return nil }

// runFixTypeStage finds cards where cert/decl number is correct but in the wrong char_id.
// Only fixes cards where the number string matches between 1C and WB.
func runFixTypeStage(ctx context.Context, db *sql.DB, f *filter.Filter) error {
	if _, err := db.ExecContext(ctx, stagingTableDDL); err != nil {
		return fmt.Errorf("create staging table: %w", err)
	}
	if _, err := db.ExecContext(ctx, "DELETE FROM fix_certificates_staging"); err != nil {
		return fmt.Errorf("clear staging table: %w", err)
	}

	fmt.Println("Querying cards with cert/decl type mismatch (same number, wrong char_id)...")

	rows, err := db.QueryContext(ctx, `
		SELECT c.nm_id, c.vendor_code, COALESCE(c.title,''), COALESCE(c.subject_name,''),
		       og.certificate, og.certificate_number,
		       cc_wrong.char_id, cc_wrong.json_value
		FROM cards c
		JOIN onec_goods og ON c.vendor_code = og.article
		JOIN card_characteristics cc_wrong ON cc_wrong.nm_id = c.nm_id
			AND cc_wrong.json_value NOT IN ('[]', '')
		WHERE og.has_certificate = 1 AND og.certificate_number != ''
		  AND (
		    (og.certificate LIKE 'Декларация%' AND cc_wrong.char_id = 15001136)
		    OR
		    (og.certificate LIKE 'Сертификат%' AND cc_wrong.char_id = 15001135)
		  )
		  AND cc_wrong.json_value LIKE '%' || og.certificate_number || '%'
		  AND NOT EXISTS (
		    SELECT 1 FROM card_characteristics cc_ok
		    WHERE cc_ok.nm_id = c.nm_id
		      AND cc_ok.char_id = CASE WHEN og.certificate LIKE 'Декларация%' THEN 15001135 ELSE 15001136 END
		      AND cc_ok.json_value NOT IN ('[]', '')
		  )
		ORDER BY c.nm_id
	`)
	if err != nil {
		return fmt.Errorf("query mismatches: %w", err)
	}
	defer rows.Close()

	var fixes []fixRow
	for rows.Next() {
		var r fixRow
		if err := rows.Scan(&r.NmID, &r.VendorCode, &r.Title, &r.SubjectName,
			&r.Certificate, &r.CertNum, &r.WrongCharID, &r.WrongValue); err != nil {
			return fmt.Errorf("scan: %w", err)
		}
		fixes = append(fixes, r)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("rows: %w", err)
	}

	// Apply in-memory filters.
	fixes = applyFixMemFilters(fixes, f)

	// Apply SQL filters (DB-dependent fields).
	fixes, err = applyFixSQLFilters(ctx, db, fixes, f)
	if err != nil {
		return fmt.Errorf("sql filters: %w", err)
	}

	fmt.Printf("Found %d cards with type mismatch\n\n", len(fixes))

	if len(fixes) == 0 {
		return nil
	}

	// Load characteristics and sizes for all cards.
	nmIDs := make([]int, len(fixes))
	for i, f := range fixes {
		nmIDs[i] = f.NmID
	}
	charsMap, err := loadCharacteristics(ctx, db, nmIDs)
	if err != nil {
		return fmt.Errorf("load characteristics: %w", err)
	}
	sizesMap, err := loadSizes(ctx, db, nmIDs)
	if err != nil {
		return fmt.Errorf("load sizes: %w", err)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT OR REPLACE INTO fix_certificates_staging
			(nm_id, vendor_code, title, subject_name,
			 onec_certificate, onec_certificate_number, onec_certificate_begin, onec_certificate_end,
			 changes_json, all_chars_json, sizes_json)
		VALUES (?, ?, ?, ?, ?, ?, '', '', ?, ?, ?)
	`)
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("prepare insert: %w", err)
	}
	defer stmt.Close()

	staged := 0
	for _, f := range fixes {
		kind := parseCertKind(f.Certificate)
		correctCharID := kind.targetCharID()

		// Extract the actual number from JSON value (e.g. '["RU Д-CN..."]' → 'RU Д-CN...')
		var valArr []string
		if err := json.Unmarshal([]byte(f.WrongValue), &valArr); err == nil && len(valArr) > 0 {
			f.WrongValue = valArr[0]
		}

		changes := []changeEntry{
			{CharID: f.WrongCharID, Old: f.WrongValue, New: ""},    // remove from wrong char
			{CharID: correctCharID, Old: "", New: f.WrongValue},     // add to correct char
		}

		allCharsJSON, _ := json.Marshal(charsMap[f.NmID])
		sizesJSON, _ := json.Marshal(sizesMap[f.NmID])
		changesJSON, _ := json.Marshal(changes)

		if _, err := stmt.ExecContext(ctx,
			f.NmID, f.VendorCode, f.Title, f.SubjectName,
			f.Certificate, f.CertNum,
			string(changesJSON), string(allCharsJSON), string(sizesJSON),
		); err != nil {
			tx.Rollback()
			return fmt.Errorf("insert nm_id=%d: %w", f.NmID, err)
		}
		staged++
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	printStageStats(ctx, db)
	fmt.Printf("\nNext steps:\n")
	fmt.Printf("  Diff:    fix-certificates --diff --db <db>\n")
	fmt.Printf("  Apply:   fix-certificates --apply --dry-run --db <db>\n")
	return nil
}

// runReconcileStage finds cards where WB cert/decl data differs from 1C (any discrepancy).
func runReconcileStage(ctx context.Context, db *sql.DB, f *filter.Filter, refTime time.Time) error {
	if _, err := db.ExecContext(ctx, stagingTableDDL); err != nil {
		return fmt.Errorf("create staging table: %w", err)
	}
	if _, err := db.ExecContext(ctx, "DELETE FROM fix_certificates_staging"); err != nil {
		return fmt.Errorf("clear staging table: %w", err)
	}

	fmt.Println("Querying cards with cert/decl discrepancies between WB and 1C...")

	rows, err := db.QueryContext(ctx, `
		SELECT c.nm_id, c.vendor_code, COALESCE(c.title,''), COALESCE(c.subject_name,''),
		       COALESCE(og.certificate,''), COALESCE(og.certificate_number,''),
		       COALESCE(og.certificate_begin,''), COALESCE(og.certificate_end,'')
		FROM cards c
		JOIN onec_goods og ON c.vendor_code = og.article
		WHERE og.has_certificate = 1
		  AND og.certificate_number != ''
		  AND (og.certificate LIKE 'Декларация%' OR og.certificate LIKE 'Сертификат%')
		  AND EXISTS (
		    SELECT 1 FROM card_characteristics cc
		    WHERE cc.nm_id = c.nm_id
		      AND cc.char_id IN (15001135, 15001136)
		      AND cc.json_value NOT IN ('[]', '')
		  )
		ORDER BY c.nm_id
	`)
	if err != nil {
		return fmt.Errorf("query cards: %w", err)
	}
	defer rows.Close()

	var cards []stageRow
	for rows.Next() {
		var r stageRow
		if err := rows.Scan(&r.NmID, &r.VendorCode, &r.Title, &r.SubjectName,
			&r.OneCCertificate, &r.OneCCertificateNum,
			&r.OneCCertificateBegin, &r.OneCCertificateEnd); err != nil {
			return fmt.Errorf("scan: %w", err)
		}
		cards = append(cards, r)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("rows: %w", err)
	}

	cards = applyMemFilters(cards, f)
	cards, err = applySQLFilters(ctx, db, cards, f)
	if err != nil {
		return fmt.Errorf("sql filters: %w", err)
	}

	fmt.Printf("Found %d candidate cards\n\n", len(cards))

	if len(cards) == 0 {
		return nil
	}

	nmIDs := make([]int, len(cards))
	for i, c := range cards {
		nmIDs[i] = c.NmID
	}
	charsMap, err := loadCharacteristics(ctx, db, nmIDs)
	if err != nil {
		return fmt.Errorf("load characteristics: %w", err)
	}
	sizesMap, err := loadSizes(ctx, db, nmIDs)
	if err != nil {
		return fmt.Errorf("load sizes: %w", err)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT OR REPLACE INTO fix_certificates_staging
			(nm_id, vendor_code, title, subject_name,
			 onec_certificate, onec_certificate_number, onec_certificate_begin, onec_certificate_end,
			 changes_json, all_chars_json, sizes_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("prepare insert: %w", err)
	}
	defer stmt.Close()

	staged := 0
	skipped := 0
	for _, c := range cards {
		changes := buildReconcileChanges(charsMap[c.NmID], c, refTime)
		if len(changes) == 0 {
			skipped++
			continue
		}

		allCharsJSON, _ := json.Marshal(charsMap[c.NmID])
		sizesJSON, _ := json.Marshal(sizesMap[c.NmID])
		changesJSON, _ := json.Marshal(changes)

		if _, err := stmt.ExecContext(ctx,
			c.NmID, c.VendorCode, c.Title, c.SubjectName,
			c.OneCCertificate, c.OneCCertificateNum, c.OneCCertificateBegin, c.OneCCertificateEnd,
			string(changesJSON), string(allCharsJSON), string(sizesJSON),
		); err != nil {
			tx.Rollback()
			return fmt.Errorf("insert nm_id=%d: %w", c.NmID, err)
		}
		staged++
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	printStageStats(ctx, db)
	fmt.Printf("  already correct (skipped): %d\n", skipped)
	fmt.Printf("\nNext steps:\n")
	fmt.Printf("  Diff:    fix-certificates --diff --db <db>\n")
	fmt.Printf("  Apply:   fix-certificates --apply --dry-run --db <db>\n")
	return nil
}

// buildCertChanges creates change entries for missing certificate/declaration fields.
func buildCertChanges(currentChars []CardChar, c stageRow, refTime time.Time) []changeEntry {
	kind := parseCertKind(c.OneCCertificate)
	if kind == certNone {
		return nil // Отказное письмо или неизвестный тип — пропускаем
	}

	// Skip cards with expired certificates entirely.
	if c.OneCCertificateEnd != "" && !isZeroDate(c.OneCCertificateEnd) && isExpired(c.OneCCertificateEnd, refTime) {
		return nil
	}

	// Build lookup of existing certificate/declaration chars.
	existing := make(map[int]string)
	for _, ch := range currentChars {
		if ch.CharID == charDeclNumber || ch.CharID == charCertNumber || ch.CharID == charCertBegin || ch.CharID == charCertEnd {
			existing[ch.CharID] = ch.Value
		}
	}

	// Skip if card already has a declaration or certificate — one is enough.
	if v := existing[charDeclNumber]; v != "" && v != "[]" {
		return nil
	}
	if v := existing[charCertNumber]; v != "" && v != "[]" {
		return nil
	}

	var changes []changeEntry
	targetID := kind.targetCharID()

	// Certificate/declaration number
	if c.OneCCertificateNum != "" {
		old := existing[targetID]
		if old == "" || old == "[]" {
			changes = append(changes, changeEntry{
				CharID: targetID,
				Old:    old,
				New:    c.OneCCertificateNum,
			})
		}
	}

	// Begin date (15001137) — shared for certs and decls, skip zero dates
	if c.OneCCertificateBegin != "" && !isZeroDate(c.OneCCertificateBegin) {
		old := existing[charCertBegin]
		if old == "" || old == "[]" {
			changes = append(changes, changeEntry{
				CharID: charCertBegin,
				Old:    old,
				New:    formatDateToDMY(c.OneCCertificateBegin),
			})
		}
	}

	// End date (15001138) — shared for certs and decls, skip zero dates
	if c.OneCCertificateEnd != "" && !isZeroDate(c.OneCCertificateEnd) {
		old := existing[charCertEnd]
		if old == "" || old == "[]" {
			changes = append(changes, changeEntry{
				CharID: charCertEnd,
				Old:    old,
				New:    formatDateToDMY(c.OneCCertificateEnd),
			})
		}
	}

	return changes
}

// buildReconcileChanges creates change entries for ANY discrepancies between WB card and 1C data.
// Handles: wrong number, type swap (cert↔decl), both.
// Returns nil if data already matches — card won't be staged.
func buildReconcileChanges(currentChars []CardChar, c stageRow, refTime time.Time) []changeEntry {
	kind := parseCertKind(c.OneCCertificate)
	if kind == certNone {
		return nil
	}
	if c.OneCCertificateEnd != "" && !isZeroDate(c.OneCCertificateEnd) && isExpired(c.OneCCertificateEnd, refTime) {
		return nil
	}

	existing := make(map[int]string)
	for _, ch := range currentChars {
		if ch.CharID == charDeclNumber || ch.CharID == charCertNumber || ch.CharID == charCertBegin || ch.CharID == charCertEnd {
			existing[ch.CharID] = ch.Value
		}
	}

	correctID := kind.targetCharID()
	wrongID := charCertNumber
	if correctID == charCertNumber {
		wrongID = charDeclNumber
	}

	var changes []changeEntry

	// Type swap: data is in the wrong char_id → remove it.
	wrongVal := normalizeValue(existing[wrongID])
	if wrongVal != "" {
		changes = append(changes, changeEntry{CharID: wrongID, Old: wrongVal, New: ""})
	}

	// Compare number in correctID with 1C.
	existingNum := normalizeValue(existing[correctID])
	oneCNum := strings.TrimSpace(c.OneCCertificateNum)
	if existingNum != oneCNum {
		changes = append(changes, changeEntry{CharID: correctID, Old: existingNum, New: oneCNum})
	}

	// Compare begin date.
	existingBegin := normalizeDate(existing[charCertBegin])
	oneCBegin := normalizeDate(c.OneCCertificateBegin)
	if oneCBegin != "" && existingBegin != oneCBegin {
		changes = append(changes, changeEntry{CharID: charCertBegin, Old: existingBegin, New: oneCBegin})
	}

	// Compare end date.
	existingEnd := normalizeDate(existing[charCertEnd])
	oneCEnd := normalizeDate(c.OneCCertificateEnd)
	if oneCEnd != "" && existingEnd != oneCEnd {
		changes = append(changes, changeEntry{CharID: charCertEnd, Old: existingEnd, New: oneCEnd})
	}

	return changes
}

// formatDateToDMY converts "2023-02-07T00:00:00" → "07.02.2006".
// Returns the input unchanged if parsing fails.
func formatDateToDMY(iso string) string {
	if iso == "" {
		return ""
	}
	parts := strings.SplitN(iso, "T", 2)
	dateParts := strings.Split(parts[0], "-")
	if len(dateParts) != 3 {
		return iso
	}
	return dateParts[2] + "." + dateParts[1] + "." + dateParts[0]
}

func isZeroDate(iso string) bool {
	return strings.HasPrefix(iso, "0001-01-01")
}

// normalizeValue unwraps JSON arrays from card_characteristics values.
// ["RU Д-CD000000"] → RU Д-CD000000, "" or "[]" → "", plain string → trimmed.
func normalizeValue(val string) string {
	val = strings.TrimSpace(val)
	if val == "" || val == "[]" {
		return ""
	}
	var arr []string
	if err := json.Unmarshal([]byte(val), &arr); err == nil && len(arr) > 0 {
		return strings.TrimSpace(arr[0])
	}
	return val
}

// normalizeDate returns a DMY string for comparison.
// Accepts ISO (2023-02-07T00:00:00), date-only (2023-02-07), or DMY (07.02.2023).
// Returns "" for empty/zero values.
func normalizeDate(val string) string {
	val = normalizeValue(val)
	if val == "" || strings.HasPrefix(val, "0001-01-01") {
		return ""
	}
	for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, val); err == nil {
			return t.Format("02.01.2006")
		}
	}
	if _, err := time.Parse("02.01.2006", val); err == nil {
		return val
	}
	return val
}

func printStageStats(ctx context.Context, db *sql.DB) {
	var total, withChanges int
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM fix_certificates_staging").Scan(&total)
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM fix_certificates_staging WHERE changes_json != '[]'").Scan(&withChanges)

	fmt.Printf("Staged %d cards:\n", total)
	fmt.Printf("  with certificate changes: %d\n", withChanges)

	// Distribution by subject.
	type subj struct {
		name  string
		count int
	}
	subjRows, err := db.QueryContext(ctx, `
		SELECT subject_name, COUNT(*) FROM fix_certificates_staging
		GROUP BY subject_name ORDER BY COUNT(*) DESC LIMIT 10
	`)
	if err != nil {
		return
	}
	defer subjRows.Close()
	fmt.Println("\n  By subject:")
	for subjRows.Next() {
		var s subj
		subjRows.Scan(&s.name, &s.count)
		fmt.Printf("    %-40s %d\n", s.name, s.count)
	}
}

// applyMemFilters applies in-memory filters on fields available in stageRow.
func applyMemFilters(rows []stageRow, f *filter.Filter) []stageRow {
	memF := filter.Filter{
		NmIDs:              f.NmIDs,
		VendorCodes:        f.VendorCodes,
		AllowedYears:       f.AllowedYears,
		ExcludeLengths:     f.ExcludeLengths,
		ExcludeVendorCodes: f.ExcludeVendorCodes,
		VendorCodePrefix:   f.VendorCodePrefix,
		SubjectName:        f.SubjectName,
	}
	if memF.Empty() {
		return rows
	}
	var result []stageRow
	for _, r := range rows {
		if memF.Matches(stageRowAdapter{row: r}, nil) {
			result = append(result, r)
		}
	}
	return result
}

// --- Local helpers (Rule 6: cmd/ cannot import cmd/) ---
func applySQLFilters(ctx context.Context, db *sql.DB, rows []stageRow, f *filter.Filter) ([]stageRow, error) {
	nmIDs := make([]int, len(rows))
	for i, r := range rows {
		nmIDs[i] = r.NmID
	}
	passSet, err := filterNmIDsBySQL(ctx, db, nmIDs, f)
	if err != nil {
		return nil, err
	}
	if passSet == nil {
		return rows, nil
	}
	var result []stageRow
	for _, r := range rows {
		if passSet[r.NmID] {
			result = append(result, r)
		}
	}
	return result, nil
}

// filterNmIDsBySQL runs SQL-based filters and returns a set of passing nmIDs.
// Returns nil if no SQL filters are active (caller should skip filtering).
func filterNmIDsBySQL(ctx context.Context, db *sql.DB, nmIDs []int, f *filter.Filter) (map[int]bool, error) {
	sqlF := filter.Filter{
		InStock:        f.InStock,
		SubjectIDs:     f.SubjectIDs,
		Seasons:        f.Seasons,
		OneCType:       f.OneCType,
		CategoryLevel1: f.CategoryLevel1,
		CategoryLevel2: f.CategoryLevel2,
		ActiveOnly:     f.ActiveOnly,
	}
	if sqlF.Empty() {
		return nil, nil
	}

	r, err := sqlF.BuildSQL(filter.SQLConfig{CardsAlias: "c"})
	if err != nil {
		return nil, fmt.Errorf("build sql filter: %w", err)
	}

	query := "SELECT DISTINCT c.nm_id FROM cards c"
	for _, join := range r.JOINs {
		query += " " + join
	}

	ph := make([]string, len(nmIDs))
	args := make([]any, len(nmIDs))
	for i, id := range nmIDs {
		ph[i] = "?"
		args[i] = id
	}
	if r.Where != "" {
		query += " WHERE c.nm_id IN (" + strings.Join(ph, ",") + ") AND " + r.Where
	} else {
		query += " WHERE c.nm_id IN (" + strings.Join(ph, ",") + ")"
	}
	args = append(args, r.Args...)

	sqlRows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("sql filter query: %w", err)
	}
	defer sqlRows.Close()

	passSet := make(map[int]bool)
	for sqlRows.Next() {
		var id int
		if err := sqlRows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan filter: %w", err)
		}
		passSet[id] = true
	}
	return passSet, sqlRows.Err()
}

func runDiff(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, `
		SELECT nm_id, vendor_code, changes_json, all_chars_json, onec_certificate
		FROM fix_certificates_staging
		WHERE status = 'new' AND changes_json != '[]'
		ORDER BY nm_id
	`)
	if err != nil {
		return fmt.Errorf("query staging: %w", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var nmID int
		var vc, changesJSON, allCharsJSON, onecCert string
		if err := rows.Scan(&nmID, &vc, &changesJSON, &allCharsJSON, &onecCert); err != nil {
			return fmt.Errorf("scan: %w", err)
		}

		var changes []changeEntry
		json.Unmarshal([]byte(changesJSON), &changes)
		var allChars []CardChar
		json.Unmarshal([]byte(allCharsJSON), &allChars)

		kind := parseCertKind(onecCert)
		kindLabel := "?"
		switch kind {
		case certDecl:
			kindLabel = "Декларация"
		case certType:
			kindLabel = "Сертификат"
		}

		fmt.Printf("\n--- nm_id=%d vendor_code=%s [%s] ---\n", nmID, vc, kindLabel)
		charNames := make(map[int]string)
		for _, c := range allChars {
			charNames[c.CharID] = c.Name
		}
		for _, ch := range changes {
			name := charNames[ch.CharID]
			if name == "" {
				name = fmt.Sprintf("char_%d", ch.CharID)
			}
			fmt.Printf("  %s (id=%d): %s → %s\n", name, ch.CharID, fmtVal(ch.Old), ch.New)
		}
		count++
	}

	fmt.Printf("\nTotal: %d cards with changes\n", count)
	return nil
}

func fmtVal(s string) string {
	if s == "" || s == "[]" {
		return "(empty)"
	}
	return s
}

// applyFixMemFilters applies in-memory filters on fixRow fields.
func applyFixMemFilters(rows []fixRow, f *filter.Filter) []fixRow {
	memF := filter.Filter{
		NmIDs:              f.NmIDs,
		VendorCodes:        f.VendorCodes,
		AllowedYears:       f.AllowedYears,
		ExcludeLengths:     f.ExcludeLengths,
		ExcludeVendorCodes: f.ExcludeVendorCodes,
		VendorCodePrefix:   f.VendorCodePrefix,
		SubjectName:        f.SubjectName,
	}
	if memF.Empty() {
		return rows
	}
	var result []fixRow
	for _, r := range rows {
		if memF.Matches(fixRowAdapter{row: r}, nil) {
			result = append(result, r)
		}
	}
	return result
}

// applyFixSQLFilters applies SQL filters for DB-dependent fields.
func applyFixSQLFilters(ctx context.Context, db *sql.DB, rows []fixRow, f *filter.Filter) ([]fixRow, error) {
	nmIDs := make([]int, len(rows))
	for i, r := range rows {
		nmIDs[i] = r.NmID
	}
	passSet, err := filterNmIDsBySQL(ctx, db, nmIDs, f)
	if err != nil {
		return nil, err
	}
	if passSet == nil {
		return rows, nil
	}
	var result []fixRow
	for _, r := range rows {
		if passSet[r.NmID] {
			result = append(result, r)
		}
	}
	return result, nil
}
