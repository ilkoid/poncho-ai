// Vision CLI — standalone утилита для анализа изображений через Vision AI.
//
// Распространяется вместе с config.yaml и prompts/ в одной директории.
// Строгое поведение: падает если не находит конфиг или промпты.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/app"
	"github.com/ilkoid/poncho-ai/pkg/utils"
)

var (
	configFlag = flag.String("config", "", "Path to config.yaml (default: next to binary)")
	queryFlag  = flag.String("query", "", "Query to execute (if empty, runs in interactive mode)")
	timeoutFlag = flag.Duration("timeout", 5*time.Minute, "Timeout for query execution")
)

func main() {
	flag.Parse()

	// === ИНИЦИАЛИЗАЦИЯ ЛОГГЕРА ===
	if err := utils.InitLogger(); err != nil {
		fmt.Fprintf(os.Stderr, "Logger init failed: %v\n", err)
	}
	defer utils.Close()

	utils.Info("vision-cli started", "config", *configFlag, "query", *queryFlag)

	// === СТРОГАЯ ИНИЦИАЛИЗАЦИЯ ===
	// Rule 11: Создаём родительский контекст для инициализации
	initCtx, initCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer initCancel()

	// Ищет config.yaml рядом с бинарником, падает если не найден
	finder := &app.StandaloneConfigPathFinder{ConfigFlag: *configFlag}

	components, cfgPath, err := app.InitializeForStandalone(initCtx, finder, 10, "")
	if err != nil {
		utils.Error("Initialization failed", "error", err)
		fmt.Fprintf(os.Stderr, "Initialization failed: %v\n\n"+
			"Make sure config.yaml and prompts/ are next to the binary.\n"+
			"Binary location: %s\n", err, getBinDir())
		os.Exit(1)
	}

	utils.Info("Config loaded", "path", cfgPath)
	utils.Info("Prompts directory", "path", components.Config.App.PromptsDir)

	// === ВЫПОЛНЕНИЕ ЗАПРОСА ===
	query := *queryFlag

	// Если запрос не указан через флаг — запускаем в интерактивном режиме
	if query == "" {
		if len(flag.Args()) > 0 {
			// Можно передать запрос как позиционный аргумент
			query = flag.Arg(0)
		} else {
			// Интерактивный режим
			fmt.Print("Enter query: ")
			fmt.Scanln(&query)
		}
	}

	if query == "" {
		utils.Error("Query is required")
		fmt.Fprintln(os.Stderr, "Query is required. Use -query flag or pass as argument.")
		os.Exit(1)
	}

	utils.Info("Executing query", "query", query)

	// Выполняем запрос (Правило 11: передаём контекст для отмены)
	result, err := app.Execute(context.Background(), components, query, *timeoutFlag)
	if err != nil {
		utils.Error("Query execution failed", "error", err)
		fmt.Fprintf(os.Stderr, "Query execution failed: %v\n", err)
		os.Exit(1)
	}

	utils.Info("Query completed", "duration", result.Duration, "todos", result.TodoStats.Total)

	// === ВЫВОД РЕЗУЛЬТАТА ===
	fmt.Println("\n=== Response ===")
	fmt.Println(result.Response)

	if result.TodoStats.Total > 0 {
		fmt.Printf("\n=== Todo Stats ===\n")
		fmt.Printf("Total: %d | Done: %d | Failed: %d | Pending: %d\n",
			result.TodoStats.Total, result.TodoStats.Done,
			result.TodoStats.Failed, result.TodoStats.Pending)
	}

	fmt.Printf("\nDuration: %v\n", result.Duration)
}

// getBinDir возвращает директорию где находится бинарник.
func getBinDir() string {
	if execPath, err := os.Executable(); err == nil {
		return execPath
	}
	return "unknown"
}
