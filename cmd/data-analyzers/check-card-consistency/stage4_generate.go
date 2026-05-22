package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/llm"
	"github.com/ilkoid/poncho-ai/pkg/progress"
)

// skipCharcIDs — WB системные поля, которые LLM не должна заполнять.
// Платформенные характеристики (Бренд, SKU, Описание, ИКПУ, упаковка и т.д.),
// присутствующие во всех предметах WB. Заполняются отдельно или автоматически.
var skipCharcIDs = map[int]bool{
	14177452: true, // Описание
	14177453: true, // SKU
	14177446: true, // Бренд
	14177456: true, // Рос. размер
	54337:    true, // Размер
	88952:    true, // Вес товара с упаковкой (г)
	90745:    true, // Ширина упаковки
	90846:    true, // Высота упаковки
	90849:    true, // Длина упаковки
	15001706: true, // Код упаковки
	15001650: true, // ИКПУ
	15003293: true, // Артикул OZON
	15003988: true, // NTIN
	15003989: true, // Код ТРУ 1
	15003990: true, // Код ТРУ 2
	15003991: true, // Кол-во штук в товаре по ЭС
	165482:   true, // Рост модели на фото
	246961:   true, // Размер на модели
	165505:   true, // Параметры модели на фото (ОГ-ОТ-ОБ)
	15000001: true, // ТНВЭД
	1010:     true, // Утеплитель
}

// preserveCharNames — имена характеристик, которые нельзя менять даже если Stage 1 их пометил.
// Это данные, которые невозможно определить по фото и которые заполняются вручную/из сертификатов.
var preserveCharNames = map[string]bool{
	"тнвэд":                true,
	"сертификат":           true, // покрывает "Сертификат", "Сертификат соответствия" и т.д.
	"ндс":                  true,
	"маркированный товар":  true,
	"состав":               true, // Состав ткани в %
	"страна производства":  true,
	"коллекция":            true, // "Весна-Лето 2025" — брендовая коллекция, НЕ сезон
	"пол":                  true, // WB допускает "Девочки", "Мальчики" — LLM ломает эти значения
}

// stage1Issue — один issue из Stage 1: расхождение между фото и карточкой.
type stage1Issue struct {
	CharcID      *int   `json:"charc_id"`
	Field        string `json:"field"`
	CardValue    string `json:"card_value"`
	CorrectValue string `json:"correct_value"`
	Reason       string `json:"reason"`
}

// matchedIssue — issue с привязанным charc_id из справочника предмета.
type matchedIssue struct {
	CharcID      int
	Name         string
	CardValue    string
	CorrectValue string
	Reason       string
	IsEmpty      bool // true если card_value == "(пусто)" или пустая строка
}

// parseIssuesFromSummary извлекает структурированные issues из vision_summary.
// Stage 1 хранит issues в формате: "<summary text>\n[ISSUES] [{...}]"
func parseIssuesFromSummary(summary string) []stage1Issue {
	idx := strings.Index(summary, "[ISSUES] ")
	if idx == -1 {
		return nil
	}
	jsonPart := summary[idx+len("[ISSUES] "):]

	var issues []stage1Issue
	if err := json.Unmarshal([]byte(jsonPart), &issues); err != nil {
		return nil
	}
	return issues
}

// matchIssuesToCharcIDs маппит issues на charc_id из справочника предмета.
// Приоритет: charc_id из Stage 1 > name matching (backward compat).
// Фильтрует системные характеристики (skipCharcIDs + preserveCharNames).
func matchIssuesToCharcIDs(nmID int, issues []stage1Issue, charEntries []CharEntry) (matched []matchedIssue, unmatched []stage1Issue) {
	nameIndex := make(map[string]CharEntry, len(charEntries))
	idIndex := make(map[int]CharEntry, len(charEntries))
	for _, e := range charEntries {
		nameIndex[strings.ToLower(e.Name)] = e
		idIndex[e.CharcID] = e
	}

	for _, issue := range issues {
		var entry CharEntry
		var found bool

		// Path 1: charc_id из Stage 1 (предпочтительный)
		if issue.CharcID != nil {
			entry, found = idIndex[*issue.CharcID]
			if !found {
				log.Printf("    WARN nm_id=%d: charc_id=%d не найден в справочнике предмета — unmatched", nmID, *issue.CharcID)
				unmatched = append(unmatched, issue)
				continue
			}
		} else {
			// Path 2: fallback на name matching (старые данные)
			entry, found = nameIndex[strings.ToLower(issue.Field)]
			if !found {
				unmatched = append(unmatched, issue)
				continue
			}
		}

		// Защита: системные charc_id
		if skipCharcIDs[entry.CharcID] {
			log.Printf("    WARN nm_id=%d: issue для системного поля %q (charc_id=%d) — пропускаем", nmID, entry.Name, entry.CharcID)
			continue
		}

		// Защита: характеристик по имени (ТНВЭД, сертификаты, НДС и т.д.)
		if preserveCharNames[strings.ToLower(entry.Name)] {
			log.Printf("    WARN nm_id=%d: issue для защищённого поля %q (charc_id=%d) — пропускаем", nmID, entry.Name, entry.CharcID)
			continue
		}

		isEmpty := issue.CardValue == "(пусто)" || issue.CardValue == ""
		matched = append(matched, matchedIssue{
			CharcID:      entry.CharcID,
			Name:         entry.Name,
			CardValue:    issue.CardValue,
			CorrectValue: issue.CorrectValue,
			Reason:       issue.Reason,
			IsEmpty:      isEmpty,
		})
	}
	return
}

// runStage4 генерирует исправленные характеристики карточки через линейный pipeline (этап 4).
//
// Линейный pipeline:
//  1. (КОД) Загрузить ВСЕ предметы WB из source DB
//  2. (LLM) Выбрать подходящий предмет по Vision-описанию → JSON {subject_id, subject_name}
//  3. (КОД) Загрузить характеристики для выбранного subject_id из кэша
//  4. (КОД) Парсинг issues из vision_summary → маппинг на charc_id
//  5. (LLM) Форматирование исправленных характеристик по issues
func runStage4(ctx context.Context, source *SourceRepo, results *ResultsRepo, provider llm.Provider, cfg CLIConfig, force bool, keepSubject bool) error {
	nmIDs, err := results.LoadVisionDiscrepancies(ctx, force)
	if err != nil {
		return fmt.Errorf("load vision discrepancies: %w", err)
	}
	if len(nmIDs) == 0 {
		log.Println("Stage 4: no vision discrepancies found. Run stage 1 first.")
		return nil
	}

	// Применяем фильтры из config: nm_ids, subject_ids, vendor_codes
	nmIDs, err = results.FilterByConfig(ctx, nmIDs, cfg.Filter)
	if err != nil {
		return fmt.Errorf("filter by config: %w", err)
	}
	log.Printf("  After filter: %d cards match", len(nmIDs))

	if len(nmIDs) == 0 {
		log.Println("  No cards match filter config")
		return nil
	}

	if cfg.Analysis.Limit > 0 && len(nmIDs) > cfg.Analysis.Limit {
		nmIDs = nmIDs[:cfg.Analysis.Limit]
	}

	// Resume: пропускаем карточки, уже обработанные Stage 4
	if !force {
		pending, err := results.LoadPendingGenerateCards(ctx, nmIDs)
		if err != nil {
			return fmt.Errorf("load pending generate cards: %w", err)
		}
		log.Printf("  Resume: %d already done, %d pending", len(nmIDs)-len(pending), len(pending))
		nmIDs = pending
	} else {
		log.Printf("  Force: re-processing all %d cards", len(nmIDs))
	}

	if len(nmIDs) == 0 {
		log.Println("  All cards already generated")
		return nil
	}

	log.Printf("Stage 4: generating corrected characteristics for %d cards with %s (issues-driven pipeline)", len(nmIDs), cfg.Text.Model)

	cachePath := expandHome(cfg.CharDict.DBPath)
	charDict, err := NewCharDictRepo(cachePath)
	if err != nil {
		return fmt.Errorf("open char-dict-cache: %w (path: %s)", err, cachePath)
	}
	defer charDict.Close()

	// Шаг 1: загрузить ВСЕ предметы WB
	allSubjects, err := source.LoadAllSubjects(ctx)
	if err != nil {
		return fmt.Errorf("load all subjects: %w", err)
	}
	log.Printf("  Loaded %d WB subjects", len(allSubjects))

	subjectSet := make(map[int]string, len(allSubjects))
	for _, s := range allSubjects {
		subjectSet[s.SubjectID] = s.SubjectName
	}

	analysisRows, err := results.LoadAnalysisForVision(ctx, nmIDs)
	if err != nil {
		return fmt.Errorf("load analysis rows: %w", err)
	}

	charsMap, err := source.LoadCharacteristics(ctx, nmIDs)
	if err != nil {
		return fmt.Errorf("load characteristics: %w", err)
	}

	cardsMap, err := loadCardsMap(ctx, source, nmIDs)
	if err != nil {
		return fmt.Errorf("load cards map: %w", err)
	}

	for i := range analysisRows {
		if card, ok := cardsMap[analysisRows[i].NmID]; ok {
			analysisRows[i].Description = card.Description
		}
	}

	var (
		wg        sync.WaitGroup
		semaphore = make(chan struct{}, cfg.Analysis.Concurrency)
		errCount  int
		mu        sync.Mutex
	)

	tracker := progress.NewCLITrackerWithConfig(progress.CLITrackerConfig{
		Total:  len(analysisRows),
		Prefix: "Stage 4",
		Width:  -1,
	})

	for _, row := range analysisRows {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		wg.Add(1)
		go func(r VisionAnalysisRow) {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			card := cardsMap[r.NmID]
			chars := charsMap[r.NmID]

			start := time.Now()
			newChars, subjectName, subjectID, err := generateNewParams(
				ctx, provider, charDict, allSubjects, subjectSet, r, chars, card, cfg, force, keepSubject, results)
			dur := time.Since(start)

			if err != nil {
				mu.Lock()
				errCount++
				mu.Unlock()
				log.Printf("  ERROR nm_id=%d: %v", r.NmID, err)
				return
			}

				if newChars == nil {
					return // guard уже сохранил + отметил generate_done
				}

				charsJSON, _ := json.Marshal(newChars)
			if err := results.SaveNewParams(ctx, r.NmID, "", "", string(charsJSON), subjectID, subjectName); err != nil {
				mu.Lock()
				errCount++
				mu.Unlock()
				log.Printf("  ERROR save nm_id=%d: %v", r.NmID, err)
				return
			}
			if err := results.MarkGenerateDone(ctx, r.NmID, force); err != nil {
				mu.Lock()
				errCount++
				mu.Unlock()
				log.Printf("  ERROR mark generate done nm_id=%d: %v", r.NmID, err)
				return
			}

			tracker.Update(1)

			mu.Lock()
			n := tracker.Current()
			log.Printf("  [%d/%d] %s | nm_id=%d | %.1fs | subject=%s (%d) | chars=%d | ETA %s",
				n, tracker.Total(),
				time.Now().Format("15:04:05"),
				row.NmID,
				dur.Seconds(),
				subjectName, subjectID, len(newChars),
				tracker.ETA())
			mu.Unlock()
		}(row)
	}

	wg.Wait()
	tracker.Done()

	log.Printf("Stage 4 complete: %d generated, %d errors", tracker.Current(), errCount)
	return nil
}

// generateNewParams — линейный pipeline для одной карточки (2 вызова LLM, без цикла).
func generateNewParams(
	ctx context.Context,
	provider llm.Provider,
	charDict *CharDictRepo,
	allSubjects []SubjectEntry,
	subjectSet map[int]string,
	row VisionAnalysisRow,
	chars []CardChar,
	card CardData,
	cfg CLIConfig,
	force bool, keepSubject bool,
	results *ResultsRepo,
) ([]any, string, int, error) {
	// Шаг 2: LLM выбирает предмет из списка всех предметов WB
	subjectID, subjectName, err := selectSubject(ctx, provider, allSubjects, row, card, cfg.Text, cfg.Prompts)
	if err != nil {
		return nil, "", 0, fmt.Errorf("select subject: %w", err)
	}

	// Валидация: subject_id обязан быть в справочнике
	canonicalName, ok := subjectSet[subjectID]
	if !ok {
		return nil, "", 0, fmt.Errorf("LLM вернула subject_id=%d (%q), которого нет в справочнике из %d предметов — галлюцинация модели",
			subjectID, subjectName, len(allSubjects))
	}
	// Исправляем subject_name если LLM выдумала название
	if subjectName != canonicalName {
		log.Printf("    WARN nm_id=%d: LLM вернула subject_name=%q, каноническое=%q — исправляем",
			row.NmID, subjectName, canonicalName)
		subjectName = canonicalName
	}

	log.Printf("    nm_id=%d: selected subject=%s (%d)", row.NmID, subjectName, subjectID)

	// Guard: если LLM предложила смену предмета
	if subjectID != card.SubjectID {
		if !keepSubject {
			log.Printf("  SKIP nm_id=%d: subject changed %d→%d (%q→%q). Use --keep-subject to override.",
				row.NmID, card.SubjectID, subjectID, card.SubjectName, subjectName)
			// Сохраняем обнаруженную смену предмета для видимости в XLSX
			results.SaveNewParams(ctx, row.NmID, "", "", "", subjectID, subjectName)
			results.MarkGenerateDone(ctx, row.NmID, force)
			return nil, "", 0, nil
		}
		log.Printf("  INFO nm_id=%d: --keep-subject: keeping %d (%q), ignoring LLM suggestion %d (%q)",
			row.NmID, card.SubjectID, card.SubjectName, subjectID, subjectName)
		subjectID = card.SubjectID
		subjectName = card.SubjectName
	}

	// Шаг 3: загружаем характеристики для выбранного предмета
	charEntries, err := charDict.LoadCharacteristicsForSubject(ctx, subjectID)
	if err != nil {
		return nil, "", 0, fmt.Errorf("load characteristics for subject %d: %w", subjectID, err)
	}

	// Шаг 4: LLM форматирует исправленные характеристики по issues
	return fillCardParams(ctx, provider, row, chars, subjectID, subjectName, charEntries, cfg.Text, cfg.Prompts)
}

// selectSubject — шаг 2: LLM выбирает подходящий предмет из списка.
func selectSubject(
	ctx context.Context,
	provider llm.Provider,
	allSubjects []SubjectEntry,
	row VisionAnalysisRow,
	card CardData,
	modelCfg ModelConfig,
	prompts PromptConfig,
) (int, string, error) {
	subjectsJSON, _ := json.Marshal(allSubjects)

	system, user := buildStage4SelectMessages(card, row, string(subjectsJSON), prompts)

	var result struct {
		SubjectID   int    `json:"subject_id"`
		SubjectName string `json:"subject_name"`
	}
	if err := llm.GenerateJSON(ctx, provider,
		[]llm.Message{
			{Role: llm.RoleSystem, Content: system},
			{Role: llm.RoleUser, Content: user},
		},
		&result,
		llm.WithModel(modelCfg.Model),
		llm.WithTemperature(0),
		llm.WithMaxTokens(2000),
	); err != nil {
		return 0, "", fmt.Errorf("select subject: %w", err)
	}
	if result.SubjectID == 0 {
		return 0, "", fmt.Errorf("LLM returned subject_id=0")
	}
	return result.SubjectID, result.SubjectName, nil
}

// fillCardParams — issues-driven генерация характеристик.
// Парсит issues из vision_summary, маппит на charc_id, отправляет LLM для форматирования.
func fillCardParams(
	ctx context.Context,
	provider llm.Provider,
	row VisionAnalysisRow,
	chars []CardChar,
	subjectID int,
	subjectName string,
	charEntries []CharEntry,
	modelCfg ModelConfig,
	prompts PromptConfig,
) ([]any, string, int, error) {
	// Шаг 4a: парсим issues из vision_summary
	issues := parseIssuesFromSummary(row.VisionSummary)
	if len(issues) == 0 {
		return nil, "", 0, fmt.Errorf("no [ISSUES] block in vision_summary — skip card (run stage 1 first)")
	}
	log.Printf("    nm_id=%d: parsed %d issues from vision_summary", row.NmID, len(issues))

	// Шаг 4b: маппим issues на charc_id из справочника предмета
	matched, unmatched := matchIssuesToCharcIDs(row.NmID, issues, charEntries)
	log.Printf("    nm_id=%d: %d matched, %d unmatched issues", row.NmID, len(matched), len(unmatched))

	if len(matched) == 0 && len(unmatched) == 0 {
		return nil, "", 0, fmt.Errorf("all issues filtered (system/preserved fields) — nothing to fix")
	}

	// Шаг 4c: фильтруем charEntries — только релевантные для issues
	relevantCharcIDs := make(map[int]bool)
	for _, m := range matched {
		relevantCharcIDs[m.CharcID] = true
	}
	// Для unmatched issues тоже пытаемся включить возможные маппинги
	type charDef struct {
		CharcID  int    `json:"charc_id"`
		Name     string `json:"name"`
		Required bool   `json:"required"`
		MaxCount int    `json:"max_count"`
	}
	var relevantDefs []charDef
	for _, e := range charEntries {
		if relevantCharcIDs[e.CharcID] {
			relevantDefs = append(relevantDefs, charDef{e.CharcID, e.Name, e.Required, e.MaxCount})
		}
	}
	// Для unmatched — добавляем все записи, чьё имя может совпасть по подстроке
	for _, u := range unmatched {
		uLower := strings.ToLower(u.Field)
		for _, e := range charEntries {
			if relevantCharcIDs[e.CharcID] {
				continue
			}
			if strings.Contains(strings.ToLower(e.Name), uLower) || strings.Contains(uLower, strings.ToLower(e.Name)) {
				relevantDefs = append(relevantDefs, charDef{e.CharcID, e.Name, e.Required, e.MaxCount})
				relevantCharcIDs[e.CharcID] = true
			}
		}
	}
	// Если unmatched остались без маппинга — добавляем ВСЕ charEntries (LLM сама разберётся)
	if len(unmatched) > 0 && len(relevantDefs) == 0 {
		for _, e := range charEntries {
			relevantDefs = append(relevantDefs, charDef{e.CharcID, e.Name, e.Required, e.MaxCount})
		}
	}

	defsJSON, _ := json.Marshal(relevantDefs)

	// Шаг 4d: формируем issues_structured JSON
	issuesJSON := buildIssuesStructuredJSON(matched, unmatched)

	// Шаг 4e: фильтруем текущие характеристики — только проблемные
	var relevantChars []CardChar
	relevantCharNames := make(map[string]bool)
	for id := range relevantCharcIDs {
		for _, e := range charEntries {
			if e.CharcID == id {
				relevantCharNames[strings.ToLower(e.Name)] = true
			}
		}
	}
	for _, c := range chars {
		if relevantCharNames[strings.ToLower(c.Name)] {
			relevantChars = append(relevantChars, c)
		}
	}

	// Шаг 4f: LLM форматирование с retry на parse failure
	system, user := buildStage4CharsMessages(
		row, relevantChars, subjectID, subjectName, string(defsJSON), issuesJSON, prompts,
	)

	type charResult struct {
		CharcID int            `json:"charc_id"`
		Value   flexibleString `json:"value"`
	}
	type charsResponse struct {
		Characteristics []charResult `json:"characteristics"`
	}

	var result charsResponse

	if err := llm.GenerateJSON(ctx, provider,
		[]llm.Message{
			{Role: llm.RoleSystem, Content: system},
			{Role: llm.RoleUser, Content: user},
		},
		&result,
		llm.WithModel(modelCfg.Model),
		llm.WithTemperature(modelCfg.Temperature),
		llm.WithMaxTokens(modelCfg.MaxTokens),
	); err != nil {
		return nil, "", 0, fmt.Errorf("fill chars: %w", err)
	}

	if len(result.Characteristics) == 0 {
		return nil, "", 0, fmt.Errorf("LLM вернула пустые характеристики")
	}

	// Валидация charc_id против справочника предмета
	validCharcIDs := make(map[int]bool, len(charEntries))
	charcNames := make(map[int]string, len(charEntries))
	for _, e := range charEntries {
		validCharcIDs[e.CharcID] = true
		charcNames[e.CharcID] = e.Name
	}

	var charsRaw []any
	seen := make(map[int]bool)
	for _, c := range result.Characteristics {
		if skipCharcIDs[c.CharcID] || seen[c.CharcID] {
			continue
		}
		if !validCharcIDs[c.CharcID] {
			log.Printf("    WARN nm_id=%d: LLM вернула charc_id=%d не из справочника subject %d — пропускаем",
				row.NmID, c.CharcID, subjectID)
			continue
		}
		seen[c.CharcID] = true
		charsRaw = append(charsRaw, map[string]any{
			"charc_id": c.CharcID,
			"name":     charcNames[c.CharcID],
			"value":    c.Value,
		})
	}

	return charsRaw, subjectName, subjectID, nil
}

// buildIssuesStructuredJSON формирует JSON для плейсхолдера {issues_structured}.
func buildIssuesStructuredJSON(matched []matchedIssue, unmatched []stage1Issue) string {
	type jsonMatched struct {
		CharcID  int    `json:"charc_id"`
		Name     string `json:"name"`
		Current  string `json:"current"`
		Suggested string `json:"suggested"`
		Reason   string `json:"reason"`
		IsEmpty  bool   `json:"is_empty"`
	}
	type jsonUnmatched struct {
		Field    string `json:"field"`
		Current  string `json:"current"`
		Suggested string `json:"suggested"`
		Reason   string `json:"reason"`
	}

	jm := make([]jsonMatched, len(matched))
	for i, m := range matched {
		jm[i] = jsonMatched{m.CharcID, m.Name, m.CardValue, m.CorrectValue, m.Reason, m.IsEmpty}
	}
	ju := make([]jsonUnmatched, len(unmatched))
	for i, u := range unmatched {
		ju[i] = jsonUnmatched{u.Field, u.CardValue, u.CorrectValue, u.Reason}
	}

	data := map[string]any{
		"matched":   jm,
		"unmatched": ju,
	}
	b, _ := json.Marshal(data)
	return string(b)
}

// runStage4Diff показывает before/after сравнение характеристик для карточек, обработанных Stage 4.
func runStage4Diff(ctx context.Context, source *SourceRepo, results *ResultsRepo, cfg CLIConfig, full bool, force bool) error {
	rows, err := results.LoadAnalysisForUpdate(ctx, cfg.Filter, force)
	if err != nil {
		return fmt.Errorf("load analysis for update: %w", err)
	}
	if len(rows) == 0 {
		log.Println("No cards with generated params. Run stage 4 first.")
		return nil
	}

	// Применяем фильтры из config: nm_ids, subject_ids, vendor_codes
	if len(cfg.Filter.NmIDs) > 0 || len(cfg.Filter.SubjectIDs) > 0 || len(cfg.Filter.VendorCodes) > 0 {
		nmIDSet := make(map[int]bool)
		rowMap := make(map[int]AnalysisRow)
		filteredIDs := make([]int, 0, len(rows))
		for _, r := range rows {
			nmIDSet[r.NmID] = true
			rowMap[r.NmID] = r
			filteredIDs = append(filteredIDs, r.NmID)
		}
		filteredIDs, err = results.FilterByConfig(ctx, filteredIDs, cfg.Filter)
		if err != nil {
			return fmt.Errorf("filter diff by config: %w", err)
		}
		var filtered []AnalysisRow
		for _, id := range filteredIDs {
			filtered = append(filtered, rowMap[id])
		}
		rows = filtered
		log.Printf("  After filter: %d cards match", len(rows))
	}

	if cfg.Analysis.Limit > 0 && len(rows) > cfg.Analysis.Limit {
		rows = rows[:cfg.Analysis.Limit]
	}

	// Open charDict for --full mode (all characteristics per subject)
	var charDict *CharDictRepo
	if full {
		cachePath := expandHome(cfg.CharDict.DBPath)
		dict, err := NewCharDictRepo(cachePath)
		if err != nil {
			return fmt.Errorf("open char-dict-cache: %w", err)
		}
		charDict = dict
		defer charDict.Close()
	}

	for _, row := range rows {
		if full {
			// --full: show ALL characteristics for the subject (filled + empty)
			if row.NewSubjectID == nil {
				log.Printf("  WARN nm_id=%d: no subject_id \u2014 skipping", row.NmID)
				continue
			}
			subjectID := *row.NewSubjectID

			allCharEntries, err := charDict.LoadCharacteristicsForSubject(ctx, subjectID)
			if err != nil {
				log.Printf("  ERROR load char entries nm_id=%d: %v", row.NmID, err)
				continue
			}

			charsMap, err := source.LoadCharacteristics(ctx, []int{row.NmID})
			if err != nil {
				log.Printf("  ERROR load chars nm_id=%d: %v", row.NmID, err)
				continue
			}
			currentIdx := make(map[int]string)
			for _, c := range charsMap[row.NmID] {
				currentIdx[c.CharID] = unpackCharValue(c.Value)
			}

			var newChars []charcEntry
			json.Unmarshal([]byte(row.NewCharacteristics), &newChars)
			newIdx := make(map[int]string)
			for _, nc := range newChars {
				newIdx[nc.CharcID] = nc.Value
			}

			fmt.Printf("═══ nm_id=%d vendor_code=%s subject=%s (%d) ═══\n", row.NmID, row.VendorCode, row.NewSubjectName, subjectID)
			fmt.Printf("  %-35s │ %-30s │ %-30s\n", "Характеристика", "Текущее", "Stage 4")
			fmt.Printf("  %s─%s─%s\n", strings.Repeat("─", 35), strings.Repeat("─", 30), strings.Repeat("─", 30))

			for _, e := range allCharEntries {
				if skipCharcIDs[e.CharcID] {
					continue
				}
				cur := currentIdx[e.CharcID]
				if cur == "" {
					cur = "(пусто)"
				}
				if newVal, ok := newIdx[e.CharcID]; ok {
					fmt.Printf("  %-35s │ %-30s │ %-30s\n", e.Name, cur, newVal)
				} else {
					fmt.Printf("  %-35s │ %-30s │ %s\n", e.Name, cur, "—")
				}
			}
			fmt.Println()
		} else {
			// --diff: show only changed characteristics
			fmt.Printf("═══ nm_id=%d vendor_code=%s subject=%s═══\n", row.NmID, row.VendorCode, row.NewSubjectName)

			charsMap, err := source.LoadCharacteristics(ctx, []int{row.NmID})
			if err != nil {
				log.Printf("  ERROR load chars nm_id=%d: %v", row.NmID, err)
				continue
			}
			currentChars := charsMap[row.NmID]

			currentIdx := make(map[int]string)
			currentNames := make(map[int]string)
			for _, c := range currentChars {
				currentIdx[c.CharID] = unpackCharValue(c.Value)
				currentNames[c.CharID] = c.Name
			}

			var newChars []charcEntry
			if err := json.Unmarshal([]byte(row.NewCharacteristics), &newChars); err != nil {
				log.Printf("  ERROR parse new chars nm_id=%d: %v", row.NmID, err)
				continue
			}

			fmt.Printf("  %-35s │ %-30s │ %-30s\n", "Характеристика", "Было", "Стало")
			fmt.Printf("  %s─%s─%s\n", strings.Repeat("─", 35), strings.Repeat("─", 30), strings.Repeat("─", 30))

			for _, nc := range newChars {
				oldVal := currentIdx[nc.CharcID]
				name := nc.Name
				if name == "" {
					name = currentNames[nc.CharcID]
				}
				if oldVal == "" {
					oldVal = "(пусто)"
				}
				fmt.Printf("  %-35s │ %-30s │ %-30s\n", name, oldVal, nc.Value)
			}

			fmt.Println()
		}
	}

	return nil
}

func unpackCharValue(raw string) string {
	var arr []string
	if err := json.Unmarshal([]byte(raw), &arr); err == nil {
		return strings.Join(arr, ", ")
	}
	var s string
	if err := json.Unmarshal([]byte(raw), &s); err == nil {
		return s
	}
	return raw
}

// loadCardsMap загружает map nm_id → CardData для получения subject_id.
func loadCardsMap(ctx context.Context, source *SourceRepo, nmIDs []int) (map[int]CardData, error) {
	cards, err := source.LoadCardsForAnalysis(ctx, FilterConfig{NmIDs: nmIDs})
	if err != nil {
		return nil, err
	}
	m := make(map[int]CardData, len(cards))
	for _, c := range cards {
		m[c.NmID] = c
	}
	return m, nil
}

// expandHome раскрывает ~ в начале пути в домашнюю директорию.
func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, path[2:])
	}
	return path
}
