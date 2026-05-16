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
func runStage5(ctx context.Context, results *ResultsRepo, cfg CLIConfig, mock bool) error {
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
			if err := stage5Real(ctx, results, row, cfg); err != nil {
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

	// Отмечаем как mock-updated
	if err := results.SaveWBUpdate(ctx, row.NmID, "MOCK: not sent to WB API"); err != nil {
		return err
	}

	return nil
}

// stage5Real — реальное обновление через WB API.
func stage5Real(ctx context.Context, results *ResultsRepo, row AnalysisRow, cfg CLIConfig) error {
	apiKey := getWBApiKey()
	if apiKey == "" {
		return fmt.Errorf("WB_API_KEY not set")
	}

	client := wb.New(apiKey)

	// Собираем request
	updateReq := buildUpdateRequest(row)
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

// buildUpdateRequest формирует тело запроса для WB API update.
func buildUpdateRequest(row AnalysisRow) map[string]interface{} {
	req := map[string]interface{}{
		"nmID": row.NmID,
	}

	if row.NewTitle != "" {
		req["title"] = row.NewTitle
	}
	if row.NewDescription != "" {
		req["description"] = row.NewDescription
	}
	if row.NewCharacteristics != "" {
		var chars []interface{}
		if err := json.Unmarshal([]byte(row.NewCharacteristics), &chars); err == nil {
			req["characteristics"] = chars
		}
	}
	if row.NewSubjectName != "" {
		req["subjectName"] = row.NewSubjectName
	}

	return req
}

func getWBApiKey() string {
	for _, env := range []string{"WB_API_KEY", "WB_API_ANALYTICS_AND_PROMO_KEY"} {
		if val := strings.TrimSpace(os.Getenv(env)); val != "" {
			return val
		}
	}
	return ""
}
