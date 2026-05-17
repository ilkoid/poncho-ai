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

// runStage1 выполняет текстовый анализ карточек: description vs characteristics (этап 1).
func runStage1(ctx context.Context, source *SourceRepo, results *ResultsRepo, provider llm.Provider, cfg CLIConfig) error {
	// Статистика фильтрации: total в базе
	totalInDB, _ := source.CountCards(ctx)

	// Загружаем карточки с фильтрацией
	cards, err := source.LoadCardsForAnalysis(ctx, cfg.Filter)
	if err != nil {
		return fmt.Errorf("load cards: %w", err)
	}
	if len(cards) == 0 {
		log.Println("No cards found for analysis")
		return nil
	}

	// Логируем статистику фильтрации
	filterDesc := describeFilter(cfg.Filter, totalInDB, len(cards))
	log.Printf("  Filter: %s", filterDesc)
	if cfg.Filter.InStock {
		sd := source.LoadLatestStockDate(ctx)
		log.Printf("  In-stock: snapshot date %s", sd)
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

	// Загружаем характеристики
	charsMap, err := source.LoadCharacteristics(ctx, nmIDs)
	if err != nil {
		return fmt.Errorf("load characteristics: %w", err)
	}

	// Создаём строки в results DB
	inserted, err := results.EnsureRows(ctx, cards)
	if err != nil {
		return fmt.Errorf("ensure rows: %w", err)
	}
	log.Printf("  Created %d new rows in card_analysis", inserted)

	// Resume: пропускаем карточки, уже обработанные Stage 1
	pending, err := results.LoadPendingTextCards(ctx, nmIDs)
	if err != nil {
		return fmt.Errorf("load pending text cards: %w", err)
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
			hasDisc, summary, err := analyzeCardText(ctx, provider, c, chars, cfg.Text, cfg.Prompts)
			dur := time.Since(start)

			if err != nil {
				mu.Lock()
				errors++
				mu.Unlock()
				log.Printf("  ERROR nm_id=%d vendor_code=%s: %v", c.NmID, c.VendorCode, err)
				return
			}

			if err := results.SaveTextAnalysis(ctx, c.NmID, hasDisc, summary); err != nil {
				mu.Lock()
				errors++
				mu.Unlock()
				log.Printf("  ERROR save nm_id=%d: %v", c.NmID, err)
				return
			}

			if err := results.MarkTextDone(ctx, c.NmID); err != nil {
				mu.Lock()
				errors++
				mu.Unlock()
				log.Printf("  ERROR mark done nm_id=%d: %v", c.NmID, err)
				return
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

// analyzeCardText отправляет карточку в LLM и парсит результат.
func analyzeCardText(ctx context.Context, provider llm.Provider, card CardData, chars []CardChar, modelCfg ModelConfig, prompts PromptConfig) (bool, string, error) {
	messages := buildTextAnalysisMessages(card.Title, card.Description, chars, prompts)

	resp, err := generateWithRetry(ctx, provider, messages,
		llm.WithModel(modelCfg.Model),
		llm.WithTemperature(modelCfg.Temperature),
		llm.WithMaxTokens(modelCfg.MaxTokens),
	)
	if err != nil {
		return false, "", fmt.Errorf("LLM call: %w", err)
	}

	content := strings.TrimSpace(resp.Content)
	if content == "" {
		return false, "", fmt.Errorf("empty LLM response")
	}

	var result textAnalysisResult
	if err := json.Unmarshal([]byte(extractJSON(content)), &result); err != nil {
		return false, truncate(content, 200), nil
	}

	return result.Discrepancy, result.Summary, nil
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
