// Chain-cli — CLI утилита для тестирования Chain Pattern.
//
// Использование:
//   ./chain-cli "запрос"
//   ./chain-cli -debug "запрос"
//   ./chain-cli -json "запрос"
//   ./chain-cli -model glm-4.6 "запрос"
//
// Rule 11: config.yaml должен находиться рядом с бинарником.
// Если config не найден — утилита падает с ошибкой.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/chain"
	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/prompt"
	"github.com/ilkoid/poncho-ai/pkg/utils"
)

// Version — версия утилиты (заполняется при сборке)
var Version = "dev"

func main() {
	// 1. Парсим флаги
	var (
		configPath  = flag.String("config", "", "Path to config.yaml (default: ./config.yaml)")
		modelName   = flag.String("model", "", "Override model name")
		debugFlag   = flag.Bool("debug", false, "Enable debug logging")
		noColor     = flag.Bool("no-color", false, "Disable colors in output")
		jsonOutput  = flag.Bool("json", false, "Output in JSON format")
		showHelp    = flag.Bool("help", false, "Show help")
		showVersion = flag.Bool("version", false, "Show version")
	)
	flag.Parse()

	// 2. Обработка специальных флагов
	if *showVersion {
		fmt.Printf("chain-cli version %s\n", Version)
		os.Exit(0)
	}

	if *showHelp {
		printHelp()
		os.Exit(0)
	}

	if flag.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Error: query argument is required")
		fmt.Fprintln(os.Stderr, "Usage: chain-cli [flags] \"query\"")
		fmt.Fprintln(os.Stderr, "Run 'chain-cli -help' for more information")
		os.Exit(1)
	}

	userQuery := flag.Arg(0)

	// 3. Загружаем конфигурацию (Rule 11: рядом с бинарником или падаем)
	cfgPath := *configPath
	if cfgPath == "" {
		cfgPath = findConfigPath()
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config from %s: %v\n", cfgPath, err)
		os.Exit(1)
	}

	// 4. Создаём компоненты через appcomponents (Rule 0: переиспользуем код)
	comps, err := createComponents(cfg, *modelName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating components: %v\n", err)
		os.Exit(1)
	}

	// Извлекаем компоненты для удобства
	modelRegistry := comps.ModelRegistry
	registry := comps.State.GetToolsRegistry()
	state := comps.State

	// 5. Определяем дефолтную модель
	defaultModel := cfg.Models.DefaultReasoning
	if defaultModel == "" {
		defaultModel = cfg.Models.DefaultChat
	}
	if defaultModel == "" {
		fmt.Fprintln(os.Stderr, "Error: neither default_reasoning nor default_chat configured")
		os.Exit(1)
	}

	// Override model если указан в флаге
	if *modelName != "" {
		defaultModel = *modelName
	}

	// 6. Загружаем post-prompts
	toolPostPrompts, err := prompt.LoadToolPostPrompts(cfg)
	if err != nil {
		utils.Error("Failed to load tool post-prompts", "error", err)
		// Не критично — продолжаем без post-prompts
		toolPostPrompts = nil
	}

	// 7. Создаём ReActCycle
	cycleConfig := chain.ReActCycleConfig{
		SystemPrompt:    defaultSystemPrompt(),
		ToolPostPrompts:  toolPostPrompts,
		PromptsDir:      cfg.App.PromptsDir,
		MaxIterations:   10,
		Timeout:         5 * time.Minute,
	}

	reactCycle := chain.NewReActCycle(cycleConfig)
	reactCycle.SetModelRegistry(modelRegistry, defaultModel)
	reactCycle.SetRegistry(registry)
	// Rule 6: Передаем CoreState в cycle (framework logic)
	reactCycle.SetState(state.CoreState)

	// 7. Подключаем debug если включен
	if *debugFlag || cfg.App.DebugLogs.Enabled {
		debugCfg := chain.DebugConfig{
			Enabled:             true,
			SaveLogs:            cfg.App.DebugLogs.SaveLogs,
			LogsDir:             cfg.App.DebugLogs.LogsDir,
			IncludeToolArgs:     cfg.App.DebugLogs.IncludeToolArgs,
			IncludeToolResults:  cfg.App.DebugLogs.IncludeToolResults,
			MaxResultSize:       cfg.App.DebugLogs.MaxResultSize,
		}
		debugRecorder, err := chain.NewChainDebugRecorder(debugCfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to create debug recorder: %v\n", err)
		} else {
			reactCycle.AttachDebug(debugRecorder)
		}
	}

	// 8. Выполняем Cycle
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Rule 6: Передаем CoreState в cycle (framework logic)
	input := chain.ChainInput{
		UserQuery: userQuery,
		State:     state.CoreState,
		Registry:  registry,
		Config:    chain.ChainConfig{},
	}

	output, err := reactCycle.Execute(ctx, input)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// 9. Выводим результат
	if *jsonOutput {
		printJSON(output, *noColor)
	} else {
		printHuman(output, *noColor)
	}

	// 10. Debug лог уже сохранён
	if output.DebugPath != "" {
		fmt.Fprintf(os.Stderr, "\nDebug log: %s\n", output.DebugPath)
	}
}

// printHelp выводит справку
func printHelp() {
	fmt.Println("Chain CLI — утилита для тестирования Chain Pattern")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  chain-cli [flags] \"query\"")
	fmt.Println()
	fmt.Println("Flags:")
	fmt.Println("  -chain string   Chain type (default \"react\")")
	fmt.Println("  -config string  Path to config.yaml (default \"./config.yaml\")")
	fmt.Println("  -model string   Override model name")
	fmt.Println("  -debug          Enable debug logging")
	fmt.Println("  -json           Output in JSON format")
	fmt.Println("  -no-color       Disable colors in output")
	fmt.Println("  -version        Show version")
	fmt.Println("  -help           Show this help")
	fmt.Println()
	fmt.Println("Rule 11: config.yaml must be located next to the binary.")
	fmt.Println("If config is not found, the utility will fail.")
}
