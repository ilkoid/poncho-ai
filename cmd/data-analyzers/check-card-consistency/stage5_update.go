package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/progress"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// runStage5 обновляет карточки через WB API или моковый прогон (этап 5).
func runStage5(ctx context.Context, source *SourceRepo, results *ResultsRepo, cfg CLIConfig, mock bool, force bool) error {
	rows, err := results.LoadAnalysisForUpdate(ctx, cfg.Filter, force)
	if err != nil {
		return fmt.Errorf("load analysis for update: %w", err)
	}
	if len(rows) == 0 {
		log.Println("Stage 5: no cards with new params. Run stage 4 first.")
		return nil
	}

	if cfg.Analysis.Limit > 0 && len(rows) > cfg.Analysis.Limit {
		log.Printf("  Limit: %d → %d cards", len(rows), cfg.Analysis.Limit)
		rows = rows[:cfg.Analysis.Limit]
	}

	mode := "MOCK→SANDBOX"
	if !mock {
		mode = "REAL→PROD"
	}
	log.Printf("Stage 5 (%s): %d cards to update", mode, len(rows))

	// Warn about subject changes (can't be fixed via update API)
	for _, row := range rows {
		if row.NewSubjectID != nil && *row.NewSubjectID != row.SubjectID {
			log.Printf("  WARN nm_id=%d: subject changed %d→%d (%q→%q) — WB update API cannot change subject",
				row.NmID, row.SubjectID, *row.NewSubjectID, row.SubjectName, row.NewSubjectName)
		}
	}

	// Load max_count map from char_dict for sanitizer
	var maxCounts map[int]int
	charDictPath := expandHome(cfg.CharDict.DBPath)
	charDict, err := NewCharDictRepo(charDictPath)
	if err != nil {
		log.Printf("  WARN: open char_dict (%s): %v — sanitizer disabled", charDictPath, err)
	} else {
		defer charDict.Close()
		maxCounts = loadMaxCountMap(ctx, charDict, rows)
		log.Printf("  Loaded %d max_count rules from char_dict", len(maxCounts))
	}

	tracker := progress.NewCLITrackerWithConfig(progress.CLITrackerConfig{
		Total:  len(rows),
		Prefix: "Stage 5",
		Width:  -1,
	})
	defer tracker.Done()

	if mock {
		return stage5Mock(ctx, source, results, rows, tracker, cfg, maxCounts)
	}
	return stage5Real(ctx, source, results, rows, tracker, cfg, maxCounts)
}

// stage5Real — реальное обновление через WB API (production) с батчами и rate limiting.
func stage5Real(ctx context.Context, source *SourceRepo, results *ResultsRepo, rows []AnalysisRow, tracker *progress.CLITracker, cfg CLIConfig, maxCounts map[int]int) error {
	apiKey := getWBApiKey(cfg.WBUpdate.APIKey)
	if apiKey == "" {
		return fmt.Errorf("WB_API_KEY (or WB_API_ANALYTICS_AND_PROMO_KEY) not set")
	}

	wbClient := wb.New(apiKey)
	wbClient.SetRateLimit("cards_content",
		cfg.WBUpdate.RatePerMin, cfg.WBUpdate.RateBurst,
		cfg.WBUpdate.APIFloorPerMin, cfg.WBUpdate.APIFloorBurst)
	wbClient.SetAdaptiveParams(0, cfg.WBUpdate.AdaptiveProbeAfter, cfg.WBUpdate.MaxBackoffSeconds)

	batchSize := cfg.WBUpdate.BatchSize
	total := len(rows)
	var updated int

	for i := 0; i < total; i += batchSize {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		end := i + batchSize
		if end > total {
			end = total
		}
		batch := rows[i:end]

		// Build update requests for this batch
		items := make([]wb.CardUpdateItem, 0, len(batch))
		var batchNmIDs []int
		for _, row := range batch {
			req, err := buildUpdateRequest(ctx, row, source, maxCounts)
			if err != nil {
				log.Printf("  ERROR build request nm_id=%d: %v", row.NmID, err)
				tracker.Update(1)
				continue
			}
			items = append(items, req)
			batchNmIDs = append(batchNmIDs, row.NmID)
		}

		if len(items) == 0 {
			continue
		}

		reqJSON, _ := json.Marshal(items)
		log.Printf("  Batch %d-%d/%d (%d cards, %d bytes)...", i+1, end, total, len(items), len(reqJSON))

		respBody, errText, err := wbClient.UpdateCards(ctx, wb.CardsBaseURL,
			cfg.WBUpdate.RatePerMin, cfg.WBUpdate.RateBurst, items)
		log.Printf("  WBREPLY: %q", respBody)
		if err != nil {
			tracker.Update(len(items))
			return fmt.Errorf("WB API rejected batch %d-%d: %s (%w). Stop to investigate", i+1, end, errText, err)
		}

		// Проверяем асинхронные ошибки валидации WB (non-fatal — список кумулятивный, старые ошибки могут висеть)
		if err := checkWBErrors(ctx, wbClient, wb.CardsBaseURL, cfg, batch); err != nil {
			log.Printf("  WARN: %v", err)
		}

		// Log changes and mark updated
		for _, row := range batch {
			logCardChanges(ctx, results, row, source)
		}

		resp := fmt.Sprintf("Updated at %s (batch %d-%d)", time.Now().Format(time.DateTime), i+1, end)
		if err := results.SaveWBUpdateBatch(ctx, batchNmIDs, resp); err != nil {
			log.Printf("  ERROR save batch: %v", err)
		}

		updated += len(items)
		tracker.Update(len(items))
		log.Printf("  Batch OK: %d/%d updated", updated, total)
	}

	log.Printf("Stage 5 (REAL→PROD) complete: %d/%d updated", updated, total)
	return nil
}

// stage5Mock — отправка в песочницу WB (sandbox) через WB_API_TEST.
// Ошибки sandbox — non-fatal, логируются и не прерывают обработку.
func stage5Mock(ctx context.Context, source *SourceRepo, results *ResultsRepo, rows []AnalysisRow, tracker *progress.CLITracker, cfg CLIConfig, maxCounts map[int]int) error {
	apiKey := os.Getenv("WB_API_TEST")

	var wbClient *wb.Client
	if apiKey != "" {
		wbClient = wb.New(apiKey)
		wbClient.SetRateLimit("cards_content", 5, 2, 5, 2)
		wbClient.SetAdaptiveParams(0, 10, 60)
	}

	var updated, errors int
	for _, row := range rows {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		log.Printf("  MOCK: nm_id=%d vendor_code=%s", row.NmID, row.VendorCode)
		logCardChanges(ctx, results, row, source)

		if wbClient == nil {
			log.Printf("    SKIP sandbox: WB_API_TEST not set")
			tracker.Update(1)
			continue
		}

		ok, err := stage5MockCard(ctx, wbClient, source, results, row, maxCounts)
		if err != nil {
			log.Printf("    ERROR mock nm_id=%d: %v", row.NmID, err)
			errors++
		} else if ok {
			updated++
			if wbClient != nil {
				if checkErr := checkWBErrors(ctx, wbClient, wb.CardsSandboxURL, cfg, []AnalysisRow{row}); checkErr != nil {
						log.Printf("    WARN: %v", checkErr)
					}
			}
		}
		tracker.Update(1)
	}

	log.Printf("Stage 5 (MOCK→SANDBOX) complete: %d/%d updated, %d errors", updated, len(rows), errors)
	return nil
}

// stage5MockCard — sandbox-логика для одной карточки (search → update/create).
func stage5MockCard(ctx context.Context, client *wb.Client, source *SourceRepo, results *ResultsRepo, row AnalysisRow, maxCounts map[int]int) (bool, error) {
	updateReq, err := buildUpdateRequest(ctx, row, source, maxCounts)
	if err != nil {
		return false, fmt.Errorf("build update request: %w", err)
	}

	// 1. Search for card in sandbox by vendor_code
	products, _ := client.GetProductsByArticles(ctx, "cards_content", wb.CardsSandboxURL, 5, 1, []string{row.VendorCode})

	if len(products) > 0 {
		// Card exists in sandbox — UPDATE with sandbox nmID
		sandboxNmID := products[0].NmID
		log.Printf("    Found in sandbox: nmID=%d (prod nmID=%d)", sandboxNmID, row.NmID)
		updateReq.NmID = sandboxNmID
		if j, err := json.MarshalIndent(updateReq, "    ", "  "); err == nil {
			log.Printf("    UPDATE payload:\n%s", string(j))
		}
		_, errText, err := client.UpdateCards(ctx, wb.CardsSandboxURL, 5, 2, []wb.CardUpdateItem{updateReq})
		if err != nil {
			log.Printf("    SANDBOX UPDATE ERROR nmID=%d: %s (%v)", sandboxNmID, errText, err)
			return false, nil
		}
		log.Printf("    SANDBOX UPDATE OK: nmID=%d vendor_code=%s", sandboxNmID, row.VendorCode)
		return true, nil
	}

	// 2. Card not in sandbox — CREATE
	log.Printf("    Not found in sandbox, trying CREATE subject_id=%d", row.SubjectID)
	if row.SubjectID == 0 {
		log.Printf("    SKIP CREATE: subject_id is 0")
		return false, nil
	}

	createReq := buildCreateRequest(row, updateReq)
	if j, err := json.MarshalIndent(createReq, "    ", "  "); err == nil {
		log.Printf("    CREATE payload:\n%s", string(j))
	}
	errText, err := client.CreateCards(ctx, wb.CardsSandboxURL, 5, 2, []wb.CardCreateGroup{createReq})
	if err != nil {
		log.Printf("    SANDBOX CREATE ERROR: %s (%v)", errText, err)
		return false, nil
	}
	log.Printf("    SANDBOX CREATE OK: vendor_code=%s — waiting for nmID...", row.VendorCode)

	// Retry loop: wait for sandbox sync (5s, 10s, 15s)
	var sandboxNmID int
	for attempt := 1; attempt <= 3; attempt++ {
		time.Sleep(time.Duration(attempt*5) * time.Second)
		products2, _ := client.GetProductsByArticles(ctx, "cards_content", wb.CardsSandboxURL, 5, 1, []string{row.VendorCode})
		if len(products2) > 0 {
			sandboxNmID = products2[0].NmID
			break
		}
		log.Printf("    Retry %d/3: card not synced yet", attempt)
	}
	if sandboxNmID == 0 {
		log.Printf("    SANDBOX: card not synced after 3 attempts, UPDATE not tested (retry later)")
		return false, nil
	}
	log.Printf("    Found after CREATE: sandbox nmID=%d, trying UPDATE", sandboxNmID)
	updateReq.NmID = sandboxNmID
	if j, err := json.MarshalIndent(updateReq, "    ", "  "); err == nil {
		log.Printf("    UPDATE-RETRY payload:\n%s", string(j))
	}
	_, errText, err = client.UpdateCards(ctx, wb.CardsSandboxURL, 5, 2, []wb.CardUpdateItem{updateReq})
	if err != nil {
		log.Printf("    SANDBOX UPDATE ERROR nmID=%d: %s (%v)", sandboxNmID, errText, err)
		return false, nil
	}
	log.Printf("    SANDBOX UPDATE OK: nmID=%d vendor_code=%s", sandboxNmID, row.VendorCode)
	return true, nil
}

// logCardChanges записывает детальный diff изменений в card_change_log.
func logCardChanges(ctx context.Context, results *ResultsRepo, row AnalysisRow, source *SourceRepo) {
	// Title change
	if row.NewTitle != "" && row.NewTitle != row.Title {
		if err := results.LogChange(ctx, row.NmID, row.VendorCode, "title", row.Title, row.NewTitle); err != nil {
			log.Printf("    WARN log title nm_id=%d: %v", row.NmID, err)
		}
		log.Printf("    title: %q → %q", truncate(row.Title, 50), truncate(row.NewTitle, 50))
	}

	// Description change
	if row.NewDescription != "" {
		if err := results.LogChange(ctx, row.NmID, row.VendorCode, "description", "(current)", truncate(row.NewDescription, 80)); err != nil {
			log.Printf("    WARN log description nm_id=%d: %v", row.NmID, err)
		}
		log.Printf("    description: updated (%d chars)", len(row.NewDescription))
	}

	// Characteristics change — per-characteristic diff
	if row.NewCharacteristics != "" {
		var newChars []charcEntry
		if err := json.Unmarshal([]byte(row.NewCharacteristics), &newChars); err != nil {
			results.LogChange(ctx, row.NmID, row.VendorCode, "characteristics", "(current)", truncate(row.NewCharacteristics, 80))
			log.Printf("    characteristics: updated (bulk log)")
			return
		}

		currentCharsMap, err := source.LoadCharacteristics(ctx, []int{row.NmID})
		if err != nil {
			results.LogChange(ctx, row.NmID, row.VendorCode, "characteristics", "(load failed)", truncate(row.NewCharacteristics, 80))
			log.Printf("    characteristics: updated (load failed)")
			return
		}
		currentChars := currentCharsMap[row.NmID]

		// Index current chars by charc_id
		currentIdx := make(map[int]string)
		currentNames := make(map[int]string)
		for _, c := range currentChars {
			currentIdx[c.CharID] = unpackCharValue(c.Value)
			currentNames[c.CharID] = c.Name
		}

		for _, nc := range newChars {
			oldVal := currentIdx[nc.CharcID]
			if oldVal == "" {
				oldVal = "(пусто)"
			}
			name := nc.Name
			if name == "" {
				name = currentNames[nc.CharcID]
			}
			if name == "" {
				name = fmt.Sprintf("charc_%d", nc.CharcID)
			}
			results.LogChange(ctx, row.NmID, row.VendorCode,
				"charc:"+name, oldVal, nc.Value)
		}
		log.Printf("    characteristics: %d fields updated", len(newChars))
	}
}

// buildUpdateRequest формирует тело запроса для WB API update со Smart Merge.
func buildUpdateRequest(ctx context.Context, row AnalysisRow, source *SourceRepo, maxCounts map[int]int) (wb.CardUpdateItem, error) {
	req := wb.CardUpdateItem{
		NmID:       row.NmID,
		VendorCode: row.VendorCode,
	}

	// WB API полностью перезаписывает title/description — всегда отправляем текущие значения
	title, desc, err := source.LoadTitleDescription(ctx, row.NmID)
	if err != nil {
		return req, fmt.Errorf("load title/description: %w", err)
	}
	req.Title = row.NewTitle
	if req.Title == "" {
		req.Title = title
	}
	req.Description = row.NewDescription
	if req.Description == "" {
		req.Description = desc
	}

	if row.NewCharacteristics != "" {
		var generatedChars []charcEntry
		if err := json.Unmarshal([]byte(row.NewCharacteristics), &generatedChars); err != nil {
			return req, fmt.Errorf("unmarshal new characteristics: %w", err)
		}

		generatedMap := make(map[int]charcEntry)
		for _, c := range generatedChars {
			generatedMap[c.CharcID] = c
		}

		// Загружаем ТЕКУЩИЕ характеристики из сырой базы
		currentCharsMap, err := source.LoadCharacteristics(ctx, []int{row.NmID})
		if err != nil {
			return req, fmt.Errorf("load current characteristics: %w", err)
		}
		currentChars := currentCharsMap[row.NmID]

		var finalChars []wb.CardUpdateCharc
		seenIDs := make(map[int]bool)

		// 1. Smart Merge: проходим по текущим характеристикам
		for _, curr := range currentChars {
			var val interface{}
			if err := json.Unmarshal([]byte(curr.Value), &val); err != nil {
				val = curr.Value
			}
			val = unwrapValue(val)

			if skipCharcIDs[curr.CharID] {
				finalChars = append(finalChars, wb.CardUpdateCharc{ID: curr.CharID, Value: val})
				seenIDs[curr.CharID] = true
			} else if gen, exists := generatedMap[curr.CharID]; exists {
				convertedValue := convertCharValue(gen.Value, curr.Value)
				finalChars = append(finalChars, wb.CardUpdateCharc{ID: gen.CharcID, Value: convertedValue})
				seenIDs[gen.CharcID] = true
			} else {
				finalChars = append(finalChars, wb.CardUpdateCharc{ID: curr.CharID, Value: val})
				seenIDs[curr.CharID] = true
			}
		}

		// 2. Добавляем новые поля, которых раньше не было (default: array)
		for _, gen := range generatedChars {
			if !seenIDs[gen.CharcID] {
				finalChars = append(finalChars, wb.CardUpdateCharc{ID: gen.CharcID, Value: stringToCharArray(gen.Value)})
			}
		}

		finalChars = sanitizeCharacteristics(finalChars, maxCounts)
			req.Characteristics = finalChars
	}

	// Загружаем текущие размеры из сырой базы (WB требует sizes в update)
	sizesMap, err := source.LoadSizes(ctx, []int{row.NmID})
	if err != nil {
		return req, fmt.Errorf("load sizes: %w", err)
	}
	if sizes, ok := sizesMap[row.NmID]; ok {
		req.Sizes = sizes
	}

	return req, nil
}

func getWBApiKey(preferred string) string {
	if preferred != "" {
		return preferred
	}
	if val := strings.TrimSpace(os.Getenv("WB_API_KEY")); val != "" {
		return val
	}
	if val := strings.TrimSpace(os.Getenv("WB_API_ANALYTICS_AND_PROMO_KEY")); val != "" {
		return val
	}
	return ""
}

// buildCreateRequest конвертирует update-запрос в create-запрос для sandbox.
func buildCreateRequest(row AnalysisRow, updateReq wb.CardUpdateItem) wb.CardCreateGroup {
	return wb.CardCreateGroup{
		SubjectID: row.SubjectID,
		Variants: []wb.CardCreateVariant{{
			VendorCode:      updateReq.VendorCode,
			Title:           updateReq.Title,
			Description:     updateReq.Description,
			Characteristics: updateReq.Characteristics,
		}},
	}
}

// unwrapValue разворачивает [3] → 3 для числовых полей, хранимых в source DB как массивы.
func unwrapValue(val interface{}) interface{} {
	arr, ok := val.([]interface{})
	if !ok || len(arr) != 1 {
		return val
	}
	if n, ok := arr[0].(float64); ok {
		if n == float64(int(n)) {
			return int(n) // [3.0] → 3
		}
		return n // [2.5] → 2.5
	}
	return val
}

// convertCharValue конвертирует строковое значение LLM в формат, ожидаемый WB API.
// Определяет формат по текущему значению характеристики: array, number или string.
func convertCharValue(generated string, currentJSON string) interface{} {
	var current interface{}
	if err := json.Unmarshal([]byte(currentJSON), &current); err != nil {
		return stringToCharArray(generated)
	}

	switch unwrapped := unwrapValue(current).(type) {
	case int, float64:
		if n, err := strconv.Atoi(generated); err == nil {
			return n
		}
		if f, err := strconv.ParseFloat(generated, 64); err == nil {
			return f
		}
		return generated
	case []interface{}:
		return stringToCharArray(generated)
	default:
		_ = unwrapped
		return generated
	}
}

// stringToCharArray разбивает строку по запятой в массив строк для WB API.
func stringToCharArray(s string) []string {
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	if len(result) == 0 {
		return []string{s}
	}
	return result
}

// sanitizeCharacteristics обрезает массивы значений до max_count из справочника WB.
// ЛLM иногда генерирует несколько значений для полей, где WB разрешает только одно.
func sanitizeCharacteristics(chars []wb.CardUpdateCharc, maxCounts map[int]int) []wb.CardUpdateCharc {
	if len(maxCounts) == 0 {
		return chars
	}
	for i, c := range chars {
		maxCount := maxCounts[c.ID]
		if maxCount <= 0 {
			continue
		}
		arr, ok := c.Value.([]string)
		if !ok || len(arr) <= maxCount {
			continue
		}
		log.Printf("    SANITIZE: charc_id=%d has %d values, max=%d — truncating to first %d",
			c.ID, len(arr), maxCount, maxCount)
		chars[i].Value = arr[:maxCount]
	}
	return chars
}

// loadMaxCountMap загружает мапу charc_id→max_count из char_dict для всех subject_id из rows.
func loadMaxCountMap(ctx context.Context, charDict *CharDictRepo, rows []AnalysisRow) map[int]int {
	subjectIDs := make(map[int]bool)
	for _, row := range rows {
		sid := row.SubjectID
		if row.NewSubjectID != nil && *row.NewSubjectID != 0 {
			sid = *row.NewSubjectID
		}
		subjectIDs[sid] = true
	}

	result := make(map[int]int)
	for sid := range subjectIDs {
		entries, err := charDict.LoadCharacteristicsForSubject(ctx, sid)
		if err != nil {
			log.Printf("  WARN: load char entries for subject_id=%d: %v", sid, err)
			continue
		}
		for _, e := range entries {
			if e.MaxCount > 0 {
				result[e.CharcID] = e.MaxCount
			}
		}
	}
	return result
}

// wbErrorReport — структура для записи ошибок WB в JSON-файл.
type wbErrorReport struct {
	Timestamp   string              `json:"timestamp"`
	BatchNmIDs  []int               `json:"batch_nm_ids"`
	VendorCodes []string            `json:"vendor_codes"`
	WBErrors    map[string][]string `json:"wb_errors"`
	RawResponse []wb.CardErrorItem  `json:"raw_response"`
}

// checkWBErrors проверяет ошибки валидации WB после обновления карточек.
// Вызывает /content/v2/cards/error/list и ищет vendor_codes из текущего батча.
// При обнаружении ошибок — записывает в файл, выводит на экран, возвращает error.
func checkWBErrors(ctx context.Context, client *wb.Client, baseURL string, cfg CLIConfig, batch []AnalysisRow) error {
	items, err := client.GetCardErrorsList(ctx, baseURL,
		cfg.WBUpdate.RatePerMin, cfg.WBUpdate.RateBurst,
		wb.CardErrorsListRequest{
			Cursor: &wb.CardErrorsCursor{Limit: 100},
			Order:  &wb.CardErrorsOrder{Ascending: false},
		})
	if err != nil {
		log.Printf("  WARN: error list query failed: %v (non-fatal)", err)
		return nil
	}

	// Build vendor_code set from batch
	batchVC := make(map[string]int)
	for _, row := range batch {
		batchVC[row.VendorCode] = row.NmID
	}

	// Search for errors matching our vendor_codes
	var foundErrors map[string][]string
	var rawItems []wb.CardErrorItem
	for _, item := range items {
		matched := false
		for vc, errs := range item.Errors {
			if _, ok := batchVC[vc]; ok {
				if foundErrors == nil {
					foundErrors = make(map[string][]string)
				}
				foundErrors[vc] = append(foundErrors[vc], errs...)
				matched = true
			}
		}
		if matched {
			rawItems = append(rawItems, item)
		}
	}

	if len(foundErrors) == 0 {
		log.Printf("  WBREPLY: no errors found in error list")
		return nil
	}

	// Build error report
	nmIDs := make([]int, 0, len(foundErrors))
	vcs := make([]string, 0, len(foundErrors))
	for vc := range foundErrors {
		nmIDs = append(nmIDs, batchVC[vc])
		vcs = append(vcs, vc)
	}

	report := wbErrorReport{
		Timestamp:   time.Now().Format(time.RFC3339),
		BatchNmIDs:  nmIDs,
		VendorCodes: vcs,
		WBErrors:    foundErrors,
		RawResponse: rawItems,
	}

	// Write to file
	filename := fmt.Sprintf("wb-errors-%s.json", time.Now().Format("2006-01-02_150405"))
	data, _ := json.MarshalIndent(report, "", "  ")
	if writeErr := os.WriteFile(filename, data, 0644); writeErr != nil {
		log.Printf("  ERROR: failed to write error report: %v", writeErr)
	} else {
		log.Printf("  Error report saved: %s", filename)
	}

	// Print to screen
	for vc, errs := range foundErrors {
		nmID := batchVC[vc]
		for _, e := range errs {
			log.Printf("  WB ERROR vendor_code=%s nm_id=%d: %q", vc, nmID, e)
		}
	}

	return fmt.Errorf("WB validation errors found for %d vendor_codes — pipeline stopped. See %s", len(foundErrors), filename)
}

// runStage5Check — standalone проверка error list без обновления карточек.
// Вызывает /content/v2/cards/error/list и выводит все найденные ошибки.
func runStage5Check(ctx context.Context, cfg CLIConfig) error {
	apiKey := getWBApiKey(cfg.WBUpdate.APIKey)
	if apiKey == "" {
		return fmt.Errorf("WB_API_KEY (or WB_API_CONTENT_KEY) not set")
	}

	wbClient := wb.New(apiKey)
	wbClient.SetRateLimit("cards_content",
		cfg.WBUpdate.RatePerMin, cfg.WBUpdate.RateBurst,
		cfg.WBUpdate.APIFloorPerMin, cfg.WBUpdate.APIFloorBurst)
	wbClient.SetAdaptiveParams(0, cfg.WBUpdate.AdaptiveProbeAfter, cfg.WBUpdate.MaxBackoffSeconds)

	log.Println("Stage 5 (--check): querying WB error list...")

	items, err := wbClient.GetCardErrorsList(ctx, wb.CardsBaseURL,
		cfg.WBUpdate.RatePerMin, cfg.WBUpdate.RateBurst,
		wb.CardErrorsListRequest{
			Cursor: &wb.CardErrorsCursor{Limit: 100},
			Order:  &wb.CardErrorsOrder{Ascending: false},
		})
	if err != nil {
		return fmt.Errorf("get card errors: %w", err)
	}

	if len(items) == 0 {
		log.Println("  No errors found in WB error list.")
		return nil
	}

	totalErrors := 0
	for i, item := range items {
		fmt.Printf("\n── Batch %d (UUID: %s) ──\n", i+1, item.BatchUUID)
		fmt.Printf("  Vendor codes: %v\n", item.VendorCodes)
		for vc, sub := range item.Subjects {
			fmt.Printf("  Subject: %s → %s (id=%d)\n", vc, sub.Name, sub.ID)
		}
		for vc, errs := range item.Errors {
			for _, e := range errs {
				fmt.Printf("  ERROR vendor_code=%s: %s\n", vc, e)
				totalErrors++
			}
		}
	}

	fmt.Printf("\nTotal: %d error batches, %d individual errors\n", len(items), totalErrors)

	// Save full report to file
	filename := fmt.Sprintf("wb-errors-%s.json", time.Now().Format("2006-01-02_150405"))
	report := wbErrorReport{
		Timestamp:   time.Now().Format(time.RFC3339),
		RawResponse: items,
	}
	// Collect all vendor codes and errors from all items
	allVCs := make(map[string]bool)
	allErrors := make(map[string][]string)
	for _, item := range items {
		for _, vc := range item.VendorCodes {
			allVCs[vc] = true
		}
		for vc, errs := range item.Errors {
			allErrors[vc] = append(allErrors[vc], errs...)
		}
	}
	for vc := range allVCs {
		report.VendorCodes = append(report.VendorCodes, vc)
	}
	report.WBErrors = allErrors

	data, _ := json.MarshalIndent(report, "", "  ")
	if err := os.WriteFile(filename, data, 0644); err != nil {
		log.Printf("  WARN: failed to write report: %v", err)
	} else {
		log.Printf("Full report saved: %s", filename)
	}

	if totalErrors > 0 {
		return fmt.Errorf("WB error list contains %d errors", totalErrors)
	}
	return nil
}

