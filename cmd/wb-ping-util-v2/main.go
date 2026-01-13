// wb-ping-util-v2 — упрощённая версия с использованием нового pkg/agent API.
//
// Сравнение с оригиналом (wb-ping-util):
//
// ОРИГИНАЛ (расширенный API):
//   cfg, _ := config.Load(configPath)
//   comps, _ := app.Initialize(cfg, 3, "")
//   promptPath := filepath.Join(...)
//   systemPrompt, _ := loadSystemPrompt(promptPath)
//   toolPostPrompts, _ := pkgprompt.LoadToolPostPrompts(cfg)
//   cycleConfig := chain.ReActCycleConfig{...}
//   reactCycle := chain.NewReActCycle(cycleConfig)
//   reactCycle.SetModelRegistry(...)
//   reactCycle.SetRegistry(...)
//   reactCycle.SetState(...)
//   debugRecorder, _ := chain.NewChainDebugRecorder(...)
//   reactCycle.AttachDebug(debugRecorder)
//   input := chain.ChainInput{...}
//   output, _ := reactCycle.Execute(ctx, input)
//
// НОВЫЙ API (pkg/agent):
//   client, _ := agent.New(agent.Config{ConfigPath: "config.yaml"})
//   result, _ := client.Run(ctx, query)
//
// Использование:
//   ./wb-ping-util-v2
//
// Rule 11: config.yaml должен находиться рядом с бинарником.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/agent"
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

	utils.Info("Starting wb-ping-util-v2", "version", Version)

	// 2. Rule 11: Создаём родительский контекст для инициализации
	initCtx, initCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer initCancel()

	// 3. Создаём агент с контекстом - ОДНА СТРОКА! (всё остальное под капотом)
	//
	// agent.New() автоматически:
	//   - Загружает config.yaml
	//   - Создаёт S3 и WB клиентов
	//   - Загружает справочники
	//   - Регистрирует tools (только enabled: true)
	//   - Создаёт ReActCycle с debug recorder
	//
	// Параметры из config.yaml (chains.default):
	//   - max_iterations: 10 (можно переопределить в YAML)
	//   - timeout: "5m" (можно переопределить в YAML)
	utils.Info("Creating agent...")
	client, err := agent.New(initCtx, agent.Config{
		ConfigPath: "config.yaml",
		// MaxIterations: 0 // <- используем значение из config.yaml chains.default.max_iterations
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating agent: %v\n", err)
		utils.Error("Agent creation failed", "error", err)
		os.Exit(1)
	}

	// Получаем конфигурацию для логирования
	cfg := client.GetConfig()
	maxIters := cfg.GetChainMaxIterations("default")
	chainTimeout := cfg.GetChainTimeout("default")

	utils.Info("Agent created successfully",
		"s3_bucket", cfg.S3.Bucket,
		"tools_registered", len(client.GetToolsRegistry().GetDefinitions()),
		"max_iterations", maxIters,
		"chain_timeout", chainTimeout)

	// 3. Выполняем диагностику - ОДНА СТРОКА!
	// Context timeout берётся из config.yaml chains.default.timeout
	ctx, cancel := context.WithTimeout(context.Background(), chainTimeout)
	defer cancel()

	query := "Проверь доступность S3 хранилища и Wildberries Content API. Сформируй подробный отчет о состоянии обоих сервисов."

	utils.Info("Running diagnostics", "timeout", chainTimeout)
	startTime := time.Now()

	result, err := client.Run(ctx, query)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		utils.Error("Diagnostics failed", "error", err)
		os.Exit(1)
	}

	duration := time.Since(startTime)

	// 4. Выводим результат
	fmt.Println("\n" + result)
	fmt.Printf("\n⏱️  Duration: %v\n", duration)

	// 5. История (бонус)
	history := client.GetHistory()
	fmt.Printf("📝 Messages exchanged: %d\n", len(history))

	utils.Info("Diagnostics completed", "duration", duration)
}
