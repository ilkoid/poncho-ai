// WB Ping Test - утилита для проверки инструмента ping_wb_api
//
// Запускает WbPingTool напрямую и выводит результат в формате JSON.
// Используется для тестирования доступности Wildberries Content API
// и валидности API ключа.
//
// Правило 9 из dev_manifest.md: утилита вместо тестов.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/ilkoid/poncho-ai/pkg/app"
	"github.com/ilkoid/poncho-ai/pkg/tools/std"
	"github.com/ilkoid/poncho-ai/pkg/utils"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// CLI flags
var (
	flagConfig = flag.String("config", "", "Path to config.yaml (default: auto-detect)")
	flagRaw    = flag.Bool("raw", false, "Output raw JSON without formatting")
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	flag.Parse()

	// 0. Инициализируем логгер
	if err := utils.InitLogger(); err != nil {
		log.Printf("Warning: failed to init logger: %v", err)
	}
	defer utils.Close()
	utils.Info("wb-ping-test started")

	// 1. Инициализируем конфигурацию
	cfg, cfgPath, err := app.InitializeConfig(&app.DefaultConfigPathFinder{ConfigFlag: *flagConfig})
	if err != nil {
		return err
	}
	log.Printf("Config loaded from %s", cfgPath)

	// 2. Валидируем конфигурацию (используем общую функцию из pkg/app)
	if err := app.ValidateWBKey(cfg.WB.APIKey); err != nil {
		return err
	}

	// 3. Создаём WB клиент напрямую (без всей инфраструктуры оркестратора)
	wbClient := wb.New(cfg.WB.APIKey)

	// 4. Создаём инструмент ping_wb_api
	tool := std.NewWbPingTool(wbClient)

	// 5. Выполняем инструмент
	ctx := context.Background()
	result, err := tool.Execute(ctx, "{}")
	if err != nil {
		return fmt.Errorf("tool execution failed: %w", err)
	}

	// 6. Выводим результат
	if *flagRaw {
		fmt.Println(result)
	} else {
		printFormattedResult(result)
	}

	return nil
}

// printFormattedResult выводит результат в читаемом формате.
func printFormattedResult(jsonStr string) {
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
		fmt.Println(jsonStr)
		return
	}

	separator := "=========================================="

	fmt.Println()
	fmt.Println(separator)
	fmt.Println("WB API PING RESULT")
	fmt.Println(separator)

	if available, ok := data["available"].(bool); ok {
		status := "NOT AVAILABLE"
		if available {
			status = "AVAILABLE"
		}
		color := "\033[31m" // red
		if available {
			color = "\033[32m" // green
		}
		fmt.Printf("%sStatus: %s%s\033[0m\n", color, status, color)
	}

	if status, ok := data["status"].(string); ok {
		fmt.Printf("Status: %s\n", status)
	}

	if ts, ok := data["timestamp"].(string); ok {
		fmt.Printf("Timestamp: %s\n", ts)
	}

	if msg, ok := data["message"].(string); ok {
		fmt.Printf("Message: %s\n", msg)
	}

	if errType, ok := data["error_type"].(string); ok {
		fmt.Printf("Error Type: %s\n", errType)
	}

	if errMsg, ok := data["error"].(string); ok {
		fmt.Printf("Error: %s\n", errMsg)
	}

	fmt.Println(separator)
	fmt.Println()
}
