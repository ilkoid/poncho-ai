package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/llm"
	"github.com/ilkoid/poncho-ai/pkg/llm/openai"
	"github.com/ilkoid/poncho-ai/pkg/progress"
)

// runStage1Audit выполняет единый анализ карточек: текст + фото (этап 1).
func runStage1Audit(ctx context.Context, source *SourceRepo, results *ResultsRepo, provider llm.Provider, cfg CLIConfig) error {
	// Статистика фильтрации: total в базе
	totalInDB, _ := source.CountCards(ctx)

	// Загружаем nm_id проблемных карточек, если включен фильтр Problems
	if cfg.Filter.Problems.AnyDiscrepancy || cfg.Filter.Problems.HasParseErrors || cfg.Filter.Problems.PendingWBUpdate {
		problemIDs, err := results.LoadProblemNmIDs(ctx, cfg.Filter.Problems)
		if err != nil {
			return fmt.Errorf("load problem nm_ids: %w", err)
		}
		if len(problemIDs) == 0 {
			log.Println("No problem cards found matching the criteria")
			return nil
		}
		// Переопределяем фильтр NmIDs, чтобы загрузить только проблемные карточки
		cfg.Filter.NmIDs = problemIDs
	}

	// Загружаем карточки с фильтрацией
	cards, err := source.LoadCardsForAnalysis(ctx, cfg.Filter)
	if err != nil {
		return fmt.Errorf("load cards: %w", err)
	}
	if len(cards) == 0 {
		log.Println("No cards found for analysis")
		return nil
	}

	// ЛОГИЧЕСКИЙ ФИКС: Сначала создаем строки в БД и подтягиваем метрики!
	inserted, err := results.EnsureRows(ctx, cards)
	if err != nil {
		return fmt.Errorf("ensure rows: %w", err)
	}
	log.Printf("  Created %d new rows in card_analysis", inserted)

	if n, err := results.BackfillMetrics(ctx, cfg.Source.DBPath); err != nil {
		log.Printf("  WARN: backfill metrics: %v", err)
	} else if n > 0 {
		log.Printf("  Backfilled metrics: %d updates", n)
	}

	// Фильтрация по порогам рейтингов/видимости (только худшие)
	if cfg.Filter.hasThresholds() {
		nmIDs := make([]int, len(cards))
		for i, c := range cards {
			nmIDs[i] = c.NmID
		}
		filtered, err := results.FilterByThresholds(ctx, nmIDs, cfg.Filter)
		if err != nil {
			return fmt.Errorf("filter thresholds: %w", err)
		}
		filterSet := make(map[int]bool, len(filtered))
		for _, id := range filtered {
			filterSet[id] = true
		}
		before := len(cards)
		var kept []CardData
		for _, c := range cards {
			if filterSet[c.NmID] {
				kept = append(kept, c)
			}
		}
		cards = kept
		log.Printf("  Thresholds: %d → %d cards", before, len(cards))
		if len(cards) == 0 {
			log.Println("No cards pass threshold filter")
			return nil
		}
	}

	afterLimit := len(cards)
	if cfg.Analysis.Limit > 0 && len(cards) > cfg.Analysis.Limit {
		cards = cards[:cfg.Analysis.Limit]
		log.Printf("  Limit: %d → %d", afterLimit, len(cards))
	}

	log.Printf("Stage 1: analyzing %d cards with %s", len(cards), cfg.Text.Model)

	// Собираем nm_id для загрузки характеристик
	nmIDs := make([]int, len(cards))
	for i, c := range cards {
		nmIDs[i] = c.NmID
	}

	// Логируем статистику фильтрации
	filterDesc := describeFilter(cfg.Filter, totalInDB, len(cards))
	log.Printf("  Filter: %s", filterDesc)
	if cfg.Filter.InStock {
		sd := source.LoadLatestStockDate(ctx)
		log.Printf("  In-stock: snapshot date %s", sd)
	}

	// Загружаем характеристики
	charsMap, err := source.LoadCharacteristics(ctx, nmIDs)
	if err != nil {
		return fmt.Errorf("load characteristics: %w", err)
	}

	// Загружаем фото для Vision анализа
	photosMap, err := source.LoadPhotos(ctx, nmIDs, cfg.Vision.PhotosPerCard)
	if err != nil {
		return fmt.Errorf("load photos: %w", err)
	}

	// Resume: пропускаем карточки, уже обработанные Stage 1 Audit
	pending, err := results.LoadPendingAuditCards(ctx, nmIDs)
	if err != nil {
		return fmt.Errorf("load pending audit cards: %w", err)
	}
	pendingSet := make(map[int]bool, len(pending))
	for _, id := range pending {
		pendingSet[id] = true
	}
	var filtered []CardData
	for _, c := range cards {
		if pendingSet[c.NmID] {
			filtered = append(filtered, c)
		}
	}
	log.Printf("  Resume: %d already done, %d pending", len(cards)-len(filtered), len(filtered))
	cards = filtered

	if len(cards) == 0 {
		log.Println("  All cards already processed")
		return nil
	}

	// Прогресс с ETA
	tracker := progress.NewCLITrackerWithConfig(progress.CLITrackerConfig{
		Total:   len(cards),
		Prefix:  "Stage 1",
	})

	var (
		wg         sync.WaitGroup
		semaphore  = make(chan struct{}, cfg.Analysis.Concurrency)
		discrepant int
		errors     int
		mu         sync.Mutex
	)

	for _, card := range cards {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		wg.Add(1)
		go func(c CardData) {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			start := time.Now()
			chars := charsMap[c.NmID]
			photos := photosMap[c.NmID]

			if len(photos) == 0 {
				mu.Lock()
				errors++
				mu.Unlock()
				log.Printf("  WARN nm_id=%d: no photos found, skipping", c.NmID)
				// We don't mark it done, or maybe we should? Let's just return to skip
				return
			}

			hasDisc, productType, attributes, summary, parseErr, err := analyzeCardUnified(ctx, provider, c, chars, photos, cfg.Vision, cfg.Prompts)
			dur := time.Since(start)

			if err != nil {
				mu.Lock()
				errors++
				mu.Unlock()
				log.Printf("  ERROR nm_id=%d vendor_code=%s: %v", c.NmID, c.VendorCode, err)
				return
			}

			if parseErr != nil {
				summary = fmt.Sprintf("PARSE ERROR: %v | Raw: %s", parseErr, summary)
				// Увеличиваем счетчик ошибок для защиты от бесконечных циклов
				if incErr := results.IncrementErrorCount(ctx, c.NmID); incErr != nil {
					log.Printf("  ERROR increment error count nm_id=%d: %v", c.NmID, incErr)
				}
			}

			attrsJSON, _ := json.Marshal(attributes)
			photosJSON, _ := json.Marshal(photos)

			// Сохраняем результаты в колонки vision_* (так как они теперь используются для единого аудита)
			if err := results.SaveVisionAnalysis(ctx, c.NmID, productType, string(attrsJSON), string(photosJSON), summary, hasDisc); err != nil {
				mu.Lock()
				errors++
				mu.Unlock()
				log.Printf("  ERROR save nm_id=%d: %v", c.NmID, err)
				return
			}

			// Если была ошибка парсинга, не помечаем как done, чтобы карточка ушла на ретрай (пока error_count < 3)
			if parseErr == nil {
				if err := results.MarkAuditDone(ctx, c.NmID); err != nil {
					mu.Lock()
					errors++
					mu.Unlock()
					log.Printf("  ERROR mark done nm_id=%d: %v", c.NmID, err)
					return
				}
			}

			tracker.Update(1)

			mu.Lock()
			n := tracker.Current()
			if hasDisc {
				discrepant++
			}
			discStr := "ok"
			if hasDisc {
				discStr = "DISCREPANCY"
			}
			log.Printf("  [%d/%d] %s | %.1fs | %s | ETA %s",
				n, tracker.Total(),
				time.Now().Format("15:04:05"),
				dur.Seconds(),
				discStr,
				tracker.ETA())
			mu.Unlock()
		}(card)
	}

	wg.Wait()
	tracker.Done()

	log.Printf("Stage 1 complete: %d checked, %d discrepancies (%.0f%%), %d errors",
		tracker.Current(), discrepant,
		percent(int64(discrepant), int64(tracker.Current())),
		errors)

	return nil
}

// analyzeCardUnified отправляет карточку (текст + фото) в LLM и парсит результат.
func analyzeCardUnified(ctx context.Context, provider llm.Provider, card CardData, chars []CardChar, photos []string, modelCfg VisionConfig, prompts PromptConfig) (bool, string, map[string]string, string, error, error) {
	// Очищаем характеристики от мусора для экономии токенов
	var cleanChars []CardChar
	for _, c := range chars {
		if !skipCharcIDs[c.CharID] {
			cleanChars = append(cleanChars, c)
		}
	}

	messages := buildAuditMessages(card.Title, card.Description, cleanChars, photos, prompts)

	resp, err := generateWithRetry(ctx, provider, messages,
		llm.WithModel(modelCfg.Model),
		llm.WithTemperature(modelCfg.Temperature),
		llm.WithMaxTokens(modelCfg.MaxTokens),
	)
	if err != nil {
		return false, "", nil, "", nil, fmt.Errorf("LLM call: %w", err)
	}

	content := strings.TrimSpace(resp.Content)
	if content == "" {
		return false, "", nil, "", nil, fmt.Errorf("empty LLM response")
	}

	var result visionAnalysisResult
	if err := json.Unmarshal([]byte(extractJSON(content)), &result); err != nil {
		return false, "", nil, truncate(content, 200), err, nil
	}

	return result.Discrepancy, result.ProductType, result.Attributes, result.Summary, nil, nil
}

// extractJSON извлекает JSON объект из ответа LLM (может быть обёрнут в markdown).
func extractJSON(s string) string {
	if idx := strings.Index(s, "```json"); idx != -1 {
		s = s[idx+7:]
		if end := strings.Index(s, "```"); end != -1 {
			s = s[:end]
		}
	} else if idx := strings.Index(s, "```"); idx != -1 {
		s = s[idx+3:]
		if end := strings.Index(s, "```"); end != -1 {
			s = s[:end]
		}
	}

	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start != -1 && end > start {
		return s[start : end+1]
	}
	return s
}

// describeFilter формирует строку описания фильтрации для лога.
func describeFilter(f FilterConfig, totalInDB, filtered int) string {
	if totalInDB > 0 {
		return fmt.Sprintf("%d total → %d filtered", totalInDB, filtered)
	}
	return fmt.Sprintf("%d filtered", filtered)
}

func percent(part, total int64) float64 {
	if total == 0 {
		return 0
	}
	return float64(part) * 100 / float64(total)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

func createProvider(cfg CLIConfig) (llm.Provider, error) {
	modelDef := cfg.toModelDef()
	client := openai.NewClient(modelDef)
	if client == nil {
		return nil, fmt.Errorf("failed to create LLM provider (check base_url in config)")
	}
	return client, nil
}

func createVisionProvider(cfg CLIConfig) (llm.Provider, error) {
	modelDef := cfg.toVisionModelDef()
	client := openai.NewClient(modelDef)
	if client == nil {
		return nil, fmt.Errorf("failed to create Vision provider (check base_url in config)")
	}
	return client, nil
}

func generateWithRetry(ctx context.Context, provider llm.Provider, messages []llm.Message, opts ...any) (llm.Message, error) {
	var resp llm.Message
	err := retryWithBackoff(ctx, 3, func() error {
		var err error
		resp, err = provider.Generate(ctx, messages, opts...)
		return err
	})
	return resp, err
}

func retryWithBackoff(ctx context.Context, maxRetries int, fn func() error) error {
	var lastErr error
	for i := 0; i < maxRetries; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err := fn(); err != nil {
			lastErr = err
			backoff := time.Duration(i+1) * 2 * time.Second
			log.Printf("    Retry %d/%d after %v: %v", i+1, maxRetries, backoff, err)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
			continue
		}
		return nil
	}
	return fmt.Errorf("max retries exceeded: %w", lastErr)
}
