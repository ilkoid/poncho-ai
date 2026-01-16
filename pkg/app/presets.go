// Package app предоставляет Presets System для быстрого запуска AI агентов.
//
// Presets позволяют запускать приложения в 2 строки:
//   app.RunPreset(ctx, "interactive-tui")
//
// Пресеты не заменяют config.yaml, а накладываются поверх него,
// переопределяя только нужные параметры.
//
// # Basic Presets
//
// Пакет предоставляет 3 базовых пресета:
//   - simple-cli: Минималистичный CLI интерфейс
//   - interactive-tui: Полнофункциональный TUI с streaming
//   - full-featured: Все фичи для разработки и отладки
//
// # Custom Presets
//
// Пользователи могут регистрировать свои пресеты через RegisterPreset():
//
//	app.RegisterPreset("my-ecommerce", &PresetConfig{
//	    Type: AppTypeTUI,
//	    EnableBundles: []string{"wb-tools", "vision-tools"},
//	    Models: ModelSelection{Reasoning: "glm-4.6"},
//	    UI: SimpleUIConfig{Title: "My E-commerce AI"},
//	})
//
// Rule 6: Reusable library code, no app-specific logic.
// Rule 2: Configuration through YAML (presets are overlays).
package app

import (
	"bufio"
	"context"
	"fmt"
	"os"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/events"
)

// AgentClient — интерфейс для агента (избегаем циклический импорт).
//
// Определяем минимальный интерфейс который нужен preset system.
// Реализуется в pkg/agent.Client.
type AgentClient interface {
	// Run выполняет запрос к агенту (синхронно)
	Run(ctx context.Context, query string) (string, error)

	// SetEmitter устанавливает emitter для событий
	SetEmitter(emitter events.Emitter)
}

// Presets — встроенные пресеты приложения.
//
// Может быть расширен пользователем через RegisterPreset().
var Presets = map[string]*PresetConfig{
	// simple-cli — минималистичный CLI интерфейс для быстрых взаимодействий
	"simple-cli": {
		Name:        "simple-cli",
		Type:        AppTypeCLI,
		Description: "Minimal CLI interface for quick interactions",
		EnableBundles: []string{}, // Пусто — берем из config.yaml
		Features:      []string{"streaming"},
		Models: ModelSelection{
			Chat: "glm-4.6",
		},
		UI: SimpleUIConfig{
			Title:       "AI CLI",
			InputPrompt: "AI> ",
		},
	},

	// interactive-tui — полнофункциональный TUI с event streaming
	"interactive-tui": {
		Name:        "interactive-tui",
		Type:        AppTypeTUI,
		Description: "Full-featured TUI with event streaming",
		EnableBundles: []string{}, // Пусто — берем из config.yaml
		Features:      []string{"streaming", "interruptions"},
		Models: ModelSelection{
			Reasoning: "glm-4.6",
		},
		UI: SimpleUIConfig{
			Title:          "Poncho AI",
			InputPrompt:    "> ",
			ShowTimestamp:  true,
			StreamingStatus: "ON",
			Colors: ColorScheme{
				StatusBackground: "#282a36", // Dracula-like
				StatusForeground: "#f8f8f2",
				SystemMessage:    "#6272a4",
				UserMessage:      "#f1fa8c",
				AIMessage:        "#8be9fd",
				ErrorMessage:     "#ff5555",
				Thinking:         "#bd93f9",
				ThinkingDim:      "#44475a",
				InputPrompt:      "#f8f8f2",
				Border:           "#44475a",
			},
		},
	},

	// full-featured — все фичи включены для разработки и отладки
	"full-featured": {
		Name:        "full-featured",
		Type:        AppTypeTUI,
		Description: "All features enabled for development/debugging",
		EnableBundles: []string{}, // User specifies via config.yaml
		Features:      []string{"streaming", "debug", "interruptions"},
		Models: ModelSelection{
			Reasoning: "glm-4.6",
			Vision:    "glm-4.6v-flash",
		},
		UI: SimpleUIConfig{
			Title:          "Poncho AI (Debug)",
			InputPrompt:    "DEBUG> ",
			ShowTimestamp:  true,
			MaxMessages:    1000, // Large buffer for debugging
			StreamingStatus: "ON",
		},
	},
}

// GetPreset получает пресет по имени.
//
// Возвращает ошибку с подсказкой доступных пресетов если не найден.
//
// Пример:
//
//	preset, err := app.GetPreset("interactive-tui")
//	if err != nil {
//	    log.Fatal(err)
//	}
func GetPreset(name string) (*PresetConfig, error) {
	preset, ok := Presets[name]
	if !ok {
		available := make([]string, 0, len(Presets))
		for n := range Presets {
			available = append(available, n)
		}
		return nil, fmt.Errorf("preset '%s' not found. Available presets: %v", name, available)
	}
	return preset, nil
}

// RegisterPreset регистрирует пользовательский пресет.
//
// Позволяет пользователям создавать свои пресеты поверх базовых.
//
// Пример:
//
//	app.RegisterPreset("my-ecommerce", &PresetConfig{
//	    Name: "my-ecommerce",
//	    Type: AppTypeTUI,
//	    EnableBundles: []string{"wb-tools", "vision-tools"},
//	    Models: ModelSelection{Reasoning: "glm-4.6"},
//	    UI: SimpleUIConfig{Title: "My E-commerce AI"},
//	})
//
// Возвращает ошибку если пресет с таким именем уже существует.
func RegisterPreset(name string, preset *PresetConfig) error {
	if _, exists := Presets[name]; exists {
		return fmt.Errorf("preset '%s' already exists", name)
	}
	Presets[name] = preset
	return nil
}

// RunPresetWithClient — запуск приложения из пресета с готовым клиентом.
//
// Используется внутри pkg/agent для избежания циклического импорта.
//
// Пример:
//
//	client, _ := agent.New(ctx, agent.Config{ConfigPath: "config.yaml"})
//	preset, _ := app.GetPreset("interactive-tui")
//	if err := app.RunPresetWithClient(ctx, client, preset); err != nil {
//	    log.Fatal(err)
//	}
func RunPresetWithClient(ctx context.Context, client AgentClient, preset *PresetConfig) error {
	// Запускаем в зависимости от типа
	switch preset.Type {
	case AppTypeCLI:
		return runCLIPreset(ctx, client, preset)
	case AppTypeTUI:
		return runTUIPreset(ctx, client, preset)
	default:
		return fmt.Errorf("unsupported app type: %d", preset.Type)
	}
}

// runCLIPreset запускает CLI версию агента.
//
// Простая консольная петля с чтением из stdin и выводом в stdout.
func runCLIPreset(ctx context.Context, client AgentClient, preset *PresetConfig) error {
	fmt.Printf("=== %s ===\n", preset.Name)
	fmt.Println("Type 'exit' or 'quit' to exit")
	fmt.Println()

	scanner := bufio.NewScanner(os.Stdin)

	for {
		fmt.Print(preset.UI.InputPrompt)
		if !scanner.Scan() {
			break
		}

		query := scanner.Text()
		if query == "exit" || query == "quit" {
			fmt.Println("Goodbye!")
			break
		}

		if query == "" {
			continue
		}

		result, err := client.Run(ctx, query)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			continue
		}

		fmt.Println(result)
		fmt.Println()
	}

	return scanner.Err()
}

// runTUIPreset запускает TUI версию агента.
//
// Настраивает emitter и возвращает специальную ошибку для запуска TUI.
// Вызывающий код (в pkg/agent) должен создать TUI и запустить его.
func runTUIPreset(ctx context.Context, client AgentClient, preset *PresetConfig) error {
	// Настраиваем emitter
	emitter := events.NewChanEmitter(100)
	client.SetEmitter(emitter)

	// Возвращаем специальную ошибку с emitter для запуска TUI
	return &TUIRequiredError{
		Emitter: emitter,
		Preset:  preset,
	}
}

// TUIRequiredError — специальная ошибка для запуска TUI.
//
// Возвращается из runTUIPreset когда нужен TUI.
// Содержит emitter который уже настроен.
type TUIRequiredError struct {
	Emitter events.Emitter
	Preset  *PresetConfig
}

func (e *TUIRequiredError) Error() string {
	return "TUI required"
}

// LoadConfigWithPreset загружает config.yaml и применяет пресет оверлеи.
//
// Используется внутри agent.NewFromPreset().
func LoadConfigWithPreset(cfgPath string, preset *PresetConfig) (*config.AppConfig, error) {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, err
	}

	// Применяем пресет оверлеи

	// 1. Модели
	if preset.Models.Chat != "" {
		cfg.Models.DefaultChat = preset.Models.Chat
	}
	if preset.Models.Reasoning != "" {
		cfg.Models.DefaultReasoning = preset.Models.Reasoning
	}
	if preset.Models.Vision != "" {
		cfg.Models.DefaultVision = preset.Models.Vision
	}

	// 2. Bundles
	if len(preset.EnableBundles) > 0 {
		cfg.EnableBundles = preset.EnableBundles
	}

	// 3. Features
	for _, feature := range preset.Features {
		switch feature {
		case "streaming":
			cfg.App.Streaming.Enabled = true
		case "debug":
			cfg.App.DebugLogs.Enabled = true
		case "interruptions":
			// Уже включено по умолчанию в ReActCycle
		}
	}

	return cfg, nil
}
