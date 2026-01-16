// Test preset system - простой пример использования пресетов.
//
// Запуск:
//	cd cmd/preset-test
//	go run main.go
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/ilkoid/poncho-ai/pkg/agent"
)

func main() {
	ctx := context.Background()

	fmt.Println("=== Preset System Test ===")
	fmt.Println()
	fmt.Println("Available presets:")
	fmt.Println("  - simple-cli")
	fmt.Println("  - interactive-tui")
	fmt.Println("  - full-featured")
	fmt.Println()

	// Используем simple-cli пресет для тестирования
	fmt.Println("Creating agent from 'simple-cli' preset...")
	client, err := agent.NewFromPreset(ctx, "simple-cli")
	if err != nil {
		log.Fatalf("Failed to create agent: %v", err)
	}

	fmt.Println("Agent created successfully!")
	fmt.Println()
	fmt.Println("Testing agent.Run()...")
	fmt.Println()

	// Простой запрос для тестирования
	result, err := client.Run(ctx, "Say hello in one sentence")
	if err != nil {
		log.Fatalf("Failed to run agent: %v", err)
	}

	fmt.Printf("Result: %s\n", result)
	fmt.Println()
	fmt.Println("=== Test Complete ===")
	fmt.Println()
	fmt.Println("To test CLI preset, run:")
	fmt.Println("  go run main.go cli")
	fmt.Println()
	fmt.Println("To test TUI preset, you need to use pkg/tui directly:")
	fmt.Println("  client, _ := agent.NewFromPreset(ctx, \"interactive-tui\")")
	fmt.Println("  sub := client.Subscribe()")
	fmt.Println("  tui := tui.NewSimpleTui(sub, ...)")
	fmt.Println("  tui.Run()")

	os.Exit(0)
}
