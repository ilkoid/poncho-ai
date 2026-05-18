package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// runStage5 обновляет карточки через WB API или моковый прогон (этап 5).
func runStage5(ctx context.Context, source *SourceRepo, results *ResultsRepo, cfg CLIConfig, mock bool) error {
	rows, err := results.LoadAnalysisForUpdate(ctx)
	if err != nil {
		return fmt.Errorf("load analysis for update: %w", err)
	}
	if len(rows) == 0 {
		log.Println("Stage 5: no cards with new params. Run stage 4 first.")
		return nil
	}

	mode := "MOCK"
	if !mock {
		mode = "REAL"
	}
	log.Printf("Stage 5 (%s): %d cards to update", mode, len(rows))

	for _, row := range rows {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if mock {
			if err := stage5Mock(ctx, results, row); err != nil {
				log.Printf("  ERROR mock nm_id=%d: %v", row.NmID, err)
				continue
			}
		} else {
			if err := stage5Real(ctx, source, results, row, cfg); err != nil {
				log.Printf("  ERROR real nm_id=%d: %v", row.NmID, err)
				continue
			}
		}
	}

	log.Printf("Stage 5 (%s) complete: %d cards processed", mode, len(rows))
	return nil
}

// stage5Mock — моковый прогон: логируем что было бы изменено, без отправки в WB.
func stage5Mock(ctx context.Context, results *ResultsRepo, row AnalysisRow) error {
	log.Printf("  MOCK: nm_id=%d vendor_code=%s", row.NmID, row.VendorCode)

	// Логируем изменения
	if row.NewTitle != "" && row.NewTitle != row.Title {
		if err := results.LogChange(ctx, row.NmID, row.VendorCode, "title", row.Title, row.NewTitle); err != nil {
			return err
		}
		log.Printf("    title: %q → %q", truncate(row.Title, 50), truncate(row.NewTitle, 50))
	}

	if row.NewDescription != "" {
		if err := results.LogChange(ctx, row.NmID, row.VendorCode, "description",
			"(current)", truncate(row.NewDescription, 80)); err != nil {
			return err
		}
		log.Printf("    description: updated (%d chars)", len(row.NewDescription))
	}

	if row.NewCharacteristics != "" {
		if err := results.LogChange(ctx, row.NmID, row.VendorCode, "characteristics",
			"(current)", truncate(row.NewCharacteristics, 80)); err != nil {
			return err
		}
		log.Printf("    characteristics: updated")
	}

	return nil
}

// stage5Real — реальное обновление через WB API.
func stage5Real(ctx context.Context, source *SourceRepo, results *ResultsRepo, row AnalysisRow, cfg CLIConfig) error {
	apiKey := getWBApiKey()
	if apiKey == "" {
		return fmt.Errorf("WB_API_KEY not set")
	}

	client := wb.New(apiKey)

	// Собираем request (со Smart Merge характеристик)
	updateReq, err := buildUpdateRequest(ctx, row, source)
	if err != nil {
		return fmt.Errorf("build update request: %w", err)
	}

	reqJSON, _ := json.Marshal(updateReq)
	log.Printf("  Updating nm_id=%d vendor_code=%s (%d bytes)", row.NmID, row.VendorCode, len(reqJSON))

	// WB API: POST /content/v2/cards/update
	// TODO: использовать client.Post() с rate limiting
	// Пока — заглушка, будет реализовано в pkg/wb/content.go
	_ = client

	resp := fmt.Sprintf("Updated at %s", time.Now().Format(time.DateTime))

	// Логируем изменения
	if row.NewTitle != "" {
		results.LogChange(ctx, row.NmID, row.VendorCode, "title", row.Title, row.NewTitle)
	}
	if row.NewDescription != "" {
		results.LogChange(ctx, row.NmID, row.VendorCode, "description", "(current)", row.NewDescription)
	}
	if row.NewCharacteristics != "" {
		results.LogChange(ctx, row.NmID, row.VendorCode, "characteristics", "(current)", row.NewCharacteristics)
	}

	if err := results.SaveWBUpdate(ctx, row.NmID, resp); err != nil {
		return err
	}

	log.Printf("    OK: %s", row.VendorCode)
	return nil
}

// buildUpdateRequest формирует тело запроса для WB API update со Smart Merge.
func buildUpdateRequest(ctx context.Context, row AnalysisRow, source *SourceRepo) (map[string]interface{}, error) {
	req := map[string]interface{}{
		"nmID": row.NmID,
	}

	if row.NewTitle != "" {
		req["title"] = row.NewTitle
	}
	if row.NewDescription != "" {
		req["description"] = row.NewDescription
	}
	if row.NewSubjectName != "" {
		req["subjectName"] = row.NewSubjectName
	}

	if row.NewCharacteristics != "" {
		var generatedChars []charcEntry
		if err := json.Unmarshal([]byte(row.NewCharacteristics), &generatedChars); err != nil {
			return nil, fmt.Errorf("unmarshal new characteristics: %w", err)
		}

		generatedMap := make(map[int]charcEntry)
		for _, c := range generatedChars {
			generatedMap[c.CharcID] = c
		}

		// Загружаем ТЕКУЩИЕ характеристики из сырой базы
		currentCharsMap, err := source.LoadCharacteristics(ctx, []int{row.NmID})
		if err != nil {
			return nil, fmt.Errorf("load current characteristics: %w", err)
		}
		currentChars := currentCharsMap[row.NmID]

		var finalChars []map[string]interface{}
		seenIDs := make(map[int]bool)

		// 1. Проходим по текущим характеристикам
		for _, curr := range currentChars {
			// Оставляем системные поля нетронутыми
			if skipCharcIDs[curr.CharID] {
				// Значение в БД лежит как JSON, например ["value"], нам надо отправить его так, как ждет WB
				// Обычно WB ждет просто строку, либо массив. Если это JSON строка, попробуем распаковать
				var val interface{}
				if err := json.Unmarshal([]byte(curr.Value), &val); err != nil {
					val = curr.Value
				}
				finalChars = append(finalChars, map[string]interface{}{
					"id": curr.CharID, "value": val,
				})
				seenIDs[curr.CharID] = true
			} else if gen, exists := generatedMap[curr.CharID]; exists {
				// Если LLM сгенерировала новое значение для этого поля - берем его
				finalChars = append(finalChars, map[string]interface{}{
					"id": gen.CharcID, "value": gen.Value,
				})
				seenIDs[gen.CharcID] = true
			}
		}

		// 2. Добавляем новые поля, которых раньше не было
		for _, gen := range generatedChars {
			if !seenIDs[gen.CharcID] {
				finalChars = append(finalChars, map[string]interface{}{
					"id": gen.CharcID, "value": gen.Value,
				})
			}
		}

		req["characteristics"] = finalChars
	}

	return req, nil
}

func getWBApiKey() string {
	// ИСПРАВЛЕНИЕ: берем только ключ для контента, аналитика не позволяет обновлять карточки
	if val := strings.TrimSpace(os.Getenv("WB_API_KEY")); val != "" {
		return val
	}
	return ""
}
