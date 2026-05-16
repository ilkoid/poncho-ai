package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/llm"
	"github.com/ilkoid/poncho-ai/pkg/llm/openai"
)

// runStage1 выполняет текстовый анализ карточек: description vs characteristics (этап 1).
func runStage1(ctx context.Context, source *SourceRepo, results *ResultsRepo, provider llm.Provider, cfg CLIConfig) error {
	// Загружаем карточки с фильтрацией
	cards, err := source.LoadCardsForAnalysis(ctx, cfg.Filter)
	if err != nil {
		return fmt.Errorf("load cards: %w", err)
	}
	if len(cards) == 0 {
		log.Println("No cards found for analysis")
		return nil
	}

	if cfg.Analysis.Limit > 0 && len(cards) > cfg.Analysis.Limit {
		cards = cards[:cfg.Analysis.Limit]
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

	// Параллельный анализ с semaphore
	var (
		wg         sync.WaitGroup
		semaphore  = make(chan struct{}, cfg.Analysis.Concurrency)
		total      atomic.Int64
		discrepant atomic.Int64
		errors     atomic.Int64
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

			chars := charsMap[c.NmID]
			hasDisc, summary, err := analyzeCardText(ctx, provider, c, chars, cfg.Text)
			if err != nil {
				log.Printf("  ERROR nm_id=%d vendor_code=%s: %v", c.NmID, c.VendorCode, err)
				errors.Add(1)
				return
			}

			if err := results.SaveTextAnalysis(ctx, c.NmID, hasDisc, summary); err != nil {
				log.Printf("  ERROR save nm_id=%d: %v", c.NmID, err)
				errors.Add(1)
				return
			}

			if err := results.MarkTextDone(ctx, c.NmID); err != nil {
				log.Printf("  ERROR mark done nm_id=%d: %v", c.NmID, err)
				errors.Add(1)
				return
			}

			n := total.Add(1)
			if hasDisc {
				discrepant.Add(1)
			}
			if n%50 == 0 || n == int64(len(cards)) {
				log.Printf("  Progress: %d/%d (discrepancies: %d, errors: %d)",
					n, len(cards), discrepant.Load(), errors.Load())
			}
		}(card)
	}

	wg.Wait()

	log.Printf("Stage 1 complete: %d checked, %d discrepancies (%.0f%%), %d errors",
		total.Load(), discrepant.Load(),
		percent(discrepant.Load(), total.Load()),
		errors.Load())

	return nil
}

// analyzeCardText отправляет карточку в LLM и парсит результат.
func analyzeCardText(ctx context.Context, provider llm.Provider, card CardData, chars []CardChar, modelCfg ModelConfig) (bool, string, error) {
	messages := buildTextAnalysisMessages(card.Title, card.Description, chars)

	resp, err := provider.Generate(ctx, messages,
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
		// Если JSON не парсится — считаем что LLM не нашла расхождений
		return false, truncate(content, 200), nil
	}

	return result.Discrepancy, result.Summary, nil
}

// extractJSON извлекает JSON объект из ответа LLM (может быть обёрнут в markdown).
func extractJSON(s string) string {
	// Попробуем найти JSON в markdown code block
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

	// Найдём первый { и последний }
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start != -1 && end > start {
		return s[start : end+1]
	}
	return s
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

// createProvider создаёт LLM провайдер из конфигурации.
// Переиспользует pkg/llm/openai.NewClient — никакого дублирования.
func createProvider(cfg CLIConfig) (llm.Provider, error) {
	modelDef := cfg.toModelDef()
	client := openai.NewClient(modelDef)
	if client == nil {
		return nil, fmt.Errorf("failed to create LLM provider (check base_url in config)")
	}
	return client, nil
}

// createVisionProvider создаёт Vision-провайдер из конфигурации.
func createVisionProvider(cfg CLIConfig) (llm.Provider, error) {
	modelDef := cfg.toVisionModelDef()
	client := openai.NewClient(modelDef)
	if client == nil {
		return nil, fmt.Errorf("failed to create Vision provider (check base_url in config)")
	}
	return client, nil
}

// retryWithBackoff выполняет функцию с retry и exponential backoff.
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
