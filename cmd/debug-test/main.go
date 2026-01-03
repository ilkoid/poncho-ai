// Debug-test — CLI утилита для тестирования JSON Debug Logs.
//
// Эта утилита демонстрирует работу pkg/debug пакета без необходимости
// запускать полноценный orchestrator.
//
// Правило 9: Используем CLI утилиту вместо тестов для проверки функционала.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/debug"
)

func main() {
	fmt.Println("=== Debug Logs Test Utility ===")
	fmt.Println()

	// 1. Загружаем конфигурацию
	configPath := getConfigPath()
	fmt.Printf("Loading config from: %s\n", configPath)

	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	// Применяем дефолтные значения для DebugConfig
	debugCfg := cfg.App.DebugLogs.GetDefaults()
	fmt.Printf("Debug logs enabled: %v\n", debugCfg.Enabled)
	fmt.Printf("Debug logs save: %v\n", debugCfg.SaveLogs)
	fmt.Printf("Debug logs dir: %s\n", debugCfg.LogsDir)
	fmt.Printf("Include tool args: %v\n", debugCfg.IncludeToolArgs)
	fmt.Printf("Include tool results: %v\n", debugCfg.IncludeToolResults)
	fmt.Printf("Max result size: %d\n", debugCfg.MaxResultSize)
	fmt.Println()

	// 2. Создаём Recorder
	recorder, err := debug.NewRecorder(debug.RecorderConfig{
		LogsDir:           debugCfg.LogsDir,
		IncludeToolArgs:   debugCfg.IncludeToolArgs,
		IncludeToolResults: debugCfg.IncludeToolResults,
		MaxResultSize:     debugCfg.MaxResultSize,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create recorder: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Recorder created with RunID: %s\n", recorder.GetRunID())
	fmt.Println()

	// 3. Симулируем выполнение агента
	testQuery := "найди все товары в категории Верхняя одежда"
	fmt.Printf("Simulating agent execution for query: %s\n", testQuery)
	fmt.Println()

	startTime := time.Now()
	recorder.Start(testQuery)

	// Итерация 1: LLM вызывает get_wb_parent_categories
	fmt.Println("--- Iteration 1 ---")
	recorder.StartIteration(1)

	recorder.RecordLLMRequest(debug.LLMRequest{
		Model:            "glm-4.6",
		Temperature:      0.5,
		MaxTokens:        2000,
		SystemPromptUsed: "default",
		MessagesCount:    3,
	})

	// Симулируем задержку LLM
	time.Sleep(50 * time.Millisecond)

	recorder.RecordLLMResponse(debug.LLMResponse{
		Content: "Нужно найти категории на WB",
		ToolCalls: []debug.ToolCallInfo{
			{
				ID:   "call_abc123",
				Name: "get_wb_parent_categories",
				Args: `{}`,
			},
		},
		Duration: (50 * time.Millisecond).Milliseconds(),
	})

	// Выполняем tool
	toolStartTime := time.Now()
	categoriesJSON := debug.GenerateCategoriesJSON()
	recorder.RecordToolExecution(debug.ToolExecution{
		Name:      "get_wb_parent_categories",
		Args:      `{}`,
		Result:    categoriesJSON,
		Duration:  time.Since(toolStartTime).Milliseconds(),
		Success:   true,
	})

	recorder.EndIteration()
	fmt.Printf("LLM called: get_wb_parent_categories\n")
	fmt.Printf("Tool execution time: %dms\n", time.Since(toolStartTime).Milliseconds())

	// Итерация 2: LLM вызывает get_wb_subjects
	fmt.Println()
	fmt.Println("--- Iteration 2 ---")
	time.Sleep(30 * time.Millisecond)
	recorder.StartIteration(2)

	recorder.RecordLLMRequest(debug.LLMRequest{
		Model:            "glm-4.6",
		Temperature:      0.5,
		MaxTokens:        2000,
		SystemPromptUsed: "default",
		MessagesCount:    5,
	})

	time.Sleep(40 * time.Millisecond)

	subjectsJSON := debug.GenerateSubjectsJSON()
	recorder.RecordLLMResponse(debug.LLMResponse{
		Content: "Получаю подкатегории для ID=1541",
		ToolCalls: []debug.ToolCallInfo{
			{
				ID:   "call_def456",
				Name: "get_wb_subjects",
				Args: `{"parent": 1541}`,
			},
		},
		Duration: (40 * time.Millisecond).Milliseconds(),
	})

	// Выполняем tool
	toolStartTime = time.Now()
	recorder.RecordToolExecution(debug.ToolExecution{
		Name:      "get_wb_subjects",
		Args:      `{"parent": 1541}`,
		Result:    subjectsJSON,
		Duration:  time.Since(toolStartTime).Milliseconds(),
		Success:   true,
	})

	recorder.EndIteration()
	fmt.Printf("LLM called: get_wb_subjects\n")
	fmt.Printf("Tool execution time: %dms\n", time.Since(toolStartTime).Milliseconds())

	// Итерация 3: Финальный ответ
	fmt.Println()
	fmt.Println("--- Iteration 3 (Final) ---")
	time.Sleep(20 * time.Millisecond)
	recorder.StartIteration(3)

	recorder.RecordLLMRequest(debug.LLMRequest{
		Model:            "glm-4.6",
		Temperature:      0.7, // Изменено через post-prompt
		MaxTokens:        2000,
		SystemPromptUsed: "wb/subject_analysis.yaml",
		MessagesCount:    7,
	})

	time.Sleep(60 * time.Millisecond)

	finalAnswer := "В категории Верхняя одежда найдено 1240 товаров в 12 подкатегориях."
	recorder.RecordLLMResponse(debug.LLMResponse{
		Content:   finalAnswer,
		ToolCalls: []debug.ToolCallInfo{},
		Duration:  (60 * time.Millisecond).Milliseconds(),
	})

	recorder.EndIteration()
	fmt.Printf("Final answer generated\n")

	// 4. Завершаем и сохраняем лог
	fmt.Println()
	fmt.Println("--- Finalizing ---")

	totalDuration := time.Since(startTime)
	filePath, err := recorder.Finalize(finalAnswer, totalDuration)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to finalize recorder: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Debug log saved to: %s\n", filePath)
	fmt.Printf("Total execution time: %dms\n", totalDuration.Milliseconds())
	fmt.Println()

	// 5. Показываем содержимое файла
	fmt.Println("=== Debug Log Content ===")
	printJSONFile(filePath)

	fmt.Println()
	fmt.Println("=== Test Complete ===")
	fmt.Printf("To view the full log: cat %s\n", filePath)
}

// getConfigPath возвращает путь к config.yaml
// Rule 11: Ищет рядом с бинарником, затем в текущей директории
func getConfigPath() string {
	// 1. Проверяем рядом с бинарником (Rule 11: автономность)
	exePath, err := os.Executable()
	if err == nil {
		exeDir := filepath.Dir(exePath)
		configPath := filepath.Join(exeDir, "config.yaml")
		if _, err := os.Stat(configPath); err == nil {
			return configPath
		}
	}

	// 2. Проверяем текущую директорию
	if _, err := os.Stat("config.yaml"); err == nil {
		return "config.yaml"
	}

	// 3. Config не найден - возвращаем дефолтный путь
	// Ошибка будет при загрузке конфигурации (Rule 7: возвращаем ошибку, не panic)
	return "config.yaml"
}

// printJSONFile читает и красиво выводит JSON файл
func printJSONFile(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to read file: %v\n", err)
		return
	}

	var prettyJSON map[string]interface{}
	if err := json.Unmarshal(data, &prettyJSON); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to parse JSON: %v\n", err)
		fmt.Println(string(data))
		return
	}

	formatted, _ := json.MarshalIndent(prettyJSON, "", "  ")
	fmt.Println(string(formatted))
}
