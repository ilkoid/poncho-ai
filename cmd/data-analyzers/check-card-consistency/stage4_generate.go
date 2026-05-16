package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"strings"
	"sync/atomic"

	"github.com/ilkoid/poncho-ai/pkg/llm"
)

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

	log.Printf("Stage 4: generating new params for %d cards with %s (linear pipeline)", len(nmIDs), cfg.Text.Model)

	cachePath := defaultCharDictPath()
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

	cardsMap, err := loadCardsMap(ctx, source)
	if err != nil {
		return fmt.Errorf("load cards map: %w", err)
	}

	var (
		wg        sync.WaitGroup
		semaphore = make(chan struct{}, cfg.Analysis.Concurrency)
		total     atomic.Int64
		errors    atomic.Int64
	)

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

			newTitle, newDesc, newChars, subjectName, subjectID, err := generateNewParams(
				ctx, provider, charDict, allSubjects, subjectSet, r, chars, card, cfg.Text)
			if err != nil {
				log.Printf("  ERROR nm_id=%d: %v", r.NmID, err)
				errors.Add(1)
				return
			}

			charsJSON, _ := json.Marshal(newChars)
			if err := results.SaveNewParams(ctx, r.NmID, newTitle, newDesc, string(charsJSON), subjectID, subjectName); err != nil {
				log.Printf("  ERROR save nm_id=%d: %v", r.NmID, err)
				errors.Add(1)
				return
			}

			n := total.Add(1)
			log.Printf("  [%d] %s: subject=%s (%d), title=%q, chars=%d",
				n, r.VendorCode, subjectName, subjectID, truncate(newTitle, 50), len(newChars))
		}(row)
	}

	wg.Wait()

	log.Printf("Stage 4 complete: %d generated, %d errors", total.Load(), errors.Load())
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
	modelCfg ModelConfig,
) (string, string, []any, string, int, error) {
	// Шаг 2: LLM выбирает предмет из списка всех предметов WB
	subjectID, subjectName, err := selectSubject(ctx, provider, allSubjects, row, card, modelCfg)
	if err != nil {
		return "", "", nil, "", 0, fmt.Errorf("select subject: %w", err)
	}

	// Валидация: subject_id обязан быть в справочнике
	if _, ok := subjectSet[subjectID]; !ok {
		return "", "", nil, "", 0, fmt.Errorf("LLM вернула subject_id=%d (%q), которого нет в справочнике из %d предметов — галлюцинация модели",
			subjectID, subjectName, len(allSubjects))
	}

	log.Printf("    nm_id=%d: selected subject=%s (%d)", row.NmID, subjectName, subjectID)

	// Шаг 3: загружаем характеристики для выбранного предмета
	charEntries, err := charDict.LoadCharacteristicsForSubject(ctx, subjectID)
	if err != nil {
		return "", "", nil, "", 0, fmt.Errorf("load characteristics for subject %d: %w", subjectID, err)
	}

	// Шаг 4: LLM заполняет параметры на основе Vision + загруженных характеристик
	return fillCardParams(ctx, provider, row, chars, subjectID, subjectName, charEntries, modelCfg)
}

// selectSubject — шаг 2: LLM выбирает подходящий предмет из списка.
func selectSubject(
	ctx context.Context,
	provider llm.Provider,
	allSubjects []SubjectEntry,
	row VisionAnalysisRow,
	card CardData,
	modelCfg ModelConfig,
) (int, string, error) {
	subjectsJSON, _ := json.Marshal(allSubjects)

	system := `Ты — специалист по классификации товаров Wildberries.
	На основе Vision анализа (фото) определи подходящий предмет WB из списка.
	Ответь ТОЛЬКО JSON, без markdown, без пояснений:
	{"subject_id": <число>, "subject_name": "<название>"}

	Правила:
	1. Выбери предмет который лучше всего описывает ТИП ИЗДЕЛИЯ с фото (Vision тип изделия).
	2. Игнорируй текущий предмет карточки — опирайся только на Vision.
	3. Выбери ТОЧНО предмет из списка — не придумывай новые.
	4. Ответь ТОЛЬКО на русском языке. Никакого английского или китайского.`

	user := fmt.Sprintf(`Текущий предмет карточки: %s (subject_id=%d) — МОЖЕТ БЫТЬ НЕВЕРНЫМ

	VISION АНАЛИЗ (ФОТО — истина):
	Тип изделия: %s
	Атрибуты: %s
	Замечания: %s

	СПИСОК ВСЕХ ПРЕДМЕТОВ WB:
	%s`,
		card.SubjectName, card.SubjectID,
		row.VisionProductType, row.VisionAttributes, row.VisionSummary,
		string(subjectsJSON))

	resp, err := provider.Generate(ctx,
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
	audience, gender := parseAudienceFromVision(row.VisionAttributes)

	titleRules, descRules, seoContext := audiencePromptRules(audience, gender)

	system := fmt.Sprintf(`Ты — контент-менеджер бренда PlayToday на Wildberries. Заполни параметры карточки на основе Vision анализа (фото — истина).

	Предмет WB: %s (subject_id=%d)
	Целевая аудитория: %s

	ПРАВИЛА ДЛЯ НАЗВАНИЯ (title, максимум 80 символов):
	Краткое и точное описание товара — 3-4 ключевых слова максимум.
	Структура: [тип изделия] [ключевое свойство] PlayToday [назначение].
	%s
	- Тип изделия: "платье", "костюм", "брюки", "футболка" и т.д.
	- Одно ключевое свойство: цвет, принт или ткань
	- Назначение если уместно: "для школы", "нарядное"
	- Бренд PlayToday
	НЕ перечисляй все свойства — выбери главное.

	ПРАВИЛА ДЛЯ ОПИСАНИЯ (description, максимум 500 символов):
	Тон — уверенный, лаконичный, про качество и стиль. Без восклицательных знаков.
	Без "вау", "must-have", "идеальный", "невероятный", "тот самый". Без marketing-клише.
	%s
	Длина 3-5 предложений, максимум 500 символов.

	ОБЩИЕ ПРАВИЛА:
	1. Используй ТОЛЬКО charcID из списка ниже. Не придумывай ID.
	2. Заполни обязательные и релевантные необязательные характеристики.
	3. НЕ заполняй: Описание, SKU, Бренд, Артикул, Рос. размер, Размер, Вес товара, упаковка, Код упаковки, ИКПУ, Артикул OZON, NTIN, Код ТРУ, Рост модели, Размер на модели, Параметры модели, ТНВЭД, Утеплитель.
	4. Цвет кратко: "зелёный/синий/оранжевый".
	5. ВСЕ тексты — ТОЛЬКО на русском. Никакого английского или китайского.
	6. Ответь ТОЛЬКО JSON, без markdown.
	7. Сезон бери ТОЛЬКО из Vision атрибутов — не придумывай.
	8. ОБЯЗАТЕЛЬНО сохрани из текущих характеристик: авторов принтов, названия серий, лицензионных персонажей (поле "Любимые герои", "Рисунок") — это точные данные, которые Vision не может определить по фото. Если указан автор (например Кандинский), обязательно перенеси его.

	Формат: {"title": "...", "description": "...", "characteristics": [{"charc_id": <число>, "value": "..."}]}`,
		subjectName, subjectID, audience, titleRules, descRules)

	charText := formatCharacteristics(chars)

	user := fmt.Sprintf(`Артикул: %s (nm_id=%d)

	ТЕКУЩИЕ ХАРАКТЕРИСТИКИ (справочно, МОГУТ СОДЕРЖАТЬ ОШИБКИ — не копируй вслепую, используй только как подсказку для сертификатов, состава, коллекции):
	%s

	VISION АНАЛИЗ (ФОТО — единственный источник истины):
	Тип изделия: %s
	Атрибуты: %s
	Замечания: %s
	Аудитория: %s

	ДОПУСТИМЫЕ ХАРАКТЕРИСТИКИ ПРЕДМЕТА "%s" (subject_id=%d):
	%s`,
		row.VendorCode, row.NmID,
		charText,
		row.VisionProductType, row.VisionAttributes, row.VisionSummary,
		seoContext,
		subjectName, subjectID, string(defsJSON))

	resp, err := provider.Generate(ctx,
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

	// Post-filter: выкинуть технические и проблемные charc_id
	skipCharcIDs := map[int]bool{
		54337: true, 88952: true, 90745: true, 90846: true, 90849: true,
		165482: true, 165505: true, 246961: true,
		14177446: true, 14177452: true, 14177453: true, 14177456: true,
		15001650: true, 15001706: true, 15003293: true,
		15003988: true, 15003989: true, 15003990: true, 15003991: true,
		15000001: true, 1010: true,
	}

	var charsRaw []any
	seen := make(map[int]bool)
	for _, c := range result.Characteristics {
		if skipCharcIDs[c.CharcID] || seen[c.CharcID] {
			continue
		}
		seen[c.CharcID] = true
		charsRaw = append(charsRaw, map[string]any{
			"charc_id": c.CharcID,
			"value":    c.Value,
		})
	}

	return result.Title, result.Description, charsRaw, subjectName, subjectID, nil
}

// parseAudienceFromVision извлекает аудиторию и пол из Vision attributes JSON.
func parseAudienceFromVision(attrsJSON string) (audience, gender string) {
	var attrs map[string]string
	if err := json.Unmarshal([]byte(attrsJSON), &attrs); err != nil {
		return "девочка (6-10)", "женский"
	}
	if a, ok := attrs["аудитория"]; ok && a != "" {
		audience = a
	} else {
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
func audiencePromptRules(audience, gender string) (titleRules, descRules, seoContext string) {
	a := strings.ToLower(audience)

	switch {
	case strings.Contains(a, "взрослая женщина") || strings.Contains(a, "женщин"):
		titleRules = `- Сезон из Vision (летнее, демисезонное, зимнее). НЕ придумывай "круглогодичное".
		- "для женщин", "женская"
		Пример: "Летние сабо женские PlayToday — эко-замша"`
		descRules = `- Пиши лаконично и стильно — женщина выбирает сама.
		- Первое предложение — про стиль и назначение вещи.
		- Упомяни бренд PlayToday.
		- Опиши сценарий: куда носить, с чем сочетать. Женщина ценит универсальность.
		- Упомяни уход: стирка, не мнётся, принт не выцветает.
		- НЕ перечисляй отсутствие чего-либо — только позитивные свойства.
		- Включи 2-3 SEO-фразы (женская, базовая, летняя, повседневная).`
		seoContext = "женская одежда, аудитория — взрослая женщина"
		return

	case strings.Contains(a, "взрослый мужч") || strings.Contains(a, "мужчин"):
		titleRules = `- Сезон из Vision (летнее, демисезонное, зимнее). НЕ придумывай "круглогодичное".
		- "мужская", "для мужчин"
		Пример: "Летняя мужская футболка PlayToday — хлопок, свободный крой"`
		descRules = `- Пиши прямо и по делу — мужчина ценит комфорт и функциональность.
		- Первое предложение — про комфорт: "лёгкая, удобная".
		- Упомяни бренд PlayToday.
		- Опиши сценарий: "для тренировки", "на дачу", "каждый день".
		- Упомяни уход: стирка, не мнётся, не садится.
		- Включи 2-3 SEO-фразы (мужская, спортивная, базовая).`
		seoContext = "мужская одежда, аудитория — взрослый мужчина"
		return

	case strings.Contains(a, "подросток") && (strings.Contains(a, "девочк") || gender == "женский"):
		titleRules = `- Сезон из Vision (летнее, демисезонное, зимнее). НЕ придумывай "круглогодичное".
		- "подростковая", "для девочки-подростка"
		Пример: "Подростковая футболка для девочки PlayToday — оверсайз, с принтом"`
		descRules = `- Пиши для мамы, но с оглядкой на подростка — она покупает, но дочь решает.
		- Первое предложение — стиль и тренд.
		- Упомяни бренд PlayToday.
		- Опиши сценарий: "в школу", "на встречу с друзьями". Подросток ценит самовыражение.
		- Упомяни уход: стирка, не мнётся, принт не выцветает.
		- Включи 2-3 SEO-фразы (подростковая, для девочки, школьная).`
		seoContext = "подростковая одежда для девочки 11-16 лет"
		return

	case strings.Contains(a, "подросток") && strings.Contains(a, "мальчик"):
		titleRules = `- Сезон из Vision (летнее, демисезонное, зимнее). НЕ придумывай "круглогодичное".
		- "подростковая", "для мальчика-подростка"
		Пример: "Подростковый костюм для мальчика PlayToday — толстовка и джоггеры"`
		descRules = `- Пиши для мамы, но с оглядкой на подростка — она покупает, но сын решает.
		- Первое предложение — комфорт и стиль.
		- Упомяни бренд PlayToday.
		- Опиши сценарий: "в школу", "на тренировку". Подросток-мальчик ценит, когда вещь не "детская".
		- Упомяни уход: стирка, не мнётся, принт не выцветает.
		- Включи 2-3 SEO-фразы (подростковая, для мальчика, спортивная).`
		seoContext = "подростковая одежда для мальчика 11-16 лет"
		return

	case strings.Contains(a, "малыш"):
		titleRules = `- Сезон из Vision (летнее, демисезонное, зимнее). НЕ придумывай "круглогодичное".
		- "для малышки", "малышковая"
		Пример: "Малышковое платье для девочки PlayToday — хлопок, с принтом"`
		descRules = `- Пиши для мамы — ей важны мягкость и комфорт.
		- Первое предложение — про комфорт и материал.
		- Упомяни бренд PlayToday.
		- Опиши сценарий: "для прогулки", "в садик".
		- Упомяни уход: стирка, гипоаллергенно, не линяет.
		- Включи 2-3 SEO-фразы (малышковая, для малышки, детская).`
		seoContext = "малышковая одежда (2-5 лет)"
		return

	default:
		// Девочка/мальчик 6-10 — default
		titleRules = `- Сезон из Vision (летнее, демисезонное, зимнее). НЕ придумывай "круглогодичное".
		- Кому: "для девочки", "для мальчика"
		Пример: "Летнее платье для девочки PlayToday — джерси с принтом"`
		descRules = `- Пиши для мамы — лаконично, про качество и стиль.
		- Первое предложение — про назначение и материал.
		- Упомяни бренд PlayToday.
		- Опиши сценарий: "в школу", "на прогулку", "на праздник".
		- Упомяни уход: стирка, не мнётся, принт не выцветает.
		- НЕ перечисляй отсутствие чего-либо — только позитивные свойства.
		- Включи 2-3 SEO-фразы (детское, нарядное, школьное).`
		seoContext = "детская одежда (6-10 лет)"
		return
	}
}

// loadCardsMap загружает map nm_id → CardData для получения subject_id.
func loadCardsMap(ctx context.Context, source *SourceRepo) (map[int]CardData, error) {
	cards, err := source.LoadCardsForAnalysis(ctx, FilterConfig{})
	if err != nil {
		return nil, err
	}
	m := make(map[int]CardData, len(cards))
	for _, c := range cards {
		m[c.NmID] = c
	}
	return m, nil
}

// defaultCharDictPath возвращает путь к кэшу характеристик.
func defaultCharDictPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cache", "poncho", "char-dict-cache.db")
}
