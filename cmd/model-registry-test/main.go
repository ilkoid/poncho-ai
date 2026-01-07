// Model Registry Test — CLI утилита для тестирования Model Registry.
//
// Использование:
//   ./model-registry-test                    # Показать все зарегистрированные модели
//   ./model-registry-test -test glm-4.6    # Протестировать получение модели
//
// Rule 11: config.yaml, prompts, logs лежат рядом с бинарником.
// Rule 0: Переиспользуем код из pkg/app/components.go
package main

import (
	"flag"
	"fmt"
	"os"

	appcomponents "github.com/ilkoid/poncho-ai/pkg/app"
	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/models"
	"github.com/ilkoid/poncho-ai/pkg/utils"
)

// Version — версия утилиты (заполняется при сборке)
var Version = "dev"

func main() {
	// 1. Парсим флаги
	var (
		configPath  = flag.String("config", "", "Path to config.yaml (default: ./config.yaml)")
		testModel   = flag.String("test", "", "Test model retrieval (e.g., glm-4.6)")
		showVersion = flag.Bool("version", false, "Show version")
	)
	flag.Parse()

	// 2. Обработка специальных флагов
	if *showVersion {
		fmt.Printf("model-registry-test version %s\n", Version)
		os.Exit(0)
	}

	// 3. Загружаем конфигурацию (Rule 11: рядом с бинарником)
	cfgPath := *configPath
	if cfgPath == "" {
		cfgPath = findConfigPath()
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config from %s: %v\n", cfgPath, err)
		os.Exit(1)
	}

	utils.Info("Config loaded", "path", cfgPath)

	// 4. Создаём компоненты через pkg/app (Rule 0: переиспользуем код)
	comps, err := appcomponents.Initialize(cfg, 10, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error initializing components: %v\n", err)
		os.Exit(1)
	}

	registry := comps.ModelRegistry
	if registry == nil {
		fmt.Fprintln(os.Stderr, "Error: ModelRegistry is nil")
		os.Exit(1)
	}

	// 5. Тестирование получения модели если запрошено
	if *testModel != "" {
		testModelRetrieval(registry, cfg, *testModel)
		os.Exit(0)
	}

	// 6. По умолчанию показываем список моделей
	listModels(comps)
}

// findConfigPath находит config.yaml используя существующую инфраструктуру.
func findConfigPath() string {
	finder := &appcomponents.DefaultConfigPathFinder{}
	return finder.FindConfigPath()
}

// listModels выводит список всех зарегистрированных моделей.
func listModels(comps *appcomponents.Components) {
	registry := comps.ModelRegistry

	models := registry.ListNames()

	fmt.Println("╔════════════════════════════════════════════════════════════╗")
	fmt.Println("║           Model Registry - Registered Models                 ║")
	fmt.Println("╚════════════════════════════════════════════════════════════╝")
	fmt.Println()

	for i, name := range models {
		provider, modelDef, err := registry.Get(name)
		if err != nil {
			fmt.Printf("❌ %d. %s - ERROR: %v\n", i+1, name, err)
			continue
		}

		// Определяем тип модели
		modelType := "standard"
		if name == comps.Config.Models.DefaultReasoning {
			modelType = "default_reasoning"
		}
		if name == comps.Config.Models.DefaultChat {
			modelType = "default_chat"
		}
		if name == comps.Config.Models.DefaultVision {
			modelType = "default_vision"
		}

		// Маркер для дефолтных моделей
		marker := "  "
		if modelType != "standard" {
			marker = "★ "
		}

		fmt.Printf("%s[%d] %s\n", marker, i+1, name)
		fmt.Printf("    Type:       %s\n", modelType)
		fmt.Printf("    Provider:    %s\n", modelDef.Provider)
		fmt.Printf("    Model Name:  %s\n", modelDef.ModelName)
		fmt.Printf("    Temperature: %.1f\n", modelDef.Temperature)
		fmt.Printf("    Max Tokens:  %d\n", modelDef.MaxTokens)
		if provider != nil {
			fmt.Printf("    Instance:    ✓ OK\n")
		} else {
			fmt.Printf("    Instance:    ✗ FAILED\n")
		}
		fmt.Println()
	}

	fmt.Printf("Total: %d model(s) registered\n", len(models))
	fmt.Println()
	fmt.Println("Legend:")
	fmt.Println("  ★ - Default model (used when no specific model requested)")
	fmt.Println("    Types: default_reasoning | default_chat | default_vision")
}

// testModelRetrieval тестирует получение модели из registry.
func testModelRetrieval(registry *models.Registry, cfg *config.AppConfig, modelName string) {
	fmt.Printf("Testing model retrieval: %s\n", modelName)
	fmt.Println("────────────────────────────────────────────────────────────────")

	// Тест 1: Прямое получение
	provider, modelDef, err := registry.Get(modelName)
	if err != nil {
		fmt.Printf("❌ Direct Get() FAILED: %v\n", err)
	} else {
		fmt.Printf("✓ Direct Get() SUCCESS\n")
		fmt.Printf("  Provider: %s\n", modelDef.Provider)
		fmt.Printf("  Model: %s\n", modelDef.ModelName)
		if provider != nil {
			fmt.Printf("  Instance: OK\n")
		}
	}
	fmt.Println()

	// Тест 2: GetWithFallback
	defaultModel := cfg.Models.DefaultReasoning
	if defaultModel == "" {
		defaultModel = cfg.Models.DefaultChat
	}

	provider, modelDef, actualModel, err := registry.GetWithFallback(modelName, defaultModel)
	if err != nil {
		fmt.Printf("❌ GetWithFallback() FAILED: %v\n", err)
	} else {
		fmt.Printf("✓ GetWithFallback() SUCCESS\n")
		fmt.Printf("  Requested: %s\n", modelName)
		fmt.Printf("  Actual:    %s\n", actualModel)
		fmt.Printf("  Provider:   %s\n", modelDef.Provider)
		fmt.Printf("  Model:      %s\n", modelDef.ModelName)
		if provider != nil {
			fmt.Printf("  Instance:   OK\n")
		}

		if actualModel != modelName {
			fmt.Printf("  ⚠ WARNING: Fallback used! Model '%s' not found, using '%s'\n", modelName, actualModel)
		}
	}
	fmt.Println()

	// Тест 3: Тест с несуществующей моделью (fallback)
	fmt.Println("Testing fallback with non-existent model...")
	_, _, actualModel2, err := registry.GetWithFallback("non-existent-model", defaultModel)
	if err != nil {
		fmt.Printf("❌ Fallback test FAILED: %v\n", err)
	} else {
		fmt.Printf("✓ Fallback test SUCCESS\n")
		fmt.Printf("  Requested: non-existent-model\n")
		fmt.Printf("  Actual:    %s (fallback)\n", actualModel2)
	}
}
