// Simple Agent - утилита для тестирования агента без TUI
//
// Запускает агент с хардкодным запросом и выводит результат на stdout.
// Использует все стандартные механики фреймворка: Orchestrator, Tools, LLM.
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
	utils.Info("simple-agent started")

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
	log.Printf("✓ Config loaded from %s", cfgPath)

	// Логируем загруженные ключи (с маскированием для безопасности)
	logKeysInfo(cfg)

	// 3. Инициализируем S3 клиент
	s3Client, err := s3storage.New(cfg.S3)
	if err != nil {
		return fmt.Errorf("failed to create S3 client: %w", err)
	}
	log.Println("✓ S3 client initialized")

	// 4. Инициализируем WB клиент
	var wbClient *wb.Client
	if cfg.WB.APIKey != "" {
		wbClient = wb.New(cfg.WB.APIKey)

		ctx := context.Background()
		if _, err := wbClient.Ping(ctx); err != nil {
			log.Printf("⚠ Warning: WB API ping failed: %v", err)
		} else {
			log.Println("✓ WB API connection verified")
		}
	} else {
		log.Println("⚠ WB API key not set - using demo client (tools will fail in real mode)")
		// Создаём демо-клиент для показа структуры инструментов
		wbClient = wb.New("demo_key")
	}

	// 5. Создаём глобальное состояние
	state := app.NewState(cfg, s3Client)
	log.Println("✓ Global state initialized")

	// 6. Регистрируем Todo команды
	app.SetupTodoCommands(state.CommandRegistry, state)
	log.Println("✓ Todo commands registered")

	// 7. Регистрируем инструменты (Tools)
	// Всегда регистрируем инструменты для демо-режима
	setupTools(state, wbClient)
	if cfg.WB.APIKey != "" {
		log.Println("✓ WB tools registered")
	} else {
		log.Println("✓ WB tools registered (demo mode)")
	}

	// 8. Создаём LLM провайдер
	modelDef, ok := cfg.Models.Definitions[cfg.Models.DefaultChat]
	if !ok {
		return fmt.Errorf("default_chat model '%s' not found in definitions", cfg.Models.DefaultChat)
	}

	llmProvider, err := factory.NewLLMProvider(modelDef)
	if err != nil {
		return fmt.Errorf("failed to create LLM provider: %w", err)
	}
	log.Printf("✓ LLM provider initialized: %s", cfg.Models.DefaultChat)

	// 9. Загружаем системный промпт агента
	agentSystemPrompt, err := prompt.LoadAgentSystemPrompt(cfg)
	if err != nil {
		log.Printf("⚠ Warning: failed to load agent prompt: %v", err)
		agentSystemPrompt = "" // Используем дефолтный из orchestrator
	}

	// 10. Создаём Orchestrator
	state.Orchestrator, err = agent.New(agent.Config{
		LLM:          llmProvider,
		Registry:     state.GetToolsRegistry(),
		State:        state,
		MaxIters:     10,
		SystemPrompt: agentSystemPrompt,
	})
	if err != nil {
		return fmt.Errorf("failed to create orchestrator: %w", err)
	}
	log.Println("✓ Agent orchestrator initialized")

	// 11. Хардкодный запрос пользователя
	// Можно менять для тестирования разных запросов:
	userQuery := "покажи только категории товаров, относящиеся к моде, одежде и аксессуарам"

	// 12. Проверяем режим демо (без API ключей)
	demoMode := os.Getenv("ZAI_API_KEY") == "" || os.Getenv("WB_API_KEY") == ""

	if demoMode {
		log.Println("\n⚠ DEMO MODE: No API keys found - showing framework flow without real API calls")
		log.Println("→ Set ZAI_API_KEY and WB_API_KEY for real execution")
		fmt.Println("\n" + strings.Repeat("=", 60))
		fmt.Println("DEMO: Framework Flow (dry-run)")
		fmt.Println(strings.Repeat("=", 60))
		fmt.Printf("Query: %s\n\n", userQuery)
		fmt.Println("Expected flow:")
		fmt.Println("1. Orchestrator.Run() → ReAct loop starts")
		fmt.Println("2. LLM receives query + tool definitions:")
		printToolDefinitions(state)
		fmt.Println("\n3. LLM calls: get_wb_parent_categories")
		fmt.Println("4. Tool executes WB API call")
		fmt.Println("5. Result returned to LLM")
		fmt.Println("6. LLM formats final answer")
		fmt.Println(strings.Repeat("=", 60))
		return nil
	}

	// 13. Выполняем запрос через агента (real mode)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	log.Printf("\n→ USER QUERY: %s", userQuery)
	log.Println("→ Processing...")

	answer, err := state.Orchestrator.Run(ctx, userQuery)
	if err != nil {
		return fmt.Errorf("agent error: %w", err)
	}

	// 13. Выводим результат
	fmt.Println("\n" + strings.Repeat("=", 60))
	fmt.Println("AGENT RESPONSE:")
	fmt.Println(strings.Repeat("=", 60))
	fmt.Println(answer)
	fmt.Println(strings.Repeat("=", 60))

	return nil
}

// setupTools регистрирует все доступные инструменты в ToolsRegistry.
func setupTools(state *app.GlobalState, wbClient *wb.Client) {
	registry := state.GetToolsRegistry()

	// Wildberries инструменты
	registry.Register(std.NewWbParentCategoriesTool(wbClient))
	registry.Register(std.NewWbSubjectsTool(wbClient))

	// Planner инструменты (управление задачами агента)
	registry.Register(std.NewPlanAddTaskTool(state.Todo))
	registry.Register(std.NewPlanMarkDoneTool(state.Todo))
	registry.Register(std.NewPlanMarkFailedTool(state.Todo))
	registry.Register(std.NewPlanClearTool(state.Todo))
}

// printToolDefinitions выводит зарегистрированные инструменты для демо-режима.
func printToolDefinitions(state *app.GlobalState) {
	tools := state.GetToolsRegistry().GetDefinitions()
	for _, tool := range tools {
		fmt.Printf("   • %s\n", tool.Name)
		fmt.Printf("     Description: %s\n", tool.Description)
	}
}

// maskKey показывает первые 8 символов ключа для идентификации.
func maskKey(key string) string {
	if key == "" {
		return "NOT SET"
	}
	if len(key) <= 8 {
		return key + "..."
	}
	return key[:8] + "..."
}

// logKeysInfo логирует информацию о загруженных API ключах.
func logKeysInfo(cfg *config.AppConfig) {
	log.Println("=== API Keys Status ===")

	// ZAI API Key (берём из первой модели определения)
	if len(cfg.Models.Definitions) > 0 {
		for _, modelDef := range cfg.Models.Definitions {
			log.Printf("  ZAI_API_KEY (model: %s): %s", modelDef.ModelName, maskKey(modelDef.APIKey))
			break // Показываем только первый
		}
	}

	// S3 Keys
	log.Printf("  S3_ACCESS_KEY: %s", maskKey(cfg.S3.AccessKey))
	log.Printf("  S3_SECRET_KEY: %s", maskKey(cfg.S3.SecretKey))

	// WB API Key
	log.Printf("  WB_API_KEY: %s", maskKey(cfg.WB.APIKey))

	log.Println("======================")
}
