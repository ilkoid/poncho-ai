// Maxiponcho AI TUI Application
// Анализ товаров Wildberries на основе PLM данных из S3
package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ilkoid/poncho-ai/internal/ui"
	appcomponents "github.com/ilkoid/poncho-ai/pkg/app"
	"github.com/ilkoid/poncho-ai/pkg/config"
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

	utils.Info("Maxiponcho application started")

	// 1. Инициализируем конфигурацию из cmd/maxiponcho/
	cfg, cfgPath, err := appcomponents.InitializeConfig(&MaxiponchoConfigPathFinder{})
	if err != nil {
		utils.Error("Failed to load config", "error", err, "path", cfgPath)
		return err
	}

	log.Printf("Config loaded from %s", cfgPath)
	utils.Info("Config loaded", "path", cfgPath, "default_model", cfg.Models.DefaultChat)

	// Логируем загруженные ключи (с маскированием для безопасности)
	logKeysInfo(cfg)

	// 2. Инициализируем компоненты
	components, err := appcomponents.Initialize(
		cfg,
		10,    // max iterations
		"",    // system prompt - загрузится из конфига
	)
	if err != nil {
		utils.Error("Components initialization failed", "error", err)
		return fmt.Errorf("initialization failed: %w", err)
	}

	log.Printf("S3 client initialized (bucket: %s)", cfg.S3.Bucket)
	log.Printf("WB client initialized")
	log.Printf("LLM provider initialized: %s", cfg.Models.DefaultChat)
	log.Printf("Agent orchestrator initialized")

	// 3. Инициализируем TUI модель
	// REFACTORED 2025-01-07: Передаем CoreState и Orchestrator напрямую
	tuiModel := ui.InitialModel(components.State, components.Orchestrator, cfg.Models.DefaultChat)

	// 5. Запускаем Bubble Tea программу
	log.Println("Starting Maxiponcho TUI...")
	utils.Info("Starting TUI")

	p := tea.NewProgram(
		tuiModel,
		// Без AltScreen - позволяет выделять текст мышкой и копировать в буфер обмена
	)

	if _, err := p.Run(); err != nil {
		utils.Error("TUI error", "error", err)
		return fmt.Errorf("TUI error: %w", err)
	}

	utils.Info("Application exited normally")
	return nil
}

// MaxiponchoConfigPathFinder ищет config.yaml в cmd/maxiponcho/
type MaxiponchoConfigPathFinder struct{}

// FindConfigPath находит путь к config.yaml в директории cmd/maxiponcho/
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

	// 3. Текущая директория (для запуска из cmd/maxiponcho/)
	cfgPath = "config.yaml"
	if _, err := os.Stat(cfgPath); err == nil {
		return resolveAbsPath(cfgPath)
	}

	// 4. Родительская директория (для запуска из cmd/maxiponcho/)
	cfgPath = filepath.Join("..", "..", "config.yaml")
	if _, err := os.Stat(cfgPath); err == nil {
		return resolveAbsPath(cfgPath)
	}

	// Возвращаем дефолтный путь
	return resolveAbsPath("cmd/maxiponcho/config.yaml")
}

// resolveAbsPath возвращает абсолютный путь
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

// maskKey показывает первые 8 символов ключа для идентификации
func maskKey(key string) string {
	if key == "" {
		return "NOT SET"
	}
	if len(key) <= 8 {
		return key + "..."
	}
	return key[:8] + "..."
}

// logKeysInfo логирует информацию о загруженных API ключах
func logKeysInfo(cfg *config.AppConfig) {
	log.Println("=== API Keys Status ===")

	if len(cfg.Models.Definitions) > 0 {
		for _, modelDef := range cfg.Models.Definitions {
			log.Printf("  ZAI_API_KEY (model: %s): %s", modelDef.ModelName, maskKey(modelDef.APIKey))
			break
		}
	}

	log.Printf("  S3_ACCESS_KEY: %s", maskKey(cfg.S3.AccessKey))
	log.Printf("  S3_SECRET_KEY: %s", maskKey(cfg.S3.SecretKey))
	log.Printf("  WB_API_CONTENT_KEY: %s", maskKey(cfg.WB.APIKey))

	log.Println("======================")
}
