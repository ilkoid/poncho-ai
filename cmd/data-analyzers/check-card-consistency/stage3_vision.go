package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/ilkoid/poncho-ai/pkg/llm"
)

// runStage3 выполняет Vision анализ рискованных карточек (этап 3).
func runStage3(ctx context.Context, source *SourceRepo, results *ResultsRepo, provider llm.Provider, cfg CLIConfig) error {
	// Загружаем nm_id карточек с text_has_discrepancy = 1
	nmIDs, err := results.LoadTextDiscrepancies(ctx)
	if err != nil {
		return fmt.Errorf("load text discrepancies: %w", err)
	}
	if len(nmIDs) == 0 {
		log.Println("Stage 3: no text discrepancies found. Run stage 1 first.")
		return nil
	}

	if cfg.Analysis.Limit > 0 && len(nmIDs) > cfg.Analysis.Limit {
		nmIDs = nmIDs[:cfg.Analysis.Limit]
	}

	log.Printf("Stage 3: Vision analysis for %d cards with %s", len(nmIDs), cfg.Vision.Model)

	// Загружаем карточки для получения title/description
	cards, err := source.LoadCardsForAnalysis(ctx, FilterConfig{}) // no filter — we have nmIDs
	if err != nil {
		return fmt.Errorf("load cards: %w", err)
	}
	cardMap := make(map[int]CardData, len(cards))
	for _, c := range cards {
		cardMap[c.NmID] = c
	}

	// Загружаем характеристики
	charsMap, err := source.LoadCharacteristics(ctx, nmIDs)
	if err != nil {
		return fmt.Errorf("load characteristics: %w", err)
	}

	// Загружаем фото
	photosMap, err := source.LoadPhotos(ctx, nmIDs, cfg.Vision.PhotosPerCard)
	if err != nil {
		return fmt.Errorf("load photos: %w", err)
	}

	var (
		wg          sync.WaitGroup
		semaphore   = make(chan struct{}, cfg.Analysis.Concurrency)
		total       atomic.Int64
		discrepant  atomic.Int64
		errors      atomic.Int64
	)

	for _, nmID := range nmIDs {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		card, ok := cardMap[nmID]
		if !ok {
			log.Printf("  WARN: nm_id=%d not found in source cards, skipping", nmID)
			continue
		}

		photoURLs := photosMap[nmID]
		if len(photoURLs) == 0 {
			log.Printf("  WARN: nm_id=%d has no photos, skipping", nmID)
			continue
		}

		wg.Add(1)
		go func(nmID int, card CardData) {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			chars := charsMap[nmID]
			photoURLs := photosMap[nmID]

			productType, attrs, summary, hasDisc, err := analyzeCardVision(ctx, provider, card, chars, photoURLs, cfg.Vision.ModelConfig)
			if err != nil {
				log.Printf("  ERROR nm_id=%d: %v", nmID, err)
				errors.Add(1)
				return
			}

			attrsJSON, _ := json.Marshal(attrs)
			photosJSON, _ := json.Marshal(photoURLs)

			if err := results.SaveVisionAnalysis(ctx, nmID, productType, string(attrsJSON), string(photosJSON), summary, hasDisc); err != nil {
				log.Printf("  ERROR save nm_id=%d: %v", nmID, err)
				errors.Add(1)
				return
			}

			n := total.Add(1)
			if hasDisc {
				discrepant.Add(1)
			}
			if n%10 == 0 || n == int64(len(nmIDs)) {
				log.Printf("  Progress: %d/%d (discrepancies: %d, errors: %d)",
					n, len(nmIDs), discrepant.Load(), errors.Load())
			}
		}(nmID, card)
	}

	wg.Wait()

	log.Printf("Stage 3 complete: %d checked, %d confirmed discrepancies (%.0f%%), %d errors",
		total.Load(), discrepant.Load(),
		percent(discrepant.Load(), total.Load()),
		errors.Load())

	return nil
}

// analyzeCardVision отправляет карточку с фото в Vision модель.
func analyzeCardVision(ctx context.Context, provider llm.Provider, card CardData, chars []CardChar, photoURLs []string, modelCfg ModelConfig) (string, map[string]string, string, bool, error) {
	messages := buildVisionMessages(card.Title, card.Description, chars, photoURLs)

	resp, err := provider.Generate(ctx, messages,
		llm.WithModel(modelCfg.Model),
		llm.WithTemperature(modelCfg.Temperature),
		llm.WithMaxTokens(modelCfg.MaxTokens),
	)
	if err != nil {
		return "", nil, "", false, fmt.Errorf("Vision LLM call: %w", err)
	}

	content := strings.TrimSpace(resp.Content)
	if content == "" {
		return "", nil, "", false, fmt.Errorf("empty Vision response")
	}

	var result visionAnalysisResult
	if err := json.Unmarshal([]byte(extractJSON(content)), &result); err != nil {
		return "", nil, truncate(content, 200), false, nil
	}

	return result.ProductType, result.Attributes, result.Summary, result.Discrepancy, nil
}
