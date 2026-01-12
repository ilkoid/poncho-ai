// wb-ping-util — CLI утилита для диагностики доступности S3 и WB API.
//
// Использование:
//   ./wb-ping-util
//
// Rule 11: config.yaml должен находиться рядом с бинарником.
// Если config не найден — утилита падает с ошибкой.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/app"
	"github.com/ilkoid/poncho-ai/pkg/chain"
	"github.com/ilkoid/poncho-ai/pkg/config"
	pkgprompt "github.com/ilkoid/poncho-ai/pkg/prompt"
	"github.com/ilkoid/poncho-ai/pkg/utils"
)

// Version — версия утилиты (заполняется при сборке)
var Version = "dev"

func main() {
	// 1. Инициализируем логгер
	if err := utils.InitLogger(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to init logger: %v\n", err)
	}
	defer utils.Close()

	utils.Info("Starting wb-ping-util", "version", Version)

	// 2. Определяем путь к конфигу (рядом с бинарником, Rule 11)
	configPath := findConfigPath()
	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config from %s: %v\n", configPath, err)
		utils.Error("Config loading failed", "path", configPath, "error", err)
		os.Exit(1)
	}

	utils.Info("Config loaded", "path", configPath)

	// 3. Создаём компоненты через app.Initialize (Rule 0: переиспользуем код)
	// MaxIterations=3 достаточно для ping проверки (LLM → S3 → WB → отчет)
	// Правило 11: передаём контекст для распространения отмены
	comps, err := app.Initialize(context.Background(), cfg, 3, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating components: %v\n", err)
		utils.Error("Components initialization failed", "error", err)
		os.Exit(1)
	}

	utils.Info("Components initialized",
		"s3_bucket", cfg.S3.Bucket,
		"wb_api_key_set", cfg.WB.APIKey != "")

	// 4. Определяем модель по умолчанию
	defaultModel := cfg.Models.DefaultReasoning
	if defaultModel == "" {
		defaultModel = cfg.Models.DefaultChat
	}

	// 5. Загружаем системный промпт для ping утилиты
	promptPath := filepath.Join(filepath.Dir(configPath), "prompts", "wb_ping.yaml")
	systemPrompt, err := loadSystemPrompt(promptPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading prompt from %s: %v\n", promptPath, err)
		utils.Error("Prompt loading failed", "path", promptPath, "error", err)
		os.Exit(1)
	}

	utils.Info("System prompt loaded", "path", promptPath)

	// 6. Загружаем post-prompts (опционально)
	toolPostPrompts, err := pkgprompt.LoadToolPostPrompts(cfg)
	if err != nil {
		utils.Warn("Failed to load tool post-prompts, continuing without", "error", err)
		// Создаём пустую конфигурацию вместо nil
		toolPostPrompts = &pkgprompt.ToolPostPromptConfig{
			Tools: make(map[string]pkgprompt.ToolPostPrompt),
		}
	}

	// 7. Создаём ReActCycle с настройками для ping проверки
	timeout := 2 * time.Minute
	cycleConfig := chain.ReActCycleConfig{
		SystemPrompt:   systemPrompt,
		ToolPostPrompts: toolPostPrompts,
		PromptsDir:     cfg.App.PromptsDir,
		MaxIterations:  3,
		Timeout:        timeout,
	}

	reactCycle := chain.NewReActCycle(cycleConfig)
	reactCycle.SetModelRegistry(comps.ModelRegistry, defaultModel)
	reactCycle.SetRegistry(comps.State.GetToolsRegistry())
	reactCycle.SetState(comps.State)

	// 8. Подключаем debug (всегда включен для диагностики)
	debugCfg := chain.DebugConfig{
		Enabled:            true,
		SaveLogs:           cfg.App.DebugLogs.SaveLogs,
		LogsDir:            cfg.App.DebugLogs.LogsDir,
		IncludeToolArgs:    cfg.App.DebugLogs.IncludeToolArgs,
		IncludeToolResults: cfg.App.DebugLogs.IncludeToolResults,
		MaxResultSize:      cfg.App.DebugLogs.MaxResultSize,
	}
	debugRecorder, err := chain.NewChainDebugRecorder(debugCfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to create debug recorder: %v\n", err)
	} else {
		reactCycle.AttachDebug(debugRecorder)
		utils.Info("Debug recorder attached", "logs_dir", debugCfg.LogsDir)
	}

	// 9. Выполняем диагностику
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	userQuery := "Проверь доступность S3 хранилища и Wildberries Content API. Сформируй подробный отчет о состоянии обоих сервисов."

	utils.Info("Starting diagnostics", "query", userQuery, "timeout", timeout)

	input := chain.ChainInput{
		UserQuery: userQuery,
		State:     comps.State,
		Registry:  comps.State.GetToolsRegistry(),
		Config:    chain.ChainConfig{},
	}

	output, err := reactCycle.Execute(ctx, input)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		utils.Error("Diagnostics failed", "error", err)
		os.Exit(1)
	}

	// 10. Выводим результат
	fmt.Println("\n" + output.Result)

	// 11. Debug лог
	if output.DebugPath != "" {
		fmt.Fprintf(os.Stderr, "\nDebug log saved to: %s\n", output.DebugPath)
	}

	utils.Info("Diagnostics completed",
		"iterations", output.Iterations,
		"duration", output.Duration,
		"debug_path", output.DebugPath)
}

// findConfigPath находит config.yaml используя существующую инфраструктуру.
//
// Rule 0: Переиспользуем код вместо дублирования.
// Rule 11: config.yaml должен находиться рядом с бинарником.
func findConfigPath() string {
	finder := &app.DefaultConfigPathFinder{}
	return finder.FindConfigPath()
}

// loadSystemPrompt загружает системный промпт из YAML файла.
func loadSystemPrompt(path string) (string, error) {
	promptFile, err := pkgprompt.Load(path)
	if err != nil {
		return "", fmt.Errorf("failed to load prompt: %w", err)
	}

	// Извлекаем системный промпт из первого сообщения
	if len(promptFile.Messages) == 0 {
		return "", fmt.Errorf("no messages in prompt file")
	}

	return promptFile.Messages[0].Content, nil
}
