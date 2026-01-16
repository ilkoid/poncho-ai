// Package agent предоставляет простой API для создания и запуска AI агентов.
//
// Пакет реализует фасад над существующим ReActCycle, позволяя создавать
// агентов с минимальным количеством кода. При этом сохраняется полная
// совместимость с существующим API для продвинутых сценариев.
//
// Basic usage:
//
//	client, _ := agent.New(agent.Config{ConfigPath: "config.yaml"})
//	result, _ := client.Run(ctx, "Find products under 1000₽")
//
// With custom tool:
//
//	client, _ := agent.New(agent.Config{ConfigPath: "config.yaml"})
//	client.RegisterTool(&MyCustomTool{})
//	result, _ := client.Run(ctx, "Check price of SKU123")
package agent

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/app"
	"github.com/ilkoid/poncho-ai/pkg/chain"
	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/events"
	"github.com/ilkoid/poncho-ai/pkg/llm"
	"github.com/ilkoid/poncho-ai/pkg/models"
	"github.com/ilkoid/poncho-ai/pkg/state"
	"github.com/ilkoid/poncho-ai/pkg/tools"
	"github.com/ilkoid/poncho-ai/pkg/utils"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Client представляет AI агент с простым API для запуска запросов.
//
// Client является фасадом над ReActCycle, скрывая сложность инициализации
// компонентов (Config, ModelRegistry, ToolsRegistry, CoreState).
//
// Thread-safe: все методы безопасны для параллельного вызова.
type Client struct {
	// Dependencies (инициализируются в New())
	reactCycle    *chain.ReActCycle
	modelRegistry *models.Registry
	toolsRegistry *tools.Registry
	state         *state.CoreState
	config        *config.AppConfig

	// Optional dependencies (могут быть nil)
	wbClient *wb.Client
	emitter  events.Emitter // Port & Adapter: Emitter для UI подписки

	// emitterMu protects emitter field for concurrent access
	emitterMu sync.RWMutex
}

// Config определяет конфигурацию для создания агента.
//
// Все поля опциональны - при пустых значениях используются дефолты:
//   - ConfigPath: auto-discovery (как в app.DefaultConfigPathFinder)
//   - SystemPrompt: загружается из prompts/agent_system.yaml
//   - MaxIterations: 10 (или из config.yaml)
type Config struct {
	// ConfigPath - путь к config.yaml. Если пустой - используется auto-discovery.
	ConfigPath string

	// SystemPrompt - опциональный override для системного промпта.
	// Если пустой - загружается из конфигурации.
	SystemPrompt string

	// MaxIterations - максимальное количество итераций ReAct цикла.
	// Если 0 - используется значение из config.yaml или дефолт (10).
	MaxIterations int
}

// New создаёт новый агент с указанной конфигурацией.
//
// Функция выполняет полную инициализацию всех компонентов:
//   - Загружает config.yaml
//   - Создаёт S3 клиент (опционально)
//   - Создаём WB клиент
//   - Загружаем справочники WB
//   - Создаём CoreState
//   - Создаём ModelRegistry
//   - Создаём ToolsRegistry и регистрируем tools (только enabled: true)
//   - Загружаем системный промпт и post-prompts
//   - Создаём ReActCycle
//
// Возвращает ошибку если:
//   - config.yaml не найден или невалиден
//   - обязательные зависимости не могут быть созданы
//
// Rule 2: конфигурация через YAML с ENV поддержку.
// Rule 3: tools регистрируются через Registry.
// Rule 6: только pkg/ импорты, без internal/.
// Rule 11: принимает context.Context для распространения отмены.
func New(ctx context.Context, cfg Config) (*Client, error) {
	utils.Info("Creating agent", "config_path", cfg.ConfigPath)

	// 1. Загружаем конфигурацию
	cfgPath := cfg.ConfigPath
	if cfgPath == "" {
		// Auto-discovery как в app.DefaultConfigPathFinder
		if execPath, err := filepath.Abs(filepath.Dir(filepath.Join(".", "config.yaml"))); err == nil {
			cfgPath = filepath.Join(execPath, "config.yaml")
		} else {
			cfgPath = "config.yaml"
		}
	}

	appCfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load config from %s: %w", cfgPath, err)
	}
	utils.Info("Config loaded", "path", cfgPath)

	// 2. Определяем maxIterations
	maxIters := cfg.MaxIterations
	if maxIters == 0 {
		maxIters = 10 // дефолт
	}

	// 3. Инициализируем компоненты (переиспользуем логику из app.Initialize)
	// Это гарантирует что весь init код в одном месте
	// Rule 11: передаём родительский контекст для распространения отмены
	components, err := app.Initialize(ctx, appCfg, maxIters, cfg.SystemPrompt)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize components: %w", err)
	}

	// 4. Создаём Agent фасад
	client := &Client{
		reactCycle:    components.Orchestrator,
		modelRegistry: components.ModelRegistry,
		toolsRegistry: components.State.GetToolsRegistry(),
		state:         components.State,
		config:        components.Config,
		wbClient:      components.WBClient,
	}

	// 5. Устанавливаем streaming конфигурацию из config.yaml
	// Rule 2: конфигурация через YAML
	streamingEnabled := components.Config.App.Streaming.Enabled
	components.Orchestrator.SetStreamingEnabled(streamingEnabled)
	utils.Info("Streaming configured", "enabled", streamingEnabled)

	return client, nil
}

// RegisterTool регистрирует дополнительный инструмент в агенте.
//
// Используется для добавления кастомных tools поверх тех, что
// были загружены из config.yaml.
//
// Rule 1: инструмент должен реализовывать tools.Tool interface.
// Rule 3: регистрация через Registry.
func (c *Client) RegisterTool(tool tools.Tool) error {
	if c.toolsRegistry == nil {
		return fmt.Errorf("tools registry is not initialized")
	}

	toolName := tool.Definition().Name
	if err := c.toolsRegistry.Register(tool); err != nil {
		return fmt.Errorf("failed to register tool '%s': %w", toolName, err)
	}

	utils.Info("Tool registered", "name", toolName)
	return nil
}

// SetEmitter устанавливает emitter для отправки событий.
//
// Port & Adapter паттерн: Client зависит от абстракции (events.Emitter),
// а не от конкретной реализации UI.
//
// Thread-safe.
func (c *Client) SetEmitter(emitter events.Emitter) {
	c.emitterMu.Lock()
	defer c.emitterMu.Unlock()
	c.emitter = emitter
	// Передаём emitter в ReActCycle для отправки событий во время выполнения
	if c.reactCycle != nil {
		c.reactCycle.SetEmitter(emitter)
	}
}

// Subscribe возвращает Subscriber для чтения событий.
//
// Если emitter не установлен, создаёт ChanEmitter с буфером 100.
//
// Thread-safe.
func (c *Client) Subscribe() events.Subscriber {
	c.emitterMu.Lock()
	defer c.emitterMu.Unlock()
	if c.emitter == nil {
		// Создаём дефолтный emitter если не установлен
		c.emitter = events.NewChanEmitter(100)
	}
	return c.emitter.(*events.ChanEmitter).Subscribe()
}

// SetStreamingEnabled включает или выключает streaming режим.
//
// Thread-safe.
func (c *Client) SetStreamingEnabled(enabled bool) {
	if c.reactCycle != nil {
		c.reactCycle.SetStreamingEnabled(enabled)
	}
}

// Run выполняет запрос пользователя через агента.
//
// Метод делегирует выполнение ReActCycle, который:
//  1. Добавляет запрос в историю
//  2. Выполняет ReAct цикл (LLM → Tools → LLM → ...)
//  3. Возвращает финальный ответ
//
// Отправляет события через emitter если установлен (Port & Adapter).
//
// Thread-safe.
//
// Rule 11: принимает context.Context для отмены операции.
func (c *Client) Run(ctx context.Context, query string) (string, error) {
	if c.reactCycle == nil {
		c.emitEvent(ctx, events.Event{
			Type:      events.EventError,
			Data:      events.ErrorData{Err: fmt.Errorf("agent is not properly initialized")},
			Timestamp: time.Now(),
		})
		return "", fmt.Errorf("agent is not properly initialized")
	}

	// EventThinking: агент начинает думать
	c.emitEvent(ctx, events.Event{
		Type:      events.EventThinking,
		Data:      events.ThinkingData{Query: query},
		Timestamp: time.Now(),
	})

	utils.Info("Running agent query", "query", query)

	result, err := c.reactCycle.Run(ctx, query)
	if err != nil {
		// EventError: произошла ошибка
		c.emitEvent(ctx, events.Event{
			Type:      events.EventError,
			Data:      events.ErrorData{Err: err},
			Timestamp: time.Now(),
		})
		utils.Error("Agent query failed", "error", err)
		return "", err
	}

	utils.Info("Agent query completed", "result_length", len(result))
	// EventMessage и EventDone отправляются в react.go
	return result, nil
}

// Execute выполняет запрос с расширенными параметрами через ChainInput.
//
// Этот метод предоставляет полный контроль над выполнением агента, включая:
//   - UserInputChan для механизма прерываний
//   - Кастомную ChainConfig
//   - Прямой доступ к State и Registry
//
// Thread-safe.
//
// Rule 11: принимает context.Context для отмены операции.
func (c *Client) Execute(ctx context.Context, input chain.ChainInput) (chain.ChainOutput, error) {
	if c.reactCycle == nil {
		return chain.ChainOutput{}, fmt.Errorf("agent is not properly initialized")
	}

	utils.Info("Executing agent with ChainInput", "query", input.UserQuery)

	// Выполняем через Chain interface (ReActCycle)
	output, err := c.reactCycle.Execute(ctx, input)
	if err != nil {
		utils.Error("Agent execution failed", "error", err)
		return chain.ChainOutput{}, err
	}

	utils.Info("Agent execution completed", "iterations", output.Iterations)
	return output, nil
}

// emitEvent отправляет событие через emitter если он установлен.
//
// Thread-safe.
// Rule 11: уважает context.Context.
func (c *Client) emitEvent(ctx context.Context, event events.Event) {
	c.emitterMu.RLock()
	defer c.emitterMu.RUnlock()
	if c.emitter == nil {
		utils.Debug("agent.emitEvent: emitter is nil, skipping", "event_type", event.Type)
		return
	}
	utils.Debug("agent.emitEvent: sending", "event_type", event.Type, "has_data", event.Data != nil)
	c.emitter.Emit(ctx, event)
}

// GetHistory возвращает историю диалога агента.
//
// История включает все сообщения: пользовательские, ассистента,
// и результаты выполнения инструментов.
//
// Thread-safe.
func (c *Client) GetHistory() []llm.Message {
	if c.state == nil {
		return []llm.Message{}
	}
	return c.state.GetHistory()
}

// GetModelRegistry возвращает реестр моделей для продвинутых сценариев.
//
// Позволяет напрямую работать с моделями (например, для переключения
// модели в runtime).
func (c *Client) GetModelRegistry() *models.Registry {
	return c.modelRegistry
}

// GetToolsRegistry возвращает реестр инструментов.
//
// Позволяет напрямую работать с tools (например, для динамической
// регистрации/удаления tools).
func (c *Client) GetToolsRegistry() *tools.Registry {
	return c.toolsRegistry
}

// GetState возвращает CoreState для продвинутых сценариев.
//
// Позволяет работать напрямую с состоянием (файлы, задачи, справочники).
func (c *Client) GetState() *state.CoreState {
	return c.state
}

// GetConfig возвращает конфигурацию приложения.
func (c *Client) GetConfig() *config.AppConfig {
	return c.config
}

// ===== PRESET SYSTEM METHODS =====

// NewFromPreset создаёт агент из пресета.
//
// Пресет накладывается поверх config.yaml, переопределяя только нужные параметры.
// Это позволяет запускать приложения с пред-конфигурацией в 2 строки:
//
//	client, err := agent.NewFromPreset(ctx, "interactive-tui")
//	if err != nil {
//	    log.Fatal(err)
//	}
//	result, err := client.Run(ctx, "Show me categories")
//
// Пресеты определяются в pkg/app/presets.go:
//   - simple-cli: Минималистичный CLI интерфейс
//   - interactive-tui: Полнофункциональный TUI с streaming
//   - full-featured: Все фичи для разработки и отладки
//
// Rule 2: конфигурация через YAML (presets это overlays).
func NewFromPreset(ctx context.Context, presetName string) (*Client, error) {
	// 1. Загружаем пресет
	preset, err := app.GetPreset(presetName)
	if err != nil {
		return nil, err
	}

	// 2. Применяем preset оверлеи к config.yaml
	// LoadConfigWithPreset загружает и модифицирует конфиг
	cfg, err := app.LoadConfigWithPreset("config.yaml", preset)
	if err != nil {
		return nil, fmt.Errorf("failed to load config with preset: %w", err)
	}

	// 3. Создаём агент обычным способом
	// ВАЖНО: app.Initialize внутри New() будет использовать модифицированный cfg
	// потому что config.Load кэширует результат или мы передаем cfgPath
	// Но для надежности лучше создать компоненты напрямую
	return NewFromPresetWithConfig(ctx, cfg, preset)
}

// NewFromPresetWithConfig создаёт агент из готового конфига и пресета.
//
// Внутренняя функция для избежания дублирования кода.
func NewFromPresetWithConfig(ctx context.Context, cfg *config.AppConfig, preset *app.PresetConfig) (*Client, error) {
	// Определяем maxIterations
	maxIters := 10 // дефолт
	if cfg.Chains != nil {
		if chainCfg, exists := cfg.Chains["default"]; exists && chainCfg.MaxIterations > 0 {
			maxIters = chainCfg.MaxIterations
		}
	}

	// Инициализируем компоненты с модифицированным конфигом
	components, err := app.Initialize(ctx, cfg, maxIters, "")
	if err != nil {
		return nil, fmt.Errorf("failed to initialize components: %w", err)
	}

	// Создаём Agent фасад
	client := &Client{
		reactCycle:    components.Orchestrator,
		modelRegistry: components.ModelRegistry,
		toolsRegistry: components.State.GetToolsRegistry(),
		state:         components.State,
		config:        components.Config,
		wbClient:      components.WBClient,
	}

	// Устанавливаем streaming конфигурацию
	streamingEnabled := components.Config.App.Streaming.Enabled
	components.Orchestrator.SetStreamingEnabled(streamingEnabled)

	return client, nil
}

// RunPreset — 2-строчный запуск приложения из пресета.
//
// Создаёт агент из пресета и запускает его в зависимости от типа:
//   - AppTypeCLI: консольный интерфейс (stdin/stdout)
//   - AppTypeTUI: терминальный UI (Bubble Tea)
//
// Пример:
//
//	if err := agent.RunPreset(ctx, "interactive-tui"); err != nil {
//	    log.Fatal(err)
//	}
//
// Пресеты определяются в pkg/app/presets.go.
func RunPreset(ctx context.Context, presetName string) error {
	// 1. Создаём агент из пресета
	client, err := NewFromPreset(ctx, presetName)
	if err != nil {
		return err
	}

	// 2. Загружаем пресет
	preset, err := app.GetPreset(presetName)
	if err != nil {
		return err
	}

	// 3. Запускаем с клиентом
	return runPresetWithClient(ctx, client, preset)
}

// runPresetWithClient запускает preset с готовым клиентом.
//
// Обрабатывает TUIRequiredError для TUI пресетов.
func runPresetWithClient(ctx context.Context, client *Client, preset *app.PresetConfig) error {
	err := app.RunPresetWithClient(ctx, client, preset)
	if err != nil {
		// Проверяем нужна ли TUI
		if tuiErr, ok := err.(*app.TUIRequiredError); ok {
			return runTUIFromAgent(ctx, client, tuiErr)
		}
		return err
	}
	return nil
}

// runTUIFromAgent запускает TUI из контекста agent.
//
// Импортируем pkg/tui только здесь для избежания циклического импорта.
func runTUIFromAgent(ctx context.Context, client *Client, tuiErr *app.TUIRequiredError) error {
	// Импортируем tui локально для избежания циклического импорта
	// pkg/agent → pkg/app → pkg/tui (OK!)
	return runTUIImpl(ctx, client, tuiErr.Emitter, tuiErr.Preset.UI)
}

// runTUIImpl — реализация запуска TUI (определена ниже для избежания цикла).
// Инициализируется через init() с фактической реализацией из pkg/tui.
var runTUIImpl func(ctx context.Context, client *Client, emitter events.Emitter, uiConfig app.SimpleUIConfig) error

func init() {
	// Устанавливаем фактическую реализацию runTUIImpl
	// Это избегает циклического импорта: pkg/agent может ссылаться на pkg/app,
	// а pkg/app может вызывать этот callback.
	runTUIImpl = func(ctx context.Context, client *Client, emitter events.Emitter, uiConfig app.SimpleUIConfig) error {
		// Здесь нужен импорт pkg/tui, но мы не можем сделать это напрямую
		// из-за возможного цикла. Вместо этого используем type assertion
		// или defer фактическую реализацию в момент первого вызова.
		//
		// Для простоты сейчас возвращаем ошибку - пользователь должен
		// использовать pkg/tui напрямую для TUI пресетов.

		// ПРИМЕЧАНИЕ: Для TUI пресетов пользователь должен использовать:
		//   client, _ := agent.NewFromPreset(ctx, "interactive-tui")
		//   sub := client.Subscribe() // или через emitter
		//   tui := tui.NewSimpleTui(sub, ...)
		//   tui.Run()
		//
		// Либо использовать app.RunPreset() напрямую из main.go.

		return fmt.Errorf("TUI presets require direct use of pkg/tui package. Use: client, _ := agent.NewFromPreset(ctx, 'interactive-tui'); then create tui.NewSimpleTui()")
	}
}

// Ensure Client implements Agent interface
var _ Agent = (*Client)(nil)
