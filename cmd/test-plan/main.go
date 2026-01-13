// Test Plan Tool — CLI утилита для проверки plan_set_tasks через agent.
//
// Использует тот же agent.Client что и TUI в cmd/poncho.
//
// Использование:
//   cd cmd/test-plan
//   go run main.go
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/agent"
	appcomponents "github.com/ilkoid/poncho-ai/pkg/app"
	"github.com/ilkoid/poncho-ai/pkg/utils"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// 0. Инициализируем логгер
	if err := utils.InitLogger(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to init logger: %v\n", err)
	}
	defer utils.Close()

	utils.Info("Test Plan Tool started")

	fmt.Println("╔════════════════════════════════════════════════════════════╗")
	fmt.Println("║     Test plan_set_tasks Tool via Agent Client              ║")
	fmt.Println("╚════════════════════════════════════════════════════════════╝")
	fmt.Println()

	// 1. Загружаем конфигурацию (как в TUI)
	_, cfgPath, err := appcomponents.InitializeConfig(&appcomponents.DefaultConfigPathFinder{})
	if err != nil {
		utils.Error("Failed to load config", "error", err)
		return err
	}
	fmt.Printf("✅ Config loaded: %s\n\n", cfgPath)

	// 2. Rule 11: Создаём родительский контекст для инициализации
	initCtx, initCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer initCancel()

	// 3. Создаём агент (как в TUI)
	client, err := agent.New(initCtx, agent.Config{ConfigPath: cfgPath})
	if err != nil {
		utils.Error("Agent creation failed", "error", err)
		return err
	}
	fmt.Println("✅ Agent client created")
	fmt.Println()

	// 4. Проверяем что tools зарегистрированы
	toolsRegistry := client.GetToolsRegistry()
	allTools := toolsRegistry.GetDefinitions()
	fmt.Printf("📋 Tools registered (%d):\n", len(allTools))
	for _, toolDef := range allTools {
		if toolDef.Name[:4] == "plan" {
			fmt.Printf("   • %s: %s\n", toolDef.Name, toolDef.Description)
		}
	}
	fmt.Println()

	// 5. Тестируем plan_set_tasks через agent
	testQuery := "Составь план из 3 задач для анализа товара: проверь категорию, загрузи эскизы, сгенерируй описание"
	fmt.Printf("🔍 Testing query: \"%s\"\n\n", testQuery)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	startTime := time.Now()
	result, err := client.Run(ctx, testQuery)
	duration := time.Since(startTime)

	if err != nil {
		utils.Error("Agent execution failed", "error", err)
		fmt.Printf("❌ Error: %v\n", err)
		return err
	}

	fmt.Println("✅ Agent execution completed")
	fmt.Printf("⏱️  Duration: %v\n\n", duration)

	fmt.Println("═════════════════════════════════════════════════════════════")
	fmt.Println("                    RESULT")
	fmt.Println("═════════════════════════════════════════════════════════════")
	fmt.Println(result)
	fmt.Println("═════════════════════════════════════════════════════════════")
	fmt.Println()

	// 5. Проверяем состояние Todo Manager
	state := client.GetState()
	todoManager := state.GetTodoManager()
	if todoManager != nil {
		pending, done, failed := todoManager.GetStats()
		total := pending + done + failed
		fmt.Printf("📊 Todo Manager Stats:\n")
		fmt.Printf("   Total: %d\n", total)
		fmt.Printf("   Pending: %d\n", pending)
		fmt.Printf("   Done: %d\n", done)
		fmt.Printf("   Failed: %d\n", failed)
		fmt.Println()

		fmt.Println("📝 Current Todo List:")
		fmt.Println(todoManager.String())
	}

	utils.Info("Test completed successfully")
	return nil
}
