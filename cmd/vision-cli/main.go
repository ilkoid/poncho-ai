// Vision CLI — standalone утилита для анализа изображений через Vision AI.
//
// Распространяется вместе с config.yaml и prompts/ в одной директории.
// Строгое поведение: падает если не находит конфиг или промпты.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	appcomponents "github.com/ilkoid/poncho-ai/pkg/app"
)

var (
	configFlag = flag.String("config", "", "Path to config.yaml (default: next to binary)")
	queryFlag  = flag.String("query", "", "Query to execute (if empty, runs in interactive mode)")
	timeoutFlag = flag.Duration("timeout", 5*time.Minute, "Timeout for query execution")
)

func main() {
	flag.Parse()

	// === СТРОГАЯ ИНИЦИАЛИЗАЦИЯ ===
	// Ищет config.yaml рядом с бинарником, падает если не найден
	finder := &appcomponents.StandaloneConfigPathFinder{ConfigFlag: *configFlag}

	components, cfgPath, err := appcomponents.InitializeForStandalone(finder, 10, "")
	if err != nil {
		log.Fatalf("Initialization failed: %v\n\n"+
			"Make sure config.yaml and prompts/ are next to the binary.\n"+
			"Binary location: %s\n", err, getBinDir())
	}

	log.Printf("Config loaded from: %s", cfgPath)
	log.Printf("Prompts directory: %s", components.Config.App.PromptsDir)

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
		log.Fatal("Query is required. Use -query flag or pass as argument.")
	}

	// Выполняем запрос
	result, err := appcomponents.Execute(components, query, *timeoutFlag)
	if err != nil {
		log.Fatalf("Query execution failed: %v", err)
	}

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
