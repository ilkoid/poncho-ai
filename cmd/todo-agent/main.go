// Todo Agent - TUI утилита для управления задачами через AI агента
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ilkoid/poncho-ai/cmd/todo-agent/tui"
	"github.com/ilkoid/poncho-ai/pkg/agent"
	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/events"
	"github.com/ilkoid/poncho-ai/pkg/utils"
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

	utils.Info("Todo Agent started", "version", "1.0")

	// 1. Загружаем локальную конфигурацию (Rule 11: рядом с бинарником)
	cfgPath := "./config.yaml"
	cfg, err := config.Load(cfgPath)
	if err != nil {
		utils.Error("Failed to load config", "error", err, "path", cfgPath)
		return fmt.Errorf("config load failed: %w", err)
	}

	log.Printf("Config loaded successfully from %s", cfgPath)
	utils.Info("Config loaded", "path", cfgPath, "default_model", cfg.Models.DefaultChat)

	// Логируем загруженные ключи (с маскированием для безопасности)
	logKeysInfo(cfg)

	// 2. Rule 11: Создаём родительский контекст для инициализации
	initCtx, initCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer initCancel()

	// 3. Создаём агент через pkg/agent с контекстом
	client, err := agent.New(initCtx, agent.Config{ConfigPath: cfgPath})
	if err != nil {
		utils.Error("Agent creation failed", "error", err)
		return fmt.Errorf("agent creation failed: %w", err)
	}

	// 3. Создаём emitter для событий агента (Port & Adapter)
	emitter := events.NewChanEmitter(100)
	client.SetEmitter(emitter)

	log.Printf("Agent client initialized with event emitter, emitter_ptr=%p", emitter)

	// 4. Получаем subscriber для TUI
	sub := client.Subscribe()
	log.Printf("TUI subscriber created, sub_ptr=%p", sub)

	// 5. Инициализируем TUI модель
	tuiModel := tui.InitialModel(client, cfg.Models.DefaultChat, sub)

	// 6. Запускаем Bubble Tea программу
	log.Println("Starting TUI...")
	utils.Info("Starting TUI")

	p := tea.NewProgram(
		tuiModel,
		tea.WithAltScreen(),     // Полноэкранный TUI режим
		tea.WithInput(os.Stdin), // Явно используем stdin (фикс для WSL2)
		tea.WithOutput(os.Stdout), // Явно используем stdout (фикс для WSL2)
	)

	if _, err := p.Run(); err != nil {
		utils.Error("TUI error", "error", err)
		return fmt.Errorf("TUI error: %w", err)
	}

	utils.Info("Application exited normally")
	return nil
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
	log.Printf("  WB_API_CONTENT_KEY: %s", maskKey(cfg.WB.APIKey))

	log.Println("======================")
}
