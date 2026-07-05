package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/cardupdate"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

const vatCharID = 15001405

// NDSRow — одна карточка с результатом сравнения НДС.
type NDSRow struct {
	NmID        int
	VendorCode  string
	Brand       string
	SubjectName string
	WBNDS       string // текущее значение WB (пустое если не задано)
	OneCNDS     int    // 10 или 22 из 1C
	Action      string // "SET" | "UPDATE" | "OK"
}

// Filters — фильтры CLI.
type Filters struct {
	Article string
	NmID    int
	NDS     int
}

// SyncResult — итог синхронизации.
type SyncResult struct {
	TotalCards    int
	MatchedOneC   int
	AlreadySynced int
	ToUpdate      int
	SetCount      int
	UpdateCount   int
	NoOneCData    int
	MixedWarnings []string
	Updated       int
	Errors        int
	Duration      time.Duration
}

// SyncClient — интерфейс для тестирования (без реальных API вызовов).
type SyncClient interface {
	UpdateCards(ctx context.Context, baseURL string, rateLimit, burst int, cards []wb.CardUpdateItem) (string, string, error)
}

// RunSync — основная функция синхронизации.
//
// apply=true  — отправить полные payload в WB API (безопасный rewrite через cardupdate).
// dryRun=true — построить полные payload и дампнуть их (без отправки); apply приоритетнее.
func RunSync(ctx context.Context, db *sql.DB, client SyncClient, cfg *Config, apply, dryRun bool, filters Filters) (*SyncResult, error) {
	start := time.Now()
	result := &SyncResult{}

	// 1. Проверить наличие данных
	if err := checkData(db); err != nil {
		return nil, err
	}

	// 2. Найти конфликты (микс НДС внутри одного артикула)
	mixed, err := findMixedNDS(db)
	if err != nil {
		return nil, fmt.Errorf("mixed NDS check: %w", err)
	}
	result.MixedWarnings = mixed

	// 3. Получить расхождения
	rows, err := getDifferences(db, filters)
	if err != nil {
		return nil, fmt.Errorf("get differences: %w", err)
	}

	// 4. Посчитать статистику
	result.TotalCards = len(rows)
	for _, r := range rows {
		result.MatchedOneC++
		switch r.Action {
		case "OK":
			result.AlreadySynced++
		case "SET":
			result.ToUpdate++
			result.SetCount++
		case "UPDATE":
			result.ToUpdate++
			result.UpdateCount++
		}
	}

	// 5. Cards without 1C match
	result.NoOneCData = countNoOneCMatch(db, filters)

	// 6. Вывести таблицу
	printTable(rows, result, apply, cfg)

	// 7. Применить если --apply, или задампить payload если --dry-run.
	if (apply || dryRun) && result.ToUpdate > 0 {
		toUpdate := filterAction(rows, "SET", "UPDATE")
		updated, errors, err := applyUpdates(ctx, db, client, toUpdate, cfg.Sync.BatchSize, apply && !dryRun)
		if err != nil {
			return nil, fmt.Errorf("apply updates: %w", err)
		}
		result.Updated = updated
		result.Errors = errors
	}

	result.Duration = time.Since(start)
	return result, nil
}

// checkData проверяет что таблицы не пустые.
func checkData(db *sql.DB) error {
	var cards, onecGoods, onecSKU int
	db.QueryRow("SELECT COUNT(*) FROM cards").Scan(&cards)
	db.QueryRow("SELECT COUNT(*) FROM onec_goods").Scan(&onecGoods)
	db.QueryRow("SELECT COUNT(*) FROM onec_goods_sku").Scan(&onecSKU)

	if cards == 0 {
		return fmt.Errorf("cards table is empty. Run download-wb-cards first")
	}
	if onecGoods == 0 || onecSKU == 0 {
		return fmt.Errorf("1C data is empty. Run download-1c-data first")
	}
	return nil
}

// findMixedNDS находит артикулы где разные SKU имеют разный НДС (исключая 0).
func findMixedNDS(db *sql.DB) ([]string, error) {
	query := `
		SELECT g.article, GROUP_CONCAT(DISTINCT s.nds), COUNT(DISTINCT s.nds) as distinct_nds
		FROM onec_goods_sku s
		JOIN onec_goods g ON g.guid = s.guid
		WHERE g.article != '' AND s.nds > 0
		GROUP BY g.article
		HAVING COUNT(DISTINCT s.nds) > 1
		ORDER BY g.article`

	rows, err := db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var warnings []string
	for rows.Next() {
		var article, ndsValues string
		var distinctNDS int
		if err := rows.Scan(&article, &ndsValues, &distinctNDS); err != nil {
			return warnings, err
		}
		warnings = append(warnings, fmt.Sprintf("article %s: mixed NDS values [%s] — using majority", article, ndsValues))
	}
	return warnings, nil
}

// getDifferences возвращает расхождения между 1C НДС и WB карточками.
func getDifferences(db *sql.DB, f Filters) ([]NDSRow, error) {
	args := []interface{}{}

	query := `
		SELECT
			c.nm_id,
			c.vendor_code,
			c.brand,
			c.subject_name,
			cc.json_value AS wb_nds_raw,
			onec.nds AS onec_nds
		FROM cards c
		JOIN (
			SELECT g.article,
				CASE
					WHEN SUM(CASE WHEN s.nds = 22 THEN 1 ELSE 0 END) >= SUM(CASE WHEN s.nds = 10 THEN 1 ELSE 0 END)
						THEN 22
					ELSE 10
				END as nds
			FROM onec_goods_sku s
			JOIN onec_goods g ON g.guid = s.guid
			WHERE g.article != '' AND s.nds > 0
			GROUP BY g.article
		) onec ON onec.article = c.vendor_code
		LEFT JOIN card_characteristics cc
			ON cc.nm_id = c.nm_id AND cc.char_id = ?
		WHERE c.vendor_code != ''`

	args = append(args, vatCharID)

	if f.Article != "" {
		query += " AND c.vendor_code = ?"
		args = append(args, f.Article)
	}
	if f.NmID > 0 {
		query += " AND c.nm_id = ?"
		args = append(args, f.NmID)
	}
	if f.NDS > 0 {
		query += " AND onec.nds = ?"
		args = append(args, f.NDS)
	}

	query += " ORDER BY c.vendor_code"

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []NDSRow
	for rows.Next() {
		var r NDSRow
		var wbRaw sql.NullString
		if err := rows.Scan(&r.NmID, &r.VendorCode, &r.Brand, &r.SubjectName, &wbRaw, &r.OneCNDS); err != nil {
			return nil, err
		}

		r.WBNDS = parseWBValue(wbRaw)
		expected := fmt.Sprintf("%d", r.OneCNDS)

		switch {
		case r.WBNDS == "" && r.OneCNDS > 0:
			r.Action = "SET"
		case r.WBNDS != expected:
			r.Action = "UPDATE"
		default:
			r.Action = "OK"
		}

		result = append(result, r)
	}
	return result, rows.Err()
}

// parseWBValue извлекает значение НДС из JSON массива ["10"] → "10".
func parseWBValue(raw sql.NullString) string {
	if !raw.Valid || raw.String == "" {
		return ""
	}
	var arr []string
	if err := json.Unmarshal([]byte(raw.String), &arr); err != nil {
		return raw.String // fallback: return raw
	}
	if len(arr) == 0 {
		return ""
	}
	return arr[0]
}

// countNoOneCMatch считает карточки без маппинга на 1C.
func countNoOneCMatch(db *sql.DB, f Filters) int {
	query := `
		SELECT COUNT(*)
		FROM cards c
		WHERE c.vendor_code NOT IN (
			SELECT DISTINCT g.article FROM onec_goods g WHERE g.article != ''
		) AND c.vendor_code != ''`

	args := []interface{}{}
	if f.Article != "" {
		query += " AND c.vendor_code = ?"
		args = append(args, f.Article)
	}
	if f.NmID > 0 {
		query += " AND c.nm_id = ?"
		args = append(args, f.NmID)
	}

	var count int
	db.QueryRow(query, args...).Scan(&count)
	return count
}

// filterAction фильтрует строки по action.
func filterAction(rows []NDSRow, actions ...string) []NDSRow {
	set := make(map[string]bool, len(actions))
	for _, a := range actions {
		set[a] = true
	}
	var result []NDSRow
	for _, r := range rows {
		if set[r.Action] {
			result = append(result, r)
		}
	}
	return result
}

// applyUpdates строит ПОЛНЫЕ payload обновлений и либо отправляет их в WB API
// (send=true), либо дампит как JSON (send=false, dry-run).
//
// Безопасный rewrite: каждая карточка грузится целиком через cardupdate.LoadFullCard,
// мутируется только характеристика НДС (vatCharID), и отправляется полный payload
// (vendorCode/brand/title/description/dimensions/characteristics/sizes). Частичный
// payload {NmID, Characteristics} обнулил бы карточку — WB делает полную замену.
func applyUpdates(ctx context.Context, db *sql.DB, client SyncClient, rows []NDSRow, batchSize int, send bool) (int, int, error) {
	total := len(rows)
	updated := 0
	errors := 0
	updater := cardupdate.NewCardUpdater(db)

	for i := 0; i < total; i += batchSize {
		end := i + batchSize
		if end > total {
			end = total
		}
		batch := rows[i:end]

		items := make([]wb.CardUpdateItem, 0, len(batch))
		for _, r := range batch {
			item, err := buildNDSPayload(ctx, updater, r)
			if err != nil {
				log.Printf("  ERROR build payload nm_id=%d: %v", r.NmID, err)
				errors++
				continue
			}
			items = append(items, item)
		}
		if len(items) == 0 {
			continue
		}

		if !send {
			payload, _ := json.MarshalIndent(items, "", "  ")
			fmt.Printf("\n--- Batch %d-%d/%d (%d cards) ---\n%s\n", i+1, end, total, len(items), payload)
			updated += len(items)
			continue
		}

		fmt.Printf("  Batch %d-%d/%d... ", i+1, end, total)
		_, errorText, err := client.UpdateCards(ctx, wb.CardsBaseURL, 10, 5, items)
		if err != nil {
			errors += len(items)
			fmt.Printf("ERROR: %s\n", err)
			if errorText != "" {
				log.Printf("  WB API error: %s", errorText)
			}
			continue
		}
		updated += len(items)
		fmt.Println("OK")
	}

	if !send {
		fmt.Printf("\nDry-run: %d payloads dumped, %d errors. Use --apply to send.\n", updated, errors)
	}
	return updated, errors, nil
}

// buildNDSPayload строит ПОЛНЫЙ CardUpdateItem с одной мутированной характеристикой —
// НДС (char_id vatCharID). Инвариант безопасного rewrite:
// LoadFullCard (все поля) → ToUpdateItem (полный payload) → мутация только НДС.
func buildNDSPayload(ctx context.Context, u *cardupdate.CardUpdater, r NDSRow) (wb.CardUpdateItem, error) {
	card, err := u.LoadFullCard(ctx, r.NmID)
	if err != nil {
		return wb.CardUpdateItem{}, fmt.Errorf("load full card: %w", err)
	}
	item := cardupdate.ToUpdateItem(card)
	newVal := fmt.Sprintf("%d", r.OneCNDS)
	for i, c := range item.Characteristics {
		if c.ID == vatCharID {
			item.Characteristics[i].Value = newVal
			return item, nil
		}
	}
	item.Characteristics = append(item.Characteristics, wb.CardUpdateCharc{ID: vatCharID, Value: newVal})
	return item, nil
}

// printTable выводит таблицу расхождений.
func printTable(rows []NDSRow, result *SyncResult, apply bool, cfg *Config) {
	mode := "DRY-RUN"
	if apply {
		mode = "APPLY"
	}

	fmt.Println()
	fmt.Println(strings.Repeat("=", 60))
	fmt.Printf("WB Cards VAT Sync (%s)\n", mode)
	fmt.Println(strings.Repeat("=", 60))
	fmt.Printf("Database:  %s\n", cfg.Sync.DbPath)
	fmt.Printf("BatchSize: %d\n", cfg.Sync.BatchSize)

	filters := []string{}
	if cfg.Filters.Article != "" {
		filters = append(filters, fmt.Sprintf("article=%s", cfg.Filters.Article))
	}
	if cfg.Filters.NmID > 0 {
		filters = append(filters, fmt.Sprintf("nm_id=%d", cfg.Filters.NmID))
	}
	if cfg.Filters.NDS > 0 {
		filters = append(filters, fmt.Sprintf("nds=%d", cfg.Filters.NDS))
	}
	if len(filters) > 0 {
		fmt.Printf("Filters:   %s\n", strings.Join(filters, ", "))
	} else {
		fmt.Println("Filters:   none")
	}
	fmt.Println(strings.Repeat("=", 60))

	// Count by action
	toUpdate := filterAction(rows, "SET", "UPDATE")

	fmt.Printf("\nScanning cards...\n")
	fmt.Printf("  Cards matched to 1C:   %d\n", result.MatchedOneC)
	fmt.Printf("  Cards with no 1C:      %d\n", result.NoOneCData)
	fmt.Printf("  Cards total in DB:     %d\n", result.MatchedOneC+result.NoOneCData)

	if len(toUpdate) == 0 {
		fmt.Println("\n  All matched cards are already synced!")
		return
	}

	fmt.Printf("\nNDS Discrepancies (%d cards):\n", len(toUpdate))
	fmt.Println(strings.Repeat("=", 100))
	fmt.Printf("%-12s | %-12s | %-20s | %-20s | %-6s | %-4s | %s\n",
		"NM_ID", "ARTICLE", "BRAND", "SUBJECT", "WB", "1C", "ACTION")
	fmt.Println(strings.Repeat("=", 100))

	for _, r := range toUpdate {
		brand := r.Brand
		if len(brand) > 20 {
			brand = brand[:17] + "..."
		}
		subject := r.SubjectName
		if len(subject) > 20 {
			subject = subject[:17] + "..."
		}
		wbVal := r.WBNDS
		if wbVal == "" {
			wbVal = "(none)"
		}
		fmt.Printf("%-12d | %-12s | %-20s | %-20s | %-6s | %-4d | %s\n",
			r.NmID, r.VendorCode, brand, subject, wbVal, r.OneCNDS, r.Action)
	}

	fmt.Println(strings.Repeat("=", 100))
}

// printSummary выводит итоговую статистику.
func printSummary(result *SyncResult, apply bool) {
	fmt.Println()
	fmt.Println(strings.Repeat("=", 60))
	fmt.Println("Summary:")
	fmt.Printf("  Already synced:  %d\n", result.AlreadySynced)
	fmt.Printf("  To update:       %d  (SET: %d, UPDATE: %d)\n",
		result.ToUpdate, result.SetCount, result.UpdateCount)
	fmt.Printf("  No 1C data:      %d\n", result.NoOneCData)
	fmt.Printf("  Mixed NDS:       %d\n", len(result.MixedWarnings))

	if apply {
		fmt.Printf("  Updated:         %d\n", result.Updated)
		fmt.Printf("  Errors:          %d\n", result.Errors)
	}
	fmt.Printf("  Duration:        %s\n", result.Duration.Round(time.Millisecond))

	if !apply && result.ToUpdate > 0 {
		fmt.Println("\nDRY-RUN: No changes were made. Use --apply to update.")
	}
	fmt.Println(strings.Repeat("=", 60))
}
