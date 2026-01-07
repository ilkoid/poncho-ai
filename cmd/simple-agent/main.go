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
	"time"

	"github.com/ilkoid/poncho-ai/pkg/agent"
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

	// 2. Создаём агент (1 строка!)
	fmt.Println("⏳ Initializing agent...")
	client, err := agent.New(agent.Config{
		ConfigPath: "config.yaml",
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating agent: %v\n", err)
		os.Exit(1)
	}

	// 3. Выполняем запрос (1 строка!)
	fmt.Println("🚀 Running query...")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	startTime := time.Now()
	result, err := client.Run(ctx, query)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error running query: %v\n", err)
		os.Exit(1)
	}

	duration := time.Since(startTime)

	// 4. Выводим результат
	fmt.Println("\n✅ Result:")
	fmt.Println("-----------")
	fmt.Println(result)
	fmt.Println("-----------")
	fmt.Printf("\n⏱️  Duration: %v\n", duration)

	// Бонус: покажем историю
	history := client.GetHistory()
	fmt.Printf("📝 History: %d messages\n", len(history))
}
