// Simple-agent — демонстрация нового простого API pkg/agent.
//
// Это минимальный пример использования Poncho AI:
//   - 3 строки кода для создания агента
//   - 1 строка для выполнения запроса
//
// Использование:
//   go run cmd/simple-agent/main.go "запрос"
//   ./simple-agent "покажи категории"
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/ilkoid/poncho-ai/pkg/agent"
	"github.com/ilkoid/poncho-ai/pkg/utils"
)

func main() {
	// 1. Получаем запрос из аргументов
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: simple-agent \"query\"")
		fmt.Fprintln(os.Stderr, "Example: simple-agent \"покажи категории\"")
		os.Exit(1)
	}
	query := os.Args[1]

	fmt.Println("🤖 Poncho AI - Simple Agent Demo")
	fmt.Println("================================")
	fmt.Printf("Query: %s\n\n", query)

	// 2. Graceful Shutdown: обрабатываем Ctrl+C для корректного завершения
	// Rule 11: создаём родительский контекст с поддержкой отмены
	ctx, shutdown := utils.SetupGracefulShutdownWithContext()
	defer shutdown()

	// 3. Создаём агент с контекстом (1 строка!)
	fmt.Println("⏳ Initializing agent...")
	client, err := agent.New(ctx, agent.Config{
		ConfigPath: "config.yaml",
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating agent: %v\n", err)
		os.Exit(1)
	}

	// 4. Выполняем запрос с контекстом (1 строка!)
	fmt.Println("🚀 Running query...")
	result, err := client.Run(ctx, query)
	if err != nil {
		// Проверяем что это была отмена пользователем
		if ctx.Err() == context.Canceled {
			fmt.Println("\n⚠️  Query cancelled by user")
			os.Exit(130) // Стандартный код выхода для SIGINT
		}
		fmt.Fprintf(os.Stderr, "Error running query: %v\n", err)
		os.Exit(1)
	}

	// 5. Выводим результат
	fmt.Println("\n✅ Result:")
	fmt.Println("-----------")
	fmt.Println(result)
	fmt.Println("-----------")

	// Бонус: покажем историю
	history := client.GetHistory()
	fmt.Printf("📝 History: %d messages\n", len(history))
}
