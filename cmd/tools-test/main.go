// Tools Test Utility - CLI утилита для тестирования S3 и WB tools.
//
// Последовательно вызывает все зарегистрированные инструменты и выводит результаты.
//
// Использование:
//   cd cmd/tools-test
//   go run main.go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	appcomponents "github.com/ilkoid/poncho-ai/pkg/app"
	"github.com/ilkoid/poncho-ai/pkg/utils"
)

// TestResult - результат выполнения инструмента
type TestResult struct {
	ToolName   string        `json:"tool_name"`
	Arguments  interface{}   `json:"arguments"`
	Result     string        `json:"result"`
	Error      string        `json:"error,omitempty"`
	Duration   time.Duration `json:"duration"`
	Success    bool          `json:"success"`
}

// TestSummary - итоговая статистика
type TestSummary struct {
	Total     int       `json:"total"`
	Success   int       `json:"success"`
	Failed    int       `json:"failed"`
	StartTime time.Time `json:"start_time"`
	EndTime   time.Time `json:"end_time"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// 1. Инициализируем логгер
	if err := utils.InitLogger(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to init logger: %v\n", err)
	}
	defer utils.Close()

	utils.Info("Tools Test Utility started")

	// 2. Загружаем конфигурацию используя pkg/app (Rule 0: переиспользуем код)
	// ToolsTestConfigPathFinder ищет config.yaml в cmd/tools-test/
	cfg, cfgPath, err := appcomponents.InitializeConfig(&ToolsTestConfigPathFinder{})
	if err != nil {
		utils.Error("Failed to load config", "error", err, "path", cfgPath)
		return err
	}

	utils.Info("Config loaded", "path", cfgPath)

	// 3. Инициализируем компоненты используя pkg/app (Rule 0: переиспользуем код)
	// Правило 11: передаём контекст для распространения отмены
	components, err := appcomponents.Initialize(context.Background(), cfg, 20, "")
	if err != nil {
		utils.Error("Components initialization failed", "error", err)
		return err
	}

	utils.Info("Components initialized")

	// 4. Получаем реестр инструментов
	registry := components.State.GetToolsRegistry()
	allTools := registry.GetDefinitions()

	utils.Info("Found tools", "count", len(allTools))

	// 5. Определяем порядок выполнения инструментов
	testOrder := []string{
		// S3 Tools
		"list_s3_files",
		"read_s3_object",
		"read_s3_image",

		// WB Ping
		"ping_wb_api",

		// WB Catalog
		"get_wb_parent_categories",
		"get_wb_subjects",
		"get_wb_subjects_by_name",

		// WB Characteristics
		"get_wb_characteristics",
		"get_wb_tnved",
		"get_wb_brands",

		// WB Dictionaries (только если справочники загружены)
		// "wb_colors",
		// "wb_countries",
		// "wb_genders",
		// "wb_seasons",
		// "wb_vat_rates",

		// Planner Tools
		"plan_set_tasks",
		"plan_add_task",
		"plan_mark_done",
		"plan_clear",
	}

	// 6. Выполняем инструменты последовательно
	results := make([]TestResult, 0)
	summary := TestSummary{
		StartTime: time.Now(),
	}

	ctx := context.Background()

	fmt.Println("╔════════════════════════════════════════════════════════════╗")
	fmt.Println("║       Tools Test Utility - S3 & WB Tools Testing          ║")
	fmt.Println("╚════════════════════════════════════════════════════════════╝")
	fmt.Println()

	for _, toolName := range testOrder {
		// Проверяем что инструмент зарегистрирован
		tool, err := registry.Get(toolName)
		if err != nil {
			fmt.Printf("❌ %s: Tool not found in registry\n\n", toolName)
			summary.Failed++
			summary.Total++
			continue
		}

		// Получаем аргументы для инструмента
		args := getArguments(toolName)

		// Выполняем инструмент
		fmt.Printf("🔧 Testing: %s\n", toolName)
		fmt.Printf("   Arguments: %s\n", formatArgs(args))
		
		start := time.Now()
		result, err := tool.Execute(ctx, args)
		duration := time.Since(start)

		testResult := TestResult{
			ToolName:  toolName,
			Arguments: args,
			Duration:  duration,
		}

		if err != nil {
			testResult.Error = err.Error()
			testResult.Success = false
			summary.Failed++
			fmt.Printf("   ❌ Error: %v\n", err)
		} else {
			testResult.Result = result
			testResult.Success = true
			summary.Success++
			
			// Форматируем результат для вывода
			if len(result) > 500 {
				fmt.Printf("   ✅ Success (%v)\n", duration)
				fmt.Printf("   Result (truncated): %s...\n", result[:500])
			} else {
				fmt.Printf("   ✅ Success (%v)\n", duration)
				fmt.Printf("   Result: %s\n", result)
			}
		}

		fmt.Printf("   Duration: %v\n", duration)
		fmt.Println()
		
		results = append(results, testResult)
		summary.Total++
	}

	summary.EndTime = time.Now()

	// 7. Выводим итоговую статистику
	fmt.Println("═════════════════════════════════════════════════════════════")
	fmt.Println("                    SUMMARY")
	fmt.Println("═════════════════════════════════════════════════════════════")
	fmt.Printf("Total:     %d\n", summary.Total)
	fmt.Printf("Success:   %d\n", summary.Success)
	fmt.Printf("Failed:    %d\n", summary.Failed)
	fmt.Printf("Duration:  %v\n", summary.EndTime.Sub(summary.StartTime))
	fmt.Println("═════════════════════════════════════════════════════════════")

	// 8. Сохраняем результаты в лог
	if err := saveResults(results, summary); err != nil {
		utils.Error("Failed to save results", "error", err)
	}

	utils.Info("Test completed", "total", summary.Total, "success", summary.Success, "failed", summary.Failed)
	return nil
}

// ToolsTestConfigPathFinder ищет config.yaml в cmd/tools-test/
//
// Rule 0: Переиспользуем код из pkg/app/components.go
type ToolsTestConfigPathFinder struct{}

// FindConfigPath находит config.yaml в cmd/tools-test/
func (f *ToolsTestConfigPathFinder) FindConfigPath() string {
	// cmd/tools-test/config.yaml (приоритет для tools-test)
	cfgPath := "cmd/tools-test/config.yaml"
	if _, err := os.Stat(cfgPath); err == nil {
		return cfgPath
	}

	// Текущая директория (для запуска из cmd/tools-test/)
	cfgPath = "config.yaml"
	if _, err := os.Stat(cfgPath); err == nil {
		return cfgPath
	}

	// Директория бинарника (для автономного развертывания)
	if execPath, err := os.Executable(); err == nil {
		binDir := filepath.Dir(execPath)
		cfgPath = filepath.Join(binDir, "config.yaml")
		if _, err := os.Stat(cfgPath); err == nil {
			return cfgPath
		}
	}

	return "cmd/tools-test/config.yaml"
}

// getArguments возвращает аргументы для инструмента
func getArguments(toolName string) string {
	switch toolName {
	case "list_s3_files":
		return `{"prefix": ""}`
	case "read_s3_object":
		// Будет пропущен если файл не существует
		return `{"key": "example.json"}`
	case "read_s3_image":
		// Будет пропущен если файл не существует
		return `{"key": "example.jpg"}`
	case "get_wb_subjects":
		return `{"parentID": 1541}` // Женщинам
	case "get_wb_subjects_by_name":
		return `{"name": "платье", "limit": 10}`
	case "get_wb_characteristics":
		return `{"subjectID": 685}` // Платья
	case "get_wb_tnved":
		return `{"subjectID": 685}`
	case "get_wb_brands":
		return `{"subjectID": 685}`
	case "plan_set_tasks":
		// Создаём план из 3 задач для теста
		return `{"tasks": [{"description": "Проверить API Wildberries"}, {"description": "Загрузить эскизы из S3"}, {"description": "Сгенерировать описание товара"}]}`
	case "plan_add_task":
		return `{"description": "Новая задача для теста"}`
	case "plan_mark_done":
		return `{"task_id": 1}`
	case "plan_clear":
		return `{}`
	default:
		return "{}"
	}
}

// formatArgs форматирует аргументы для вывода
func formatArgs(args string) string {
	if args == "{}" {
		return "none"
	}
	
	var parsed interface{}
	if err := json.Unmarshal([]byte(args), &parsed); err != nil {
		return args
	}
	
	formatted, _ := json.Marshal(parsed)
	return string(formatted)
}

// saveResults сохраняет результаты в лог
//
// Rule 0: Переиспользуем логирование из pkg/utils (через utils.Info/Error)
// Но сохранение JSON результатов специфично для этой утилиты
func saveResults(results []TestResult, summary TestSummary) error {
	// Создаем директорию логов если не существует
	logsDir := "logs"
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		return err
	}

	// Формируем имя файла
	timestamp := time.Now().Format("20060102_150405")
	filename := fmt.Sprintf("test_results_%s.json", timestamp)
	logFile := filepath.Join(logsDir, filename)

	// Формируем данные для записи
	data := map[string]interface{}{
		"summary": summary,
		"results": results,
	}

	// Записываем в файл
	formatted, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(logFile, formatted, 0644)
}
