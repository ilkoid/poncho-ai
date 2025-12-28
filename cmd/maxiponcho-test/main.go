// Maxiponcho Test CLI
// Утилита для тестирования maxiponcho без TUI
package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

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
	// Парсим аргументы
	args := os.Args[1:]
	var query string
	verbose := false

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-query", "-q":
			if i+1 < len(args) {
				query = args[i+1]
				i++
			}
		case "-verbose", "-v":
			verbose = true
		}
	}

	if query == "" {
		query = "загрузи из S3 артикул 12612012, определи тип товара на WB и составь продающее описание"
	}

	if verbose {
		log.Printf("Query: %s", query)
	}

	// Инициализируем логгер
	if err := utils.InitLogger(); err != nil {
		log.Printf("Warning: failed to init logger: %v", err)
	}
	defer utils.Close()

	utils.Info("Maxiponcho-test started")

	// Загружаем конфиг
	cfg, cfgPath, err := appcomponents.InitializeConfig(&MaxiponchoConfigPathFinder{})
	if err != nil {
		utils.Error("Failed to load config", "error", err)
		return err
	}
	log.Printf("Config loaded from %s", cfgPath)

	// Инициализируем компоненты
	components, err := appcomponents.Initialize(
		cfg,
		10,    // max iterations
		"",    // system prompt - из конфига
		appcomponents.ToolS3|appcomponents.ToolWB|appcomponents.ToolPlanner,
	)
	if err != nil {
		utils.Error("Components initialization failed", "error", err)
		return err
	}

	log.Printf("Components initialized")

	// Выполняем запрос
	fmt.Println("=== Maxiponcho Test ===")
	fmt.Printf("Query: %s\n\n", query)
	fmt.Println("Processing...")

	result, err := appcomponents.Execute(components, query, 5*time.Minute)
	if err != nil {
		utils.Error("Execution failed", "error", err)
		return err
	}

	// Выводим результат
	fmt.Println("\n=== Response ===")
	fmt.Println(result.Response)

	if result.TodoString != "" {
		fmt.Println("\n=== Tasks ===")
		fmt.Println(result.TodoString)
	}

	fmt.Printf("\nDuration: %v\n", result.Duration)

	utils.Info("Test completed successfully", "duration", result.Duration)
	return nil
}

// MaxiponchoConfigPathFinder ищет config.yaml в cmd/maxiponcho/
type MaxiponchoConfigPathFinder struct{}

func (f *MaxiponchoConfigPathFinder) FindConfigPath() string {
	// 1. cmd/maxiponcho/config.yaml (приоритет - специфичный для maxiponcho)
	cfgPath := filepath.Join("cmd", "maxiponcho", "config.yaml")
	if _, err := os.Stat(cfgPath); err == nil {
		return resolveAbsPath(cfgPath)
	}

	// 2. Директория бинарника
	if execPath, err := os.Executable(); err == nil {
		binDir := filepath.Dir(execPath)
		cfgPath = filepath.Join(binDir, "config.yaml")
		if _, err := os.Stat(cfgPath); err == nil {
			return cfgPath
		}
	}

	// 3. Текущая директория
	cfgPath = "config.yaml"
	if _, err := os.Stat(cfgPath); err == nil {
		return resolveAbsPath(cfgPath)
	}

	// 4. Родительская директория
	cfgPath = filepath.Join("..", "..", "config.yaml")
	if _, err := os.Stat(cfgPath); err == nil {
		return resolveAbsPath(cfgPath)
	}

	return resolveAbsPath("cmd/maxiponcho/config.yaml")
}

func resolveAbsPath(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return abs
}
