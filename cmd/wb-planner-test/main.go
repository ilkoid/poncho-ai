// WB + Planner Test - утилита для демонстрации комбинирования инструментов
//
// Показывает как использовать битовые флаги для регистрации только
// нужных инструментов (WB + Planner), экономя токены.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/ilkoid/poncho-ai/pkg/app"
	"github.com/ilkoid/poncho-ai/pkg/utils"
)

// CLI flags
var (
	flagConfig = flag.String("config", "", "Path to config.yaml (default: auto-detect)")
	flagQuery  = flag.String("query", "проверь WB API и добавь задачу протестировать инструменты", "Query for the agent")
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
	utils.Info("wb-planner-test started", "query", *flagQuery)

	// 1. Инициализируем конфигурацию
	cfg, cfgPath, err := app.InitializeConfig(&app.DefaultConfigPathFinder{ConfigFlag: *flagConfig})
	if err != nil {
		return err
	}
	log.Printf("Config loaded from %s", cfgPath)

	// 2. Инициализируем компоненты с ТОЛЬКО WB + Planner
	// Это экономит токены по сравнению с ToolsAll
	components, err := app.Initialize(cfg, 10, "", app.ToolWB|app.ToolPlanner)
	if err != nil {
		return fmt.Errorf("initialization failed: %w", err)
	}
	log.Println("Components initialized with WB + Planner tools only")

	// 3. Выполняем запрос
	result, err := app.Execute(components, *flagQuery, 2*60*1000*1000*1000)
	if err != nil {
		return err
	}

	// 4. Выводим результаты
	fmt.Println("\n=== RESPONSE ===")
	fmt.Println(result.Response)
	fmt.Println("\n=== TODOS ===")
	fmt.Println(result.TodoString)
	fmt.Println("\n=== STATS ===")
	fmt.Printf("Total: %d, Pending: %d, Done: %d, Failed: %d\n",
		result.TodoStats.Total, result.TodoStats.Pending,
		result.TodoStats.Done, result.TodoStats.Failed)

	return nil
}
