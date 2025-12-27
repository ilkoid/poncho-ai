// Test Wrap - утилита для тестирования переноса строк без TUI
//
// Запускает агент с заданным запросом и демонстрирует работу WrapText.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ilkoid/poncho-ai/internal/app"
	"github.com/ilkoid/poncho-ai/internal/agent"
	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/factory"
	"github.com/ilkoid/poncho-ai/pkg/prompt"
	"github.com/ilkoid/poncho-ai/pkg/s3storage"
	"github.com/ilkoid/poncho-ai/pkg/tools/std"
	"github.com/ilkoid/poncho-ai/pkg/utils"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// 0. Инициализируем логгер
	if err := utils.InitLogger(); err != nil {
		log.Printf("Warning: failed to init logger: %v", err)
	}
	defer utils.Close()
	utils.Info("test-wrap started")

	// 1. Определяем путь к config.yaml
	cfgPath := "config.yaml"
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		execPath, err := os.Executable()
		if err == nil {
			binDir := filepath.Dir(execPath)
			cfgPath = filepath.Join(binDir, "config.yaml")
		}
	}

	// 2. Загружаем конфигурацию
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	log.Printf("Config loaded from %s", cfgPath)

	// 3. Проверяем API ключи
	if os.Getenv("ZAI_API_KEY") == "" {
		return fmt.Errorf("ZAI_API_KEY not set")
	}
	if os.Getenv("WB_API_KEY") == "" {
		return fmt.Errorf("WB_API_KEY not set")
	}

	// 4. Инициализируем клиенты
	s3Client, err := s3storage.New(cfg.S3)
	if err != nil {
		return fmt.Errorf("failed to create S3 client: %w", err)
	}

	wbClient := wb.New(cfg.WB.APIKey)

	// 5. Создаём глобальное состояние
	state := app.NewState(cfg, s3Client)
	app.SetupTodoCommands(state.CommandRegistry, state)

	// 6. Регистрируем инструменты
	registry := state.GetToolsRegistry()
	registry.Register(std.NewWbParentCategoriesTool(wbClient))
	registry.Register(std.NewWbSubjectsTool(wbClient))

	// 7. Создаём LLM провайдер
	modelDef, ok := cfg.Models.Definitions[cfg.Models.DefaultChat]
	if !ok {
		return fmt.Errorf("default_chat model '%s' not found", cfg.Models.DefaultChat)
	}

	llmProvider, err := factory.NewLLMProvider(modelDef)
	if err != nil {
		return fmt.Errorf("failed to create LLM provider: %w", err)
	}

	// 8. Загружаем системный промпт
	agentSystemPrompt, err := prompt.LoadAgentSystemPrompt(cfg)
	if err != nil {
		log.Printf("Warning: failed to load agent prompt: %v", err)
		agentSystemPrompt = ""
	}

	// 9. Создаём Orchestrator
	state.Orchestrator, err = agent.New(agent.Config{
		LLM:          llmProvider,
		Registry:     registry,
		State:        state,
		MaxIters:     10,
		SystemPrompt: agentSystemPrompt,
	})
	if err != nil {
		return fmt.Errorf("failed to create orchestrator: %w", err)
	}

	// 10. Запрос пользователя
	userQuery := "покажи содержимое категории верхняя одежда"

	// 11. Выполняем запрос с увеличенным таймаутом
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	log.Printf("Query: %s", userQuery)
	log.Println("Processing...")

	answer, err := state.Orchestrator.Run(ctx, userQuery)
	if err != nil {
		return fmt.Errorf("agent error: %w", err)
	}

	// 12. Демонстрируем работу WrapText
	fmt.Println("\n" + strings.Repeat("=", 70))
	fmt.Println("ORIGINAL RESPONSE (without wrap):")
	fmt.Println(strings.Repeat("=", 70))
	fmt.Println(answer)

	fmt.Println("\n" + strings.Repeat("=", 70))
	fmt.Println("WRAPPED RESPONSE (width=60):")
	fmt.Println(strings.Repeat("=", 70))
	wrapped := utils.WrapText(answer, 60)
	fmt.Println(wrapped)

	fmt.Println("\n" + strings.Repeat("=", 70))
	fmt.Println("WRAPPED RESPONSE (width=40):")
	fmt.Println(strings.Repeat("=", 70))
	wrapped = utils.WrapText(answer, 40)
	fmt.Println(wrapped)

	return nil
}
