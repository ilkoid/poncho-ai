// Test Post-Prompt Parameters — проверка переопределения параметров LLM.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/ilkoid/poncho-ai/pkg/agent"
	appcomponents "github.com/ilkoid/poncho-ai/pkg/app"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	fmt.Println("╔════════════════════════════════════════════════════════════╗")
	fmt.Println("║     Test Post-Prompt Parameters Override                  ║")
	fmt.Println("╚════════════════════════════════════════════════════════════╝")
	fmt.Println()

	// 1. Загружаем конфигурацию
	cfg, cfgPath, err := appcomponents.InitializeConfig(&appcomponents.DefaultConfigPathFinder{})
	if err != nil {
		return err
	}
	fmt.Printf("✅ Config loaded: %s\n\n", cfgPath)

	// 2. Показываем дефолтные параметры модели
	fmt.Println("📊 DEFAULT MODEL PARAMETERS:")
	defaultModel := cfg.Models.DefaultReasoning
	if def, ok := cfg.Models.Definitions[defaultModel]; ok {
		fmt.Printf("   Model:       %s\n", def.ModelName)
		fmt.Printf("   Temperature: %.1f\n", def.Temperature)
		fmt.Printf("   MaxTokens:   %d\n", def.MaxTokens)
	}
	fmt.Println()

	// 3. Создаём агент
	client, err := agent.New(agent.Config{ConfigPath: cfgPath})
	if err != nil {
		return err
	}
	fmt.Println("✅ Agent created")
	fmt.Println()

	// 4. Тестируем ping_wb_api (должен активировать api_health_report.yaml)
	//    В post-prompt указано: temperature: 0.2, max_tokens: 1500
	fmt.Println("🧪 TEST: Calling ping_wb_api (should use api_health_report.yaml)")
	fmt.Println("   Expected: temperature=0.2, max_tokens=1500")
	fmt.Println()

	ctx := context.Background()
	result, err := client.Run(ctx, "Проверь доступность Wildberries API")
	if err != nil {
		return err
	}

	fmt.Println("✅ Query completed")
	fmt.Println()
	fmt.Println("═════════════════════════════════════════════════════════════")
	fmt.Println("                    RESULT")
	fmt.Println("═════════════════════════════════════════════════════════════")
	fmt.Println(result)
	fmt.Println("═════════════════════════════════════════════════════════════")
	fmt.Println()

	// 5. Проверяем debug лог
	fmt.Println("🔍 CHECKING DEBUG LOG FOR PARAMETERS...")
	fmt.Println()
	fmt.Println("Find latest debug log and check:")
	fmt.Println("  1. First iteration: should use DEFAULT parameters")
	fmt.Println("  2. Second iteration: should use POST-PROMPT parameters")
	fmt.Println()
	fmt.Println("Run this command to check:")
	fmt.Println("  ls -t debug_logs/*.json | head -1 | xargs cat | grep -E 'temperature|max_tokens|system_prompt_used'")
	fmt.Println()

	return nil
}
