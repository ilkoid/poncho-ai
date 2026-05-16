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

// runStage4 генерирует новые параметры карточки через линейный pipeline (этап 4).
//
// Линейный pipeline (без цикла):
//  1. (КОД) Загрузить ВСЕ предметы WB из source DB
//  2. (LLM) Выбрать подходящий предмет по Vision-описанию → JSON {subject_id, subject_name}
//  3. (КОД) Загрузить характеристики для выбранного subject_id из кэша
//  4. (LLM) Заполнить title, description, характеристики → JSON результат
func runStage4(ctx context.Context, source *SourceRepo, results *ResultsRepo, provider llm.Provider, cfg CLIConfig) error {
	nmIDs, err := results.LoadVisionDiscrepancies(ctx)
	if err != nil {
		return fmt.Errorf("load vision discrepancies: %w", err)
	}
	if len(nmIDs) == 0 {
		log.Println("Stage 4: no vision discrepancies found. Run stage 3 first.")
		return nil
	}

	if cfg.Analysis.Limit > 0 && len(nmIDs) > cfg.Analysis.Limit {
		nmIDs = nmIDs[:cfg.Analysis.Limit]
	}

	// Resume: пропускаем карточки, уже обработанные Stage 4
	pending, err := results.LoadPendingGenerateCards(ctx, nmIDs)
	if err != nil {
		return fmt.Errorf("load pending generate cards: %w", err)
	}
	log.Printf("  Resume: %d already done, %d pending", len(nmIDs)-len(pending), len(pending))
	nmIDs = pending

	if len(nmIDs) == 0 {
		log.Println("  All cards already generated")
		return nil
	}

	log.Printf("Stage 4: generating new params for %d cards with %s (linear pipeline)", len(nmIDs), cfg.Text.Model)

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

	brand := cfg.Brand
	if brand == "" {
		brand = "PlayToday"
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
			newTitle, newDesc, newChars, subjectName, subjectID, err := generateNewParams(
				ctx, provider, charDict, allSubjects, subjectSet, r, chars, card, cfg, brand)
			dur := time.Since(start)

			if err != nil {
				mu.Lock()
				errCount++
				mu.Unlock()
				log.Printf("  ERROR nm_id=%d: %v", r.NmID, err)
				return
			}

			charsJSON, _ := json.Marshal(newChars)
			if err := results.SaveNewParams(ctx, r.NmID, newTitle, newDesc, string(charsJSON), subjectID, subjectName); err != nil {
				mu.Lock()
				errCount++
				mu.Unlock()
				log.Printf("  ERROR save nm_id=%d: %v", r.NmID, err)
				return
			}
			if err := results.MarkGenerateDone(ctx, r.NmID); err != nil {
				mu.Lock()
				errCount++
				mu.Unlock()
				log.Printf("  ERROR mark generate done nm_id=%d: %v", r.NmID, err)
				return
			}

			tracker.Update(1)

			mu.Lock()
			n := tracker.Current()
			log.Printf("  [%d/%d] %s | %.1fs | subject=%s (%d) | chars=%d | ETA %s",
				n, tracker.Total(),
				time.Now().Format("15:04:05"),
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
	brand string,
) (string, string, []any, string, int, error) {
	// Шаг 2: LLM выбирает предмет из списка всех предметов WB
	subjectID, subjectName, err := selectSubject(ctx, provider, allSubjects, row, card, cfg.Text, cfg.Prompts)
	if err != nil {
		return "", "", nil, "", 0, fmt.Errorf("select subject: %w", err)
	}

	// Валидация: subject_id обязан быть в справочнике
	canonicalName, ok := subjectSet[subjectID]
	if !ok {
		return "", "", nil, "", 0, fmt.Errorf("LLM вернула subject_id=%d (%q), которого нет в справочнике из %d предметов — галлюцинация модели",
			subjectID, subjectName, len(allSubjects))
	}
	// Исправляем subject_name если LLM выдумала название
	if subjectName != canonicalName {
		log.Printf("    WARN nm_id=%d: LLM вернула subject_name=%q, каноническое=%q — исправляем",
			row.NmID, subjectName, canonicalName)
		subjectName = canonicalName
	}

	log.Printf("    nm_id=%d: selected subject=%s (%d)", row.NmID, subjectName, subjectID)

	// Шаг 3: загружаем характеристики для выбранного предмета
	charEntries, err := charDict.LoadCharacteristicsForSubject(ctx, subjectID)
	if err != nil {
		return "", "", nil, "", 0, fmt.Errorf("load characteristics for subject %d: %w", subjectID, err)
	}

	// Шаг 4: LLM заполняет параметры на основе Vision + загруженных характеристик
	return fillCardParams(ctx, provider, row, chars, subjectID, subjectName, charEntries, cfg.Text, brand, cfg.Prompts)
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

	resp, err := generateWithRetry(ctx, provider,
		[]llm.Message{
			{Role: llm.RoleSystem, Content: system},
			{Role: llm.RoleUser, Content: user},
		},
		llm.WithModel(modelCfg.Model),
		llm.WithTemperature(0),
		llm.WithMaxTokens(200),
	)
	if err != nil {
		return 0, "", fmt.Errorf("LLM call: %w", err)
	}

	var result struct {
		SubjectID   int    `json:"subject_id"`
		SubjectName string `json:"subject_name"`
	}
	if err := json.Unmarshal([]byte(extractJSON(resp.Content)), &result); err != nil {
		return 0, "", fmt.Errorf("parse subject JSON: %w (raw: %s)", err, truncate(resp.Content, 200))
	}
	if result.SubjectID == 0 {
		return 0, "", fmt.Errorf("LLM returned subject_id=0 (raw: %s)", truncate(resp.Content, 200))
	}
	return result.SubjectID, result.SubjectName, nil
}

// fillCardParams — шаг 4: LLM заполняет характеристики на основе Vision (без старых характеристик).
func fillCardParams(
	ctx context.Context,
	provider llm.Provider,
	row VisionAnalysisRow,
	chars []CardChar,
	subjectID int,
	subjectName string,
	charEntries []CharEntry,
	modelCfg ModelConfig,
	brand string,
	prompts PromptConfig,
) (string, string, []any, string, int, error) {
	// Формируем список доступных характеристик с charcID
	type charDef struct {
		CharcID  int    `json:"charc_id"`
		Name     string `json:"name"`
		Required bool   `json:"required"`
		MaxCount int    `json:"max_count"`
	}
	defs := make([]charDef, len(charEntries))
	for i, c := range charEntries {
		defs[i] = charDef{c.CharcID, c.Name, c.Required, c.MaxCount}
	}
	defsJSON, _ := json.Marshal(defs)

	// Парсим аудиторию и пол из Vision attributes
	audience, gender := parseAudienceFromVision(row.NmID, row.VisionAttributes)

	titleRules, descRules, seoContext := audiencePromptRules(audience, gender, brand, prompts)

	system, user := buildStage4FillMessages(
		row, chars, subjectID, subjectName, string(defsJSON),
		brand, audience, titleRules, descRules, seoContext, prompts,
	)

	resp, err := generateWithRetry(ctx, provider,
		[]llm.Message{
			{Role: llm.RoleSystem, Content: system},
			{Role: llm.RoleUser, Content: user},
		},
		llm.WithModel(modelCfg.Model),
		llm.WithTemperature(modelCfg.Temperature),
		llm.WithMaxTokens(modelCfg.MaxTokens),
	)
	if err != nil {
		return "", "", nil, "", 0, fmt.Errorf("LLM call: %w", err)
	}

	var result struct {
		Title           string `json:"title"`
		Description     string `json:"description"`
		Characteristics []struct {
			CharcID int    `json:"charc_id"`
			Value   string `json:"value"`
		} `json:"characteristics"`
	}
	if err := json.Unmarshal([]byte(extractJSON(resp.Content)), &result); err != nil {
		return "", "", nil, "", 0, fmt.Errorf("parse fill result: %w (raw: %s)", err, truncate(resp.Content, 200))
	}

	// Guard: пустой результат — ошибка
	if result.Title == "" && result.Description == "" && len(result.Characteristics) == 0 {
		return "", "", nil, "", 0, fmt.Errorf("LLM вернула пустой результат (raw: %s)", truncate(resp.Content, 200))
	}
	if result.Title == "" {
		log.Printf("    WARN nm_id=%d: LLM вернула пустой title", row.NmID)
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
		entry := map[string]any{
			"charc_id": c.CharcID,
			"name":     charcNames[c.CharcID],
			"value":    c.Value,
		}
		charsRaw = append(charsRaw, entry)
	}

	return result.Title, result.Description, charsRaw, subjectName, subjectID, nil
}

// parseAudienceFromVision извлекает аудиторию и пол из Vision attributes JSON.
func parseAudienceFromVision(nmID int, attrsJSON string) (audience, gender string) {
	var attrs map[string]string
	if err := json.Unmarshal([]byte(attrsJSON), &attrs); err != nil {
		log.Printf("    WARN nm_id=%d: failed to parse vision attributes, using default audience", nmID)
		return "девочка (6-10)", "женский"
	}
	if a, ok := attrs["аудитория"]; ok && a != "" {
		audience = a
	} else {
		log.Printf("    WARN nm_id=%d: audience not determined from vision, using default: девочка (6-10)", nmID)
		audience = "девочка (6-10)"
	}
	if g, ok := attrs["пол"]; ok && g != "" {
		gender = g
	} else {
		gender = "женский"
	}
	return
}

// audiencePromptRules возвращает правила для title/description в зависимости от аудитории.
// Сначала ищет в конфиге (prompts.AudienceRules), затем fallback на дефолты.
func audiencePromptRules(audience, gender, brand string, prompts PromptConfig) (titleRules, descRules, seoContext string) {
	rules := resolveAudienceRules(prompts.AudienceRules)

	// Определяем ключ для поиска правил на основе fuzzy matching
	a := strings.ToLower(audience)
	var key string
	switch {
	case strings.Contains(a, "взрослая женщина") || strings.Contains(a, "женщин"):
		key = "взрослая женщина"
	case strings.Contains(a, "взрослый мужч") || strings.Contains(a, "мужчин"):
		key = "взрослый мужчина"
	case strings.Contains(a, "подросток") && (strings.Contains(a, "девочк") || gender == "женский"):
		key = "девочка-подросток (11-16)"
	case strings.Contains(a, "подросток") && strings.Contains(a, "мальчик"):
		key = "мальчик-подросток (11-16)"
	case strings.Contains(a, "малыш"):
		key = "малыш"
	default:
		key = "по умолчанию"
	}

	rule, ok := rules[key]
	if !ok {
		rule = rules["по умолчанию"]
	}

	titleRules = applyTemplate(rule.TitleRules, "{brand}", brand)
	descRules = applyTemplate(rule.DescRules, "{brand}", brand)
	seoContext = rule.SEOContext
	return
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
