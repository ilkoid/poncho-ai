// llm-ping — утилита для проверки доступности LLM провайдера.
//
// Использование:
//   go run cmd/llm-ping/main.go
//   или
//   go build -o llm-ping cmd/llm-ping/main.go && ./llm-ping
//
// Переменные окружения:
//   OPENROUTER_API_KEY - API ключ для OpenRouter
//   ZAI_API_KEY        - API ключ для Zai
//   OPENAI_API_KEY     - API ключ для OpenAI
//
// Конфигурация:
//   Использует config.yaml из текущей директории
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/models"
	"github.com/ilkoid/poncho-ai/pkg/tools/std"
)

func main() {
	// 1. Загружаем конфигурацию
	cfgPath := "config.yaml"
	if len(os.Args) > 1 {
		cfgPath = os.Args[1]
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatalf("Failed to load config from %s: %v", cfgPath, err)
	}

	// 2. Создаем ModelRegistry
	modelRegistry, err := models.NewRegistryFromConfig(cfg)
	if err != nil {
		log.Fatalf("Failed to create model registry: %v", err)
	}

	// 3. Создаем LLM Ping Tool
	toolCfg, exists := cfg.Tools["ping_llm_provider"]
	if !exists {
		fmt.Println("❌ ping_llm_provider tool not found in config.yaml")
		fmt.Println("\nAdd this to your config.yaml:")
		fmt.Println(`
tools:
  ping_llm_provider:
    enabled: true
    description: "Проверяет доступность LLM провайдера"
`)
		os.Exit(1)
	}

	pingTool := std.NewLLMPingTool(modelRegistry, cfg, toolCfg)

	// 4. Получаем дефолтную модель из конфига
	modelAlias := cfg.Models.DefaultChat
	if modelAlias == "" {
		fmt.Println("⚠️  No default_chat model configured in config.yaml")
		fmt.Println("Testing first available model...")

		// Берем первую доступную модель
		for name := range cfg.Models.Definitions {
			modelAlias = name
			break
		}
	}

	fmt.Printf("🔍 Testing LLM Provider: %s\n\n", modelAlias)

	// 5. Выполняем ping
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	args := ""
	if modelAlias != "" {
		argsJSON, _ := json.Marshal(map[string]string{"model": modelAlias})
		args = string(argsJSON)
	}

	result, err := pingTool.Execute(ctx, args)
	if err != nil {
		log.Fatalf("Failed to execute ping: %v", err)
	}

	// 6. Парсим и выводим результат
	var pingResult map[string]interface{}
	if err := json.Unmarshal([]byte(result), &pingResult); err != nil {
		fmt.Printf("Raw result: %s\n", result)
		return
	}

	// Красивый вывод
	printResult(pingResult)
}

// printResult выводит результат пинга в красивом формате
func printResult(result map[string]interface{}) {
	available, _ := result["available"].(bool)
	statusCode, _ := result["status_code"].(float64)
	latencyMs, _ := result["latency_ms"].(float64)
	provider, _ := result["provider"].(string)
	model, _ := result["model"].(string)

	if available {
		fmt.Printf("✅ Status: AVAILABLE\n")
		fmt.Printf("   Provider: %s\n", provider)
		fmt.Printf("   Model: %s\n", model)
		fmt.Printf("   Latency: %dms\n", int(latencyMs))
		if statusCode > 0 {
			fmt.Printf("   HTTP Code: %d\n", int(statusCode))
		}
		if msg, ok := result["message"].(string); ok {
			fmt.Printf("   Message: %s\n", msg)
		}
	} else {
		fmt.Printf("❌ Status: UNAVAILABLE\n")
		if provider != "" {
			fmt.Printf("   Provider: %s\n", provider)
		}
		if model != "" {
			fmt.Printf("   Model: %s\n", model)
		}
		if errType, ok := result["error_type"].(string); ok {
			fmt.Printf("   Error Type: %s\n", errType)
		}
		if errMsg, ok := result["error"].(string); ok {
			fmt.Printf("   Error: %s\n", errMsg)
		}
		if statusCode > 0 {
			fmt.Printf("   HTTP Code: %d\n", int(statusCode))
		}
		if latencyMs > 0 {
			fmt.Printf("   Latency: %dms\n", int(latencyMs))
		}
	}
}
