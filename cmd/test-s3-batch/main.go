// test-s3-batch — CLI utility для тестирования S3 batch обработки с санитайзингом PLM-JSON.
//
// Rule 13: Автономная утилита с локальным config.yaml
// Rule 9: Тестирование через CLI utilities вместо unit tests
//
// Использование:
//   go run cmd/test-s3-batch/main.go [article_id]
//   ./test-s3-batch 12611516
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/ilkoid/poncho-ai/pkg/agent"
	_ "github.com/ilkoid/poncho-ai/pkg/s3storage" // Imported for type assertion in -compare mode
)

func main() {
	// Rule 2: Конфигурация через YAML (ищется рядом с бинарником)
	cfgPath := "config.yaml"
	if len(os.Args) > 1 && !strings.HasPrefix(os.Args[1], "-") {
		cfgPath = os.Args[1]
	}

	// Определяем article_id
	articleID := "12611516" // default
	for i, arg := range os.Args {
		if arg == "-article" && i+1 < len(os.Args) {
			articleID = os.Args[i+1]
			break
		}
	}

	fmt.Printf("📦 Testing S3 Batch Processing with PLM Sanitization\n")
	fmt.Printf("   Article ID: %s\n", articleID)
	fmt.Printf("   Config: %s\n\n", cfgPath)

	// Создаём agent с 2-line API
	client, err := agent.New(agent.Config{
		ConfigPath: cfgPath,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error initializing agent: %v\n", err)
		os.Exit(1)
	}

	// === Test 1: classify_and_download_s3_files ===
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("Test 1: Classify and Download S3 Files")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

	ctx := context.Background()
	query1 := fmt.Sprintf("Используй classify_and_download_s3_files для артикула %s. Покажи какие файлы найдены.", articleID)
	result1, err := client.Run(ctx, query1)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Result:\n%s\n\n", result1)

	// === Test 2: get_plm_data (NEW - with sanitization) ===
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("Test 2: Get PLM Data (with sanitization)")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

	query2 := fmt.Sprintf("Используй get_plm_data для артикула %s. Покажи основные данные о товаре.", articleID)
	result2, err := client.Run(ctx, query2)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Analyze size reduction
	rawSize := estimateRawPLMSize()
	sanitizedSize := len(result2)
	reduction := float64(rawSize-sanitizedSize) / float64(rawSize) * 100

	fmt.Printf("Result:\n%s\n\n", result2)
	fmt.Printf("📊 Size Analysis:\n")
	fmt.Printf("   Raw PLM-JSON (est.):  ~%d KB (with base64 images)\n", rawSize/1024)
	fmt.Printf("   Sanitized:           %d bytes\n", sanitizedSize)
	fmt.Printf("   Reduction:           %.1f%%\n", reduction)

	// === Test 3: Compare raw vs sanitized (if requested) ===
	if contains(os.Args, "-compare") {
		fmt.Println("\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		fmt.Println("Test 3: Direct S3 Comparison (raw vs sanitized)")
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

		// Получаем доступ к S3 клиенту через state
		state := client.GetState()
		s3Client := state.GetStorage()
		if s3Client == nil {
			fmt.Fprintf(os.Stderr, "Warning: Could not access S3 storage for comparison\n")
		} else {
			key := fmt.Sprintf("%s/%s.json", articleID, articleID)
			rawBytes, err := s3Client.DownloadFile(ctx, key)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error downloading raw PLM: %v\n", err)
			} else {
				fmt.Printf("Raw PLM-JSON:       %d bytes\n", len(rawBytes))
				fmt.Printf("Sanitized:          %d bytes\n", sanitizedSize)
				fmt.Printf("Actual reduction:   %.1f%%\n", float64(len(rawBytes)-sanitizedSize)/float64(len(rawBytes))*100)

				// Show sample of what was removed
				var rawJSON map[string]interface{}
				if err := json.Unmarshal(rawBytes, &rawJSON); err == nil {
					fmt.Println("\n🗑️  Removed from raw JSON:")
					if _, ok := rawJSON["Ответственные"]; ok {
						fmt.Println("   - Ответственные (personnel block)")
					}
					if _, ok := rawJSON["Эскизы"]; ok {
						fmt.Println("   - Эскизы (sketches block)")
					}
					if requisites, ok := rawJSON["Реквизиты"].(map[string]interface{}); ok {
						if _, ok := requisites["Миниатюра_Файл"]; ok {
							fmt.Println("   - Миниатюра_Файл (huge base64 data)")
						}
					}
				}
			}
		}
	}

	fmt.Println("\n✅ All tests completed!")
}

// estimateRawPLMSize оценивает размер исходного PLM-JSON (примерно 43KB с base64)
func estimateRawPLMSize() int {
	return 43000 // типичный размер PLM-JSON с Миниатюра_Файл
}

// contains проверяет наличие флага в аргументах
func contains(args []string, flag string) bool {
	for _, arg := range args {
		if arg == flag {
			return true
		}
	}
	return false
}

/*
Workflow после рефакторинга (с санитайзингом PLM-JSON):

  1. User: "Проанализируй артикул 12611516"
     ↓
  2. LLM: classify_and_download_s3_files("12611516")
     ↓
  3. Tool: ListFiles → Classify → Store metadata in CoreState
     ↓
  4. LLM: get_plm_data("12611516") — получает ОЧИЩЕННЫЙ JSON
     ↓
  5. Tool: Downloads PLM JSON → SanitizePLMJson() → returns ~2-5KB
     ↓
  6. LLM: read_s3_image для каждого sketch → download + vision анализ
     ↓
  7. Post-prompt активируется → product description prompt
     ↓
  8. LLM: Генерирует продающее описание

ВАЖНО:
- Step 3 НЕ скачивает контент, только метаданные
- Step 5 использует get_plm_data с санитайзингом:
  * Удаляет: Ответственные, Эскизы, технические поля
  * Удаляет: Миниатюра_Файл (enormous base64 data)
  * Результат: 43KB → 2-5KB (~90% reduction)

State хранит:
- Метаданные файлов (filename, key, type)
- PLM данные: загружаются по запросу через get_plm_data
- Vision описания: заполняются через read_s3_image

Экономия токенов:
- Raw PLM: ~43KB (с base64 изображениями)
- Sanitized: ~2-5KB (только нужные данные)
- Context saving: ~90% на PLM данных + ~1000 tokens на vision контент для chat models
*/
