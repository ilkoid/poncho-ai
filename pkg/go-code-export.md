# agent/agent.go

```go
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
	reactCycle     *chain.ReActCycle
	modelRegistry  *models.Registry
	toolsRegistry  *tools.Registry
	state          *state.CoreState
	config         *config.AppConfig

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
		reactCycle:     components.Orchestrator,
		modelRegistry:  components.ModelRegistry,
		toolsRegistry:  components.State.GetToolsRegistry(),
		state:          components.State,
		config:         components.Config,
		wbClient:       components.WBClient,
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
//   1. Добавляет запрос в историю
//   2. Выполняет ReAct цикл (LLM → Tools → LLM → ...)
//   3. Возвращает финальный ответ
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

// Ensure Client implements Agent interface
var _ Agent = (*Client)(nil)

```

=================

# agent/types.go

```go
// Package agent предоставляет простой API для создания и запуска AI агентов.
//
// Пакет agent является фасадом над pkg/chain, предоставляя более удобный API
// для создания агентов. Интерфейс Agent определён в pkg/chain для избежания
// циклических импортов.
package agent

import (
	"github.com/ilkoid/poncho-ai/pkg/chain"
)

// Agent - это переэкспорт интерфейса из pkg/chain.
//
// Переэкспорт выполняется для обратной совместимости и удобства использования:
//   import "github.com/ilkoid/poncho-ai/pkg/agent"
//
//   var a agent.Agent = ...
//
// Оригинальный интерфейс определён в pkg/chain.Agent.
type Agent = chain.Agent

```

=================

# app/components.go

```go
// Package app предоставляет переиспользуемые компоненты для инициализации
// и выполнения AI-агента в разных контекстах (CLI, TUI, HTTP и т.д.).
//
// Пакет следует правилам из dev_manifest.md:
//   - Работает через llm.Provider интерфейс (Правило 4)
//   - Использует tools.Registry (Правило 3)
//   - Использует thread-safe GlobalState (Правило 5)
//   - Все ошибки возвращаются, никаких panic (Правило 7)
package app

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/chain"
	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/llm"
	"github.com/ilkoid/poncho-ai/pkg/models"
	"github.com/ilkoid/poncho-ai/pkg/prompt"
	"github.com/ilkoid/poncho-ai/pkg/s3storage"
	"github.com/ilkoid/poncho-ai/pkg/state"
	"github.com/ilkoid/poncho-ai/pkg/todo"
	"github.com/ilkoid/poncho-ai/pkg/tools"
	"github.com/ilkoid/poncho-ai/pkg/tools/std"
	"github.com/ilkoid/poncho-ai/pkg/utils"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Components содержит все компоненты приложения для переиспользования.
//
// Эта структура может быть использована в TUI для избежания дублирования
// кода инициализации между CLI и GUI версиями.
//
// REFACTORED 2025-01-07: State теперь *state.CoreState (framework core).
// UI-specific состояние (Orchestrator, CurrentModel и т.д.) хранится в TUI Model.
// REFACTORED 2025-01-07: Orchestrator теперь *chain.ReActCycle (конкретный тип),
// чтобы избежать циклического импорта pkg/app → pkg/agent → pkg/app.
type Components struct {
	Config        *config.AppConfig
	State         *state.CoreState // Framework core (без TUI-specific полей)
	ModelRegistry *models.Registry // Registry всех LLM провайдеров
	LLM           llm.Provider     // DEPRECATED: Используйте ModelRegistry
	VisionLLM     llm.Provider     // Vision модель (для batch image analysis)
	WBClient      *wb.Client
	Orchestrator  *chain.ReActCycle // ReActCycle implements Agent interface
}

// ExecutionResult содержит результаты выполнения запроса.
//
// Используется для отделения логики вывода от логики выполнения,
// что позволяет переиспользовать код в TUI с собственным рендерингом.
type ExecutionResult struct {
	Response  string        // Финальный ответ оркестратора
	TodoString string        // Состояние todo листа (formatted)
	TodoStats  TodoStats     // Статистика задач
	History    []llm.Message // История сообщений
	Duration   time.Duration // Время выполнения
}

// TodoStats представляет статистику задач.
type TodoStats struct {
	Pending int
	Done    int
	Failed  int
	Total   int
}

// ConfigPathFinder определяет стратегию поиска пути к config.yaml.
//
// По умолчанию используется DefaultConfigPathFinder, но можно
// реализовать свою стратегию для тестов или специальных случаев.
type ConfigPathFinder interface {
	FindConfigPath() string
}

// DefaultConfigPathFinder реализует стандартную стратегию поиска config.yaml.
//
// Правило 11: автономные приложения хранят ресурсы рядом с бинарником.
//
// Упрощенный порядок поиска:
// 1. Флаг -config (явное указание)
// 2. Директория бинарника (autonomous deployment)
// 3. Ошибка при загрузке (если config не найден)
type DefaultConfigPathFinder struct {
	// ConfigFlag - значение флага -config, если указан
	ConfigFlag string
}

// FindConfigPath находит путь к config.yaml.
//
// Правило 11: автономные приложения хранят ресурсы рядом.
// Логика поиска (в порядке приоритета):
//   1. Явно указанный флаг --config
//   2. Директория бинарника (autonomous deployment)
//   3. Ошибка (неудача - config не найден)
func (f *DefaultConfigPathFinder) FindConfigPath() string {
	// 1. Флаг имеет наивысший приоритет
	if f.ConfigFlag != "" {
		return resolveAbsPath(f.ConfigFlag)
	}

	// 2. Директория бинарника (autonomous deployment)
	if execPath, err := os.Executable(); err == nil {
		binDir := filepath.Dir(execPath)
		cfgPath := filepath.Join(binDir, "config.yaml")
		if _, err := os.Stat(cfgPath); err == nil {
			return cfgPath
		}
	}

	// 3. Config не найден - возвращаем дефолтный путь, ошибка будет при загрузке
	return resolveAbsPath("config.yaml")
}

// InitializeConfig инициализирует и загружает конфигурацию.
//
// Правило 2: все настройки в YAML с поддержкой ENV-переменных.
func InitializeConfig(finder ConfigPathFinder) (*config.AppConfig, string, error) {
	cfgPath := finder.FindConfigPath()

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, "", fmt.Errorf("failed to load config from %s: %w", cfgPath, err)
	}

	return cfg, cfgPath, nil
}

// Initialize создаёт и инициализирует все компоненты приложения.
//
// Эта функция является переиспользуемой - она может быть вызвана
// из TUI версии без изменений. Вся логика инициализации инкапсулирована здесь.
//
// Правило 6: entry points - initialization and orchestration only.
// Правило 5: использует thread-safe GlobalState.
// Правило 11: распространяет context.Context через все слои.
//
// Параметры:
//   - parentCtx: родительский контекст для отмены операций
//   - cfg: конфигурация приложения
//   - maxIters: максимальное количество итераций оркестратора
//   - systemPrompt: опциональный системный промпт (если пустой, загружается из промптов)
func Initialize(parentCtx context.Context, cfg *config.AppConfig, maxIters int, systemPrompt string) (*Components, error) {
	utils.Info("Initializing components", "maxIters", maxIters)

	// 1. Инициализируем S3 клиент (ОПЦИОНАЛЬНО)
	// REFACTORED 2026-01-04: S3 теперь опционален, ошибка не прерывает инициализацию
	var s3Client *s3storage.Client
	s3Client, err := s3storage.New(cfg.S3)
	if err != nil {
		utils.Warn("S3 client creation failed, continuing without S3", "error", err)
		// НЕ прерываем инициализацию - S3 опционален
		s3Client = nil
	} else {
		utils.Info("S3 client initialized", "bucket", cfg.S3.Bucket)
	}

	// 2. Инициализируем WB клиент из конфигурации
	var wbClient *wb.Client
	if cfg.WB.APIKey != "" {
		var err error
		wbClient, err = wb.NewFromConfig(cfg.WB)
		if err != nil {
			utils.Error("WB client creation failed", "error", err)
			return nil, fmt.Errorf("failed to create WB client: %w", err)
		}
		// Ping с параметрами из конфига
		wbCfg := cfg.WB.GetDefaults()
		if _, err := wbClient.Ping(parentCtx, wbCfg.BaseURL, wbCfg.RateLimit, wbCfg.BurstLimit); err != nil {
			log.Printf("Warning: WB API ping failed: %v", err)
			utils.Error("WB API ping failed", "error", err)
		} else {
			utils.Info("WB API ping successful")
		}
	} else {
		log.Println("Warning: WB API key not set - using demo client")
		utils.Info("WB API key not set, using demo client")
		wbClient = wb.New("demo_key")
	}
	utils.Info("WB client initialized",
		"api_key_set", cfg.WB.APIKey != "",
		"rate_limit", cfg.WB.RateLimit,
		"burst_limit", cfg.WB.BurstLimit)

	// 3. Загружаем справочники WB (кэшируем при старте)
	var dicts *wb.Dictionaries
	if wbClient != nil {
		// Применяем defaults для WB секции
		wbCfg := cfg.WB.GetDefaults()
		dicts, err = wbClient.LoadDictionaries(parentCtx, wbCfg.BaseURL, wbCfg.RateLimit, wbCfg.BurstLimit)
		if err != nil {
			log.Printf("Warning: failed to load WB dictionaries: %v", err)
			utils.Error("WB dictionaries loading failed", "error", err)
			// Продолжаем работу со справочниками = nil (dictionary tools вернут ошибку)
		} else {
			utils.Info("WB dictionaries loaded",
				"colors", len(dicts.Colors),
				"genders", len(dicts.Genders),
				"countries", len(dicts.Countries),
				"seasons", len(dicts.Seasons),
				"vats", len(dicts.Vats))
		}
	}

	// 4. Создаём CoreState (thread-safe)
	// REFACTORED 2025-01-07: Используем state.NewCoreState вместо app.NewAppState
	state := state.NewCoreState(cfg)
	state.SetDictionaries(dicts) // Сохраняем справочники в CoreState

	// 5. Создаём Todo Manager для управления задачами
	// Rule 3: Registry pattern через TodoRepository interface
	todoManager := todo.NewManager()
	if err := state.SetTodoManager(todoManager); err != nil {
		return nil, fmt.Errorf("failed to set todo manager: %w", err)
	}
	utils.Info("Todo manager initialized")

	// 6. Устанавливаем S3 клиент опционально
	if s3Client != nil {
		if err := state.SetStorage(s3Client); err != nil {
			utils.Warn("Failed to set S3 client in state", "error", err)
		} else {
			utils.Info("S3 client set in CoreState")
		}
	}

	// 6. Создаём Model Registry со всеми моделями из config.yaml
	// Rule 3: Registry pattern для централизованного управления моделями
	modelRegistry, err := models.NewRegistryFromConfig(cfg)
	if err != nil {
		utils.Error("Model registry creation failed", "error", err)
		return nil, fmt.Errorf("failed to create model registry: %w", err)
	}
	utils.Info("Model registry created", "models", modelRegistry.ListNames())

	// Определяем дефолтную модель для reasoning
	defaultReasoning := cfg.Models.DefaultReasoning
	if defaultReasoning == "" {
		defaultReasoning = cfg.Models.DefaultChat
	}
	if defaultReasoning == "" {
		return nil, fmt.Errorf("neither default_reasoning nor default_chat configured")
	}

	// Валидируем что дефолтная модель существует
	if _, _, err := modelRegistry.Get(defaultReasoning); err != nil {
		return nil, fmt.Errorf("default reasoning model '%s' not found: %w", defaultReasoning, err)
	}

	// Vision LLM (для batch image analysis) - получаем из registry
	var visionLLM llm.Provider
	if cfg.Models.DefaultVision != "" {
		visionLLM, _, err = modelRegistry.Get(cfg.Models.DefaultVision)
		if err != nil {
			utils.Warn("Vision model not found in registry", "model", cfg.Models.DefaultVision, "error", err)
		} else {
			utils.Info("Vision LLM retrieved from registry", "model", cfg.Models.DefaultVision)
		}
	}

	// 7. Создаём Tools Registry и сохраняем в CoreState
	// Rule 3: Registry pattern для управления инструментами
	toolsRegistry := tools.NewRegistry()
	if err := state.SetToolsRegistry(toolsRegistry); err != nil {
		return nil, fmt.Errorf("failed to set tools registry: %w", err)
	}

	// 8. Регистрируем инструменты из YAML конфигурации
	// Передаем CoreState для регистрации инструментов (Rule 6: framework logic)
	if err := SetupTools(state, wbClient, visionLLM, cfg); err != nil {
		utils.Error("Tools registration failed", "error", err)
		return nil, fmt.Errorf("failed to register tools: %w", err)
	}

	// 9. Загружаем системный промпт
	agentSystemPrompt := systemPrompt
	if agentSystemPrompt == "" {
		agentPrompt, err := prompt.LoadAgentSystemPrompt(cfg)
		if err != nil {
			log.Printf("Warning: failed to load agent prompt: %v", err)
			agentSystemPrompt = "" // Используем дефолтный из orchestrator
		} else {
			agentSystemPrompt = agentPrompt
		}
	}

	// 10. Загружаем post-prompts для tools
	toolPostPrompts, err := prompt.LoadToolPostPrompts(cfg)
	if err != nil {
		utils.Error("Failed to load tool post-prompts", "error", err)
		return nil, fmt.Errorf("failed to load tool post-prompts: %w", err)
	}
	if len(toolPostPrompts.Tools) > 0 {
		utils.Info("Tool post-prompts loaded", "count", len(toolPostPrompts.Tools))
	}

	// 11. Создаём ReActCycle напрямую (Rule 6 compliance)
	// Timeout берётся из конфигурации chains.default.timeout с fallback на дефолт
	cycleConfig := chain.ReActCycleConfig{
		SystemPrompt:    agentSystemPrompt,
		ToolPostPrompts: toolPostPrompts,
		PromptsDir:      cfg.App.PromptsDir,
		MaxIterations:   maxIters,
		Timeout:         cfg.GetChainTimeout("default"),
	}

	reactCycle := chain.NewReActCycle(cycleConfig)
	reactCycle.SetModelRegistry(modelRegistry, defaultReasoning)
	reactCycle.SetRegistry(state.GetToolsRegistry())
	reactCycle.SetState(state)

	// Создаём debug recorder если включён
	debugRecorder, err := chain.NewChainDebugRecorder(chain.DebugConfig{
		Enabled:             cfg.App.DebugLogs.Enabled,
		SaveLogs:            cfg.App.DebugLogs.SaveLogs,
		LogsDir:             cfg.App.DebugLogs.LogsDir,
		IncludeToolArgs:     cfg.App.DebugLogs.IncludeToolArgs,
		IncludeToolResults:  cfg.App.DebugLogs.IncludeToolResults,
		MaxResultSize:       cfg.App.DebugLogs.MaxResultSize,
	})
	if err != nil {
		utils.Error("Failed to create debug recorder", "error", err)
	} else if debugRecorder.Enabled() {
		reactCycle.AttachDebug(debugRecorder)
	}

	return &Components{
		Config:        cfg,
		State:         state,
		ModelRegistry: modelRegistry,
		LLM:           nil, // DEPRECATED: Используйте ModelRegistry
		VisionLLM:     visionLLM,
		WBClient:      wbClient,
		Orchestrator:  reactCycle,
	}, nil
}

// Execute выполняет запрос через оркестратор.
//
// Эта функция является переиспользуемой - она может быть вызвана
// из TUI версии для выполнения запросов с тем же кодом.
//
// Правило 4: работает только через llm.Provider интерфейс.
// Правило 3: использует tools.Registry для инструментов.
// Правило 11: распространяет context.Context через все слои.
func Execute(parentCtx context.Context, c *Components, query string, timeout time.Duration) (*ExecutionResult, error) {
	startTime := time.Now()
	utils.Info("Executing query", "query", query, "timeout", timeout)

	// Создаём контекст с timeout
	ctx, cancel := context.WithTimeout(parentCtx, timeout)
	defer cancel()

	log.Printf("Executing query: %s", query)

	// Выполняем запрос через оркестратор
	response, err := c.Orchestrator.Run(ctx, query)
	if err != nil {
		utils.Error("Orchestrator execution failed", "error", err, "query", query)
		return nil, fmt.Errorf("orchestrator error: %w", err)
	}

	utils.Info("Query executed successfully",
		"response_length", len(response),
		"duration_ms", time.Since(startTime).Milliseconds())

	// Собираем результаты
	result := &ExecutionResult{
		Response:   response,
		TodoString: c.State.GetTodoString(),
		History:    c.Orchestrator.GetHistory(),
		Duration:   time.Since(startTime),
	}

	// Получаем статистику задач
	pending, done, failed := c.State.GetTodoStats()
	result.TodoStats = TodoStats{
		Pending: pending,
		Done:    done,
		Failed:  failed,
		Total:   pending + done + failed,
	}

	return result, nil
}

// SetupTools регистрирует инструменты из YAML конфигурации.
//
// Правило 3: все инструменты регистрируются через Registry.Register().
// Правило 1: инструменты реализуют Tool интерфейс.
// Правило 6: Принимает *state.CoreState (framework core) вместо *app.AppState.
//
// Конфигурация читается из cfg.Tools, где ключом является имя tool.
// Инструмент регистрируется только если cfg.Tools[toolName].Enabled == true.
//
// Возвращает ошибку если:
//   - инструмент не найден в коде (unknown tool)
//   - валидация схемы инструмента не прошла
func SetupTools(state *state.CoreState, wbClient *wb.Client, visionLLM llm.Provider, cfg *config.AppConfig) error {
	registry := state.GetToolsRegistry()
	var registered []string

	// Helper для регистрации с логированием
	register := func(name string, tool tools.Tool) error {
		if err := registry.Register(tool); err != nil {
			return fmt.Errorf("failed to register tool '%s': %w", name, err)
		}
		registered = append(registered, name)
		return nil
	}

	// Helper для получения конфигурации tool с дефолтными значениями
	getToolCfg := func(name string) (config.ToolConfig, bool) {
		tc, exists := cfg.Tools[name]
		if !exists {
			// Если tool не указан в конфиге, возвращаем disabled
			return config.ToolConfig{Enabled: false}, false
		}
		return tc, true
	}

	// === WB Content API Tools ===
	if toolCfg, exists := getToolCfg("search_wb_products"); exists && toolCfg.Enabled {
		if err := register("search_wb_products", std.NewWbProductSearchTool(wbClient, toolCfg, cfg.WB)); err != nil {
			return err
		}
	}

	if toolCfg, exists := getToolCfg("get_wb_parent_categories"); exists && toolCfg.Enabled {
		if err := register("get_wb_parent_categories", std.NewWbParentCategoriesTool(wbClient, toolCfg, cfg.WB)); err != nil {
			return err
		}
	}

	if toolCfg, exists := getToolCfg("get_wb_subjects"); exists && toolCfg.Enabled {
		if err := register("get_wb_subjects", std.NewWbSubjectsTool(wbClient, toolCfg, cfg.WB)); err != nil {
			return err
		}
	}

	if toolCfg, exists := getToolCfg("ping_wb_api"); exists && toolCfg.Enabled {
		if err := register("ping_wb_api", std.NewWbPingTool(wbClient, toolCfg, cfg.WB)); err != nil {
			return err
		}
	}

	// === WB Feedbacks API Tools (новые) ===
	if toolCfg, exists := getToolCfg("get_wb_feedbacks"); exists && toolCfg.Enabled {
		if err := register("get_wb_feedbacks", std.NewWbFeedbacksTool(wbClient, toolCfg)); err != nil {
			return err
		}
	}

	if toolCfg, exists := getToolCfg("get_wb_questions"); exists && toolCfg.Enabled {
		if err := register("get_wb_questions", std.NewWbQuestionsTool(wbClient, toolCfg)); err != nil {
			return err
		}
	}

	if toolCfg, exists := getToolCfg("get_wb_new_feedbacks_questions"); exists && toolCfg.Enabled {
		if err := register("get_wb_new_feedbacks_questions", std.NewWbNewFeedbacksQuestionsTool(wbClient, toolCfg)); err != nil {
			return err
		}
	}

	if toolCfg, exists := getToolCfg("get_wb_unanswered_feedbacks_counts"); exists && toolCfg.Enabled {
		if err := register("get_wb_unanswered_feedbacks_counts", std.NewWbUnansweredFeedbacksCountsTool(wbClient, toolCfg)); err != nil {
			return err
		}
	}

	if toolCfg, exists := getToolCfg("get_wb_unanswered_questions_counts"); exists && toolCfg.Enabled {
		if err := register("get_wb_unanswered_questions_counts", std.NewWbUnansweredQuestionsCountsTool(wbClient, toolCfg)); err != nil {
			return err
		}
	}

	// === WB Dictionary Tools ===
	if state.GetDictionaries() != nil {
		if toolCfg, exists := getToolCfg("wb_colors"); exists && toolCfg.Enabled {
			if err := register("wb_colors", std.NewWbColorsTool(state.GetDictionaries(), toolCfg)); err != nil {
				return err
			}
		}

		if toolCfg, exists := getToolCfg("wb_countries"); exists && toolCfg.Enabled {
			if err := register("wb_countries", std.NewWbCountriesTool(state.GetDictionaries(), toolCfg)); err != nil {
				return err
			}
		}

		if toolCfg, exists := getToolCfg("wb_genders"); exists && toolCfg.Enabled {
			if err := register("wb_genders", std.NewWbGendersTool(state.GetDictionaries(), toolCfg)); err != nil {
				return err
			}
		}

		if toolCfg, exists := getToolCfg("wb_seasons"); exists && toolCfg.Enabled {
			if err := register("wb_seasons", std.NewWbSeasonsTool(state.GetDictionaries(), toolCfg)); err != nil {
				return err
			}
		}

		if toolCfg, exists := getToolCfg("wb_vat_rates"); exists && toolCfg.Enabled {
			if err := register("wb_vat_rates", std.NewWbVatRatesTool(state.GetDictionaries(), toolCfg)); err != nil {
				return err
			}
		}
	}

	// === WB Characteristics Tools ===
	if toolCfg, exists := getToolCfg("get_wb_subjects_by_name"); exists && toolCfg.Enabled {
		if err := register("get_wb_subjects_by_name", std.NewWbSubjectsByNameTool(wbClient, toolCfg, cfg.WB)); err != nil {
			return err
		}
	}

	if toolCfg, exists := getToolCfg("get_wb_characteristics"); exists && toolCfg.Enabled {
		if err := register("get_wb_characteristics", std.NewWbCharacteristicsTool(wbClient, toolCfg, cfg.WB)); err != nil {
			return err
		}
	}

	if toolCfg, exists := getToolCfg("get_wb_tnved"); exists && toolCfg.Enabled {
		if err := register("get_wb_tnved", std.NewWbTnvedTool(wbClient, toolCfg, cfg.WB)); err != nil {
			return err
		}
	}

	if toolCfg, exists := getToolCfg("get_wb_brands"); exists && toolCfg.Enabled {
		if err := register("get_wb_brands", std.NewWbBrandsTool(wbClient, toolCfg, cfg.WB)); err != nil {
			return err
		}
	}

	// === WB Service Tools ===
	if toolCfg, exists := getToolCfg("reload_wb_dictionaries"); exists && toolCfg.Enabled {
		if err := register("reload_wb_dictionaries", std.NewReloadWbDictionariesTool(wbClient, toolCfg)); err != nil {
			return err
		}
	}

	// === WB Analytics Tools ===
	if toolCfg, exists := getToolCfg("get_wb_product_funnel"); exists && toolCfg.Enabled {
		if err := register("get_wb_product_funnel", std.NewWbProductFunnelTool(wbClient, toolCfg, cfg.WB)); err != nil {
			return err
		}
	}

	if toolCfg, exists := getToolCfg("get_wb_product_funnel_history"); exists && toolCfg.Enabled {
		if err := register("get_wb_product_funnel_history", std.NewWbProductFunnelHistoryTool(wbClient, toolCfg, cfg.WB)); err != nil {
			return err
		}
	}

	if toolCfg, exists := getToolCfg("get_wb_search_positions"); exists && toolCfg.Enabled {
		if err := register("get_wb_search_positions", std.NewWbSearchPositionsTool(wbClient, toolCfg, cfg.WB)); err != nil {
			return err
		}
	}

	if toolCfg, exists := getToolCfg("get_wb_top_search_queries"); exists && toolCfg.Enabled {
		if err := register("get_wb_top_search_queries", std.NewWbTopSearchQueriesTool(wbClient, toolCfg, cfg.WB)); err != nil {
			return err
		}
	}

	if toolCfg, exists := getToolCfg("get_wb_top_organic_positions"); exists && toolCfg.Enabled {
		if err := register("get_wb_top_organic_positions", std.NewWbTopOrganicPositionsTool(wbClient, toolCfg, cfg.WB)); err != nil {
			return err
		}
	}

	if toolCfg, exists := getToolCfg("get_wb_campaign_stats"); exists && toolCfg.Enabled {
		if err := register("get_wb_campaign_stats", std.NewWbCampaignStatsTool(wbClient, toolCfg, cfg.WB)); err != nil {
			return err
		}
	}

	if toolCfg, exists := getToolCfg("get_wb_keyword_stats"); exists && toolCfg.Enabled {
		if err := register("get_wb_keyword_stats", std.NewWbKeywordStatsTool(wbClient, toolCfg, cfg.WB)); err != nil {
			return err
		}
	}

	if toolCfg, exists := getToolCfg("get_wb_attribution_summary"); exists && toolCfg.Enabled {
		if err := register("get_wb_attribution_summary", std.NewWbAttributionSummaryTool(wbClient, toolCfg, cfg.WB)); err != nil {
			return err
		}
	}

	// === S3 Basic Tools ===
	if toolCfg, exists := getToolCfg("list_s3_files"); exists && toolCfg.Enabled {
		if err := register("list_s3_files", std.NewS3ListTool(state.GetStorage())); err != nil {
			return err
		}
	}

	if toolCfg, exists := getToolCfg("read_s3_object"); exists && toolCfg.Enabled {
		if err := register("read_s3_object", std.NewS3ReadTool(state.GetStorage())); err != nil {
			return err
		}
	}

	if toolCfg, exists := getToolCfg("read_s3_image"); exists && toolCfg.Enabled {
		tool := std.NewS3ReadImageTool(state.GetStorage(), cfg.ImageProcessing)
		if err := register("read_s3_image", tool); err != nil {
			return err
		}
	}

	if toolCfg, exists := getToolCfg("get_plm_data"); exists && toolCfg.Enabled {
		tool := std.NewGetPLMDataTool(state.GetStorage())
		if err := register("get_plm_data", tool); err != nil {
			return err
		}
	}

	// === S3 Batch Tools ===
	if toolCfg, exists := getToolCfg("classify_and_download_s3_files"); exists && toolCfg.Enabled {
		tool := std.NewClassifyAndDownloadS3Files(
			state.GetStorage(),
			state,
			cfg.ImageProcessing,
			cfg.FileRules,
			toolCfg,
		)
		if err := register("classify_and_download_s3_files", tool); err != nil {
			return err
		}
	}

	if toolCfg, exists := getToolCfg("analyze_article_images_batch"); exists && toolCfg.Enabled {
		// Vision LLM обязателен для этого tool
		if visionLLM == nil {
			return fmt.Errorf("analyze_article_images_batch tool requires default_vision model to be configured")
		}

		// Load vision system prompt
		visionPrompt, err := prompt.LoadVisionSystemPrompt(cfg)
		if err != nil {
			return fmt.Errorf("failed to load vision prompt: %w", err)
		}

		tool := std.NewAnalyzeArticleImagesBatch(
			state,
			state.GetStorage(), // S3 клиент для скачивания изображений
			visionLLM,
			visionPrompt,
			cfg.ImageProcessing,
			toolCfg,
		)
		if err := register("analyze_article_images_batch", tool); err != nil {
			return err
		}
	}

	// === Planner Tools ===
	if toolCfg, exists := getToolCfg("plan_add_task"); exists && toolCfg.Enabled {
		if err := register("plan_add_task", std.NewPlanAddTaskTool(state.GetTodoManager(), toolCfg)); err != nil {
			return err
		}
	}

	if toolCfg, exists := getToolCfg("plan_mark_done"); exists && toolCfg.Enabled {
		if err := register("plan_mark_done", std.NewPlanMarkDoneTool(state.GetTodoManager(), toolCfg)); err != nil {
			return err
		}
	}

	if toolCfg, exists := getToolCfg("plan_mark_failed"); exists && toolCfg.Enabled {
		if err := register("plan_mark_failed", std.NewPlanMarkFailedTool(state.GetTodoManager(), toolCfg)); err != nil {
			return err
		}
	}

	if toolCfg, exists := getToolCfg("plan_clear"); exists && toolCfg.Enabled {
		if err := register("plan_clear", std.NewPlanClearTool(state.GetTodoManager(), toolCfg)); err != nil {
			return err
		}
	}

	if toolCfg, exists := getToolCfg("plan_set_tasks"); exists && toolCfg.Enabled {
		if err := register("plan_set_tasks", std.NewPlanSetTasksTool(state.GetTodoManager(), toolCfg)); err != nil {
			return err
		}
	}

	// === Валидация на неизвестные tools ===
	for toolName := range cfg.Tools {
		if !isKnownTool(toolName) {
			return fmt.Errorf("unknown tool '%s' in config. Known tools: %v",
				toolName, getAllKnownToolNames())
		}
	}

	log.Printf("Tools registered (%d): %s", len(registered), registered)
	utils.Info("Tools registered", "count", len(registered), "tools", registered)
	return nil
}

// isKnownTool проверяет что tool известен в коде.
func isKnownTool(name string) bool {
	knownTools := getAllKnownToolNames()
	for _, known := range knownTools {
		if name == known {
			return true
		}
	}
	return false
}

// getAllKnownToolNames возвращает список всех известных tools.
func getAllKnownToolNames() []string {
	return []string{
		// WB Content API
		"search_wb_products",
		"get_wb_parent_categories",
		"get_wb_subjects",
		"ping_wb_api",
		// WB Feedbacks API
		"get_wb_feedbacks",
		"get_wb_questions",
		"get_wb_new_feedbacks_questions",
		"get_wb_unanswered_feedbacks_counts",
		"get_wb_unanswered_questions_counts",
		// WB Dictionary Tools
		"wb_colors",
		"wb_countries",
		"wb_genders",
		"wb_seasons",
		"wb_vat_rates",
		// WB Characteristics Tools
		"get_wb_subjects_by_name",
		"get_wb_characteristics",
		"get_wb_tnved",
		"get_wb_brands",
		// WB Service Tools
		"reload_wb_dictionaries",
		// WB Analytics Tools
		"get_wb_product_funnel",
		"get_wb_product_funnel_history",
		"get_wb_search_positions",
		"get_wb_top_search_queries",
		"get_wb_top_organic_positions",
		"get_wb_campaign_stats",
		"get_wb_keyword_stats",
		"get_wb_attribution_summary",
		// S3 Basic Tools
		"list_s3_files",
		"read_s3_object",
		"read_s3_image",
		"get_plm_data",
		// S3 Batch Tools
		"classify_and_download_s3_files",
		"analyze_article_images_batch",
		// Planner Tools
		"plan_add_task",
		"plan_mark_done",
		"plan_mark_failed",
		"plan_clear",
		"plan_set_tasks",
	}
}

// ValidateWBKey проверяет что API ключ установлен и не является шаблоном.
//
// Проверяет что ключ не пустой и не равен строке-шаблону "${WB_API_KEY}",
// которая означает что переменная окружения не была раскрыта.
//
// Возвращает ошибку с подробным сообщением если ключ невалиден, nil иначе.
//
// Используется в утилитах (cmd/) для ранней проверки конфигурации
// перед выполнением запросов к WB API.
func ValidateWBKey(apiKey string) error {
	if apiKey == "" || apiKey == "${WB_API_KEY}" {
		return fmt.Errorf("WB_API_KEY not set in config or environment.\n\n" +
			"Please set the WB_API_KEY environment variable:\n" +
			"  export WB_API_KEY=your_api_key_here\n\n" +
			"Or add it to your config.yaml:\n" +
			"  wb:\n" +
			"    api_key: \"${WB_API_KEY}\"")
	}
	return nil
}

// resolveAbsPath преобразует путь в абсолютный (если это не уже абсолютный путь).
func resolveAbsPath(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}

```

=================

# app/standalone.go

```go
// Package app предоставляет переиспользуемые компоненты для инициализации
// и выполнения AI-агента в разных контекстах (CLI, TUI, HTTP и т.д.).
//
// Этот файл предоставляет компоненты для standalone CLI утилит с
// строгим поведением поиска конфигов и промптов.
package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ilkoid/poncho-ai/pkg/config"
)

// StandaloneConfigPathFinder реализует строгою стратегию поиска для CLI утилит.
//
// Правила:
// 1. Если указан флаг -config — использует его (может быть относительный путь)
// 2. Ищет config.yaml в той же папке где находится бинарник
// 3. НЕ ищет в текущей директории или родительских
// 4. Возвращает ошибку если файл не найден
//
// Используется для standalone CLI утилит которые распространяются
// вместе с config.yaml и prompts/ в одной директории.
type StandaloneConfigPathFinder struct {
	// ConfigFlag - значение флага -config, если указан
	ConfigFlag string
}

// FindConfigPath находит путь к config.yaml.
//
// Возвращает пустую строку если файл не найден (ошибка будет позже в Load).
func (f *StandaloneConfigPathFinder) FindConfigPath() string {
	var cfgPath string

	// 1. Флаг имеет приоритет (может быть относительный путь)
	if f.ConfigFlag != "" {
		return resolveAbsPath(f.ConfigFlag)
	}

	// 2. Директория бинарника
	if execPath, err := os.Executable(); err == nil {
		binDir := filepath.Dir(execPath)
		cfgPath = filepath.Join(binDir, "config.yaml")
		if _, err := os.Stat(cfgPath); err == nil {
			return cfgPath
		}
	}

	// Не нашли — возвращаем пустую строку
	// Ошибка будет возвращена в InitializeConfigStrict
	return ""
}

// InitializeConfigStrict инициализирует конфигурацию со строгими проверками.
//
// В отличии от InitializeConfig, эта функция:
// - Падает если config.yaml не найден
// - Падает если prompts директория не существует
// - Падает если любой post-prompt файл не существует
//
// Параметры:
//   - finder: стратегия поиска конфига (обычно StandaloneConfigPathFinder)
//
// Возвращает:
//   - cfg: загруженная конфигурация
//   - cfgPath: абсолютный путь к config.yaml
//   - err: ошибка если что-то не найдено
func InitializeConfigStrict(finder ConfigPathFinder) (*config.AppConfig, string, error) {
	cfgPath := finder.FindConfigPath()

	// 1. Проверяем что путь не пустой
	if cfgPath == "" {
		return nil, "", fmt.Errorf("config.yaml not found\n\n" +
			"Standalone CLI requires config.yaml in the same directory as the binary.\n" +
			"Usage: place config.yaml next to the binary or use -config flag.")
	}

	// 2. Проверяем что файл существует
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		return nil, "", fmt.Errorf("config.yaml not found at: %s", cfgPath)
	}

	// 3. Загружаем конфиг
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, "", fmt.Errorf("failed to load config from %s: %w", cfgPath, err)
	}

	// 4. Валидируем что prompts директория существует относительно конфига
	promptsDir := cfg.App.PromptsDir
	if promptsDir == "" {
		return nil, "", fmt.Errorf("app.prompts_dir is required in config.yaml")
	}

	// Преобразуем относительный путь в абсолютный относительно директории конфига
	cfgDir := filepath.Dir(cfgPath)
	if !filepath.IsAbs(promptsDir) {
		promptsDir = filepath.Join(cfgDir, promptsDir)
	}

	if _, err := os.Stat(promptsDir); os.IsNotExist(err) {
		return nil, "", fmt.Errorf("prompts directory not found: %s\n\n"+
			"Create this directory or check app.prompts_dir in config.yaml",
			promptsDir)
	}

	// 5. Валидируем все post-prompt файлы для tools
	if err := ValidateToolPromptsStrict(cfg, promptsDir); err != nil {
		return nil, "", fmt.Errorf("tool prompts validation failed: %w", err)
	}

	// 6. Обновляем promptsDir в конфиге на абсолютный путь
	cfg.App.PromptsDir = promptsDir

	return cfg, cfgPath, nil
}

// ValidateToolPromptsStrict строго валидирует все post-prompt файлы.
//
// Проходит по всем enabled tools в конфиге и проверяет что:
// - Если указан post_prompt — файл должен существовать
// - Возвращает ошибку с детальным списком проблемных файлов
//
// Это fail-fast проверка которая выполняется перед запуском агента.
func ValidateToolPromptsStrict(cfg *config.AppConfig, promptsDir string) error {
	var missingFiles []string

	for toolName, toolCfg := range cfg.Tools {
		if !toolCfg.Enabled {
			continue
		}

		if toolCfg.PostPrompt != "" {
			promptPath := filepath.Join(promptsDir, toolCfg.PostPrompt)
			if _, err := os.Stat(promptPath); os.IsNotExist(err) {
				missingFiles = append(missingFiles,
					fmt.Sprintf("  - tool '%s': %s → %s",
						toolName, toolCfg.PostPrompt, promptPath))
			}
		}
	}

	if len(missingFiles) > 0 {
		return fmt.Errorf("post-prompt files not found:\n%s",
			joinStrings(missingFiles, "\n"))
	}

	return nil
}

// ValidateVisionPromptsStrict валидирует vision промпт файл.
//
// Проверяет что:
// - image_analysis.yaml существует (или fallback доступен)
//
// Возвращает ошибку если файл не найден.
func ValidateVisionPromptsStrict(cfg *config.AppConfig) error {
	promptPath := filepath.Join(cfg.App.PromptsDir, "image_analysis.yaml")

	if _, err := os.Stat(promptPath); os.IsNotExist(err) {
		// Файл не существует — это нормально, есть fallback
		// Но для CLI с строгим режимом warn'ем
		fmt.Printf("Warning: image_analysis.yaml not found at %s, using default prompt\n", promptPath)
	}

	return nil
}

// ValidateAllPromptsStrict валидирует все промпты (tool post-prompts + vision).
//
// Вызывает ValidateToolPromptsStrict и ValidateVisionPromptsStrict.
func ValidateAllPromptsStrict(cfg *config.AppConfig) error {
	promptsDir := cfg.App.PromptsDir

	// 1. Валидируем tool post-prompts
	if err := ValidateToolPromptsStrict(cfg, promptsDir); err != nil {
		return err
	}

	// 2. Валидируем vision prompt
	if err := ValidateVisionPromptsStrict(cfg); err != nil {
		return err
	}

	return nil
}

// InitializeForStandalone - полная инициализация для standalone CLI утилиты.
//
// Это функция-обёртка которая:
// 1. Ищет config.yaml рядом с бинарником
// 2. Валидирует prompts директорию
// 3. Валидирует все post-prompt файлы
// 4. Инициализирует все компоненты
//
// Возвращает ошибку если что-то не найдено — fail-fast поведение.
//
// Rule 11: Принимает context.Context для распространения отмены.
//
// Пример использования:
//
//	finder := &app.StandaloneConfigPathFinder{ConfigFlag: *configFlag}
//	components, cfgPath, err := app.InitializeForStandalone(ctx, finder, 10, "")
//	if err != nil {
//	    log.Fatalf("Initialization failed: %v", err)
//	}
func InitializeForStandalone(
	ctx context.Context,
	finder ConfigPathFinder,
	maxIters int,
	systemPrompt string,
) (*Components, string, error) {
	// 1. Загружаем конфиг со строгими проверками
	cfg, cfgPath, err := InitializeConfigStrict(finder)
	if err != nil {
		return nil, "", err
	}

	// 2. Инициализируем компоненты
	// Rule 11: передаём родительский контекст для распространения отмены
	components, err := Initialize(ctx, cfg, maxIters, systemPrompt)
	if err != nil {
		return nil, "", fmt.Errorf("failed to initialize components: %w", err)
	}

	return components, cfgPath, nil
}

// joinStrings объединяет строки с разделителем.
func joinStrings(strs []string, sep string) string {
	if len(strs) == 0 {
		return ""
	}
	result := strs[0]
	for i := 1; i < len(strs); i++ {
		result += sep + strs[i]
	}
	return result
}

```

=================

# chain/agent.go

```go
// Package chain предоставляет Chain Pattern для AI агента.
package chain

import (
	"context"

	"github.com/ilkoid/poncho-ai/pkg/llm"
)

// Agent представляет интерфейс AI-агента для обработки запросов пользователя.
//
// Этот интерфейс находится в пакете chain, чтобы избежать циклических импортов:
//   - pkg/agent → pkg/app → pkg/chain → (закольцовывалось на pkg/agent)
//   - Теперь: pkg/agent → pkg/app → pkg/chain (без цикла)
//
// ReActCycle реализует этот интерфейс, что позволяет использовать его
// как Agent для простых сценариев (Run(query)) и как Chain для сложных
// (Execute(input) с полным контролем).
type Agent interface {
	// Run выполняет обработку запроса пользователя.
	//
	// Принимает:
	//   - ctx: контекст для отмены операции
	//   - query: текстовый запрос пользователя
	//
	// Возвращает:
	//   - string: финальный ответ агента
	//   - error: ошибка если не удалось выполнить запрос
	Run(ctx context.Context, query string) (string, error)

	// GetHistory возвращает копию истории диалога агента.
	//
	// Включает все сообщения: пользовательские, ассистента и результаты tools.
	GetHistory() []llm.Message
}

```

=================

# chain/chain.go

```go
// Package chain предоставляет Chain Pattern для AI агента.
//
// Chain позволяет компоновать сложное поведение из простых шагов (Step).
// Каждый Step является изолированным, тестируемым и переиспользуемым.
//
// Правила из dev_manifest.md:
//   - Rule 1: Работает с Tool interface ("Raw In, String Out")
//   - Rule 2: Конфигурируется через YAML
//   - Rule 3: Tools вызываются через Registry
//   - Rule 4: LLM вызывается через llm.Provider
//   - Rule 5: Thread-safe через ChainContext
//   - Rule 7: Все ошибки возвращаются, нет panic
//   - Rule 10: Godoc на всех public API
package chain

import (
	"context"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/llm"
	"github.com/ilkoid/poncho-ai/pkg/state"
	"github.com/ilkoid/poncho-ai/pkg/tools"
)

// Chain представляет последовательность шагов для выполнения запроса.
//
// Chain является иммутабельным после создания и thread-safe для выполнения.
type Chain interface {
	// Execute выполняет цепочку и возвращает результат.
	Execute(ctx context.Context, input ChainInput) (ChainOutput, error)
}

// ChainInput — входные данные для выполнения цепочки.
type ChainInput struct {
	// UserQuery — запрос пользователя
	UserQuery string

	// State — framework core состояние (thread-safe)
	// Rule 6: Использует pkg/state.CoreState вместо internal/app
	State *state.CoreState

	// Registry — реестр инструментов (Rule 3)
	Registry *tools.Registry

	// Config — конфигурация цепочки (из YAML)
	Config ChainConfig
}

// ChainOutput — результат выполнения цепочки.
type ChainOutput struct {
	// Result — финальный ответ агента
	Result string

	// Iterations — количество выполненных итераций
	Iterations int

	// Duration — общее время выполнения
	Duration time.Duration

	// FinalState — финальное состояние истории сообщений
	FinalState []llm.Message

	// DebugPath — путь к сохраненному debug логу (если включен)
	DebugPath string

	// Signal — типизированный сигнал от последнего шага
	// PHASE 2 REFACTOR: Указывает на особые условия завершения
	//   - SignalNone: нормальное завершение
	//   - SignalFinalAnswer: финальный ответ получен
	//   - SignalNeedUserInput: требуется пользовательский ввод
	//   - SignalError: ошибка выполнения
	Signal ExecutionSignal
}

// ChainConfig — конфигурация цепочки из YAML.
type ChainConfig struct {
	// Type — тип цепочки ("react", "sequential", "conditional")
	Type string

	// Description — описание цепочки
	Description string

	// MaxIterations — максимальное количество итераций (для ReAct)
	MaxIterations int

	// Timeout — таймаут выполнения
	Timeout time.Duration

	// Steps — шаги цепочки
	Steps []StepConfig

	// Debug — конфигурация debug логирования
	Debug DebugConfig

	// PostPromptsDir — директория с post-prompts
	PostPromptsDir string
}

// StepConfig — конфигурация шага из YAML.
type StepConfig struct {
	// Name — имя шага
	Name string

	// Type — тип шага ("llm", "tools", "custom")
	Type string

	// Config — дополнительные параметры шага
	Config map[string]interface{}
}

// DebugConfig — конфигурация debug логирования для Chain.
type DebugConfig struct {
	// Enabled — включено ли debug логирование
	Enabled bool

	// SaveLogs — сохранять ли логи в файлы
	SaveLogs bool

	// LogsDir — директория для логов
	LogsDir string

	// IncludeToolArgs — включать аргументы инструментов
	IncludeToolArgs bool

	// IncludeToolResults — включать результаты инструментов
	IncludeToolResults bool

	// MaxResultSize — максимальный размер результата (символов)
	MaxResultSize int
}

```

=================

# chain/config.go

```go
// Package chain предоставляет Chain Pattern для AI агента.
package chain

import (
	"fmt"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/events"
	"github.com/ilkoid/poncho-ai/pkg/prompt"
)

// ReActCycleConfig — конфигурация ReAct цикла.
//
// Используется при создании ReActCycle через NewReActCycle.
// Конфигурация может быть загружена из YAML или создана программно.
type ReActCycleConfig struct {
	// SystemPrompt — базовый системный промпт для ReAct агента.
	SystemPrompt string

	// ToolPostPrompts — конфигурация post-prompts для инструментов.
	// Опционально: может быть nil.
	ToolPostPrompts *prompt.ToolPostPromptConfig

	// PromptsDir — директория с post-prompts файлами.
	// Rule 11: Поиск рядом с бинарником или в текущей директории.
	PromptsDir string

	// MaxIterations — максимальное количество итераций ReAct цикла.
	// По умолчанию: 10.
	MaxIterations int

	// Timeout — таймаут выполнения всей цепочки.
	// По умолчанию: 5 минут.
	Timeout time.Duration

	// DefaultEmitter — emitter по умолчанию для созданных executions.
	// Может быть переопределён через SetEmitter.
	// Thread-safe: Emitter не должен модифицироваться после установки.
	DefaultEmitter events.Emitter

	// DefaultDebugRecorder — debug recorder по умолчанию для созданных executions.
	// Может быть переопределён через AttachDebug.
	// Thread-safe: Recorder не должен модифицироваться после установки.
	DefaultDebugRecorder *ChainDebugRecorder

	// StreamingEnabled — включён ли streaming по умолчанию.
	// Может быть переопределён через SetStreamingEnabled.
	StreamingEnabled bool
}

// NewReActCycleConfig создаёт конфигурацию ReAct цикла с дефолтными значениями.
//
// Rule 10: Godoc на public API.
func NewReActCycleConfig() ReActCycleConfig {
	return ReActCycleConfig{
		SystemPrompt:  DefaultSystemPrompt,
		MaxIterations: 10,
		Timeout:       5 * time.Minute,
	}
}

// Validate проверяет конфигурацию на валидность.
//
// Rule 7: Возвращает ошибку вместо panic.
func (c *ReActCycleConfig) Validate() error {
	if c.SystemPrompt == "" {
		return fmt.Errorf("system_prompt is required")
	}
	if c.MaxIterations <= 0 {
		return fmt.Errorf("max_iterations must be positive, got %d", c.MaxIterations)
	}
	if c.Timeout <= 0 {
		return fmt.Errorf("timeout must be positive, got %v", c.Timeout)
	}
	return nil
}

// UserChoiceRequest — маркер для передачи управления UI.
// Используется для прерывания ReAct цикла и запроса пользовательского ввода.
const UserChoiceRequest = "__USER_CHOICE_REQUIRED__"

// DefaultSystemPrompt — базовый системный промпт по умолчанию.
const DefaultSystemPrompt = `You are a helpful AI assistant with access to tools.

When you need to use a tool, respond with a function call in the following format:
{
  "name": "tool_name",
  "arguments": {"param1": "value1"}
}

After receiving tool results, analyze them and determine if you need more information or can provide a final answer.
Be concise and helpful in your responses.`

```

=================

# chain/context.go

```go
// Package chain предоставляет Chain Pattern для AI агента.
package chain

import (
	"fmt"
	"strings"
	"sync"

	"github.com/ilkoid/poncho-ai/pkg/llm"
	"github.com/ilkoid/poncho-ai/pkg/prompt"
	"github.com/ilkoid/poncho-ai/pkg/state"
)

// ChainContext содержит состояние выполнения цепочки.
//
// Thread-safe через sync.RWMutex (Rule 5).
// Все изменения состояния должны проходить через методы этого типа.
//
// REFACTORED 2026-01-04: Удалено дублирующее поле messages.
// Теперь используется state.CoreState как единый source of truth для истории.
type ChainContext struct {
	mu sync.RWMutex

	// Входные данные (неизменяемые после создания)
	Input *ChainInput

	// Ссылка на CoreState (единый source of truth для истории)
	// Rule 6: Используем pkg/state.CoreState вместо дублирования
	State *state.CoreState

	// Текущее состояние
	currentIteration int

	// Post-prompt состояние
	activePostPrompt   string
	activePromptConfig *prompt.PromptConfig

	// LLM параметры текущей итерации (определяются в runtime)
	actualModel      string
	actualTemperature float64
	actualMaxTokens  int
}

// NewChainContext создаёт новый контекст выполнения цепочки.
//
// REFACTORED 2026-01-04: Теперь требует state.CoreState как обязательный параметр.
// История сообщений хранится в CoreState, ChainContext не дублирует данные.
func NewChainContext(input ChainInput) *ChainContext {
	return &ChainContext{
		Input:            &input,
		State:            input.State, // CoreState из ChainInput
		currentIteration: 0,
	}
}

// GetInput возвращает входные данные (thread-safe).
func (c *ChainContext) GetInput() *ChainInput {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Input
}

// GetCurrentIteration возвращает номер текущей итерации (thread-safe).
func (c *ChainContext) GetCurrentIteration() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.currentIteration
}

// IncrementIteration увеличивает счётчик итераций (thread-safe).
func (c *ChainContext) IncrementIteration() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.currentIteration++
	return c.currentIteration
}

// ============================================================
// Message Methods (делегируют в CoreState)
// ============================================================

// GetMessages возвращает копию сообщений из CoreState (thread-safe).
//
// REFACTORED 2026-01-04: Теперь делегирует в CoreState.GetHistory().
// Раньше дублировало историю в c.messages.
func (c *ChainContext) GetMessages() []llm.Message {
	return c.State.GetHistory()
}

// GetLastMessage возвращает последнее сообщение из CoreState (thread-safe).
//
// REFACTORED 2026-01-04: Теперь использует CoreState.GetHistory().
func (c *ChainContext) GetLastMessage() *llm.Message {
	history := c.State.GetHistory()
	if len(history) == 0 {
		return nil
	}
	// Возвращаем копию
	msg := history[len(history)-1]
	return &msg
}

// AppendMessage добавляет сообщение в историю CoreState (thread-safe).
//
// REFACTORED 2026-01-04: Теперь делегирует в CoreState.Append().
// Раньше дублировало историю в c.messages.
func (c *ChainContext) AppendMessage(msg llm.Message) error {
	return c.State.Append(msg)
}

// SetMessages заменяет список сообщений в CoreState (thread-safe).
//
// REFACTORED 2026-01-04: Теперь очищает историю в CoreState и добавляет новые сообщения.
// Используется для восстановления состояния.
func (c *ChainContext) SetMessages(msgs []llm.Message) error {
	// Очищаем текущую историю через Update с возвратом пустого слайса
	// REFACTORED 2026-01-04: ClearHistory() удален, используем Set напрямую
	if err := c.State.Set(state.KeyHistory, []llm.Message{}); err != nil {
		return fmt.Errorf("failed to clear history: %w", err)
	}

	// Добавляем все сообщения
	for _, msg := range msgs {
		if err := c.State.Append(msg); err != nil {
			return fmt.Errorf("failed to append message: %w", err)
		}
	}

	return nil
}

// ============================================================
// Post-prompt Methods
// ============================================================

// GetActivePostPrompt возвращает активный post-prompt (thread-safe).
func (c *ChainContext) GetActivePostPrompt() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.activePostPrompt
}

// GetActivePromptConfig возвращает конфигурацию активного post-prompt (thread-safe).
func (c *ChainContext) GetActivePromptConfig() *prompt.PromptConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.activePromptConfig
}

// SetActivePostPrompt устанавливает активный post-prompt (thread-safe).
//
// Сбрасывает предыдущий активный post-prompt.
func (c *ChainContext) SetActivePostPrompt(prompt string, config *prompt.PromptConfig) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.activePostPrompt = prompt
	c.activePromptConfig = config
}

// ClearActivePostPrompt сбрасывает активный post-prompt (thread-safe).
func (c *ChainContext) ClearActivePostPrompt() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.activePostPrompt = ""
	c.activePromptConfig = nil
}

// ============================================================
// LLM Parameters Methods
// ============================================================

// GetActualModel возвращает модель для текущей итерации (thread-safe).
func (c *ChainContext) GetActualModel() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.actualModel
}

// SetActualModel устанавливает модель для текущей итерации (thread-safe).
func (c *ChainContext) SetActualModel(model string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.actualModel = model
}

// GetActualTemperature возвращает температуру для текущей итерации (thread-safe).
func (c *ChainContext) GetActualTemperature() float64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.actualTemperature
}

// SetActualTemperature устанавливает температуру для текущей итерации (thread-safe).
func (c *ChainContext) SetActualTemperature(temp float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.actualTemperature = temp
}

// GetActualMaxTokens возвращает max_tokens для текущей итерации (thread-safe).
func (c *ChainContext) GetActualMaxTokens() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.actualMaxTokens
}

// SetActualMaxTokens устанавливает max_tokens для текущей итерации (thread-safe).
func (c *ChainContext) SetActualMaxTokens(tokens int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.actualMaxTokens = tokens
}

// ============================================================
// Context Building
// ============================================================

// BuildContextMessages формирует сообщения для LLM на основе текущего состояния (thread-safe).
//
// REFACTORED 2026-01-04: Теперь использует CoreState.BuildAgentContext()
// который собирает полный контекст (системный промпт, рабочая память,
// контекст плана, история диалога).
//
// Использует активный post-prompt если установлен.
func (c *ChainContext) BuildContextMessages(systemPrompt string) []llm.Message {
	c.mu.RLock()
	activePostPrompt := c.activePostPrompt
	c.mu.RUnlock()

	// Определяем системный промпт
	actualSystemPrompt := systemPrompt
	if activePostPrompt != "" {
		actualSystemPrompt = activePostPrompt
	}

	// Используем CoreState.BuildAgentContext для сборки полного контекста
	// REFACTORED: Раньше использовали c.messages (дублирование)
	messages := c.State.BuildAgentContext(actualSystemPrompt)

	return messages
}

// BuildContextMessagesForModel формирует сообщения для LLM с учетом типа модели.
//
// Фильтрует контекст в зависимости от того, является ли модель vision-моделью:
//   - Vision модели: полный контекст (включая VisionDescription и Images)
//   - Chat модели: контекст WITHOUT VisionDescription и без Images из истории
//
// Thread-safe. Определяет тип модели по actualModel из ChainContext.
func (c *ChainContext) BuildContextMessagesForModel(systemPrompt string) []llm.Message {
	c.mu.RLock()
	actualModel := c.actualModel
	activePostPrompt := c.activePostPrompt
	c.mu.RUnlock()

	// Определяем системный промпт
	actualSystemPrompt := systemPrompt
	if activePostPrompt != "" {
		actualSystemPrompt = activePostPrompt
	}

	// Получаем базовый контекст
	messages := c.State.BuildAgentContext(actualSystemPrompt)

	// Если модель не vision - фильтруем контент
	if !c.isModelVision(actualModel) {
		messages = c.filterMessagesForChatModel(messages)
	}

	return messages
}

// isModelVision проверяет, является ли модель vision-моделью.
// Использует эвристику по названию (TODO: использовать ModelRegistry).
func (c *ChainContext) isModelVision(modelName string) bool {
	if modelName == "" {
		return false
	}

	// Эвристика по названию
	return strings.Contains(strings.ToLower(modelName), "vision") ||
		strings.Contains(strings.ToLower(modelName), "v-")
}

// filterMessagesForChatModel фильтрует сообщения для chat-модели.
//
// Удаляет:
//   1. VisionDescription из системных промптов (блок "КОНТЕКСТ АРТИКУЛА")
//   2. Images из всех сообщений истории
//
// Возвращает новый слайс, не модифицирует оригинал.
func (c *ChainContext) filterMessagesForChatModel(messages []llm.Message) []llm.Message {
	filtered := make([]llm.Message, 0, len(messages))

	for _, msg := range messages {
		filteredMsg := msg

		// Если это системное сообщение - убираем блок КОНТЕКСТ АРТИКУЛА
		if msg.Role == llm.RoleSystem {
			filteredMsg.Content = c.removeVisionContextFromSystem(msg.Content)
		}

		// Убираем Images из всех сообщений
		filteredMsg.Images = nil

		filtered = append(filtered, filteredMsg)
	}

	return filtered
}

// removeVisionContextFromSystem удаляет блок с описаниями изображений из системного промпта.
//
// Ищет и удаляет текст вида:
//   КОНТЕКСТ АРТИКУЛА (Результаты анализа файлов):
//   - Файл [tag] filename: description
//
// Также удаляет пустую строку перед блоком, если она есть.
func (c *ChainContext) removeVisionContextFromSystem(content string) string {
	// Ищем начало блока
	marker := "КОНТЕКСТ АРТИКУЛА"
	idx := strings.Index(content, marker)

	if idx == -1 {
		return content // Блок не найден
	}

	// Ищем начало строки (новая строка перед маркером)
	newlineBefore := strings.LastIndex(content[:idx], "\n")
	if newlineBefore == -1 {
		return "" // Блок в начале строки
	}

	// Проверяем, есть ли перед нами еще одна пустая строка
	result := content[:newlineBefore]
	// Удаляем завершающий \n если он есть
	result = strings.TrimSuffix(result, "\n")
	// Удаляем еще один \n если был двойной перенос
	result = strings.TrimSuffix(result, "\n")

	return result
}

```

=================

# chain/context_test.go

```go
package chain

import (
	"strings"
	"testing"

	"github.com/ilkoid/poncho-ai/pkg/llm"
)

func TestFilterMessagesForChatModel(t *testing.T) {
	chainCtx := &ChainContext{}

	t.Run("removes vision context from system messages", func(t *testing.T) {
		messages := []llm.Message{
			{Role: llm.RoleSystem, Content: "System prompt\n\nКОНТЕКСТ АРТИКУЛА (Результаты анализа файлов):\n- Файл [sketch] dress.jpg: Красное платье\n"},
		}

		filtered := chainCtx.filterMessagesForChatModel(messages)

		if strings.Contains(filtered[0].Content, "КОНТЕКСТ АРТИКУЛА") {
			t.Errorf("Expected vision context to be removed, but got: %s", filtered[0].Content)
		}
	})

	t.Run("removes images from all messages", func(t *testing.T) {
		messages := []llm.Message{
			{Role: llm.RoleUser, Content: "What do you see?", Images: []string{"data:image/jpeg;base64,..."}},
		}

		filtered := chainCtx.filterMessagesForChatModel(messages)

		if len(filtered[0].Images) > 0 {
			t.Errorf("Expected images to be removed, but got %d images", len(filtered[0].Images))
		}
	})

	t.Run("keeps non-system content intact", func(t *testing.T) {
		originalContent := "This is regular content"
		messages := []llm.Message{
			{Role: llm.RoleUser, Content: originalContent},
		}

		filtered := chainCtx.filterMessagesForChatModel(messages)

		if filtered[0].Content != originalContent {
			t.Errorf("Expected content to remain intact, got: %s", filtered[0].Content)
		}
	})
}

func TestIsModelVision(t *testing.T) {
	chainCtx := &ChainContext{}

	tests := []struct {
		name     string
		model    string
		expected bool
	}{
		{"chat model", "glm-4.6", false},
		{"vision model with v- prefix", "glm-4.6v-flash", true},
		{"vision model with vision in name", "vision-model", true},
		{"vision model with v- prefix only", "v-1.0", true},
		{"empty model", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := chainCtx.isModelVision(tt.model)
			if result != tt.expected {
				t.Errorf("isModelVision(%q) = %v, want %v", tt.model, result, tt.expected)
			}
		})
	}
}

func TestRemoveVisionContextFromSystem(t *testing.T) {
	chainCtx := &ChainContext{}

	t.Run("removes vision context block", func(t *testing.T) {
		content := "System prompt\n\nКОНТЕКСТ АРТИКУЛА (Результаты анализа файлов):\n- Файл [sketch] dress.jpg: Красное платье\n"
		expected := "System prompt"

		result := chainCtx.removeVisionContextFromSystem(content)

		if result != expected {
			t.Errorf("Expected %q, got %q", expected, result)
		}
	})

	t.Run("returns content unchanged if no vision context", func(t *testing.T) {
		content := "Just a system prompt"
		result := chainCtx.removeVisionContextFromSystem(content)

		if result != content {
			t.Errorf("Expected %q, got %q", content, result)
		}
	})

	t.Run("handles content starting with vision context", func(t *testing.T) {
		content := "КОНТЕКСТ АРТИКУЛА\nsome content"
		expected := ""

		result := chainCtx.removeVisionContextFromSystem(content)

		if result != expected {
			t.Errorf("Expected %q, got %q", expected, result)
		}
	})
}

```

=================

# chain/debug.go

```go
// Package chain предоставляет Chain Pattern для AI агента.
package chain

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/debug"
	"github.com/ilkoid/poncho-ai/pkg/llm"
)

// ChainDebugRecorder — обёртка над debug.Recorder для Chain Pattern.
//
// Предоставляет удобные методы для записи LLM вызовов и tool execution.
type ChainDebugRecorder struct {
	recorder   *debug.Recorder
	enabled    bool
	logsDir    string
	startTime  time.Time // PHASE 4: для отслеживания длительности выполнения
}

// NewChainDebugRecorder создаёт новый ChainDebugRecorder.
//
// Rule 10: Godoc на public API.
func NewChainDebugRecorder(cfg DebugConfig) (*ChainDebugRecorder, error) {
	if !cfg.Enabled {
		return &ChainDebugRecorder{enabled: false}, nil
	}

	recorderCfg := debug.RecorderConfig{
		LogsDir:            cfg.LogsDir,
		IncludeToolArgs:    cfg.IncludeToolArgs,
		IncludeToolResults: cfg.IncludeToolResults,
		MaxResultSize:      cfg.MaxResultSize,
	}

	recorder, err := debug.NewRecorder(recorderCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create debug recorder: %w", err)
	}

	return &ChainDebugRecorder{
		recorder: recorder,
		enabled:  true,
		logsDir:  cfg.LogsDir,
	}, nil
}

// Enabled возвращает true если debug логирование включено.
func (r *ChainDebugRecorder) Enabled() bool {
	return r.enabled
}

// Start начинает запись новой цепочки.
//
// Должен вызываться в начале Chain.Execute().
func (r *ChainDebugRecorder) Start(input ChainInput) {
	if !r.enabled {
		return
	}
	r.recorder.Start(input.UserQuery)
}

// Finalize завершает запись цепочки.
//
// Должен вызываться в конце Chain.Execute() перед возвратом результата.
// Возвращает путь к сохраненному файлу лога.
func (r *ChainDebugRecorder) Finalize(result string, duration time.Duration) (string, error) {
	if !r.enabled {
		return "", nil
	}
	return r.recorder.Finalize(result, duration)
}

// StartIteration начинает запись новой итерации ReAct цикла.
//
// Принимает номер итерации (начиная с 1).
func (r *ChainDebugRecorder) StartIteration(iterationNum int) {
	if !r.enabled {
		return
	}
	r.recorder.StartIteration(iterationNum)
}

// EndIteration завершает запись текущей итерации.
//
// Должен вызываться после StartIteration и всех Record* вызовов.
func (r *ChainDebugRecorder) EndIteration() {
	if !r.enabled {
		return
	}
	r.recorder.EndIteration()
}

// RecordLLMRequest записывает LLM запрос.
func (r *ChainDebugRecorder) RecordLLMRequest(model string, temperature float64, maxTokens int, systemPromptUsed string, messagesCount int) {
	if !r.enabled {
		return
	}
	r.recorder.RecordLLMRequest(debug.LLMRequest{
		Model:            model,
		Temperature:      temperature,
		MaxTokens:        maxTokens,
		SystemPromptUsed: systemPromptUsed,
		MessagesCount:    messagesCount,
	})
}

// RecordLLMResponse записывает LLM ответ.
func (r *ChainDebugRecorder) RecordLLMResponse(content string, toolCalls []llm.ToolCall, duration int64) {
	if !r.enabled {
		return
	}

	// Конвертируем tool calls в debug format
	debugToolCalls := make([]debug.ToolCallInfo, len(toolCalls))
	for i, tc := range toolCalls {
		debugToolCalls[i] = debug.ToolCallInfo{
			ID:   tc.ID,
			Name: tc.Name,
			Args: tc.Args,
		}
	}

	r.recorder.RecordLLMResponse(debug.LLMResponse{
		Content:    content,
		ToolCalls:  debugToolCalls,
		Duration:   duration,
	})
}

// RecordToolExecution записывает выполнение инструмента.
func (r *ChainDebugRecorder) RecordToolExecution(toolName, argsJSON, result string, duration int64, success bool) {
	if !r.enabled {
		return
	}

	r.recorder.RecordToolExecution(debug.ToolExecution{
		Name:     toolName,
		Args:     argsJSON,
		Result:   result,
		Duration: duration,
		Success:  success,
	})
}

// GetLogPath возвращает путь к файлу лога.
//
// Возвращает пустую строку если debug отключён или log ещё не сохранён.
func (r *ChainDebugRecorder) GetLogPath() string {
	if !r.enabled {
		return ""
	}
	runID := r.recorder.GetRunID()
	if runID == "" {
		return ""
	}
	if r.logsDir != "" {
		return filepath.Join(r.logsDir, runID+".json")
	}
	return runID + ".json"
}

// GetRunID возвращает ID текущего запуска.
func (r *ChainDebugRecorder) GetRunID() string {
	if !r.enabled {
		return ""
	}
	return r.recorder.GetRunID()
}

// PHASE 4 REFACTOR: ExecutionObserver implementation
//
// ChainDebugRecorder теперь реализует ExecutionObserver для отслеживания
// жизненного цикла выполнения ReAct цикла.

// OnStart вызывается в начале выполнения Execute().
func (r *ChainDebugRecorder) OnStart(ctx context.Context, exec *ReActExecution) {
	if !r.enabled {
		return
	}
	r.startTime = time.Now()
	r.Start(*exec.chainCtx.Input)
}

// OnIterationStart вызывается в начале каждой итерации.
func (r *ChainDebugRecorder) OnIterationStart(iteration int) {
	if !r.enabled {
		return
	}
	r.StartIteration(iteration)
}

// OnIterationEnd вызывается в конце каждой итерации.
func (r *ChainDebugRecorder) OnIterationEnd(iteration int) {
	if !r.enabled {
		return
	}
	r.EndIteration()
}

// OnFinish вызывается в конце выполнения Execute().
func (r *ChainDebugRecorder) OnFinish(result ChainOutput, err error) {
	if !r.enabled {
		return
	}
	resultStr := result.Result
	if err != nil {
		resultStr = ""
	}
	r.Finalize(resultStr, time.Since(r.startTime))
}

// Ensure ChainDebugRecorder implements ExecutionObserver
var _ ExecutionObserver = (*ChainDebugRecorder)(nil)

```

=================

# chain/execution.go

```go
// Package chain предоставляет Chain Pattern для AI агента.
package chain

import (
	"context"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/events"
	"github.com/ilkoid/poncho-ai/pkg/utils"
)

// ReActExecution — runtime состояние выполнения ReAct цикла.
//
// # Template vs Execution
//
// ReActExecution is the RUNTIME STATE that is created per Execute() call.
// It is a pure data container with no execution logic (PHASE 3 REFACTOR).
//
// ## Template (ReActCycle)
//   - Immutable configuration shared across executions
//   - Created once during initialization
//   - Example: model registry, tool registry, system prompt
//
// ## Execution (this struct)
//   - Mutable runtime state per execution
//   - Created for each Execute() call
//   - Example: message history, step instances, emitter, debug recorder
//
// # Lifecycle
//
// 1. Created by ReActCycle.Execute() with isolated state
// 2. Passed to StepExecutor.Execute() for execution
// 3. Discarded after execution (not reused)
//
// # Thread Safety
//
// ReActExecution is NOT thread-safe and does NOT need to be:
//   - Created per execution (never shared)
//   - Used by only one goroutine
//   - No synchronization needed
//
// # Data Isolation
//
// Each execution gets cloned step instances to prevent state leakage:
//   - LLMInvocationStep: Has emitter, debugRecorder, modelRegistry
//   - ToolExecutionStep: Has debugRecorder, promptLoader
//
// This allows multiple concurrent Execute() calls from the same ReActCycle.
type ReActExecution struct {
	// Context
	ctx      context.Context
	chainCtx *ChainContext

	// Steps (локальные экземпляры для этого выполнения)
	llmStep  *LLMInvocationStep
	toolStep *ToolExecutionStep

	// Cross-cutting concerns (локальные)
	emitter       events.Emitter
	debugRecorder *ChainDebugRecorder

	// Configuration
	streamingEnabled bool
	startTime       time.Time

	// Configuration reference (не создаём копию, читаем только)
	config *ReActCycleConfig

	// PHASE 2 REFACTOR: Трекаем финальный сигнал от выполнения
	finalSignal ExecutionSignal
}

// NewReActExecution создаёт execution для одного вызова Execute().
//
// Клонирует шаги из шаблона для изоляции состояния между выполнениями.
func NewReActExecution(
	ctx context.Context,
	input ChainInput,
	llmStepTemplate *LLMInvocationStep,
	toolStepTemplate *ToolExecutionStep,
	emitter events.Emitter,
	debugRecorder *ChainDebugRecorder,
	streamingEnabled bool,
	config *ReActCycleConfig,
) *ReActExecution {
	// Создаём контекст выполнения
	chainCtx := NewChainContext(input)

	// Клонируем LLM шаг для этого выполнения (изолируем emitter, debugRecorder)
	llmStep := &LLMInvocationStep{
		modelRegistry:  llmStepTemplate.modelRegistry,
		defaultModel:   llmStepTemplate.defaultModel,
		registry:       llmStepTemplate.registry,
		systemPrompt:   llmStepTemplate.systemPrompt,
		emitter:        emitter,
		debugRecorder:  debugRecorder,
	}

	// Клонируем Tool шаг для этого выполнения
	toolStep := &ToolExecutionStep{
		registry:       toolStepTemplate.registry,
		promptLoader:   toolStepTemplate.promptLoader,
		debugRecorder:  debugRecorder,
	}

	return &ReActExecution{
		ctx:              ctx,
		chainCtx:         chainCtx,
		llmStep:          llmStep,
		toolStep:         toolStep,
		emitter:          emitter,
		debugRecorder:    debugRecorder,
		streamingEnabled: streamingEnabled,
		startTime:        time.Now(),
		config:           config,
	}
}

// emitEvent отправляет событие если emitter установлен.
func (e *ReActExecution) emitEvent(event events.Event) {
	if e.emitter == nil {
		utils.Debug("emitEvent: emitter is nil, skipping", "event_type", event.Type)
		return
	}
	utils.Debug("emitEvent: sending", "event_type", event.Type, "has_data", event.Data != nil)
	e.emitter.Emit(e.ctx, event)
}

// endDebugIteration завершает текущую debug итерацию.
func (e *ReActExecution) endDebugIteration() {
	if e.debugRecorder != nil && e.debugRecorder.Enabled() {
		e.debugRecorder.EndIteration()
	}
}

// finalizeWithError завершает выполнение с ошибкой.
func (e *ReActExecution) finalizeWithError(err error) (ChainOutput, error) {
	// Финализируем debug с ошибкой
	if e.debugRecorder != nil && e.debugRecorder.Enabled() {
		e.debugRecorder.Finalize("", time.Since(e.startTime))
	}

	return ChainOutput{}, err
}

```

=================

# chain/executor.go

```go
// Package chain предоставляет Chain Pattern для AI агента.
package chain

import (
	"context"
	"fmt"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/llm"
	"github.com/ilkoid/poncho-ai/pkg/utils"
)

// StepExecutor — интерфейс для исполнителей шагов в ReAct цикле.
//
// # StepExecutor Pattern (PHASE 3 REFACTOR)
//
// StepExecutor separates execution logic from data (ReActExecution).
// This enables:
//   - Adding new execution strategies without modifying ReActCycle
//   - Composing steps into different pipelines (sequential, branching)
//   - Testing executors in isolation
//
// # Implementations
//
// ReActExecutor: Classic ReAct loop (LLM → Tools → Repeat)
// Future: ReflectionExecutor, ValidationExecutor, ParallelExecutor, etc.
//
// # Thread Safety
//
// StepExecutor implementations receive isolated ReActExecution instances,
// so concurrent execution is inherently safe.
type StepExecutor interface {
	// Execute выполняет пайплайн шагов и возвращает результат.
	//
	// Принимает ReActExecution как контейнер runtime состояния,
	// но не создаёт его — этим занимается ReActCycle.Execute().
	Execute(ctx context.Context, exec *ReActExecution) (ChainOutput, error)
}

// ReActExecutor — базовая реализация StepExecutor для классического ReAct цикла.
//
// # Architecture (PHASE 3-4 REFACTOR)
//
// Separation of concerns:
//   - ReActExecution = pure data container (runtime state)
//   - ReActExecutor = execution logic (iteration loop)
//   - Observers = cross-cutting concerns (debug, events)
//
// # Iteration Loop
//
// For each iteration (up to MaxIterations):
//   1. Notify observers: OnIterationStart
//   2. Execute LLMInvocationStep
//   3. Send events via IterationObserver
//   4. Check ExecutionSignal
//      - SignalFinalAnswer/SignalNeedUserInput → BREAK
//      - SignalNone → continue if no tool calls
//   5. If tool calls: Execute ToolExecutionStep
//   6. Send events via IterationObserver
//   7. Notify observers: OnIterationEnd
//
// # Observer Notifications (PHASE 4)
//
// Lifecycle:
//   - OnStart: Before execution begins
//   - OnIterationStart: Before each iteration
//   - OnIterationEnd: After each iteration
//   - OnFinish: After execution completes (success or error)
//
// # Thread Safety
//
// Thread-safe when used with isolated ReActExecution instances.
// Each Execute() call uses its own execution state, enabling concurrent execution.
type ReActExecutor struct {
	// observers — список наблюдателей за выполнением (Phase 4)
	observers []ExecutionObserver

	// iterationObserver — наблюдатель для событий внутри итерации (PHASE 4)
	iterationObserver *EmitterIterationObserver
}

// ExecutionObserver — интерфейс для наблюдения за выполнением (PHASE 4).
//
// # Observer Pattern (PHASE 4 REFACTOR)
//
// ExecutionObserver isolates cross-cutting concerns from core orchestration.
// Instead of calling Emit() or debug methods directly, executor notifies observers
// of lifecycle events and delegates concerns to observer implementations.
//
// # Implementations
//
// ChainDebugRecorder: Records debug logs for each execution
//   - OnStart: Starts debug recording
//   - OnIterationStart: Starts new iteration in debug log
//   - OnIterationEnd: Ends iteration in debug log
//   - OnFinish: Finalizes debug log and writes to file
//
// EmitterObserver: Sends final events to UI
//   - OnStart: (no action)
//   - OnIterationStart: (no action)
//   - OnIterationEnd: (no action)
//   - OnFinish: Sends EventDone or EventError
//
// # Thread Safety
//
// Observer implementations must be thread-safe as they may be called
// from concurrent Execute() executions (each with isolated ReActExecution).
//
// # Lifecycle Contract
//
// 1. OnStart is called once at the beginning of execution
// 2. OnIterationStart/OnIterationEnd are called for each iteration
// 3. OnFinish is called once at the end (success or error)
type ExecutionObserver interface {
	OnStart(ctx context.Context, exec *ReActExecution)
	OnIterationStart(iteration int)
	OnIterationEnd(iteration int)
	OnFinish(result ChainOutput, err error)
}

// NewReActExecutor создаёт новый ReActExecutor.
func NewReActExecutor() *ReActExecutor {
	return &ReActExecutor{
		observers:         make([]ExecutionObserver, 0),
		iterationObserver: nil, // Будет установлен через SetIterationObserver
	}
}

// AddObserver добавляет наблюдателя за выполнением.
//
// PHASE 3 REFACTOR: Подготовка к Phase 4 (изоляция debug и events).
// Thread-safe: вызывается до Execute(), не требует синхронизации.
func (e *ReActExecutor) AddObserver(observer ExecutionObserver) {
	e.observers = append(e.observers, observer)
}

// SetIterationObserver устанавливает наблюдатель для событий внутри итерации.
//
// PHASE 4 REFACTOR: Изоляция логики отправки событий из core orchestration.
// Thread-safe: вызывается до Execute(), не требует синхронизации.
func (e *ReActExecutor) SetIterationObserver(observer *EmitterIterationObserver) {
	e.iterationObserver = observer
}

// Execute выполняет ReAct цикл.
//
// PHASE 3 REFACTOR: Основная логика из ReActExecution.Run(),
// но теперь в отдельном компоненте (StepExecutor).
//
// PHASE 4 REFACTOR: Изоляция debug и events через observer pattern.
// Execute() больше не содержит прямых вызовов Emit или debug методов.
//
// Итерация:
//   ├─ LLMInvocationStep
//   ├─ Отправка событий через iterationObserver (EventThinking, EventToolCall)
//   ├─ Проверка сигнала (SignalFinalAnswer, SignalNeedUserInput)
//   ├─ Если tool calls:
//   │  └─ ToolExecutionStep
//   └─ Иначе: break
//
// Thread-safe: Использует изолированный ReActExecution.
func (e *ReActExecutor) Execute(ctx context.Context, exec *ReActExecution) (ChainOutput, error) {
	// 0. Notify observers: OnStart
	for _, obs := range e.observers {
		obs.OnStart(ctx, exec)
	}

	// 1. Добавляем user message в историю
	if err := exec.chainCtx.AppendMessage(llm.Message{
		Role:    llm.RoleUser,
		Content: exec.chainCtx.Input.UserQuery,
	}); err != nil {
		return e.notifyFinishWithError(exec, fmt.Errorf("failed to append user message: %w", err))
	}

	// 2. Debug запись теперь обрабатывается ChainDebugRecorder observer
	// (была добавлена в ReActCycle.Execute())

	// 3. ReAct цикл
	iterations := 0
	for iterations = 0; iterations < exec.config.MaxIterations; iterations++ {
		// Notify observers: OnIterationStart
		for _, obs := range e.observers {
			obs.OnIterationStart(iterations + 1)
		}

		// 3a. LLM Invocation
		llmResult := exec.llmStep.Execute(ctx, exec.chainCtx)

		// Обрабатываем результат
		if llmResult.Action == ActionError || llmResult.Error != nil {
			err := llmResult.Error
			if err == nil {
				err = fmt.Errorf("LLM step failed")
			}
			return e.notifyFinishWithError(exec, err)
		}

		// 3b. Отправляем события через iterationObserver (PHASE 4)
		lastMsg := exec.chainCtx.GetLastMessage()

		// Проверяем: был ли streaming?
		shouldSendThinking := true
		if exec.emitter != nil && exec.streamingEnabled {
			// Streaming был включен, EventThinkingChunk уже отправляли
			shouldSendThinking = false
		}

		if shouldSendThinking && e.iterationObserver != nil {
			e.iterationObserver.EmitThinking(ctx, lastMsg.Content)
		}

		// Отправляем EventToolCall для каждого tool call
		if e.iterationObserver != nil {
			for _, tc := range lastMsg.ToolCalls {
				e.iterationObserver.EmitToolCall(ctx, tc)
			}
		}

		// 3c. Проверяем сигнал от LLM шага
		if llmResult.Signal == SignalFinalAnswer || llmResult.Signal == SignalNeedUserInput {
			exec.finalSignal = llmResult.Signal
			break
		}

		// 3d. Проверяем есть ли tool calls
		if len(lastMsg.ToolCalls) == 0 {
			// Финальный ответ - нет tool calls
			if exec.finalSignal == SignalNone {
				exec.finalSignal = SignalFinalAnswer
			}
			break
		}

		// 3e. Tool Execution
		toolResult := exec.toolStep.Execute(ctx, exec.chainCtx)

		// Обрабатываем результат
		if toolResult.Action == ActionError || toolResult.Error != nil {
			err := toolResult.Error
			if err == nil {
				err = fmt.Errorf("tool execution failed")
			}
			return e.notifyFinishWithError(exec, err)
		}

		// Отправляем EventToolResult через iterationObserver (PHASE 4)
		if e.iterationObserver != nil {
			for _, tr := range exec.toolStep.GetToolResults() {
				e.iterationObserver.EmitToolResult(ctx, tr.Name, tr.Result, time.Duration(tr.Duration)*time.Millisecond)
			}
		}

		// Notify observers: OnIterationEnd
		for _, obs := range e.observers {
			obs.OnIterationEnd(iterations + 1)
		}
	}

	// 4. Формируем результат
	lastMsg := exec.chainCtx.GetLastMessage()
	result := lastMsg.Content

	utils.Debug("ReAct cycle completed",
		"iterations", iterations+1,
		"result_length", len(result),
		"duration_ms", time.Since(exec.startTime).Milliseconds())

	// 5. Отправляем EventMessage через iterationObserver (PHASE 4)
	if e.iterationObserver != nil {
		e.iterationObserver.EmitMessage(ctx, result)
	}

	// 6. EventDone будет отправлен через EmitterObserver.OnFinish (PHASE 4)

	// 7. Debug финализация теперь обрабатывается ChainDebugRecorder.OnFinish

	// 8. Возвращаем результат
	output := ChainOutput{
		Result:     result,
		Iterations: iterations + 1,
		Duration:   time.Since(exec.startTime),
		FinalState: exec.chainCtx.GetMessages(),
		DebugPath:  "", // Будет заполнен в ChainDebugObserver.OnFinish
		Signal:     exec.finalSignal,
	}

	// 9. Notify observers: OnFinish (EmitterObserver отправит EventDone, ChainDebugRecorder финализирует)
	for _, obs := range e.observers {
		obs.OnFinish(output, nil)
	}

	return output, nil
}

// notifyFinishWithError завершает выполнение с ошибкой и уведомляет наблюдателей.
func (e *ReActExecutor) notifyFinishWithError(exec *ReActExecution, err error) (ChainOutput, error) {
	// Debug финализация теперь обрабатывается ChainDebugRecorder.OnFinish

	// Notify observers: OnFinish with error (EmitterObserver отправит EventError, ChainDebugRecorder финализирует)
	for _, obs := range e.observers {
		obs.OnFinish(ChainOutput{}, err)
	}

	return ChainOutput{}, err
}

// Ensure ReActExecutor implements StepExecutor
var _ StepExecutor = (*ReActExecutor)(nil)

```

=================

# chain/executor_test.go

```go
// Package chain предоставляет Chain Pattern для AI агента.
package chain

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/events"
	"github.com/ilkoid/poncho-ai/pkg/llm"
	"github.com/ilkoid/poncho-ai/pkg/models"
	"github.com/ilkoid/poncho-ai/pkg/state"
	"github.com/ilkoid/poncho-ai/pkg/tools"
)

// TestStepExecutorInterface verifies that ReActExecutor implements StepExecutor.
func TestStepExecutorInterface(t *testing.T) {
	var _ StepExecutor = (*ReActExecutor)(nil)
}

// TestNewReActExecutor verifies executor creation.
func TestNewReActExecutor(t *testing.T) {
	executor := NewReActExecutor()

	if executor == nil {
		t.Fatal("NewReActExecutor returned nil")
	}

	if executor.observers == nil {
		t.Error("observers slice not initialized")
	}
}

// mockObserver is a mock ExecutionObserver for testing.
type mockObserver struct {
	startCalls          int
	iterationStartCalls int
	iterationEndCalls   int
	finishCalls         int
	lastResult          ChainOutput
	lastError           error
}

func (m *mockObserver) OnStart(ctx context.Context, exec *ReActExecution) {
	m.startCalls++
}

func (m *mockObserver) OnIterationStart(iteration int) {
	m.iterationStartCalls++
}

func (m *mockObserver) OnIterationEnd(iteration int) {
	m.iterationEndCalls++
}

func (m *mockObserver) OnFinish(result ChainOutput, err error) {
	m.finishCalls++
	m.lastResult = result
	m.lastError = err
}

// TestReActExecutorObserverNotifications verifies observer notifications.
func TestReActExecutorObserverNotifications(t *testing.T) {
	ctx := context.Background()

	// Create real components
	config := NewReActCycleConfig()
	config.MaxIterations = 1

	modelRegistry := models.NewRegistry()
	toolsRegistry := tools.NewRegistry()

	// Create template steps
	llmStepTemplate := &LLMInvocationStep{
		modelRegistry: modelRegistry,
		defaultModel:  "test-model",
		registry:      toolsRegistry,
		systemPrompt:  "test prompt",
	}

	toolStepTemplate := &ToolExecutionStep{
		registry:     toolsRegistry,
		promptLoader: nil,
	}

	// Create execution
	input := ChainInput{
		UserQuery: "test query",
		State:     state.NewCoreState(nil),
		Registry:  toolsRegistry,
	}

	execution := NewReActExecution(
		ctx,
		input,
		llmStepTemplate,
		toolStepTemplate,
		nil, // emitter
		nil, // debugRecorder
		false,
		&config,
	)

	// Create executor with observer
	executor := NewReActExecutor()
	observer := &mockObserver{}
	executor.AddObserver(observer)

	// Execute (will fail because no actual LLM, but we test observer notifications)
	_, err := executor.Execute(ctx, execution)

	// We expect an error since we don't have a real LLM configured
	if err == nil {
		t.Log("Note: Execute succeeded (unexpected in test setup)")
	}

	// Verify observer was notified at least once
	if observer.startCalls != 1 {
		t.Errorf("Expected 1 OnStart call, got %d", observer.startCalls)
	}

	if observer.finishCalls != 1 {
		t.Errorf("Expected 1 OnFinish call, got %d", observer.finishCalls)
	}
}

// TestReActExecutorMultipleObservers verifies multiple observers work correctly.
func TestReActExecutorMultipleObservers(t *testing.T) {
	ctx := context.Background()

	config := NewReActCycleConfig()
	config.MaxIterations = 1

	modelRegistry := models.NewRegistry()
	toolsRegistry := tools.NewRegistry()

	llmStepTemplate := &LLMInvocationStep{
		modelRegistry: modelRegistry,
		defaultModel:  "test-model",
		registry:      toolsRegistry,
		systemPrompt:  "test prompt",
	}

	toolStepTemplate := &ToolExecutionStep{
		registry:     toolsRegistry,
		promptLoader: nil,
	}

	input := ChainInput{
		UserQuery: "test query",
		State:     state.NewCoreState(nil),
		Registry:  toolsRegistry,
	}

	execution := NewReActExecution(
		ctx,
		input,
		llmStepTemplate,
		toolStepTemplate,
		nil,
		nil,
		false,
		&config,
	)

	// Create executor with multiple observers
	executor := NewReActExecutor()
	observer1 := &mockObserver{}
	observer2 := &mockObserver{}
	executor.AddObserver(observer1)
	executor.AddObserver(observer2)

	// Execute
	_, _ = executor.Execute(ctx, execution)

	// Verify both observers received notifications
	if observer1.startCalls != 1 {
		t.Errorf("Observer1: Expected 1 OnStart call, got %d", observer1.startCalls)
	}

	if observer2.startCalls != 1 {
		t.Errorf("Observer2: Expected 1 OnStart call, got %d", observer2.startCalls)
	}
}

// TestReActExecutionAsDataContainer verifies that ReActExecution is a pure data container.
func TestReActExecutionAsDataContainer(t *testing.T) {
	ctx := context.Background()
	config := NewReActCycleConfig()
	config.MaxIterations = 5

	// Create a mock model registry
	modelRegistry := models.NewRegistry()
	toolsRegistry := tools.NewRegistry()
	coreState := state.NewCoreState(nil)

	// Create template steps
	llmStepTemplate := &LLMInvocationStep{
		modelRegistry: modelRegistry,
		defaultModel:  "test-model",
		registry:      toolsRegistry,
		systemPrompt:  "test prompt",
	}

	toolStepTemplate := &ToolExecutionStep{
		registry:     toolsRegistry,
		promptLoader: nil,
	}

	emitter := events.NewChanEmitter(10)
	debugRecorder := &ChainDebugRecorder{}

	// Create execution
	input := ChainInput{
		UserQuery: "test query",
		State:     coreState,
		Registry:  toolsRegistry,
	}

	execution := NewReActExecution(
		ctx,
		input,
		llmStepTemplate,
		toolStepTemplate,
		emitter,
		debugRecorder,
		true,
		&config,
	)

	// Verify all fields are set correctly
	if execution.ctx != ctx {
		t.Error("ctx not set correctly")
	}

	if execution.chainCtx == nil {
		t.Error("chainCtx is nil")
	}

	if execution.llmStep == nil {
		t.Error("llmStep is nil")
	}

	if execution.toolStep == nil {
		t.Error("toolStep is nil")
	}

	if execution.emitter != emitter {
		t.Error("emitter not set correctly")
	}

	if execution.debugRecorder != debugRecorder {
		t.Error("debugRecorder not set correctly")
	}

	if !execution.streamingEnabled {
		t.Error("streamingEnabled not set correctly")
	}

	if execution.config != &config {
		t.Error("config not set correctly")
	}

	// Verify steps are cloned (not the same instance)
	if execution.llmStep == llmStepTemplate {
		t.Error("llmStep not cloned, same instance as template")
	}

	if execution.toolStep == toolStepTemplate {
		t.Error("toolStep not cloned, same instance as template")
	}

	// Verify emitter and debugRecorder are cloned to steps
	if execution.llmStep.emitter != emitter {
		t.Error("llmStep emitter not set correctly")
	}

	if execution.llmStep.debugRecorder != debugRecorder {
		t.Error("llmStep debugRecorder not set correctly")
	}

	if execution.toolStep.debugRecorder != debugRecorder {
		t.Error("toolStep debugRecorder not set correctly")
	}
}

// TestReActExecutionHelpers verifies helper methods on ReActExecution.
func TestReActExecutionHelpers(t *testing.T) {
	ctx := context.Background()
	config := NewReActCycleConfig()
	config.MaxIterations = 1

	modelRegistry := models.NewRegistry()
	toolsRegistry := tools.NewRegistry()

	llmStepTemplate := &LLMInvocationStep{
		modelRegistry: modelRegistry,
		defaultModel:  "test-model",
		registry:      toolsRegistry,
		systemPrompt:  "test prompt",
	}

	toolStepTemplate := &ToolExecutionStep{
		registry:     toolsRegistry,
		promptLoader: nil,
	}

	emitter := events.NewChanEmitter(10)
	debugRecorder := &ChainDebugRecorder{}

	input := ChainInput{
		UserQuery: "test query",
		State:     state.NewCoreState(nil),
		Registry:  toolsRegistry,
	}

	execution := NewReActExecution(
		ctx,
		input,
		llmStepTemplate,
		toolStepTemplate,
		emitter,
		debugRecorder,
		true,
		&config,
	)

	// Test emitEvent - should not panic
	execution.emitEvent(events.Event{
		Type:      events.EventThinking,
		Data:      events.ThinkingData{Query: "test"},
		Timestamp: time.Now(),
	})

	// Test endDebugIteration - should not panic
	execution.endDebugIteration()

	// Test finalizeWithError - should return empty output and error
	testErr := fmt.Errorf("test error")
	output, err := execution.finalizeWithError(testErr)

	if err != testErr {
		t.Errorf("Expected error %v, got %v", testErr, err)
	}

	if output.Result != "" {
		t.Error("Expected empty result on error")
	}
}

// TestStepExecutorInterfaceContract verifies the StepExecutor interface contract.
func TestStepExecutorInterfaceContract(t *testing.T) {
	// This test documents the expected behavior of StepExecutor implementations
	// Any type implementing StepExecutor must:
	//
	// 1. Accept a ReActExecution (data container)
	// 2. Execute the iteration loop
	// 3. Return ChainOutput or error
	// 4. Notify observers of lifecycle events
	//
	// ReActExecutor is the reference implementation.

	t.Skip("Documentation test - documents the StepExecutor interface contract")
}

// TestReActExecutorWithNilObservers verifies executor works with no observers.
func TestReActExecutorWithNilObservers(t *testing.T) {
	ctx := context.Background()

	config := NewReActCycleConfig()
	config.MaxIterations = 1

	modelRegistry := models.NewRegistry()
	toolsRegistry := tools.NewRegistry()

	llmStepTemplate := &LLMInvocationStep{
		modelRegistry: modelRegistry,
		defaultModel:  "test-model",
		registry:      toolsRegistry,
		systemPrompt:  "test prompt",
	}

	toolStepTemplate := &ToolExecutionStep{
		registry:     toolsRegistry,
		promptLoader: nil,
	}

	input := ChainInput{
		UserQuery: "test query",
		State:     state.NewCoreState(nil),
		Registry:  toolsRegistry,
	}

	execution := NewReActExecution(
		ctx,
		input,
		llmStepTemplate,
		toolStepTemplate,
		nil,
		nil,
		false,
		&config,
	)

	// Create executor without observers
	executor := NewReActExecutor()

	// Execute - should not panic even without observers
	_, _ = executor.Execute(ctx, execution)
}

// TestExecutionObserverInterface verifies ExecutionObserver interface.
func TestExecutionObserverInterface(t *testing.T) {
	// Verify mockObserver implements ExecutionObserver
	var _ ExecutionObserver = (*mockObserver)(nil)

	// Verify ChainDebugRecorder implements ExecutionObserver (PHASE 4)
	var _ ExecutionObserver = (*ChainDebugRecorder)(nil)

	// Verify EmitterObserver implements ExecutionObserver (PHASE 4)
	var _ ExecutionObserver = (*EmitterObserver)(nil)
}

// PHASE 4 REFACTOR: Tests for new observers

// TestEmitterObserver verifies EmitterObserver sends correct events.
func TestEmitterObserver(t *testing.T) {
	ctx := context.Background()
	config := NewReActCycleConfig()
	config.MaxIterations = 1

	// Create a mock emitter to capture events
	eventChan := make(chan events.Event, 10)
	mockEmitter := &mockEmitter{eventsCh: eventChan}

	// Create EmitterObserver
	observer := NewEmitterObserver(mockEmitter)

	// Create a mock execution
	modelRegistry := models.NewRegistry()
	toolsRegistry := tools.NewRegistry()

	llmStepTemplate := &LLMInvocationStep{
		modelRegistry: modelRegistry,
		defaultModel:  "test-model",
		registry:      toolsRegistry,
		systemPrompt:  "test prompt",
	}

	toolStepTemplate := &ToolExecutionStep{
		registry:     toolsRegistry,
		promptLoader: nil,
	}

	input := ChainInput{
		UserQuery: "test query",
		State:     state.NewCoreState(nil),
		Registry:  toolsRegistry,
	}

	execution := NewReActExecution(
		ctx,
		input,
		llmStepTemplate,
		toolStepTemplate,
		nil,
		nil,
		false,
		&config,
	)

	// Test OnStart (should not emit anything)
	observer.OnStart(ctx, execution)

	// Test OnFinish with success
	output := ChainOutput{
		Result:     "test result",
		Iterations: 1,
		Duration:   100 * time.Millisecond,
		Signal:     SignalFinalAnswer,
	}

	observer.OnFinish(output, nil)

	// Verify EventDone was sent
	select {
	case event := <-eventChan:
		if event.Type != events.EventDone {
			t.Errorf("Expected EventDone, got %v", event.Type)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("Expected EventDone event but received none")
	}

	// Test OnFinish with error
	testErr := fmt.Errorf("test error")
	observer.OnFinish(ChainOutput{}, testErr)

	// Verify EventError was sent
	select {
	case event := <-eventChan:
		if event.Type != events.EventError {
			t.Errorf("Expected EventError, got %v", event.Type)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("Expected EventError event but received none")
	}
}

// TestEmitterIterationObserver verifies EmitterIterationObserver sends correct events.
func TestEmitterIterationObserver(t *testing.T) {
	ctx := context.Background()

	// Create a mock emitter to capture events
	eventChan := make(chan events.Event, 10)
	mockEmitter := &mockEmitter{eventsSh: eventChan}

	// Create EmitterIterationObserver
	observer := NewEmitterIterationObserver(mockEmitter)

	// Test EmitThinking
	observer.EmitThinking(ctx, "test query")

	select {
	case event := <-eventChan:
		if event.Type != events.EventThinking {
			t.Errorf("Expected EventThinking, got %v", event.Type)
		}
		if data, ok := event.Data.(events.ThinkingData); ok {
			if data.Query != "test query" {
				t.Errorf("Expected query 'test query', got %v", data.Query)
			}
		} else {
			t.Error("Expected ThinkingData, got different type")
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("Expected EventThinking event but received none")
	}

	// Test EmitToolCall
	toolCall := llm.ToolCall{
		ID:   "call-123",
		Name: "test_tool",
		Args: `{"arg1": "value1"}`,
	}
	observer.EmitToolCall(ctx, toolCall)

	select {
	case event := <-eventChan:
		if event.Type != events.EventToolCall {
			t.Errorf("Expected EventToolCall, got %v", event.Type)
		}
		if data, ok := event.Data.(events.ToolCallData); ok {
			if data.ToolName != "test_tool" {
				t.Errorf("Expected tool name 'test_tool', got %v", data.ToolName)
			}
		} else {
			t.Error("Expected ToolCallData, got different type")
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("Expected EventToolCall event but received none")
	}

	// Test EmitToolResult
	observer.EmitToolResult(ctx, "test_tool", `{"result": "success"}`, 50*time.Millisecond)

	select {
	case event := <-eventChan:
		if event.Type != events.EventToolResult {
			t.Errorf("Expected EventToolResult, got %v", event.Type)
		}
		if data, ok := event.Data.(events.ToolResultData); ok {
			if data.ToolName != "test_tool" {
				t.Errorf("Expected tool name 'test_tool', got %v", data.ToolName)
			}
			if data.Duration != 50*time.Millisecond {
				t.Errorf("Expected duration 50ms, got %v", data.Duration)
			}
		} else {
			t.Error("Expected ToolResultData, got different type")
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("Expected EventToolResult event but received none")
	}

	// Test EmitMessage
	observer.EmitMessage(ctx, "test message")

	select {
	case event := <-eventChan:
		if event.Type != events.EventMessage {
			t.Errorf("Expected EventMessage, got %v", event.Type)
		}
		if data, ok := event.Data.(events.MessageData); ok {
			if data.Content != "test message" {
				t.Errorf("Expected content 'test message', got %v", data.Content)
			}
		} else {
			t.Error("Expected MessageData, got different type")
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("Expected EventMessage event but received none")
	}
}

// TestEmitterIterationObserverWithNilEmitter verifies observer works with nil emitter.
func TestEmitterIterationObserverWithNilEmitter(t *testing.T) {
	ctx := context.Background()

	// Create observer with nil emitter
	observer := NewEmitterIterationObserver(nil)

	// Should not panic
	observer.EmitThinking(ctx, "test query")
	observer.EmitToolCall(ctx, llm.ToolCall{})
	observer.EmitToolResult(ctx, "tool", "result", 0)
	observer.EmitMessage(ctx, "message")
}

// mockEmitter is a mock Emitter for testing.
type mockEmitter struct {
	eventsSh chan events.Event
	eventsCh chan events.Event // alias for compatibility
}

func (m *mockEmitter) Emit(ctx context.Context, event events.Event) {
	if m.eventsSh != nil {
		select {
		case m.eventsSh <- event:
		case <-ctx.Done():
		}
	}
	if m.eventsCh != nil {
		select {
		case m.eventsCh <- event:
		case <-ctx.Done():
		}
	}
}

```

=================

# chain/llm_step.go

```go
// Package chain предоставляет Chain Pattern для AI агента.
package chain

import (
	"context"
	"fmt"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/events"
	"github.com/ilkoid/poncho-ai/pkg/llm"
	"github.com/ilkoid/poncho-ai/pkg/models"
	"github.com/ilkoid/poncho-ai/pkg/tools"
)

// LLMInvocationStep — Step для вызова LLM.
//
// Используется в ReAct цикле для получения ответа от LLM.
// Вызывает LLM с текущим контекстом (история сообщений + системный промпт).
//
// Rule 4: Работает через llm.Provider интерфейс.
// Rule 5: Thread-safe через ChainContext.
// Rule 7: Возвращает ошибку вместо panic.
type LLMInvocationStep struct {
	// modelRegistry — реестр LLM провайдеров (Rule 3)
	modelRegistry *models.Registry

	// defaultModel — имя модели по умолчанию для fallback
	defaultModel string

	// registry — реестр инструментов для получения определений (Rule 3)
	registry *tools.Registry

	// systemPrompt — базовый системный промпт
	systemPrompt string

	// debugRecorder — опциональный debug recorder
	debugRecorder *ChainDebugRecorder

	// emitter — для отправки streaming событий (EventThinkingChunk)
	emitter events.Emitter

	// startTime — время начала выполнения step (для duration tracking)
	startTime time.Time
}

// Name возвращает имя Step (для логирования).
func (s *LLMInvocationStep) Name() string {
	return "llm_invocation"
}

// Execute выполняет LLM вызов.
//
// Возвращает:
//   - StepResult{Action: ActionContinue, Signal: SignalNone} — если LLM вернул ответ с tool calls
//   - StepResult{Action: ActionBreak, Signal: SignalFinalAnswer} — если финальный ответ (без tool calls)
//   - StepResult с ошибкой — если произошла ошибка
//
// PHASE 2 REFACTOR: Теперь возвращает StepResult с типизированным сигналом.
//
// Rule 7: Возвращает ошибку вместо panic.
func (s *LLMInvocationStep) Execute(ctx context.Context, chainCtx *ChainContext) StepResult {
	s.startTime = time.Now()

	// 1. Определяем какую модель использовать
	modelName := s.determineModelName(chainCtx)

	// 2. Получаем провайдер и конфигурацию из реестра
	provider, modelDef, actualModel, err := s.modelRegistry.GetWithFallback(modelName, s.defaultModel)
	if err != nil {
		return StepResult{}.WithError(fmt.Errorf("failed to get model provider: %w", err))
	}

	// 3. Определяем параметры LLM (defaults + post-prompt overrides)
	opts := s.determineLLMOptions(chainCtx, modelDef)

	// 4. Формируем сообщения для LLM (с учетом типа модели)
	messages := chainCtx.BuildContextMessagesForModel(s.systemPrompt)
	messagesCount := len(messages)

	// 5. Получаем определения инструментов
	toolDefs := s.registry.GetDefinitions()

	// 6. Записываем LLM request в debug
	if s.debugRecorder != nil && s.debugRecorder.Enabled() {
		systemPromptUsed := "default"
		if chainCtx.GetActivePostPrompt() != "" {
			systemPromptUsed = "post_prompt"
		}
		s.debugRecorder.RecordLLMRequest(
			actualModel,
			opts.Temperature,
			opts.MaxTokens,
			systemPromptUsed,
			messagesCount,
		)
	}

	// 7. Подготавливаем opts для Generate (конвертируем в слайс any)
	generateOpts := []any{toolDefs}
	if opts.Model != "" {
		generateOpts = append(generateOpts, llm.WithModel(opts.Model))
	}
	if opts.Temperature != 0 {
		generateOpts = append(generateOpts, llm.WithTemperature(opts.Temperature))
	}
	if opts.MaxTokens != 0 {
		generateOpts = append(generateOpts, llm.WithMaxTokens(opts.MaxTokens))
	}
	if opts.Format != "" {
		generateOpts = append(generateOpts, llm.WithFormat(opts.Format))
	}

	// 8. Вызываем LLM (Rule 4)
	// Проверяем: поддерживает ли провайдер streaming?
	llmStart := time.Now()
	var response llm.Message

	if streamingProvider, ok := provider.(llm.StreamingProvider); ok && s.emitter != nil {
		// Используем streaming режим
		response, err = s.invokeStreamingLLM(ctx, streamingProvider, messages, generateOpts)
	} else {
		// Обычный синхронный режим
		response, err = provider.Generate(ctx, messages, generateOpts...)
	}
	llmDuration := time.Since(llmStart).Milliseconds()

	if err != nil {
		// Rule 7: возвращаем ошибку вместо panic
		return StepResult{}.WithError(fmt.Errorf("LLM generation failed: %w", err))
	}

	// 9. Записываем LLM response в debug
	if s.debugRecorder != nil && s.debugRecorder.Enabled() {
		s.debugRecorder.RecordLLMResponse(
			response.Content,
			response.ToolCalls,
			llmDuration,
		)
	}

	// 10. Добавляем assistant message в историю (thread-safe)
	if err := chainCtx.AppendMessage(llm.Message{
		Role:      llm.RoleAssistant,
		Content:   response.Content,
		ToolCalls: response.ToolCalls,
	}); err != nil {
		return StepResult{}.WithError(fmt.Errorf("failed to append assistant message: %w", err))
	}

	// 11. Сохраняем фактические параметры модели в контексте
	chainCtx.SetActualModel(actualModel)
	chainCtx.SetActualTemperature(opts.Temperature)
	chainCtx.SetActualMaxTokens(opts.MaxTokens)

	// 12. Определяем сигнал на основе ответа
	// PHASE 2 REFACTOR: Используем типизированные сигналы вместо string-маркеров
	if len(response.ToolCalls) == 0 {
		// Финальный ответ - нет tool calls
		// Проверяем на маркер пользовательского ввода (для обратной совместимости)
		// TODO: В будущем это должно определяться через structured output
		if response.Content == UserChoiceRequest {
			return StepResult{
				Action: ActionBreak,
				Signal: SignalNeedUserInput,
			}
		}
		return StepResult{
			Action: ActionBreak,
			Signal: SignalFinalAnswer,
		}
	}

	// Есть tool calls - продолжаем цикл
	return StepResult{
		Action: ActionContinue,
		Signal: SignalNone,
	}
}

// determineModelName определяет какую модель использовать для текущей итерации.
//
// Приоритет:
// 1. Post-prompt config.Model (если указан)
// 2. defaultModel (fallback)
func (s *LLMInvocationStep) determineModelName(chainCtx *ChainContext) string {
	if promptConfig := chainCtx.GetActivePromptConfig(); promptConfig != nil {
		if promptConfig.Model != "" {
			return promptConfig.Model
		}
	}
	return s.defaultModel
}

// determineLLMOptions определяет параметры LLM для текущей итерации.
//
// Комбинирует дефолтные значения из modelDef с overrides из post-prompt.
//
// Приоритет параметров:
// 1. Post-prompt config (из activePromptConfig) — высший приоритет
// 2. Model defaults (из config.Models.Definitions[name])
//
// Rule 4: Runtime parameter overrides через options pattern.
func (s *LLMInvocationStep) determineLLMOptions(chainCtx *ChainContext, modelDef config.ModelDef) llm.GenerateOptions {
	// Начинаем с дефолтных значений из конфигурации модели
	opts := llm.GenerateOptions{
		Model:       modelDef.ModelName,
		Temperature: modelDef.Temperature,
		MaxTokens:   modelDef.MaxTokens,
	}

	// Копируем ParallelToolCalls из конфигурации модели
	if modelDef.ParallelToolCalls != nil {
		opts.ParallelToolCalls = modelDef.ParallelToolCalls
	}

	// Overrides из post-prompt если есть
	if promptConfig := chainCtx.GetActivePromptConfig(); promptConfig != nil {
		if promptConfig.Model != "" {
			opts.Model = promptConfig.Model
		}
		if promptConfig.Temperature != 0 {
			opts.Temperature = promptConfig.Temperature
		}
		if promptConfig.MaxTokens != 0 {
			opts.MaxTokens = promptConfig.MaxTokens
		}
		if promptConfig.Format != "" {
			opts.Format = promptConfig.Format
		}
	}

	return opts
}

// GetDuration возвращает длительность выполнения step.
func (s *LLMInvocationStep) GetDuration() time.Duration {
	return time.Since(s.startTime)
}

// invokeStreamingLLM вызывает LLM с поддержкой стриминга.
//
// Отправляет EventThinkingChunk для каждой порции reasoning_content.
func (s *LLMInvocationStep) invokeStreamingLLM(
	ctx context.Context,
	provider llm.StreamingProvider,
	messages []llm.Message,
	opts []any,
) (llm.Message, error) {
	// Callback для обработки чанков
	callback := func(chunk llm.StreamChunk) {
		// Rule 11: проверяем context
		select {
		case <-ctx.Done():
			return
		default:
		}

		switch chunk.Type {
		case llm.ChunkThinking:
			// Отправляем EventThinkingChunk с reasoning_content
			s.emitThinkingChunk(ctx, chunk)
		case llm.ChunkError:
			// Отправляем EventError
			if s.emitter != nil && chunk.Error != nil {
				s.emitter.Emit(ctx, events.Event{
					Type:      events.EventError,
					Data:      events.ErrorData{Err: chunk.Error},
					Timestamp: time.Now(),
				})
			}
		case llm.ChunkDone:
			// Стриминг завершен
		}
	}

	// Вызываем GenerateStream
	return provider.GenerateStream(ctx, messages, callback, opts...)
}

// emitThinkingChunk отправляет событие с порцией reasoning_content.
func (s *LLMInvocationStep) emitThinkingChunk(ctx context.Context, chunk llm.StreamChunk) {
	if s.emitter == nil {
		return
	}
	s.emitter.Emit(ctx, events.Event{
		Type: events.EventThinkingChunk,
		Data: events.ThinkingChunkData{
			Chunk:       chunk.Delta,
			Accumulated: chunk.ReasoningContent,
		},
		Timestamp: time.Now(),
	})
}

```

=================

# chain/observers.go

```go
// Package chain предоставляет Chain Pattern для AI агента.
package chain

import (
	"context"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/events"
	"github.com/ilkoid/poncho-ai/pkg/llm"
)

// PHASE 4 REFACTOR: Observer implementations for isolating cross-cutting concerns.
//
// This file contains observer implementations that handle:
// - Event emission to UI (EmitterObserver, EmitterIterationObserver)
//
// Observers remove cross-cutting logic from core orchestration (executor.Execute())
// and enable clean separation of concerns.

// EmitterObserver — наблюдатель который отправляет события в Emitter.
//
// # Purpose (PHASE 4 REFACTOR)
//
// EmitterObserver implements ExecutionObserver to send final lifecycle events
// to the UI through the events.Emitter interface (Port & Adapter pattern).
//
// # Event Emission
//
// - OnStart: (no event)
// - OnIterationStart: (no event)
// - OnIterationEnd: (no event)
// - OnFinish: Sends EventDone (success) or EventError (failure)
//
// # Thread Safety
//
// Thread-safe when used with thread-safe events.Emitter implementations.
// Each execution should use its own observer instance.
type EmitterObserver struct {
	emitter events.Emitter
}

// NewEmitterObserver создаёт новый EmitterObserver.
func NewEmitterObserver(emitter events.Emitter) *EmitterObserver {
	return &EmitterObserver{
		emitter: emitter,
	}
}

// OnStart вызывается в начале выполнения Execute().
func (o *EmitterObserver) OnStart(ctx context.Context, exec *ReActExecution) {
	// Нет события для начала выполнения - Execution не отправляет события старта
}

// OnIterationStart вызывается в начале каждой итерации.
func (o *EmitterObserver) OnIterationStart(iteration int) {
	// Нет события для начала итерации
}

// OnIterationEnd вызывается в конце каждой итерации.
func (o *EmitterObserver) OnIterationEnd(iteration int) {
	// События отправляются в течение итерации, не в конце
}

// OnFinish вызывается в конце выполнения Execute().
//
// Отправляет финальные события: EventDone (success) or EventError (failure).
func (o *EmitterObserver) OnFinish(result ChainOutput, err error) {
	if o.emitter == nil {
		return
	}

	ctx := context.Background()

	if err != nil {
		// Отправляем EventError
		o.emitter.Emit(ctx, events.Event{
			Type:      events.EventError,
			Data:      events.ErrorData{Err: err},
			Timestamp: time.Now(),
		})
		return
	}

	// Отправляем EventDone
	o.emitter.Emit(ctx, events.Event{
		Type:      events.EventDone,
		Data:      events.MessageData{Content: result.Result},
		Timestamp: time.Now(),
	})
}

// Ensure EmitterObserver implements ExecutionObserver
var _ ExecutionObserver = (*EmitterObserver)(nil)

// EmitterIterationObserver — наблюдатель для событий внутри итерации.
//
// # Purpose (PHASE 4 REFACTOR)
//
// EmitterIterationObserver sends events during each iteration of the ReAct loop.
// Unlike ExecutionObserver (which is notified of lifecycle events), this observer
// is called manually from executor.Execute() for specific events.
//
// # Event Emission
//
// - EmitThinking: Sends EventThinking after LLM response
// - EmitToolCall: Sends EventToolCall for each tool call from LLM
// - EmitToolResult: Sends EventToolResult after each tool execution
// - EmitMessage: Sends EventMessage with final result
//
// # Why Separate Observer?
//
// This is separate from EmitterObserver because:
//   - These events occur DURING iterations (not at lifecycle boundaries)
//   - They require iteration-specific data (query, tool calls, results)
//   - They're called manually from executor, not via observer notifications
//
// # Thread Safety
//
// Thread-safe when used with thread-safe events.Emitter implementations.
type EmitterIterationObserver struct {
	emitter events.Emitter
}

// NewEmitterIterationObserver создаёт новый EmitterIterationObserver.
func NewEmitterIterationObserver(emitter events.Emitter) *EmitterIterationObserver {
	return &EmitterIterationObserver{
		emitter: emitter,
	}
}

// EmitThinking отправляет EventThinking с контентом LLM.
func (o *EmitterIterationObserver) EmitThinking(ctx context.Context, query string) {
	if o.emitter == nil {
		return
	}
	o.emitter.Emit(ctx, events.Event{
		Type:      events.EventThinking,
		Data:      events.ThinkingData{Query: query},
		Timestamp: time.Now(),
	})
}

// EmitToolCall отправляет EventToolCall для каждого tool call.
func (o *EmitterIterationObserver) EmitToolCall(ctx context.Context, toolCall llm.ToolCall) {
	if o.emitter == nil {
		return
	}
	o.emitter.Emit(ctx, events.Event{
		Type: events.EventToolCall,
		Data: events.ToolCallData{
			ToolName: toolCall.Name,
			Args:     toolCall.Args,
		},
		Timestamp: time.Now(),
	})
}

// EmitToolResult отправляет EventToolResult для каждого выполненного tool.
func (o *EmitterIterationObserver) EmitToolResult(ctx context.Context, toolName, result string, duration time.Duration) {
	if o.emitter == nil {
		return
	}
	o.emitter.Emit(ctx, events.Event{
		Type: events.EventToolResult,
		Data: events.ToolResultData{
			ToolName: toolName,
			Result:   result,
			Duration: duration,
		},
		Timestamp: time.Now(),
	})
}

// EmitMessage отправляет EventMessage с текстом ответа.
func (o *EmitterIterationObserver) EmitMessage(ctx context.Context, content string) {
	if o.emitter == nil {
		return
	}
	o.emitter.Emit(ctx, events.Event{
		Type:      events.EventMessage,
		Data:      events.MessageData{Content: content},
		Timestamp: time.Now(),
	})
}

```

=================

# chain/react.go

```go
// Package chain предоставляет Chain Pattern для AI агента.
//
// # Architecture Overview
//
// The chain package implements the ReAct (Reasoning + Acting) pattern for building
// AI agents with composable steps. The architecture follows a template-execution
// separation pattern that enables concurrent execution and clear separation of concerns.
//
// # Template vs Execution
//
// ## Template (Immutable Configuration)
//
// ReActCycle serves as an immutable template that is created once and shared across
// multiple executions. It holds:
//   - Configuration (ReActCycleConfig)
//   - Registries (models, tools)
//   - Step templates (LLMInvocationStep, ToolExecutionStep)
//   - Runtime defaults (emitter, debug recorder, streaming flag)
//
// Template fields are either fully immutable or protected by mutex for runtime defaults.
// This allows multiple Execute() calls to run concurrently without blocking each other.
//
// ## Execution (Runtime State)
//
// ReActExecution is created per Execute() invocation and holds runtime state:
//   - ChainContext (message history)
//   - Step instances (cloned from templates)
//   - Cross-cutting concerns (emitter, debug recorder)
//   - Execution metadata (timestamps, signals)
//
// Execution instances are never shared between goroutines, eliminating the need for
// synchronization during execution.
//
// # Execution Flow
//
// 1. ReActCycle.Execute() creates ReActExecution with isolated state
// 2. ReActExecutor orchestrates the iteration loop with observer notifications
// 3. Observers handle cross-cutting concerns (debug, events) separately
// 4. ChainOutput is returned with result, iterations, duration, and signal
//
// # Thread Safety
//
// ReActCycle: Thread-safe for concurrent Execute() calls
//   - Immutable fields: No synchronization needed
//   - Runtime defaults: Protected by sync.RWMutex
//   - Multiple goroutines can call Execute() simultaneously
//
// ReActExecution: Not thread-safe (never shared)
//   - Created per execution, used only by one goroutine
//   - No synchronization needed
//
// # Example Usage
//
//	// Create template (once)
//	config := chain.ReActCycleConfig{MaxIterations: 10}
//	cycle := chain.NewReActCycle(config)
//	cycle.SetModelRegistry(modelRegistry, "glm-4.6")
//	cycle.SetRegistry(toolsRegistry)
//	cycle.SetState(coreState)
//
//	// Execute concurrently (multiple times)
//	var wg sync.WaitGroup
//	for i := 0; i < 5; i++ {
//		wg.Add(1)
//		go func(query string) {
//			defer wg.Done()
//			output, err := cycle.Execute(ctx, chain.ChainInput{
//				UserQuery: query,
//				State:     coreState,
//				Registry:  toolsRegistry,
//			})
//			// Handle output...
//		}(queries[i])
//	}
//	wg.Wait()
//
// # Observer Pattern (Phase 4)
//
// Cross-cutting concerns are handled by observers:
//   - ChainDebugRecorder: Records debug logs for each execution
//   - EmitterObserver: Sends final events (EventDone, EventError)
//   - EmitterIterationObserver: Sends iteration events
//
// Observers are registered with the executor before execution and receive
// lifecycle notifications (OnStart, OnIterationStart, OnIterationEnd, OnFinish).
//
// # Execution Signals
//
// Steps communicate their intent via typed signals:
//   - SignalNone: Continue to next step
//   - SignalFinalAnswer: Execution complete, return result
//   - SignalNeedUserInput: Execution paused, waiting for user input
//   - SignalError: Execution failed with error
//
// Signals are propagated through StepResult and included in ChainOutput.
package chain

import (
	"context"
	"fmt"
	"sync"

	"github.com/ilkoid/poncho-ai/pkg/events"
	"github.com/ilkoid/poncho-ai/pkg/llm"
	"github.com/ilkoid/poncho-ai/pkg/models"
	"github.com/ilkoid/poncho-ai/pkg/prompt"
	"github.com/ilkoid/poncho-ai/pkg/state"
	"github.com/ilkoid/poncho-ai/pkg/tools"
)

// ReActCycle — реализация ReAct (Reasoning + Acting) паттерна.
//
// # Template vs Execution (PHASE 1-4 REFACTOR)
//
// ReActCycle is an IMMUTABLE TEMPLATE that is created once and shared across
// multiple executions. Runtime state is separated into ReActExecution.
//
// ## Template (this struct)
//   - Created once during initialization
//   - Shared across all Execute() calls
//   - Holds: registries, config, step templates
//   - Thread-safe: immutable fields + mutex for runtime defaults
//
// ## Execution (ReActExecution)
//   - Created per Execute() call
//   - Never shared between goroutines
//   - Holds: chain context, step instances, emitter, debug recorder
//   - No synchronization needed (not shared)
//
// # Execution Cycle
//
// 1. LLM анализирует контекст и решает что делать (Reasoning)
// 2. Если нужны инструменты — выполняет их (Acting)
// 3. Повторяет пока не получен финальный ответ или не достигнут лимит
//
// # Thread Safety
//
// Multiple goroutines can call Execute() concurrently:
//   - Each Execute() creates its own ReActExecution
//   - No mutex held during LLM calls or tool execution
//   - Runtime defaults (emitter, debug recorder) protected by RWMutex
//
// # Rule Compliance
//
// Rule 1: Работает с Tool interface ("Raw In, String Out")
// Rule 2: Конфигурация через YAML
// Rule 3: Tools вызываются через Registry
// Rule 4: LLM вызывается через llm.Provider
// Rule 5: Thread-safe через immutability (template) + isolation (execution)
// Rule 7: Все ошибки возвращаются, нет panic
// Rule 10: Godoc на всех public API
type ReActCycle struct {
	// Dependencies (immutable)
	modelRegistry *models.Registry // Registry всех LLM провайдеров
	registry      *tools.Registry
	state         *state.CoreState

	// Default model name для fallback
	defaultModel string

	// Configuration (immutable после создания, кроме runtimeDefaults)
	config ReActCycleConfig

	// Runtime defaults protection - только для mutable полей config
	// (DefaultEmitter, DefaultDebugRecorder, StreamingEnabled)
	mu sync.RWMutex

	// Steps (immutable template - клонируются в execution)
	llmStep  *LLMInvocationStep
	toolStep *ToolExecutionStep

	// Prompts directory (immutable)
	promptsDir string
}

// NewReActCycle создаёт новый ReActCycle.
//
// Rule 10: Godoc на public API.
func NewReActCycle(config ReActCycleConfig) *ReActCycle {
	// Валидируем конфигурацию
	if err := config.Validate(); err != nil {
		// Rule 7: возвращаем ошибку вместо panic
		// Но в конструкторе не можем вернуть ошибку, поэтому логируем и используем дефолты
		config = NewReActCycleConfig()
	}

	cycle := &ReActCycle{
		config:     config,
		promptsDir: config.PromptsDir,
	}

	// Создаём шаги (immutable template - будут клонироваться в execution)
	cycle.llmStep = &LLMInvocationStep{
		systemPrompt: config.SystemPrompt,
	}

	cycle.toolStep = &ToolExecutionStep{
		promptLoader: cycle, // ReActCycle реализует PromptLoader
	}

	return cycle
}

// SetModelRegistry устанавливает реестр моделей и дефолтную модель.
//
// Rule 3: Registry pattern для моделей.
// Rule 4: Работает через models.Registry интерфейс.
// Thread-safe: устанавливает immutable dependencies.
func (c *ReActCycle) SetModelRegistry(registry *models.Registry, defaultModel string) {
	c.modelRegistry = registry
	c.defaultModel = defaultModel
	c.llmStep.modelRegistry = registry
	c.llmStep.defaultModel = defaultModel
}

// SetRegistry устанавливает реестр инструментов.
//
// Rule 3: Tools вызываются через Registry.
// Thread-safe: устанавливает immutable dependencies.
func (c *ReActCycle) SetRegistry(registry *tools.Registry) {
	c.registry = registry
	c.llmStep.registry = registry
	c.toolStep.registry = registry
}

// SetState устанавливает framework core состояние.
//
// Rule 6: Использует pkg/state.CoreState вместо internal/app.
// Thread-safe: устанавливает immutable dependencies.
func (c *ReActCycle) SetState(state *state.CoreState) {
	c.state = state
}

// AttachDebug присоединяет debug recorder к ReActCycle.
//
// PHASE 1 REFACTOR: Теперь сохраняет recorder в config для передачи в execution.
// Thread-safe: использует mutex для защиты config.DefaultDebugRecorder.
func (c *ReActCycle) AttachDebug(recorder *ChainDebugRecorder) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.config.DefaultDebugRecorder = recorder
}

// SetEmitter устанавливает emitter для отправки событий в UI.
//
// Port & Adapter pattern: ReActCycle зависит от абстракции events.Emitter.
//
// PHASE 1 REFACTOR: Теперь сохраняет emitter в config для передачи в execution.
// Thread-safe: использует mutex для защиты config.DefaultEmitter.
func (c *ReActCycle) SetEmitter(emitter events.Emitter) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.config.DefaultEmitter = emitter
}

// SetStreamingEnabled включает или выключает streaming режим.
//
// PHASE 1 REFACTOR: Теперь сохраняет флаг в config для передачи в execution.
// Thread-safe: использует mutex для защиты config.StreamingEnabled.
func (c *ReActCycle) SetStreamingEnabled(enabled bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.config.StreamingEnabled = enabled
}

// Execute выполняет ReAct цикл.
//
// PHASE 1 REFACTOR: Теперь создаёт ReActExecution на каждый вызов.
// PHASE 3 REFACTOR: Использует StepExecutor для выполнения.
// PHASE 4 REFACTOR: Регистрирует наблюдателей (debug, events) с executor.
// Thread-safe: читает runtime defaults с RWMutex, concurrent execution безопасен.
// Concurrent execution безопасен - несколько Execute() могут работать параллельно.
//
// ReAct цикл:
// 1. Валидация зависимостей (read-only, без блокировки)
// 2. Читаем runtime defaults с RWMutex
// 3. Создаём ReActExecution (runtime state)
// 4. Создаём ReActExecutor (исполнитель)
// 5. Регистрируем наблюдателей (PHASE 4)
// 6. Запускаем executor.Execute()
// 7. Возвращаем результат
//
// Rule 7: Возвращает ошибку вместо panic.
func (c *ReActCycle) Execute(ctx context.Context, input ChainInput) (ChainOutput, error) {
	// 1. Валидация (read-only, без блокировки)
	if err := c.validateDependencies(); err != nil {
		return ChainOutput{}, fmt.Errorf("invalid dependencies: %w", err)
	}

	// 2. Читаем runtime defaults с RLock
	c.mu.RLock()
	defaultEmitter := c.config.DefaultEmitter
	defaultDebugRecorder := c.config.DefaultDebugRecorder
	streamingEnabled := c.config.StreamingEnabled
	c.mu.RUnlock()

	// 3. Создаём execution (runtime state)
	execution := NewReActExecution(
		ctx,
		input,
		c.llmStep,               // Шаблон шага (будет склонирован)
		c.toolStep,              // Шаблон шага (будет склонирован)
		defaultEmitter,          // Emitter из config (thread-safe copy)
		defaultDebugRecorder,    // Debug recorder из config (thread-safe copy)
		streamingEnabled,        // Streaming флаг из config (thread-safe copy)
		&c.config,               // Reference на config (immutable part)
	)

	// 4. Создаём executor
	executor := NewReActExecutor()

	// 5. Регистрируем наблюдателей (PHASE 4 REFACTOR)
	if defaultDebugRecorder != nil {
		executor.AddObserver(defaultDebugRecorder)
	}
	if defaultEmitter != nil {
		// EmitterObserver для финальных событий (EventDone, EventError)
		emitterObserver := NewEmitterObserver(defaultEmitter)
		executor.AddObserver(emitterObserver)

		// EmitterIterationObserver для событий внутри итераций
		iterationObserver := NewEmitterIterationObserver(defaultEmitter)
		executor.SetIterationObserver(iterationObserver)
	}

	// 6. Запускаем executor (без mutex!)
	return executor.Execute(ctx, execution)
}

// validateDependencies проверяет что все зависимости установлены.
//
// Rule 7: Возвращает ошибку вместо panic.
func (c *ReActCycle) validateDependencies() error {
	if c.modelRegistry == nil {
		return fmt.Errorf("model registry is not set (call SetModelRegistry)")
	}
	if c.defaultModel == "" {
		return fmt.Errorf("default model is not set")
	}
	if c.registry == nil {
		return fmt.Errorf("tools registry is not set (call SetRegistry)")
	}
	// state опционален
	return nil
}

// LoadToolPostPrompt загружает post-prompt для инструмента.
//
// Реализует интерфейс PromptLoader для ToolExecutionStep.
func (c *ReActCycle) LoadToolPostPrompt(toolName string) (string, *prompt.PromptConfig, error) {
	// Проверяем есть ли post-prompt конфигурация
	if c.config.ToolPostPrompts == nil {
		return "", nil, fmt.Errorf("no post-prompts configured")
	}

	toolPrompt, ok := c.config.ToolPostPrompts.Tools[toolName]
	if !ok || !toolPrompt.Enabled {
		return "", nil, fmt.Errorf("no post-prompt for tool: %s", toolName)
	}

	// Загружаем prompt файл через prompt package
	promptFile, err := c.config.ToolPostPrompts.GetToolPromptFile(toolName, c.promptsDir)
	if err != nil {
		return "", nil, fmt.Errorf("failed to load prompt file: %w", err)
	}

	// Post-prompt опционален — если не настроен, возвращаем nil
	if promptFile == nil {
		return "", nil, nil
	}

	// Формируем текст системного промпта
	if len(promptFile.Messages) == 0 {
		return "", nil, fmt.Errorf("prompt file has no messages: %s", toolName)
	}
	systemPrompt := promptFile.Messages[0].Content

	return systemPrompt, &promptFile.Config, nil
}

// Ensure ReActCycle implements Chain
var _ Chain = (*ReActCycle)(nil)

// Run выполняет ReAct цикл для запроса пользователя.
//
// Реализует Agent interface для прямого использования агента.
// Удобно для простых случаев когда не нужен полный контроль ChainInput.
//
// PHASE 1 REFACTOR: Thread-safe через immutability - без mutex.
// PHASE 2 REFACTOR: Использует типизированный Signal вместо string-маркера.
func (c *ReActCycle) Run(ctx context.Context, query string) (string, error) {
	// Проверяем зависимости (read-only)
	if err := c.validateDependencies(); err != nil {
		return "", err
	}

	// Создаем ChainInput из текущей конфигурации
	input := ChainInput{
		UserQuery: query,
		State:     c.state,
		Registry:  c.registry,
	}

	// Выполняем Chain и возвращаем результат
	output, err := c.Execute(ctx, input)
	if err != nil {
		return "", err
	}

	// PHASE 2 REFACTOR: Проверяем типизированный сигнал вместо string-маркера
	// SignalNeedUserInput указывает что нужен пользовательский ввод
	if output.Signal == SignalNeedUserInput {
		return UserChoiceRequest, nil
	}

	return output.Result, nil
}

// GetHistory возвращает историю диалога.
//
// Реализует Agent interface.
// PHASE 1 REFACTOR: Блокировка не нужна - CoreState thread-safe (RWMutex).
func (c *ReActCycle) GetHistory() []llm.Message {
	if c.state == nil {
		return []llm.Message{}
	}
	return c.state.GetHistory()
}

// Ensure ReActCycle implements PromptLoader
var _ PromptLoader = (*ReActCycle)(nil)

// Ensure ReActCycle implements Agent (local interface in chain package)
var _ Agent = (*ReActCycle)(nil)

```

=================

# chain/react_race_test.go

```go
// Package chain предоставляет Chain Pattern для AI агента.
package chain

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/llm"
	"github.com/ilkoid/poncho-ai/pkg/models"
	"github.com/ilkoid/poncho-ai/pkg/state"
	"github.com/ilkoid/poncho-ai/pkg/tools"
)

// mockLLMProvider — mock implementation of llm.Provider for testing.
type mockLLMProvider struct {
	responses chan llm.Message
	mu        sync.Mutex
}

func newMockLLMProvider() *mockLLMProvider {
	return &mockLLMProvider{
		responses: make(chan llm.Message, 100),
	}
}

func (m *mockLLMProvider) Generate(_ context.Context, messages []llm.Message, opts ...any) (llm.Message, error) {
	// Return a simple response (no tool calls)
	return llm.Message{
		Role:    llm.RoleAssistant,
		Content: fmt.Sprintf("Mock response for: %s", messages[len(messages)-1].Content),
	}, nil
}

func (m *mockLLMProvider) Close() error {
	return nil
}

// setupTestCycle создаёт ReActCycle для тестирования.
func setupTestCycle(t *testing.T) *ReActCycle {
	t.Helper()

	// Create model registry with mock provider
	registry := models.NewRegistry()
	modelConfig := config.ModelDef{
		Provider:  "mock",
		APIKey:    "test-key",
		BaseURL:   "https://mock.example.com",
		ModelName: "mock-model",
		Temperature: 0.5,
		MaxTokens:   1000,
	}

	// Create mock provider
	mockProvider := newMockLLMProvider()

	if err := registry.Register("mock-model", modelConfig, mockProvider); err != nil {
		t.Fatalf("Failed to register model: %v", err)
	}

	// Create tools registry
	toolsRegistry := tools.NewRegistry()

	// Create core state with minimal config
	appCfg := &config.AppConfig{
		Models: config.ModelsConfig{
			DefaultReasoning: "mock-model",
			DefaultChat:      "mock-model",
		},
	}
	coreState := state.NewCoreState(appCfg)

	// Create ReActCycle config
	cfg := NewReActCycleConfig()
	cfg.MaxIterations = 3

	// Create ReActCycle
	cycle := NewReActCycle(cfg)
	cycle.SetModelRegistry(registry, "mock-model")
	cycle.SetRegistry(toolsRegistry)
	cycle.SetState(coreState)

	return cycle
}

// TestConcurrentExecution проверяет что несколько Execute()
// могут работать одновременно без data races.
//
// PHASE 1 REFACTOR: Этот тест подтверждает что удаление mutex
// не нарушает thread-safety и позволяет concurrent execution.
func TestConcurrentExecution(t *testing.T) {
	cycle := setupTestCycle(t)
	ctx := context.Background()

	// Запускаем 10 параллельных выполнений
	const numGoroutines = 10
	var wg sync.WaitGroup
	errors := make(chan error, numGoroutines)
	results := make(chan ChainOutput, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			input := ChainInput{
				UserQuery: fmt.Sprintf("Test query %d", idx),
				State:     state.NewCoreState(nil),
				Registry:  tools.NewRegistry(),
			}
			output, err := cycle.Execute(ctx, input)
			if err != nil {
				errors <- err
				return
			}
			results <- output
		}(i)
	}

	wg.Wait()
	close(errors)
	close(results)

	// Проверяем что нет ошибок
	for err := range errors {
		t.Errorf("Concurrent execution failed: %v", err)
	}

	// Проверяем что получили все результаты
	resultCount := 0
	for range results {
		resultCount++
	}

	if resultCount != numGoroutines {
		t.Errorf("Expected %d results, got %d", numGoroutines, resultCount)
	}
}

// TestConcurrentRun проверяет что несколько Run()
// могут работать одновременно без data races.
func TestConcurrentRun(t *testing.T) {
	cycle := setupTestCycle(t)
	ctx := context.Background()

	// Запускаем 10 параллельных Run вызовов
	const numGoroutines = 10
	var wg sync.WaitGroup
	errors := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, err := cycle.Run(ctx, fmt.Sprintf("Test query %d", idx))
			if err != nil {
				errors <- err
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	// Проверяем что нет ошибок
	for err := range errors {
		t.Errorf("Concurrent Run failed: %v", err)
	}
}

// TestConcurrentSetters проверяет что setters могут вызываться
// concurrently во время выполнения Execute.
func TestConcurrentSetters(t *testing.T) {
	cycle := setupTestCycle(t)
	ctx := context.Background()

	var wg sync.WaitGroup
	errors := make(chan error, 20)

	// Запускаем 10 параллельных Execute
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			input := ChainInput{
				UserQuery: fmt.Sprintf("Test query %d", idx),
				State:     state.NewCoreState(nil),
				Registry:  tools.NewRegistry(),
			}
			_, err := cycle.Execute(ctx, input)
			if err != nil {
				errors <- err
			}
		}(i)
	}

	// Запускаем 10 параллельных Setters
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			switch idx % 3 {
			case 0:
				cycle.SetStreamingEnabled(idx%2 == 0)
			case 1:
				cycle.SetEmitter(nil)
			case 2:
				cycle.AttachDebug(nil)
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	// Проверяем что нет ошибок
	for err := range errors {
		t.Errorf("Concurrent operations failed: %v", err)
	}
}

// BenchmarkConcurrentExecution бенчмарк для concurrent execution.
func BenchmarkConcurrentExecution(b *testing.B) {
	cycle := setupTestCycle(&testing.T{})
	ctx := context.Background()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		idx := 0
		for pb.Next() {
			idx++
			input := ChainInput{
				UserQuery: fmt.Sprintf("Benchmark query %d", idx),
				State:     state.NewCoreState(nil),
				Registry:  tools.NewRegistry(),
			}
			_, _ = cycle.Execute(ctx, input)
		}
	})
}

// BenchmarkSequentialExecution бенчмарк для sequential execution (для сравнения).
func BenchmarkSequentialExecution(b *testing.B) {
	cycle := setupTestCycle(&testing.T{})
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		input := ChainInput{
			UserQuery: fmt.Sprintf("Benchmark query %d", i),
			State:     state.NewCoreState(nil),
			Registry:  tools.NewRegistry(),
		}
		_, _ = cycle.Execute(ctx, input)
	}
}

// TestExecutionIsolation проверяет что разные execution
// не влияют друг на друга.
func TestExecutionIsolation(t *testing.T) {
	cycle := setupTestCycle(t)
	ctx := context.Background()

	// Create two executions with different queries
	input1 := ChainInput{
		UserQuery: "Query 1",
		State:     state.NewCoreState(nil),
		Registry:  tools.NewRegistry(),
	}
	input2 := ChainInput{
		UserQuery: "Query 2",
		State:     state.NewCoreState(nil),
		Registry:  tools.NewRegistry(),
	}

	// Execute them concurrently
	var wg sync.WaitGroup
	var result1, result2 ChainOutput

	wg.Add(2)
	go func() {
		defer wg.Done()
		var err error
		result1, err = cycle.Execute(ctx, input1)
		if err != nil {
			t.Errorf("Execution 1 failed: %v", err)
		}
	}()
	go func() {
		defer wg.Done()
		var err error
		result2, err = cycle.Execute(ctx, input2)
		if err != nil {
			t.Errorf("Execution 2 failed: %v", err)
		}
	}()

	wg.Wait()

	// Verify results are different
	if result1.Result == result2.Result {
		t.Error("Expected different results for different queries")
	}

	// Verify each result contains its query
	if !contains(result1.Result, "Query 1") {
		t.Errorf("Result 1 should contain 'Query 1', got: %s", result1.Result)
	}
	if !contains(result2.Result, "Query 2") {
		t.Errorf("Result 2 should contain 'Query 2', got: %s", result2.Result)
	}
}

// contains проверяет что строка содержит подстроку (case-insensitive).
func contains(s, substr string) bool {
	return len(s) >= len(substr) &&
		(len(substr) == 0 || s == substr ||
			len(s) > 0 && (s[:len(substr)] == substr ||
				s[len(s)-len(substr):] == substr ||
			 indexOf(s, substr) >= 0))
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

// TestStreamingToggle проверяет что streaming флаг можно менять
// между выполнениями.
func TestStreamingToggle(t *testing.T) {
	cycle := setupTestCycle(t)
	ctx := context.Background()

	// Execute with streaming enabled
	cycle.SetStreamingEnabled(true)
	input := ChainInput{
		UserQuery: "Test with streaming",
		State:     state.NewCoreState(nil),
		Registry:  tools.NewRegistry(),
	}
	_, err := cycle.Execute(ctx, input)
	if err != nil {
		t.Fatalf("Execute with streaming failed: %v", err)
	}

	// Execute with streaming disabled
	cycle.SetStreamingEnabled(false)
	input2 := ChainInput{
		UserQuery: "Test without streaming",
		State:     state.NewCoreState(nil),
		Registry:  tools.NewRegistry(),
	}
	_, err = cycle.Execute(ctx, input2)
	if err != nil {
		t.Fatalf("Execute without streaming failed: %v", err)
	}
}

// TestConcurrentWithTimeout проверяет concurrent execution с timeout.
func TestConcurrentWithTimeout(t *testing.T) {
	cycle := setupTestCycle(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	const numGoroutines = 5
	var wg sync.WaitGroup
	errors := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			input := ChainInput{
				UserQuery: fmt.Sprintf("Timeout test %d", idx),
				State:     state.NewCoreState(nil),
				Registry:  tools.NewRegistry(),
			}
			_, err := cycle.Execute(ctx, input)
			if err != nil {
				errors <- err
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	// Проверяем что нет ошибок (включая context.DeadlineExceeded)
	for err := range errors {
		t.Errorf("Concurrent execution with timeout failed: %v", err)
	}
}

```

=================

# chain/step.go

```go
// Package chain предоставляет Chain Pattern для AI агента.
package chain

import (
	"context"
	"fmt"
)

// NextAction определяет поведение Chain после выполнения Step.
//
// Thread-safe concept: Step возвращает action, который обрабатывается Chain.
type NextAction int

const (
	// ActionContinue — продолжить выполнение следующего Step (или следующей итерации).
	ActionContinue NextAction = iota

	// ActionBreak — прервать выполнение Chain и вернуть результат.
	// Используется для завершения ReAct цикла.
	ActionBreak

	// ActionError — прервать выполнение с ошибкой.
	ActionError
)

// String возвращает строковое представление NextAction (для дебага).
func (a NextAction) String() string {
	switch a {
	case ActionContinue:
		return "Continue"
	case ActionBreak:
		return "Break"
	case ActionError:
		return "Error"
	default:
		return fmt.Sprintf("Unknown(%d)", a)
	}
}

// ExecutionSignal представляет типизированный управляющий сигнал от Step к Chain.
//
// PHASE 2 REFACTOR: Заменяет неявные string-маркеры (например, UserChoiceRequest)
// на типобезопасные сигналы. Это позволяет чётко определить намерение Step
// без анализа строкового содержимого ответа.
type ExecutionSignal int

const (
	// SignalNone — нет специального сигнала, обычное выполнение.
	SignalNone ExecutionSignal = iota

	// SignalFinalAnswer — финальный ответ получен, можно завершать выполнение.
	// Отправляется когда LLM вернул ответ без tool calls.
	SignalFinalAnswer

	// SignalNeedUserInput — требуется пользовательский ввод.
	// Отправляется когда LLM запросил дополнительную информацию от пользователя.
	// Заменяет string-маркер UserChoiceRequest.
	SignalNeedUserInput

	// SignalError — произошла ошибка, требующая особой обработки.
	SignalError
)

// String возвращает строковое представление ExecutionSignal (для дебага).
func (s ExecutionSignal) String() string {
	switch s {
	case SignalNone:
		return "None"
	case SignalFinalAnswer:
		return "FinalAnswer"
	case SignalNeedUserInput:
		return "NeedUserInput"
	case SignalError:
		return "Error"
	default:
		return fmt.Sprintf("Unknown(%d)", s)
	}
}

// StepResult представляет результат выполнения Step.
//
// PHASE 2 REFACTOR: Комбинирует NextAction (что делать дальше) и ExecutionSignal
// (типизированный управляющий сигнал). Это позволяет Step передавать более богатую
// информацию о своём состоянии без использования string-маркеров.
//
// Примеры использования:
//   - StepResult{Action: ActionContinue, Signal: SignalNone} — продолжить нормально
//   - StepResult{Action: ActionBreak, Signal: SignalFinalAnswer} — финальный ответ
//   - StepResult{Action: ActionBreak, Signal: SignalNeedUserInput} — нужен ввод пользователя
//   - StepResult{Action: ActionError, Signal: SignalError} — ошибка выполнения
type StepResult struct {
	// Action — что делать дальше (continue/break/error)
	Action NextAction

	// Signal — типизированный управляющий сигнал (опциональный)
	Signal ExecutionSignal

	// Error — ошибка выполнения (если Action == ActionError)
	Error error
}

// WithError создаёт StepResult с ошибкой.
func (r StepResult) WithError(err error) StepResult {
	return StepResult{
		Action: ActionError,
		Signal: SignalError,
		Error:  err,
	}
}

// String возвращает строковое представление StepResult (для дебага).
func (r StepResult) String() string {
	return fmt.Sprintf("StepResult{Action: %s, Signal: %s, Error: %v}",
		r.Action, r.Signal, r.Error)
}

// Step представляет атомарный шаг выполнения Chain.
//
// Step является изолированным, тестируемым и переиспользуемым компонентом.
// Каждый Step работает с ChainContext через thread-safe методы.
//
// Rule 7: Step возвращает ошибку (не panic), которая передаётся через StepResult.
//
// Примеры Step:
//   - LLMInvocationStep — вызывает LLM
//   - ToolExecutionStep — выполняет Tool
//   - PostPromptStep — загружает post-prompt
//   - ValidationStep — валидирует состояние
//
// PHASE 2 REFACTOR: Теперь возвращает StepResult вместо (NextAction, error).
type Step interface {
	// Name возвращает уникальное имя Step (для логирования).
	Name() string

	// Execute выполняет Step и возвращает StepResult.
	//
	// Step НЕ должен модифицировать ChainInput напрямую.
	// Все изменения состояния должны проходить через ChainContext методы.
	//
	// Возвращает:
	//   - StepResult — комбинация Action, Signal и Error
	Execute(ctx context.Context, chainCtx *ChainContext) StepResult
}

```

=================

# chain/step_test.go

```go
// Package chain предоставляет Chain Pattern для AI агента.
package chain

import (
	"context"
	"fmt"
	"testing"
)

// TestStepCreation verifies StepResult creation with various actions and signals.
func TestStepCreation(t *testing.T) {
	tests := []struct {
		name     string
		result   StepResult
		wantAction NextAction
		wantSignal ExecutionSignal
	}{
		{
			name: "Continue with no signal",
			result: StepResult{
				Action: ActionContinue,
				Signal: SignalNone,
			},
			wantAction: ActionContinue,
			wantSignal: SignalNone,
		},
		{
			name: "Break with final answer signal",
			result: StepResult{
				Action: ActionBreak,
				Signal: SignalFinalAnswer,
			},
			wantAction: ActionBreak,
			wantSignal: SignalFinalAnswer,
		},
		{
			name: "Break with need user input signal",
			result: StepResult{
				Action: ActionBreak,
				Signal: SignalNeedUserInput,
			},
			wantAction: ActionBreak,
			wantSignal: SignalNeedUserInput,
		},
		{
			name: "Error with error signal",
			result: StepResult{
				Action: ActionError,
				Signal: SignalError,
				Error:  nil,
			},
			wantAction: ActionError,
			wantSignal: SignalError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.result.Action != tt.wantAction {
				t.Errorf("Action = %v, want %v", tt.result.Action, tt.wantAction)
			}
			if tt.result.Signal != tt.wantSignal {
				t.Errorf("Signal = %v, want %v", tt.result.Signal, tt.wantSignal)
			}
		})
	}
}

// TestStepResultWithError verifies WithError helper method.
func TestStepResultWithError(t *testing.T) {
	baseResult := StepResult{
		Action: ActionContinue,
		Signal: SignalNone,
	}

	err := fmt.Errorf("test error")
	result := baseResult.WithError(err)

	if result.Action != ActionError {
		t.Errorf("Action = %v, want %v", result.Action, ActionError)
	}
	if result.Signal != SignalError {
		t.Errorf("Signal = %v, want %v", result.Signal, SignalError)
	}
	if result.Error != err {
		t.Errorf("Error = %v, want %v", result.Error, err)
	}
}

// TestExecutionSignalString verifies String() method for ExecutionSignal.
func TestExecutionSignalString(t *testing.T) {
	tests := []struct {
		signal   ExecutionSignal
		expected string
	}{
		{SignalNone, "None"},
		{SignalFinalAnswer, "FinalAnswer"},
		{SignalNeedUserInput, "NeedUserInput"},
		{SignalError, "Error"},
		{ExecutionSignal(999), "Unknown(999)"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.signal.String(); got != tt.expected {
				t.Errorf("String() = %v, want %v", got, tt.expected)
			}
		})
	}
}

// TestNextActionString verifies String() method for NextAction.
func TestNextActionString(t *testing.T) {
	tests := []struct {
		action   NextAction
		expected string
	}{
		{ActionContinue, "Continue"},
		{ActionBreak, "Break"},
		{ActionError, "Error"},
		{NextAction(999), "Unknown(999)"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.action.String(); got != tt.expected {
				t.Errorf("String() = %v, want %v", got, tt.expected)
			}
		})
	}
}

// TestStepResultString verifies String() method for StepResult.
func TestStepResultString(t *testing.T) {
	result := StepResult{
		Action: ActionContinue,
		Signal: SignalNone,
		Error:  nil,
	}

	got := result.String()
	expected := "StepResult{Action: Continue, Signal: None, Error: <nil>}"

	if got != expected {
		t.Errorf("String() = %v, want %v", got, expected)
	}
}

// mockStepForSignals is a mock Step implementation for testing signals.
type mockStepForSignals struct {
	name   string
	result StepResult
}

func (m *mockStepForSignals) Name() string {
	return m.name
}

func (m *mockStepForSignals) Execute(ctx context.Context, chainCtx *ChainContext) StepResult {
	return m.result
}

// TestStepInterfaceWithStepResult verifies that Step interface works with StepResult.
func TestStepInterfaceWithStepResult(t *testing.T) {
	ctx := context.Background()
	chainCtx := NewChainContext(ChainInput{
		UserQuery: "test",
		State:     nil,
		Registry:  nil,
	})

	tests := []struct {
		name     string
		step     Step
		wantAction NextAction
		wantSignal ExecutionSignal
	}{
		{
			name: "Continue step",
			step: &mockStepForSignals{
				name: "continue_step",
				result: StepResult{
					Action: ActionContinue,
					Signal: SignalNone,
				},
			},
			wantAction: ActionContinue,
			wantSignal: SignalNone,
		},
		{
			name: "Final answer step",
			step: &mockStepForSignals{
				name: "final_step",
				result: StepResult{
					Action: ActionBreak,
					Signal: SignalFinalAnswer,
				},
			},
			wantAction: ActionBreak,
			wantSignal: SignalFinalAnswer,
		},
		{
			name: "Need user input step",
			step: &mockStepForSignals{
				name: "input_step",
				result: StepResult{
					Action: ActionBreak,
					Signal: SignalNeedUserInput,
				},
			},
			wantAction: ActionBreak,
			wantSignal: SignalNeedUserInput,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.step.Execute(ctx, chainCtx)

			if result.Action != tt.wantAction {
				t.Errorf("Action = %v, want %v", result.Action, tt.wantAction)
			}
			if result.Signal != tt.wantSignal {
				t.Errorf("Signal = %v, want %v", result.Signal, tt.wantSignal)
			}
		})
	}
}

// TestChainOutputWithSignal verifies ChainOutput includes Signal field.
func TestChainOutputWithSignal(t *testing.T) {
	tests := []struct {
		name   string
		output ChainOutput
		signal ExecutionSignal
	}{
		{
			name: "Normal completion",
			output: ChainOutput{
				Result:     "Hello, world!",
				Iterations: 1,
				Signal:     SignalFinalAnswer,
			},
			signal: SignalFinalAnswer,
		},
		{
			name: "Need user input",
			output: ChainOutput{
				Result:     "Please provide more information",
				Iterations: 2,
				Signal:     SignalNeedUserInput,
			},
			signal: SignalNeedUserInput,
		},
		{
			name: "No signal",
			output: ChainOutput{
				Result:     "Some result",
				Iterations: 3,
				Signal:     SignalNone,
			},
			signal: SignalNone,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.output.Signal != tt.signal {
				t.Errorf("Signal = %v, want %v", tt.output.Signal, tt.signal)
			}
		})
	}
}

// TestSignalConstants verifies signal constants are unique.
func TestSignalConstants(t *testing.T) {
	signals := []ExecutionSignal{
		SignalNone,
		SignalFinalAnswer,
		SignalNeedUserInput,
		SignalError,
	}

	// Verify all constants have unique values
	seen := make(map[ExecutionSignal]bool)
	for _, sig := range signals {
		if seen[sig] {
			t.Errorf("Duplicate signal value detected: %v", sig)
		}
		seen[sig] = true
	}

	// Verify we have exactly 4 unique signals
	if len(seen) != 4 {
		t.Errorf("Expected 4 unique signals, got %d", len(seen))
	}
}

// TestActionConstants verifies action constants are unique.
func TestActionConstants(t *testing.T) {
	actions := []NextAction{
		ActionContinue,
		ActionBreak,
		ActionError,
	}

	// Verify all constants have unique values
	seen := make(map[NextAction]bool)
	for _, action := range actions {
		if seen[action] {
			t.Errorf("Duplicate action value detected: %v", action)
		}
		seen[action] = true
	}

	// Verify we have exactly 3 unique actions
	if len(seen) != 3 {
		t.Errorf("Expected 3 unique actions, got %d", len(seen))
	}
}

// TestStepResultZeroValue verifies zero value of StepResult.
func TestStepResultZeroValue(t *testing.T) {
	var result StepResult

	if result.Action != ActionContinue {
		t.Errorf("Zero value Action = %v, want %v", result.Action, ActionContinue)
	}
	if result.Signal != SignalNone {
		t.Errorf("Zero value Signal = %v, want %v", result.Signal, SignalNone)
	}
	if result.Error != nil {
		t.Errorf("Zero value Error = %v, want nil", result.Error)
	}
}

// TestSignalPropagation verifies signals propagate through execution.
func TestSignalPropagation(t *testing.T) {
	// This test verifies that when a Step returns a signal,
	// it properly propagates through the execution chain.

	ctx := context.Background()
	chainCtx := NewChainContext(ChainInput{
		UserQuery: "test",
		State:     nil,
		Registry:  nil,
	})

	// Test final answer signal propagation
	finalStep := &mockStepForSignals{
		name: "final",
		result: StepResult{
			Action: ActionBreak,
			Signal: SignalFinalAnswer,
		},
	}

	result := finalStep.Execute(ctx, chainCtx)
	if result.Signal != SignalFinalAnswer {
		t.Errorf("Expected SignalFinalAnswer, got %v", result.Signal)
	}

	// Test need user input signal propagation
	inputStep := &mockStepForSignals{
		name: "input",
		result: StepResult{
			Action: ActionBreak,
			Signal: SignalNeedUserInput,
		},
	}

	result = inputStep.Execute(ctx, chainCtx)
	if result.Signal != SignalNeedUserInput {
		t.Errorf("Expected SignalNeedUserInput, got %v", result.Signal)
	}
}

```

=================

# chain/tool_step.go

```go
// Package chain предоставляет Chain Pattern для AI агента.
package chain

import (
	"context"
	"fmt"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/llm"
	"github.com/ilkoid/poncho-ai/pkg/prompt"
	"github.com/ilkoid/poncho-ai/pkg/tools"
	"github.com/ilkoid/poncho-ai/pkg/utils"
)

// ToolExecutionStep — Step для выполнения инструментов.
//
// Используется в ReAct цикле после LLM вызова с tool calls.
// Выполняет инструменты из последнего assistant message.
//
// Rule 1: Работает с Tool interface ("Raw In, String Out").
// Rule 3: Tools вызываются через Registry.
// Rule 5: Thread-safe через ChainContext.
// Rule 7: Возвращает ошибку вместо panic.
type ToolExecutionStep struct {
	// registry — реестр инструментов (Rule 3)
	registry *tools.Registry

	// promptLoader — loader для post-prompts
	promptLoader PromptLoader

	// debugRecorder — опциональный debug recorder
	debugRecorder *ChainDebugRecorder

	// startTime — время начала выполнения step
	startTime time.Time

	// toolResults — результаты выполненных инструментов (для post-processing)
	toolResults []ToolResult
}

// ToolResult — результат выполнения одного инструмента.
type ToolResult struct {
	Name     string
	Args     string
	Result   string
	Duration int64
	Success  bool
	Error    error
}

// PromptLoader — интерфейс для загрузки post-prompts.
//
// Разделён для возможности мокинга в тестах (Rule 9).
type PromptLoader interface {
	// LoadToolPostPrompt загружает post-prompt для инструмента.
	// Возвращает:
	//   - promptText: текст системного промпта
	//   - promptConfig: конфигурация с параметрами модели
	//   - error: ошибка загрузки
	LoadToolPostPrompt(toolName string) (string, *prompt.PromptConfig, error)
}

// Name возвращает имя Step (для логирования).
func (s *ToolExecutionStep) Name() string {
	return "tool_execution"
}

// Execute выполняет все инструменты из последнего LLM ответа.
//
// Возвращает:
//   - StepResult{Action: ActionContinue, Signal: SignalNone} — если инструменты выполнены успешно
//   - StepResult с ошибкой — если произошла критическая ошибка
//
// PHASE 2 REFACTOR: Теперь возвращает StepResult.
//
// Rule 7: Возвращает ошибку вместо panic (но non-critical ошибки инструментов
//    возвращаются как строки в results).
func (s *ToolExecutionStep) Execute(ctx context.Context, chainCtx *ChainContext) StepResult {
	s.startTime = time.Now()
	s.toolResults = make([]ToolResult, 0)

	// 1. Получаем последнее сообщение (должен быть assistant с tool calls)
	lastMsg := chainCtx.GetLastMessage()
	if lastMsg == nil || lastMsg.Role != llm.RoleAssistant {
		return StepResult{}.WithError(fmt.Errorf("no assistant message found"))
	}

	if len(lastMsg.ToolCalls) == 0 {
		// Нет tool calls - это нормально для финальной итерации
		return StepResult{
			Action: ActionContinue,
			Signal: SignalNone,
		}
	}

	// 2. Выполняем каждый tool call
	for _, tc := range lastMsg.ToolCalls {
		result, err := s.executeToolCall(ctx, tc, chainCtx)
		s.toolResults = append(s.toolResults, result)

		if err != nil {
			// Критическая ошибка выполнения
			return StepResult{}.WithError(fmt.Errorf("tool execution failed: %w", err))
		}

		// 3. Добавляем tool result message в историю (thread-safe)
		// REFACTORED 2026-01-04: AppendMessage теперь возвращает ошибку
		if err := chainCtx.AppendMessage(llm.Message{
			Role:       llm.RoleTool,
			ToolCallID: tc.ID,
			Content:    result.Result,
		}); err != nil {
			return StepResult{}.WithError(fmt.Errorf("failed to append tool result message: %w", err))
		}
	}

	// 4. Загружаем post-prompt для выполненного инструмента
	//
	// NOTE: С включенным parallel_tool_calls=false LLM вызывает только один инструмент
	// за раз, поэтому post-prompt всегда активируется после выполнения.
	if len(lastMsg.ToolCalls) > 0 {
		toolName := lastMsg.ToolCalls[0].Name
		if err := s.loadAndActivatePostPrompt(toolName, chainCtx); err != nil {
			// Non-critical ошибка - логируем но продолжаем
			_ = err // TODO: добавить warning лог
		}
	}

	return StepResult{
		Action: ActionContinue,
		Signal: SignalNone,
	}
}

// executeToolCall выполняет один tool call.
//
// Rule 1: "Raw In, String Out" - получаем JSON строку, возвращаем строку.
// Rule 7: Возвращает ошибку вместо panic.
func (s *ToolExecutionStep) executeToolCall(ctx context.Context, tc llm.ToolCall, chainCtx *ChainContext) (ToolResult, error) {
	start := time.Now()
	result := ToolResult{
		Name: tc.Name,
		Args: tc.Args,
	}

	// 1. Санитизируем JSON аргументы
	cleanArgs := utils.CleanJsonBlock(tc.Args)

	// 2. Получаем tool из registry (Rule 3)
	tool, err := s.registry.Get(tc.Name)
	if err != nil {
		result.Success = false
		result.Error = err
		result.Result = fmt.Sprintf("Error: tool not found: %s", tc.Name)
		return result, err
	}

	// 3. Выполняем tool (Rule 1: "Raw In, String Out")
	execResult, execErr := tool.Execute(ctx, cleanArgs)
	duration := time.Since(start).Milliseconds()

	// 4. Формируем результат
	if execErr != nil {
		result.Success = false
		result.Error = execErr
		result.Result = fmt.Sprintf("Error: %v", execErr)
	} else {
		result.Success = true
		result.Result = execResult
	}
	result.Duration = duration

	// 5. Записываем в debug
	if s.debugRecorder != nil && s.debugRecorder.Enabled() {
		s.debugRecorder.RecordToolExecution(
			tc.Name,
			cleanArgs,
			result.Result,
			duration,
			result.Success,
		)
	}

	return result, nil
}

// loadAndActivatePostPrompt загружает и активирует post-prompt для инструмента.
//
// Post-prompt:
// 1. Загружается из YAML файла
// 2. Активируется в ChainContext (для следующей итерации)
// 3. Может переопределять параметры модели (model, temperature, max_tokens)
//
// Rule 2: Конфигурация через YAML.
func (s *ToolExecutionStep) loadAndActivatePostPrompt(toolName string, chainCtx *ChainContext) error {
	// 1. Загружаем post-prompt
	promptText, promptConfig, err := s.promptLoader.LoadToolPostPrompt(toolName)
	if err != nil {
		// Post-prompt опционален, не считаем это ошибкой
		return nil
	}

	// 2. Проверяем что post-prompt включён
	if promptConfig == nil {
		return nil
	}

	// 3. Активируем post-prompt в контексте (thread-safe)
	chainCtx.SetActivePostPrompt(promptText, promptConfig)

	return nil
}

// GetToolResults возвращает результаты выполнения инструментов.
func (s *ToolExecutionStep) GetToolResults() []ToolResult {
	return s.toolResults
}

// GetDuration возвращает длительность выполнения step.
func (s *ToolExecutionStep) GetDuration() time.Duration {
	return time.Since(s.startTime)
}

```

=================

# classifier/engine.go

```go
package classifier

import (
	"path/filepath"
	"strings"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/s3storage"
)

// Deprecated: используйте s3storage.FileMeta напрямую.
// Оставлен для обратной совместимости.
type ClassifiedFile = s3storage.FileMeta

// Engine выполняет классификацию
type Engine struct {
	rules []config.FileRule
}

func New(rules []config.FileRule) *Engine {
	return &Engine{rules: rules}
}

// Process принимает список сырых объектов и возвращает карту [Tag] -> Список файлов.
//
// Возвращает map[string][]*s3storage.FileMeta - классифицированные файлы
// с базовыми метаданными. Vision описание заполняется позже через
// state.Writer.UpdateFileAnalysis().
func (e *Engine) Process(objects []s3storage.StoredObject) (map[string][]*s3storage.FileMeta, error) {
	result := make(map[string][]*s3storage.FileMeta)

	for _, obj := range objects {
		filename := filepath.Base(obj.Key) // Смотрим только на имя файла, не на путь

		matched := false
		var matchedTag string

		for _, rule := range e.rules {
			for _, pattern := range rule.Patterns {
				// Используем Case-insensitive сравнение для расширений
				isMatch, _ := filepath.Match(strings.ToLower(pattern), strings.ToLower(filename))

				if isMatch {
					matchedTag = rule.Tag
					matched = true
					break
				}
			}
			if matched {
				break
			}
		}

		// Определяем тег (matched или "other")
		tag := matchedTag
		if !matched {
			tag = "other"
		}

		// Создаём FileMeta через конструктор
		fileMeta := s3storage.NewFileMeta(tag, obj.Key, obj.Size, filename)
		result[tag] = append(result[tag], fileMeta)
	}

	return result, nil
}

```

=================

# config/config.go

```go
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// AppConfig — корневая структура конфигурации.
// Она зеркалит структуру твоего config.yaml.
type AppConfig struct {
	Models          ModelsConfig         `yaml:"models"`
	Tools           map[string]ToolConfig `yaml:"tools"`
	S3              S3Config             `yaml:"s3"`
	ImageProcessing ImageProcConfig      `yaml:"image_processing"`
	App             AppSpecific          `yaml:"app"`
	FileRules       []FileRule           `yaml:"file_rules"`
	WB              WBConfig             `yaml:"wb"`
	Chains          map[string]ChainConfig `yaml:"chains"`
}

// ChainConfig — базовая конфигурация цепочки.
// Используется для чтения timeout из YAML без циклического импорта pkg/chain.
type ChainConfig struct {
	Type     string `yaml:"type"`
	Timeout  string `yaml:"timeout"`
	MaxIterations int `yaml:"max_iterations"`
}

type WBConfig struct {
	APIKey        string `yaml:"api_key"`
	BaseURL       string `yaml:"base_url"`        // Базовый URL WB Content API
	RateLimit     int    `yaml:"rate_limit"`      // Запросов в минуту
	BurstLimit    int    `yaml:"burst_limit"`     // Burst для rate limiter
	RetryAttempts int    `yaml:"retry_attempts"`  // Количество retry попыток
	Timeout       string `yaml:"timeout"`         // Timeout для HTTP запросов (например, "30s")
	BrandsLimit   int    `yaml:"brands_limit"`    // Макс. кол-во брендов для get_wb_brands tool
}

// GetDefaults возвращает дефолтные значения для незаполненных полей.
func (c *WBConfig) GetDefaults() WBConfig {
	result := *c // Копируем текущие значения

	if result.BaseURL == "" {
		result.BaseURL = "https://content-api.wildberries.ru"
	}
	if result.RateLimit == 0 {
		result.RateLimit = 100 // запросов в минуту
	}
	if result.BurstLimit == 0 {
		result.BurstLimit = 5
	}
	if result.RetryAttempts == 0 {
		result.RetryAttempts = 3
	}
	if result.Timeout == "" {
		result.Timeout = "30s"
	}
	if result.BrandsLimit == 0 {
		result.BrandsLimit = 500 // дефолтный лимит брендов
	}

	return result
}

type FileRule struct {
    Tag      string   `yaml:"tag"`      // Например "sketch", "plm", "marketing"
    Patterns []string `yaml:"patterns"` // Glob паттерны: "*.jpg", "*_spec.json"
    Required bool     `yaml:"required"` // Если true и файлов нет -> ошибка валидации артикула
}

// ModelsConfig — настройки AI моделей.
type ModelsConfig struct {
	DefaultReasoning string              `yaml:"default_reasoning"` // Алиас reasoning модели по умолчанию (для orchestrator)
	DefaultChat      string              `yaml:"default_chat"`      // Алиас для чата по умолчанию (например, "glm-4.5")
	DefaultVision    string              `yaml:"default_vision"`    // Алиас по умолчанию (например, "glm-4.6v-flash")
	Definitions      map[string]ModelDef `yaml:"definitions"`      // Словарь определений моделей
}

// ModelDef — параметры конкретной модели.
type ModelDef struct {
	Provider          string        `yaml:"provider"`   // "zai", "openai" и т.д.
	ModelName         string        `yaml:"model_name"` // Реальное имя в API
	APIKey            string        `yaml:"api_key"`    // Поддерживает ${VAR}
	MaxTokens         int           `yaml:"max_tokens"`
	Temperature       float64       `yaml:"temperature"`
	Timeout           time.Duration `yaml:"timeout"` // Go умеет парсить строки вида "60s", "1m"
	BaseURL           string        `yaml:"base_url"`
	Thinking          string        `yaml:"thinking"` // "enabled", "disabled" или пусто (для Zai GLM)
	ParallelToolCalls *bool        `yaml:"parallel_tool_calls"` // false=один tool за раз, true=параллельные вызовы
	IsVision          bool          `yaml:"is_vision"` // Явная метка vision-модели
}

// ToolConfig — настройки инструментов.
//
// Поля поддерживают YAML-конфигурацию для каждого tool индивидуально.
// Ключом в map является имя tool (например, "get_wb_parent_categories").
type ToolConfig struct {
	Enabled     bool   `yaml:"enabled"`
	Description string `yaml:"description,omitempty"`    // Описание для LLM (function calling)
	Type        string `yaml:"type,omitempty"`           // "wb", "dictionary", "planner"
	Endpoint    string `yaml:"endpoint,omitempty"`       // Base URL для API
	Path        string `yaml:"path,omitempty"`           // API path
	RateLimit   int    `yaml:"rate_limit,omitempty"`     // запросов в минуту
	Burst       int    `yaml:"burst,omitempty"`
	Timeout     string `yaml:"timeout,omitempty"`
	PostPrompt  string `yaml:"post_prompt,omitempty"`    // Путь к post-prompt файлу
	DefaultTake int    `yaml:"default_take,omitempty"`   // Для feedbacks API
}

// S3Config — настройки объектного хранилища.
type S3Config struct {
	Endpoint  string `yaml:"endpoint"`
	Region    string `yaml:"region"`
	Bucket    string `yaml:"bucket"`
	AccessKey string `yaml:"access_key"` // Поддерживает ${VAR}
	SecretKey string `yaml:"secret_key"` // Поддерживает ${VAR}
	UseSSL    bool   `yaml:"use_ssl"`
}

// ImageProcConfig — настройки обработки изображений.
type ImageProcConfig struct {
	MaxWidth int `yaml:"max_width"`
	Quality  int `yaml:"quality"`
}

// AppSpecific — общие настройки приложения.
type AppSpecific struct {
	Debug      bool          `yaml:"debug"`
	PromptsDir string        `yaml:"prompts_dir"`
	DebugLogs  DebugConfig   `yaml:"debug_logs"`
	Streaming  StreamingConfig `yaml:"streaming"`
}

// DebugConfig — настройки отладочных логов (JSON трейсы выполнения).
type DebugConfig struct {
	// Enabled — включена ли запись отладочных логов
	Enabled bool `yaml:"enabled"`

	// SaveLogs — сохранять ли логи в файлы
	SaveLogs bool `yaml:"save_logs"`

	// LogsDir — директория для сохранения логов
	LogsDir string `yaml:"logs_dir"`

	// IncludeToolArgs — включать аргументы инструментов в лог
	IncludeToolArgs bool `yaml:"include_tool_args"`

	// IncludeToolResults — включать результаты инструментов в лог
	IncludeToolResults bool `yaml:"include_tool_results"`

	// MaxResultSize — максимальный размер результата (символов)
	// Превышение обрезается с суффиксом "... (truncated)"
	// 0 означает без ограничений
	MaxResultSize int `yaml:"max_result_size"`
}

// StreamingConfig — настройки потоковой передачи LLM ответов.
type StreamingConfig struct {
	// Enabled — включен ли стриминг (default: true, opt-out)
	Enabled bool `yaml:"enabled"`

	// ThinkingOnly — отправлять только reasoning_content события (default: true)
	ThinkingOnly bool `yaml:"thinking_only"`
}

// GetDefaults возвращает дефолтные значения для незаполненных полей.
//
// Rule 11: Автономные приложения хранят ресурсы рядом с бинарником.
func (c *DebugConfig) GetDefaults() DebugConfig {
	result := *c

	if !result.Enabled {
		return result
	}

	// Rule 11: Если включено, но нет директории - используем абсолютный путь рядом с бинарником
	if result.LogsDir == "" {
		if exePath, err := os.Executable(); err == nil {
			exeDir := filepath.Dir(exePath)
			result.LogsDir = filepath.Join(exeDir, "debug_logs")
		} else {
			// Fallback: если не удалось получить путь к бинарнику, используем относительный
			result.LogsDir = "./debug_logs"
		}
	}

	// Дефолтно включаем логирование args и results
	if result.SaveLogs && !result.IncludeToolArgs {
		result.IncludeToolArgs = true
	}
	if result.SaveLogs && !result.IncludeToolResults {
		result.IncludeToolResults = true
	}

	// Дефолтный лимит размера результата
	if result.MaxResultSize == 0 {
		result.MaxResultSize = 5000 // 5KB
	}

	return result
}

// Load читает YAML файл, подставляет ENV переменные и возвращает готовую структуру.
func Load(path string) (*AppConfig, error) {
	// 1. Проверяем существование файла
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, fmt.Errorf("config file not found at: %s", path)
	}

	// 2. Читаем файл целиком
	rawBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	// 3. Подставляем переменные окружения.
	// os.ExpandEnv заменяет ${VAR} или $VAR на значение из системы.
	contentWithEnv := os.ExpandEnv(string(rawBytes))

	// 4. Парсим YAML в структуру
	var cfg AppConfig
	if err := yaml.Unmarshal([]byte(contentWithEnv), &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse yaml: %w", err)
	}

	// 5. Валидируем критические настройки
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}

	return &cfg, nil
}

// validate проверяет обязательные поля.
func (c *AppConfig) validate() error {
	if c.S3.Bucket == "" {
		return fmt.Errorf("s3.bucket is required")
	}
	if c.S3.Endpoint == "" {
		return fmt.Errorf("s3.endpoint is required")
	}

	// Валидируем default_reasoning если указан
	if c.Models.DefaultReasoning != "" {
		if _, ok := c.Models.Definitions[c.Models.DefaultReasoning]; !ok {
			return fmt.Errorf("default_reasoning model '%s' is not defined in definitions", c.Models.DefaultReasoning)
		}
	}

	// Валидируем default_chat если указан
	if c.Models.DefaultChat != "" {
		if _, ok := c.Models.Definitions[c.Models.DefaultChat]; !ok {
			return fmt.Errorf("default_chat model '%s' is not defined in definitions", c.Models.DefaultChat)
		}
	}

	// Валидируем default_vision если указан
	if c.Models.DefaultVision != "" {
		if _, ok := c.Models.Definitions[c.Models.DefaultVision]; !ok {
			return fmt.Errorf("default_vision model '%s' is not defined in definitions", c.Models.DefaultVision)
		}
	}

	return nil
}

// Helper методы для удобства доступа (Syntactic sugar)

// GetReasoningModel возвращает конфигурацию reasoning модели по умолчанию или по имени.
// Если name пустое, использует default_reasoning из конфига.
// Если default_reasoning не указан, fallback на default_chat.
func (c *AppConfig) GetReasoningModel(name string) (ModelDef, bool) {
	if name == "" {
		name = c.Models.DefaultReasoning
		// Fallback: если default_reasoning не указан, используем default_chat
		if name == "" {
			name = c.Models.DefaultChat
		}
	}
	m, ok := c.Models.Definitions[name]
	return m, ok
}

// GetChatModel возвращает конфигурацию chat модели по умолчанию или по имени.
// Если name пустое, использует default_chat из конфига.
func (c *AppConfig) GetChatModel(name string) (ModelDef, bool) {
	if name == "" {
		name = c.Models.DefaultChat
	}
	m, ok := c.Models.Definitions[name]
	return m, ok
}

// GetVisionModel возвращает конфигурацию модели по умолчанию или по имени.
func (c *AppConfig) GetVisionModel(name string) (ModelDef, bool) {
	if name == "" {
		name = c.Models.DefaultVision
	}
	m, ok := c.Models.Definitions[name]
	return m, ok
}

// GetChainTimeout возвращает timeout для указанной цепочки из конфигурации.
// Если цепочка не найдена или timeout не указан, возвращает дефолтный 5 минут.
func (c *AppConfig) GetChainTimeout(chainName string) time.Duration {
	if c.Chains == nil {
		return 5 * time.Minute // дефолт
	}

	chainCfg, exists := c.Chains[chainName]
	if !exists || chainCfg.Timeout == "" {
		return 5 * time.Minute // дефолт
	}

	timeout, err := time.ParseDuration(chainCfg.Timeout)
	if err != nil {
		return 5 * time.Minute // fallback при ошибке парсинга
	}

	return timeout
}

// GetChainMaxIterations возвращает max_iterations для указанной цепочки из конфигурации.
// Если цепочка не найдена или значение не указано, возвращает дефолт 10.
func (c *AppConfig) GetChainMaxIterations(chainName string) int {
	if c.Chains == nil {
		return 10 // дефолт
	}

	chainCfg, exists := c.Chains[chainName]
	if !exists || chainCfg.MaxIterations == 0 {
		return 10 // дефолт
	}

	return chainCfg.MaxIterations
}

```

=================

# debug/recorder.go

```go
// Package debug предоставляет инструменты для записи и анализа выполнения AI-агента.
package debug

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Recorder записывает трейс выполнения агента и сохраняет в JSON файл.
//
// Потокобезопасен — может использоваться из разных горутин.
type Recorder struct {
	mu sync.Mutex

	// config — конфигурация рекордера
	config RecorderConfig

	// log — накапливаемый трейс выполнения
	log DebugLog

	// currentIteration — текущая итерация (заполняется по мере выполнения)
	currentIteration *Iteration

	// visitedTools — множество уникальных инструментов
	visitedTools map[string]struct{}

	// errors — список ошибок выполнения
	errors []string
}

// RecorderConfig конфигурация для создания Recorder.
type RecorderConfig struct {
	// LogsDir — директория для сохранения логов
	LogsDir string

	// IncludeToolArgs — включать аргументы инструментов в лог
	IncludeToolArgs bool

	// IncludeToolResults — включать результаты инструментов в лог
	IncludeToolResults bool

	// MaxResultSize — максимальный размер результата (превышение обрезается)
	// 0 означает без ограничений
	MaxResultSize int
}

// NewRecorder создает новый Recorder с заданной конфигурацией.
//
// Если LogsDir не существует, пытается создать её.
func NewRecorder(cfg RecorderConfig) (*Recorder, error) {
	// Создаём директорию для логов если не существует
	if cfg.LogsDir != "" {
		if err := os.MkdirAll(cfg.LogsDir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create logs directory: %w", err)
		}
	}

	// Генерируем RunID на основе времени
	runID := fmt.Sprintf("debug_%s", time.Now().Format("20060102_150405"))

	return &Recorder{
		config:      cfg,
		log: DebugLog{
			RunID:     runID,
			Timestamp: time.Now(),
		},
		visitedTools: make(map[string]struct{}),
		errors:       make([]string, 0),
	}, nil
}

// Start начинает запись новой сессии с пользовательским запросом.
//
// Очищает предыдущие итерации, позволяя переиспользовать рекордер
// в рамках одной TUI сессии для нескольких запросов.
func (r *Recorder) Start(userQuery string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Генерируем новый RunID для каждой сессии
	r.log.RunID = fmt.Sprintf("debug_%s", time.Now().Format("20060102_150405"))
	r.log.UserQuery = userQuery
	r.log.Timestamp = time.Now()

	// Очищаем предыдущие итерации и состояние
	r.log.Iterations = nil
	r.currentIteration = nil
	r.visitedTools = make(map[string]struct{})
	r.errors = make([]string, 0)
}

// StartIteration начинает запись новой итерации.
func (r *Recorder) StartIteration(num int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.currentIteration = &Iteration{
		Number:   num,
		Duration: 0,
	}
}

// RecordLLMRequest записывает информацию о запросе к LLM.
func (r *Recorder) RecordLLMRequest(req LLMRequest) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.currentIteration != nil {
		r.currentIteration.LLMRequest = req
	}
}

// RecordLLMResponse записывает ответ от LLM.
func (r *Recorder) RecordLLMResponse(resp LLMResponse) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.currentIteration != nil {
		r.currentIteration.LLMResponse = resp

		// Записываем ошибку в общий список если есть
		if resp.Error != "" {
			r.errors = append(r.errors, fmt.Sprintf("LLM error: %s", resp.Error))
		}
	}
}

// RecordToolExecution записывает выполнение инструмента.
func (r *Recorder) RecordToolExecution(exec ToolExecution) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.currentIteration == nil {
		return
	}

	// Применяем конфигурацию включения/обрезки данных
	if !r.config.IncludeToolArgs {
		exec.Args = ""
	}
	if !r.config.IncludeToolResults {
		exec.Result = ""
	} else if r.config.MaxResultSize > 0 && len(exec.Result) > r.config.MaxResultSize {
		exec.Result = exec.Result[:r.config.MaxResultSize] + "... (truncated)"
		exec.ResultTruncated = true
	}

	// Добавляем в текущую итерацию
	r.currentIteration.ToolsExecuted = append(r.currentIteration.ToolsExecuted, exec)

	// Регистрируем уникальный инструмент
	r.visitedTools[exec.Name] = struct{}{}

	// Записываем ошибку если есть
	if !exec.Success && exec.Error != "" {
		r.errors = append(r.errors, fmt.Sprintf("Tool %s: %s", exec.Name, exec.Error))
	}
}

// EndIteration завершает текущую итерацию.
func (r *Recorder) EndIteration() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.currentIteration != nil {
		r.log.Iterations = append(r.log.Iterations, *r.currentIteration)
		r.currentIteration = nil
	}
}

// Finalize завершает запись и сохраняет лог в файл.
//
// Возвращает путь к сохраненному файлу или ошибку.
func (r *Recorder) Finalize(finalResult string, duration time.Duration) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Заполняем финальные данные
	r.log.FinalResult = finalResult
	r.log.Duration = duration.Milliseconds()

	// Формируем summary
	r.buildSummary(duration)

	// Сериализуем в JSON
	data, err := json.MarshalIndent(r.log, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal debug log: %w", err)
	}

	// Определяем путь к файлу
	filePath := r.getFilePath()

	// Сохраняем в файл
	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return "", fmt.Errorf("failed to write debug log: %w", err)
	}

	return filePath, nil
}

// buildSummary формирует агрегированную статистику.
func (r *Recorder) buildSummary(totalDuration time.Duration) {
	summary := Summary{
		Errors:      r.errors,
		VisitedTools: make([]string, 0, len(r.visitedTools)),
	}

	// Собираем уникальные инструменты
	for tool := range r.visitedTools {
		summary.VisitedTools = append(summary.VisitedTools, tool)
	}

	// Анализируем итерации
	for _, iter := range r.log.Iterations {
		summary.TotalLLMCalls++

		// LLM duration
		summary.TotalLLMDuration += iter.LLMResponse.Duration

		// Tool statistics
		for _, tool := range iter.ToolsExecuted {
			summary.TotalToolsExecuted++
			summary.TotalToolDuration += tool.Duration
		}
	}

	r.log.Summary = summary
}

// getFilePath возвращает путь к файлу для сохранения.
func (r *Recorder) getFilePath() string {
	if r.config.LogsDir != "" {
		return filepath.Join(r.config.LogsDir, r.log.RunID+".json")
	}
	return r.log.RunID + ".json"
}

// GetRunID возвращает идентификатор текущей сессии.
func (r *Recorder) GetRunID() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.log.RunID
}

```

=================

# debug/testdata.go

```go
// Package debug предоставляет тестовые данные для unit-тестирования.
//
// Функции GenerateCategoriesJSON и GenerateSubjectsJSON используются
// в CLI утилитах (cmd/debug-test) для демонстрации работы debug logging.
package debug

// GenerateCategoriesJSON возвращает тестовые данные категорий WB.
//
// Используется в cmd/debug-test для симуляции работы инструмента
// get_wb_parent_categories без реального API вызова.
func GenerateCategoriesJSON() string {
	return `[{"id":1541,"name":"Женщинам","isVisible":true},{"id":1535,"name":"Мужчинам","isVisible":true},{"id":1537,"name":"Детям","isVisible":true}]`
}

// GenerateSubjectsJSON возвращает тестовые данные подкатегорий WB.
//
// Используется в cmd/debug-test для симуляции работы инструмента
// get_wb_subjects без реального API вызова.
func GenerateSubjectsJSON() string {
	return `[{"id":685,"name":"Платья","subjectID":1541,"parentID":1541},{"id":1256,"name":"Юбки","subjectID":1541,"parentID":1541},{"id":1271,"name":"Блузки и рубашки","subjectID":1541,"parentID":1541}]`
}

```

=================

# debug/types.go

```go
// Package debug предоставляет инструменты для записи и анализа выполнения AI-агента.
//
// Пакет сохраняет детальные трейсы выполнения в JSON формате для последующего
// анализа, отладки и оптимизации работы агента.
package debug

import "time"

// DebugLog представляет полный трейс выполнения одной операции агента.
//
// Сохраняется в JSON файл и содержит всю информацию о выполнении:
// LLM вызовы, выполнения инструментов, временные метрики, ошибки.
type DebugLog struct {
	// RunID — уникальный идентификатор запуска (используется в имени файла)
	RunID string `json:"run_id"`

	// Timestamp — время начала выполнения
	Timestamp time.Time `json:"timestamp"`

	// UserQuery — исходный запрос пользователя
	UserQuery string `json:"user_query"`

	// Duration — общая длительность выполнения в миллисекундах
	Duration int64 `json:"duration_ms"`

	// Iterations — список итераций ReAct цикла
	Iterations []Iteration `json:"iterations"`

	// Summary — агрегированная статистика выполнения
	Summary Summary `json:"summary"`

	// FinalResult — финальный ответ агента
	FinalResult string `json:"final_result,omitempty"`

	// Error — ошибка если выполнение завершилось неудачно
	Error string `json:"error,omitempty"`
}

// Iteration представляет одну итерацию ReAct цикла.
type Iteration struct {
	// Number — номер итерации (начиная с 1)
	Number int `json:"iteration"`

	// Duration — длительность итерации в миллисекундах
	Duration int64 `json:"duration_ms"`

	// LLMRequest — информация о запросе к LLM
	LLMRequest LLMRequest `json:"llm_request,omitempty"`

	// LLMResponse — ответ от LLM
	LLMResponse LLMResponse `json:"llm_response,omitempty"`

	// ToolsExecuted — список выполненных инструментов на этой итерации
	ToolsExecuted []ToolExecution `json:"tools_executed,omitempty"`

	// IsFinal — true если это финальная итерация (без tool calls)
	IsFinal bool `json:"is_final,omitempty"`
}

// LLMRequest содержит информацию о запросе к LLM.
type LLMRequest struct {
	// Model — использованная модель
	Model string `json:"model"`

	// Temperature — параметр температуры
	Temperature float64 `json:"temperature,omitempty"`

	// MaxTokens — максимальное количество токенов
	MaxTokens int `json:"max_tokens,omitempty"`

	// Format — формат ответа (например, "json_object")
	Format string `json:"format,omitempty"`

	// SystemPromptUsed — какой системный промпт был использован
	SystemPromptUsed string `json:"system_prompt_used,omitempty"`

	// MessagesCount — количество сообщений в запросе
	MessagesCount int `json:"messages_count"`
}

// LLMResponse содержит ответ от LLM.
type LLMResponse struct {
	// Content — текстовый ответ
	Content string `json:"content,omitempty"`

	// ToolCalls — список вызовов инструментов
	ToolCalls []ToolCallInfo `json:"tool_calls,omitempty"`

	// Duration — длительность генерации в миллисекундах
	Duration int64 `json:"duration_ms"`

	// Error — ошибка если произошла
	Error string `json:"error,omitempty"`
}

// ToolCallInfo описывает вызов инструмента от LLM.
type ToolCallInfo struct {
	// ID — уникальный идентификатор вызова
	ID string `json:"id"`

	// Name — имя инструмента
	Name string `json:"name"`

	// Args — аргументы в JSON формате
	Args string `json:"args"`
}

// ToolExecution описывает выполнение одного инструмента.
type ToolExecution struct {
	// Name — имя инструмента
	Name string `json:"name"`

	// Args — аргументы (может быть обрезано по maxSize)
	Args string `json:"args,omitempty"`

	// Result — результат выполнения (может быть обрезан по maxSize)
	Result string `json:"result,omitempty"`

	// ResultTruncated — true если результат был обрезан
	ResultTruncated bool `json:"result_truncated,omitempty"`

	// Duration — длительность выполнения в миллисекундах
	Duration int64 `json:"duration_ms"`

	// Success — true если выполнение прошло успешно
	Success bool `json:"success"`

	// Error — описание ошибки если неуспешно
	Error string `json:"error,omitempty"`

	// PostPromptActivated — какой post-prompt был активирован
	PostPromptActivated string `json:"post_prompt_activated,omitempty"`
}

// Summary содержит агрегированную статистику выполнения.
type Summary struct {
	// TotalLLMCalls — общее количество вызовов LLM
	TotalLLMCalls int `json:"total_llm_calls"`

	// TotalToolsExecuted — общее количество выполненных инструментов
	TotalToolsExecuted int `json:"total_tools_executed"`

	// TotalLLMDuration — общее время всех LLM вызовов в миллисекундах
	TotalLLMDuration int64 `json:"total_llm_duration_ms"`

	// TotalToolDuration — общее время выполнения инструментов в миллисекундах
	TotalToolDuration int64 `json:"total_tool_duration_ms"`

	// Errors — список всех ошибок выполнения
	Errors []string `json:"errors,omitempty"`

	// VisitedTools — список уникальных инструментов, которые были вызваны
	VisitedTools []string `json:"visited_tools,omitempty"`
}

```

=================

# events/chan_emitter.go

```go
package events

import (
	"context"
	"sync"
)

// ChanEmitter — стандартная реализация Emitter через канал.
//
// Thread-safe.
// Используется как дефолтная реализация в pkg/agent.
type ChanEmitter struct {
	mu     sync.RWMutex
	ch     chan Event
	closed bool
}

// NewChanEmitter создаёт новый ChanEmitter с буферизованным каналом.
//
// buffer определяет размер буфера канала.
// Если buffer = 0, канал будет небуферизованным (blocking).
func NewChanEmitter(buffer int) *ChanEmitter {
	return &ChanEmitter{
		ch: make(chan Event, buffer),
	}
}

// Emit отправляет событие в канал.
//
// Thread-safe.
// Rule 11: уважает context.Context.
// Если канал закрыт или context отменён, возвращает ошибку.
func (e *ChanEmitter) Emit(ctx context.Context, event Event) {
	e.mu.RLock()
	if e.closed {
		e.mu.RUnlock()
		return
	}
	e.mu.RUnlock()

	select {
	case e.ch <- event:
		// Успешно отправлено
	case <-ctx.Done():
		// Context отменён
		return
	}
}

// Subscribe возвращает Subscriber для чтения событий.
//
// Thread-safe.
// Можно вызвать несколько раз для создания нескольких подписчиков.
func (e *ChanEmitter) Subscribe() Subscriber {
	return &chanSubscriber{
		ch:   e.ch,
		once: &sync.Once{},
	}
}

// Close закрывает канал и освобождает ресурсы.
//
// Thread-safe.
// После закрытия Emit больше не отправляет события.
func (e *ChanEmitter) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return
	}
	e.closed = true
	close(e.ch)
}

// chanSubscriber реализует Subscriber интерфейс.
type chanSubscriber struct {
	ch   <-chan Event
	once *sync.Once
}

// Events возвращает read-only канал событий.
func (s *chanSubscriber) Events() <-chan Event {
	return s.ch
}

// Close закрывает подписчика (no-op для shared channel).
//
// Реальный канал закрывается только через ChanEmitter.Close().
func (s *chanSubscriber) Close() {
	// Ничего не делаем - канал общий для всех подписчиков
}

// Ensure ChanEmitter implements Emitter
var _ Emitter = (*ChanEmitter)(nil)

// Ensure chanSubscriber implements Subscriber
var _ Subscriber = (*chanSubscriber)(nil)

```

=================

# events/events.go

```go
// Package events предоставляет интерфейсы для реализации Port & Adapter паттерна.
//
// Это Port (интерфейс) для подписки на события от AI агента.
// Позволяет подключать любые UI (TUI, Web, CLI) без изменения библиотечной логики.
//
// # Port & Adapter Pattern
//
//	Port — это интерфейс (Emitter, Subscriber), определённый в библиотеке.
//	Adapter — это реализация интерфейса для конкретного UI (TUI, Web, etc).
//
// # Basic Usage
//
//	// В библиотеке (pkg/agent/):
//	client.SetEmitter(&events.ChanEmitter{Events: make(chan events.Event)})
//
//	// В UI (internal/ui/):
//	sub := client.Subscribe()
//	for event := range sub.Events() {
//	    switch event.Type {
//	    case events.EventThinking:
//	        ui.showSpinner()
//	    case events.EventMessage:
//	        ui.showMessage(event.Data)
//	    }
//	}
//
// # Thread Safety
//
// Все реализации интерфейсов должны быть thread-safe.
//
// # Rule 11: Context Propagation
//
// Emitter.Emit() принимает context.Context для отмены операции.
package events

import (
	"context"
	"time"
)

// EventType представляет тип события от агента.
type EventType string

const (
	// EventThinking отправляется когда агент начинает думать.
	EventThinking EventType = "thinking"

	// EventThinkingChunk отправляется для каждой порции reasoning_content.
	// Используется только в streaming mode с thinking enabled (Zai GLM).
	EventThinkingChunk EventType = "thinking_chunk"

	// EventToolCall отправляется когда агент вызывает инструмент.
	EventToolCall EventType = "tool_call"

	// EventToolResult отправляется когда инструмент вернул результат.
	EventToolResult EventType = "tool_result"

	// EventMessage отправляется когда агент генерирует сообщение.
	EventMessage EventType = "message"

	// EventError отправляется при ошибке.
	EventError EventType = "error"

	// EventDone отправляется когда агент завершил работу.
	EventDone EventType = "done"
)

// EventData — sealed interface для данных события.
//
// Только типы из пакета events могут реализовать этот интерфейс,
// что обеспечивает compile-time type safety.
type EventData interface {
	eventData()
}

// ThinkingData содержит данные для EventThinking.
type ThinkingData struct {
	Query string
}

func (ThinkingData) eventData() {}

// ThinkingChunkData содержит данные для EventThinkingChunk.
//
// Используется для потоковой передачи reasoning_content из thinking mode (Zai GLM).
type ThinkingChunkData struct {
	// Chunk — инкрементальные данные (delta)
	Chunk string

	// Accumulated — накопленные данные (полный reasoning_content на данный момент)
	Accumulated string
}

func (ThinkingChunkData) eventData() {}

// ToolCallData содержит данные о вызове инструмента.
type ToolCallData struct {
	ToolName string
	Args     string
}

func (ToolCallData) eventData() {}

// ToolResultData содержит результат выполнения инструмента.
type ToolResultData struct {
	ToolName string
	Result   string
	Duration time.Duration
}

func (ToolResultData) eventData() {}

// MessageData содержит данные для EventMessage и EventDone.
type MessageData struct {
	Content string
}

func (MessageData) eventData() {}

// ErrorData содержит данные для EventError.
type ErrorData struct {
	Err error
}

func (ErrorData) eventData() {}

// Event представляет событие от агента.
//
// Data содержит типизированные данные события (EventData).
// Для каждого EventType существует соответствующий тип данных:
//   - EventThinking: ThinkingData (запрос пользователя)
//   - EventThinkingChunk: ThinkingChunkData (порция reasoning_content)
//   - EventToolCall: ToolCallData (имя инструмента, аргументы)
//   - EventToolResult: ToolResultData (результат выполнения)
//   - EventMessage: MessageData (ответ агента)
//   - EventError: ErrorData (ошибка)
//   - EventDone: MessageData (финальный ответ)
type Event struct {
	Type      EventType
	Data      EventData
	Timestamp time.Time
}

// Emitter — это Port для отправки событий.
//
// Emitter инвертирует зависимость: библиотека (pkg/agent) зависит
// от этого интерфейса, а не от конкретного UI.
//
// Rule 11: все операции должны уважать context.Context.
type Emitter interface {
	// Emit отправляет событие.
	//
	// Если context отменён, операция должна прерваться.
	// Блокирующая реализация должна возвращать ошибку context.Canceled.
	Emit(ctx context.Context, event Event)
}

// Subscriber позволяет читать события из канала.
//
// Rule 5: thread-safe операции.
type Subscriber interface {
	// Events возвращает read-only канал событий.
	//
	// Канал закрывается при вызове Close().
	Events() <-chan Event

	// Close закрывает канал событий и освобождает ресурсы.
	Close()
}

```

=================

# llm/openai/client.go

```go
// Package openai реализует адаптер LLM провайдера для OpenAI-совместимых API.
//
// Поддерживает Function Calling (tools) для интеграции с агент-системой.
// Соблюдает правило 4 манифеста: работает только через интерфейс llm.Provider.
package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/llm"
	"github.com/ilkoid/poncho-ai/pkg/tools"
	"github.com/ilkoid/poncho-ai/pkg/utils"
	openaisdk "github.com/sashabaranov/go-openai"
)

// Client реализует интерфейс llm.Provider для OpenAI-совместимых API.
//
// Поддерживает:
//   - Базовую генерацию текста
//   - Function Calling (tools)
//   - Vision запросы (изображения)
//   - Runtime переопределение параметров через GenerateOption
//   - Thinking mode для Zai GLM (параметр thinking)
//
// Правило 4 ("Тупой клиент"): Не хранит состояние между вызовами.
// Все параметры передаются при каждом Generate() вызове.
type Client struct {
	api        *openaisdk.Client
	apiKey     string               // API Key для кастомных HTTP запросов с thinking
	baseConfig llm.GenerateOptions // Дефолтные параметры из config.yaml
	thinking   string               // Thinking parameter для Zai GLM: "enabled", "disabled" или ""
	httpClient *http.Client         // Для кастомных запросов с thinking
	baseURL    string               // Base URL для HTTP запросов
}

// NewClient создает OpenAI клиент на основе конфигурации модели.
//
// Принимает ModelDef напрямую для упрощения создания клиентов через factory.
// Использует APIKey из конфигурации для аутентификации.
//
// Правило 2: Все настройки из конфигурации, никакого хардкода.
func NewClient(modelDef config.ModelDef) *Client {
	// Поддержка custom BaseURL для non-OpenAI провайдеров (Zai, DeepSeek и т.д.)
	cfg := openaisdk.DefaultConfig(modelDef.APIKey)
	baseURL := modelDef.BaseURL
	if baseURL != "" {
		cfg.BaseURL = baseURL
	}

	// Rule 12: Нет hardcoded значений - base_url обязателен в конфиге
	if baseURL == "" {
		return nil // Возвращаем nil, так как без baseURL клиент не работает
	}

	client := openaisdk.NewClientWithConfig(cfg)

	// Заполняем baseConfig из ModelDef для использования как дефолты
	baseConfig := llm.GenerateOptions{
		Model:       modelDef.ModelName,
		Temperature: modelDef.Temperature,
		MaxTokens:   modelDef.MaxTokens,
		Format:      "",
	}

	// Устанавливаем parallel_tool_calls из конфигурации модели
	if modelDef.ParallelToolCalls != nil {
		baseConfig.ParallelToolCalls = modelDef.ParallelToolCalls
	}

	// Timeout из конфигурации модели с fallback на дефолт
	httpTimeout := modelDef.Timeout
	if httpTimeout == 0 {
		httpTimeout = 120 * time.Second // дефолтный fallback
	}

	// Custom transport для решения timeout проблем в WSL2
	transport := &http.Transport{
		// Disable HTTP/2 для стабильности (может вызывать timeout в WSL2)
		ForceAttemptHTTP2: false,
		// Increase connection timeouts
		TLSHandshakeTimeout:   30 * time.Second,
		ResponseHeaderTimeout: httpTimeout,
		// Keep-alive settings
		MaxIdleConns:        100,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  false,
	}

	return &Client{
		api:        client,
		apiKey:     modelDef.APIKey,
		baseConfig: baseConfig,
		thinking:   modelDef.Thinking,
		baseURL:    baseURL,
		httpClient: &http.Client{
			Timeout:   httpTimeout,
			Transport: transport,
		},
	}
}

// Generate выполняет запрос к API и возвращает ответ модели.
//
// Поддерживает:
//   - Function Calling (tools): []tools.ToolDefinition
//   - Runtime переопределение параметров: GenerateOption
//   - Thinking mode для Zai GLM
//
// Алгоритм:
//   1. Разделяет opts на tools и GenerateOptions
//   2. Применяет GenerateOptions к baseConfig (runtime override)
//   3. Конвертирует сообщения в формат OpenAI SDK
//   4. Если thinking включен — использует кастомный HTTP запрос
//   5. Иначе — использует стандартный go-openai клиент
//   6. Конвертирует ответ обратно в наш формат
//
// Правило 7: Все ошибки возвращаются, никаких panic.
// Правило 4: Работает только через интерфейс llm.Provider.
func (c *Client) Generate(ctx context.Context, messages []llm.Message, opts ...any) (llm.Message, error) {
	startTime := time.Now()

	// 1. Начинаем с дефолтных значений из config.yaml
	options := c.baseConfig

	// 2. Разделяем opts на tools и GenerateOption
	var toolDefs []tools.ToolDefinition
	for _, opt := range opts {
		switch v := opt.(type) {
		case []tools.ToolDefinition:
			// Tools для Function Calling
			toolDefs = v
		case llm.GenerateOption:
			// Runtime переопределение параметров модели
			v(&options)
		default:
			return llm.Message{}, fmt.Errorf("invalid option type: expected []tools.ToolDefinition or llm.GenerateOption, got %T", opt)
		}
	}

	utils.Debug("LLM request started",
		"model", options.Model,
		"thinking", c.thinking,
		"temperature", options.Temperature,
		"max_tokens", options.MaxTokens,
		"messages_count", len(messages),
		"tools_count", len(toolDefs))

	// Debug: логируем структуру сообщений
	for i, msg := range messages {
		imagesCount := len(msg.Images)
		contentPreview := msg.Content
		if len(contentPreview) > 100 {
			contentPreview = contentPreview[:100] + "..."
		}
		utils.Debug("LLM message",
			"index", i,
			"role", msg.Role,
			"content_preview", contentPreview,
			"images_count", imagesCount)
	}

	// Debug: логируем инструменты
	for i, tool := range toolDefs {
		utils.Debug("LLM tool",
			"index", i,
			"name", tool.Name,
			"description", tool.Description)
	}

	// 3. Если thinking включен - используем кастомный HTTP запрос
	if c.thinking != "" && c.thinking != "disabled" {
		return c.generateWithThinking(ctx, messages, options, toolDefs, startTime)
	}

	// 4. Иначе - стандартный путь через go-openai SDK
	return c.generateStandard(ctx, messages, options, toolDefs, startTime)
}

// generateStandard выполняет запрос через кастомный HTTP для поддержки parallel_tool_calls.
//
// ПРИМЕЧАНИЕ: Используем map[string]interface{} вместо openaisdk.ChatCompletionRequest,
// чтобы поддерживать параметр parallel_tool_calls который может отсутствовать в SDK.
func (c *Client) generateStandard(ctx context.Context, messages []llm.Message, options llm.GenerateOptions, toolDefs []tools.ToolDefinition, startTime time.Time) (llm.Message, error) {
	// Конвертируем сообщения
	openaiMsgs := make([]openaisdk.ChatCompletionMessage, len(messages))
	for i, m := range messages {
		openaiMsgs[i] = mapToOpenAI(m)
	}

	// Строим запрос body
	reqBody := map[string]interface{}{
		"model":       options.Model,
		"temperature": options.Temperature,
		"max_tokens":  options.MaxTokens,
		"messages":    openaiMsgs,
	}

	// ResponseFormat если указан
	if options.Format == "json_object" {
		reqBody["response_format"] = map[string]string{
			"type": "json_object",
		}
	}

	// Добавляем tools если переданы
	if len(toolDefs) > 0 {
		reqBody["tools"] = convertToolsToOpenAI(toolDefs)
		reqBody["tool_choice"] = "auto"

		// Устанавливаем parallel_tool_calls если указан
		if options.ParallelToolCalls != nil {
			reqBody["parallel_tool_calls"] = *options.ParallelToolCalls
		}
	}

	// Marshal в JSON
	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return llm.Message{}, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Создаём HTTP запрос
	url := c.baseURL + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jsonBody))
	if err != nil {
		return llm.Message{}, fmt.Errorf("failed to create request: %w", err)
	}

	// Устанавливаем заголовки
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	utils.Debug("Sending standard HTTP request",
		"url", url,
		"body_size", len(jsonBody),
		"parallel_tool_calls", fmt.Sprintf("%v", options.ParallelToolCalls))

	// Выполняем запрос
	resp, err := c.httpClient.Do(req)
	if err != nil {
		utils.Error("HTTP request failed",
			"error", err,
			"url", url,
			"duration_ms", time.Since(startTime).Milliseconds())
		return llm.Message{}, fmt.Errorf("http request failed: %w", err)
	}
	defer resp.Body.Close()

	// Читаем тело ответа
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return llm.Message{}, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		utils.Error("HTTP request returned error",
			"status", resp.StatusCode,
			"body", string(body),
			"duration_ms", time.Since(startTime).Milliseconds())
		return llm.Message{}, fmt.Errorf("api returned status %d: %s", resp.StatusCode, string(body))
	}

	// Парсим ответ
	var apiResp struct {
		Choices []struct {
			Message struct {
				Role      string             `json:"role"`
				Content   string             `json:"content"`
				ToolCalls []openaisdk.ToolCall `json:"tool_calls,omitempty"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.Unmarshal(body, &apiResp); err != nil {
		return llm.Message{}, fmt.Errorf("failed to parse response: %w", err)
	}

	if len(apiResp.Choices) == 0 {
		return llm.Message{}, fmt.Errorf("no choices in response")
	}

	choice := apiResp.Choices[0]

	// Конвертируем tool calls
	var toolCalls []llm.ToolCall
	if len(choice.Message.ToolCalls) > 0 {
		toolCalls = make([]llm.ToolCall, len(choice.Message.ToolCalls))
		for i, tc := range choice.Message.ToolCalls {
			toolCalls[i] = llm.ToolCall{
				ID:   tc.ID,
				Name: tc.Function.Name,
				Args: tc.Function.Arguments,
			}
		}
	}

	return llm.Message{
		Role:      llm.RoleAssistant,
		Content:   choice.Message.Content,
		ToolCalls: toolCalls,
	}, nil
}

// generateWithThinking выполняет запрос с thinking параметром через кастомный HTTP.
func (c *Client) generateWithThinking(ctx context.Context, messages []llm.Message, options llm.GenerateOptions, toolDefs []tools.ToolDefinition, startTime time.Time) (llm.Message, error) {
	// Конвертируем сообщения
	openaiMsgs := make([]openaisdk.ChatCompletionMessage, len(messages))
	for i, m := range messages {
		openaiMsgs[i] = mapToOpenAI(m)
	}

	// Строим запрос body
	reqBody := map[string]interface{}{
		"model":       options.Model,
		"temperature": options.Temperature,
		"max_tokens":  options.MaxTokens,
		"messages":    openaiMsgs,
		"thinking": map[string]string{
			"type": c.thinking,
		},
	}

	// ResponseFormat если указан
	if options.Format == "json_object" {
		reqBody["response_format"] = map[string]string{
			"type": "json_object",
		}
	}

	// Добавляем tools если переданы
	if len(toolDefs) > 0 {
		reqBody["tools"] = convertToolsToOpenAI(toolDefs)
		reqBody["tool_choice"] = "auto"

		// Устанавливаем parallel_tool_calls если указан
		if options.ParallelToolCalls != nil {
			reqBody["parallel_tool_calls"] = *options.ParallelToolCalls
		}
	}

	// Marshal в JSON
	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return llm.Message{}, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Создаём HTTP запрос
	url := c.baseURL + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jsonBody))
	if err != nil {
		return llm.Message{}, fmt.Errorf("failed to create request: %w", err)
	}

	// Устанавливаем заголовки
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	utils.Debug("Sending custom HTTP request with thinking",
		"url", url,
		"thinking", c.thinking,
		"body_size", len(jsonBody))

	// Выполняем запрос
	resp, err := c.httpClient.Do(req)
	if err != nil {
		utils.Error("HTTP request failed",
			"error", err,
			"url", url,
			"duration_ms", time.Since(startTime).Milliseconds())
		return llm.Message{}, fmt.Errorf("http request failed: %w", err)
	}
	defer resp.Body.Close()

	// Читаем тело ответа
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return llm.Message{}, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		utils.Error("HTTP request returned error",
			"status", resp.StatusCode,
			"body", string(body),
			"duration_ms", time.Since(startTime).Milliseconds())
		return llm.Message{}, fmt.Errorf("api returned status %d: %s", resp.StatusCode, string(body))
	}

	// Парсим ответ
	var apiResp struct {
		Choices []struct {
			Message struct {
				Role            string                   `json:"role"`
				Content         string                   `json:"content"`
				ToolCalls       []openaisdk.ToolCall     `json:"tool_calls,omitempty"`
				ReasoningContent string                   `json:"reasoning_content,omitempty"` // Thinking контент
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.Unmarshal(body, &apiResp); err != nil {
		return llm.Message{}, fmt.Errorf("failed to parse response: %w", err)
	}

	if len(apiResp.Choices) == 0 {
		return llm.Message{}, fmt.Errorf("no choices in response")
	}

	choice := apiResp.Choices[0].Message
	result := llm.Message{
		Role:    llm.Role(choice.Role),
		Content: choice.Content,
	}

	// Если есть reasoning_content от thinking mode, добавляем к контенту
	if choice.ReasoningContent != "" {
		// Избегаем дублирования: если content уже содержится в reasoning_content, не добавляем
		if choice.Content == "" || strings.Contains(choice.ReasoningContent, choice.Content) {
			result.Content = choice.ReasoningContent
		} else if strings.Contains(choice.Content, choice.ReasoningContent) {
			// content уже содержит reasoning_content
			result.Content = choice.Content
		} else {
			// Объединяем без дубликатов
			result.Content = choice.ReasoningContent + "\n\n" + choice.Content
		}
		utils.Debug("Thinking content extracted",
			"reasoning_length", len(choice.ReasoningContent),
			"content_length", len(choice.Content),
			"result_length", len(result.Content))
	}

	// Извлекаем ToolCalls
	if len(choice.ToolCalls) > 0 {
		result.ToolCalls = make([]llm.ToolCall, len(choice.ToolCalls))
		for i, tc := range choice.ToolCalls {
			result.ToolCalls[i] = llm.ToolCall{
				ID:   tc.ID,
				Name: tc.Function.Name,
				Args: tc.Function.Arguments,
			}
		}
	}

	utils.Info("LLM response received (with thinking)",
		"model", options.Model,
		"tool_calls_count", len(result.ToolCalls),
		"content_length", len(result.Content),
		"duration_ms", time.Since(startTime).Milliseconds())

	return result, nil
}

// parseResponse парсит ответ от go-openai SDK.
func (c *Client) parseResponse(resp openaisdk.ChatCompletionResponse, startTime time.Time, modelName string) (llm.Message, error) {
	// Проверяем что есть хотя бы один выбор
	if len(resp.Choices) == 0 {
		return llm.Message{}, fmt.Errorf("no choices in response")
	}

	// Маппим ответ обратно в наш формат
	choice := resp.Choices[0].Message

	result := llm.Message{
		Role:    llm.Role(choice.Role),
		Content: choice.Content,
	}

	// Извлекаем ToolCalls если модель решила вызвать функции
	if len(choice.ToolCalls) > 0 {
		result.ToolCalls = make([]llm.ToolCall, len(choice.ToolCalls))
		for i, tc := range choice.ToolCalls {
			result.ToolCalls[i] = llm.ToolCall{
				ID:   tc.ID,
				Name: tc.Function.Name,
				Args: tc.Function.Arguments,
			}
		}
	}

	// Debug: логируем tool calls в ответе
	for i, tc := range result.ToolCalls {
		argsPreview := tc.Args
		if len(argsPreview) > 200 {
			argsPreview = argsPreview[:200] + "..."
		}
		utils.Debug("LLM tool call",
			"index", i,
			"id", tc.ID,
			"name", tc.Name,
			"args_preview", argsPreview)
	}

	utils.Info("LLM response received",
		"model", modelName,
		"tool_calls_count", len(result.ToolCalls),
		"content_length", len(result.Content),
		"duration_ms", time.Since(startTime).Milliseconds())

	return result, nil
}

// mapToOpenAI конвертирует наше внутреннее сообщение в формат SDK.
// Здесь происходит магия Vision: если есть картинки, создаем MultiContent.
func mapToOpenAI(m llm.Message) openaisdk.ChatCompletionMessage {
	msg := openaisdk.ChatCompletionMessage{
		Role: string(m.Role),
	}

	// Если картинок нет, отправляем просто текст
	if len(m.Images) == 0 {
		msg.Content = m.Content
		return msg
	}

	// Если есть картинки (Vision запрос)
	parts := []openaisdk.ChatMessagePart{
		{
			Type: openaisdk.ChatMessagePartTypeText,
			Text: m.Content,
		},
	}

	for _, imgURL := range m.Images {
		parts = append(parts, openaisdk.ChatMessagePart{
			Type: openaisdk.ChatMessagePartTypeImageURL,
			ImageURL: &openaisdk.ChatMessageImageURL{
				URL:    imgURL, // Ожидается base64 data-uri или http ссылка
				Detail: openaisdk.ImageURLDetailAuto,
			},
		})
	}

	msg.MultiContent = parts
	return msg
}

// convertToolsToOpenAI конвертирует определения инструментов во внутреннем формате
// в формат OpenAI Function Calling.
//
// Соответствие структур:
//   tools.ToolDefinition → openaisdk.Tool (type=function)
//   Parameters (interface{}) → openaisdk.FunctionDefinition.Parameters
//
// Поскольку ToolDefinition.Parameters уже является JSON Schema объектом
// (map[string]interface{}), он напрямую передаётся в OpenAI SDK.
func convertToolsToOpenAI(defs []tools.ToolDefinition) []openaisdk.Tool {
	result := make([]openaisdk.Tool, len(defs))

	for i, def := range defs {
		result[i] = openaisdk.Tool{
			Type: "function",
			Function: &openaisdk.FunctionDefinition{
				Name:        def.Name,
				Description: def.Description,
				Parameters:  def.Parameters,
			},
		}
	}

	return result
}

// GenerateStream выполняет запрос к API с потоковой передачей ответа.
//
// Реализует llm.StreamingProvider интерфейс. Отправляет чанки через callback,
// накапливает финальное сообщение и возвращает его после завершения стриминга.
//
// Rule 11: Уважает context.Context, прерывает стриминг при отмене.
func (c *Client) GenerateStream(
	ctx context.Context,
	messages []llm.Message,
	callback func(llm.StreamChunk),
	opts ...any,
) (llm.Message, error) {
	startTime := time.Now()

	// 1. Начинаем с дефолтных значений из config.yaml
	options := c.baseConfig

	// 2. Разделяем opts на tools, GenerateOption и StreamOption
	var toolDefs []tools.ToolDefinition
	streamEnabled := true // Default: true (opt-out)
	thinkingOnly := true // Default: true

	for _, opt := range opts {
		switch v := opt.(type) {
		case []tools.ToolDefinition:
			toolDefs = v
		case llm.GenerateOption:
			v(&options)
		case llm.StreamOption:
			var so llm.StreamOptions
			v(&so)
			streamEnabled = so.Enabled
			thinkingOnly = so.ThinkingOnly
		default:
			return llm.Message{}, fmt.Errorf("invalid option type: expected []tools.ToolDefinition, llm.GenerateOption or llm.StreamOption, got %T", opt)
		}
	}

	// 3. Если стриминг выключен, fallback на обычный Generate
	if !streamEnabled {
		return c.Generate(ctx, messages, opts...)
	}

	utils.Debug("LLM streaming request started",
		"model", options.Model,
		"thinking", c.thinking,
		"thinking_only", thinkingOnly,
		"temperature", options.Temperature,
		"max_tokens", options.MaxTokens,
		"messages_count", len(messages),
		"tools_count", len(toolDefs))

	// 4. Если thinking включен - используем streaming с thinking
	if c.thinking != "" && c.thinking != "disabled" {
		return c.generateWithThinkingStream(ctx, messages, options, toolDefs, callback, thinkingOnly, startTime)
	}

	// 5. Иначе - стандартный streaming путь
	return c.generateStandardStream(ctx, messages, options, toolDefs, callback, thinkingOnly, startTime)
}

// generateWithThinkingStream выполняет streaming запрос с thinking параметром.
func (c *Client) generateWithThinkingStream(
	ctx context.Context,
	messages []llm.Message,
	options llm.GenerateOptions,
	toolDefs []tools.ToolDefinition,
	callback func(llm.StreamChunk),
	thinkingOnly bool,
	startTime time.Time,
) (llm.Message, error) {
	// Конвертируем сообщения
	openaiMsgs := make([]openaisdk.ChatCompletionMessage, len(messages))
	for i, m := range messages {
		openaiMsgs[i] = mapToOpenAI(m)
	}

	// Строим запрос body
	reqBody := map[string]interface{}{
		"model":       options.Model,
		"temperature": options.Temperature,
		"max_tokens":  options.MaxTokens,
		"messages":    openaiMsgs,
		"stream":      true, // Включаем стриминг
		"thinking": map[string]string{
			"type": c.thinking,
		},
	}

	// ResponseFormat если указан
	if options.Format == "json_object" {
		reqBody["response_format"] = map[string]string{
			"type": "json_object",
		}
	}

	// Добавляем tools если переданы
	if len(toolDefs) > 0 {
		reqBody["tools"] = convertToolsToOpenAI(toolDefs)
		reqBody["tool_choice"] = "auto"

		if options.ParallelToolCalls != nil {
			reqBody["parallel_tool_calls"] = *options.ParallelToolCalls
		}
	}

	// Marshal в JSON
	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return llm.Message{}, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Создаём HTTP запрос
	url := c.baseURL + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jsonBody))
	if err != nil {
		return llm.Message{}, fmt.Errorf("failed to create request: %w", err)
	}

	// Устанавливаем заголовки
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	utils.Debug("Sending streaming HTTP request with thinking",
		"url", url,
		"thinking", c.thinking,
		"body_size", len(jsonBody))

	// Выполняем запрос
	resp, err := c.httpClient.Do(req)
	if err != nil {
		utils.Error("HTTP request failed",
			"error", err,
			"url", url,
			"duration_ms", time.Since(startTime).Milliseconds())
		return llm.Message{}, fmt.Errorf("http request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		utils.Error("HTTP request returned error",
			"status", resp.StatusCode,
			"body", string(body),
			"duration_ms", time.Since(startTime).Milliseconds())
		return llm.Message{}, fmt.Errorf("api returned status %d: %s", resp.StatusCode, string(body))
	}

	// Парсим SSE ответ
	return c.parseSSEStream(ctx, resp.Body, callback, thinkingOnly, startTime)
}

// generateStandardStream выполняет стандартный streaming запрос (без thinking).
func (c *Client) generateStandardStream(
	ctx context.Context,
	messages []llm.Message,
	options llm.GenerateOptions,
	toolDefs []tools.ToolDefinition,
	callback func(llm.StreamChunk),
	thinkingOnly bool,
	startTime time.Time,
) (llm.Message, error) {
	// Конвертируем сообщения
	openaiMsgs := make([]openaisdk.ChatCompletionMessage, len(messages))
	for i, m := range messages {
		openaiMsgs[i] = mapToOpenAI(m)
	}

	// Строим запрос body
	reqBody := map[string]interface{}{
		"model":       options.Model,
		"temperature": options.Temperature,
		"max_tokens":  options.MaxTokens,
		"messages":    openaiMsgs,
		"stream":      true, // Включаем стриминг
	}

	// ResponseFormat если указан
	if options.Format == "json_object" {
		reqBody["response_format"] = map[string]string{
			"type": "json_object",
		}
	}

	// Добавляем tools если переданы
	if len(toolDefs) > 0 {
		reqBody["tools"] = convertToolsToOpenAI(toolDefs)
		reqBody["tool_choice"] = "auto"

		if options.ParallelToolCalls != nil {
			reqBody["parallel_tool_calls"] = *options.ParallelToolCalls
		}
	}

	// Marshal в JSON
	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return llm.Message{}, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Создаём HTTP запрос
	url := c.baseURL + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jsonBody))
	if err != nil {
		return llm.Message{}, fmt.Errorf("failed to create request: %w", err)
	}

	// Устанавливаем заголовки
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	utils.Debug("Sending standard streaming HTTP request",
		"url", url,
		"body_size", len(jsonBody))

	// Выполняем запрос
	resp, err := c.httpClient.Do(req)
	if err != nil {
		utils.Error("HTTP request failed",
			"error", err,
			"url", url,
			"duration_ms", time.Since(startTime).Milliseconds())
		return llm.Message{}, fmt.Errorf("http request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		utils.Error("HTTP request returned error",
			"status", resp.StatusCode,
			"body", string(body),
			"duration_ms", time.Since(startTime).Milliseconds())
		return llm.Message{}, fmt.Errorf("api returned status %d: %s", resp.StatusCode, string(body))
	}

	// Парсим SSE ответ
	return c.parseSSEStream(ctx, resp.Body, callback, thinkingOnly, startTime)
}

// parseSSEStream парсит Server-Sent Events ответ от API.
//
// SSE формат: "data: {json}\n\n"
// Каждая строка содержит JSON объект с чанком ответа.
func (c *Client) parseSSEStream(
	ctx context.Context,
	body io.Reader,
	callback func(llm.StreamChunk),
	thinkingOnly bool,
	startTime time.Time,
) (llm.Message, error) {
	scanner := bufio.NewScanner(body)

	// Накопленные данные
	var accumulatedContent string
	var accumulatedReasoning string

	// ID последнего tool call для накопления аргументов
	pendingToolCalls := make(map[string]*llm.ToolCall)

	for scanner.Scan() {
		// Rule 11: Проверяем context cancellation
		select {
		case <-ctx.Done():
			return llm.Message{}, fmt.Errorf("streaming cancelled: %w", ctx.Err())
		default:
		}

		line := scanner.Text()

		// SSE формат: строки начинаются с "data: "
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		// Убираем префикс "data: "
		jsonStr := strings.TrimPrefix(line, "data: ")

		// Пустые данные или [DONE] маркер
		if jsonStr == "" || jsonStr == "[DONE]" {
			continue
		}

		// Парсим JSON
		var chunkData struct {
			Choices []struct {
				Delta struct {
					Role            string `json:"role"`
					Content         string `json:"content"`
					ReasoningContent string `json:"reasoning_content,omitempty"`
					ToolCallID      string `json:"tool_call_id,omitempty"`
					// Tool calls в streaming приходят через delta
					ToolCalls []struct {
						Index    int `json:"index"`
						ID       string `json:"id,omitempty"`
						Type     string `json:"type,omitempty"`
						Function struct {
							Name      string `json:"name,omitempty"`
							Arguments string `json:"arguments,omitempty"`
						} `json:"function,omitempty"`
					} `json:"tool_calls,omitempty"`
				} `json:"delta"`
				Message struct {
					Role            string                   `json:"role,omitempty"`
					Content         string                   `json:"content,omitempty"`
					ReasoningContent string                   `json:"reasoning_content,omitempty"`
					ToolCalls       []openaisdk.ToolCall     `json:"tool_calls,omitempty"`
				} `json:"message,omitempty"`
				FinishReason string `json:"finish_reason,omitempty"`
			} `json:"choices"`
		}

		if err := json.Unmarshal([]byte(jsonStr), &chunkData); err != nil {
			utils.Debug("Failed to parse SSE chunk", "error", err, "json", jsonStr)
			continue
		}

		if len(chunkData.Choices) == 0 {
			continue
		}

		choice := chunkData.Choices[0]

		// Обрабатываем reasoning_content из thinking mode
		if choice.Delta.ReasoningContent != "" {
			delta := choice.Delta.ReasoningContent
			accumulatedReasoning += delta

			// ВСЕГДА отправляем reasoning chunks для streaming
			// (включая thinkingOnly режим - именно ради него мы стримим!)
			if callback != nil {
				callback(llm.StreamChunk{
					Type:             llm.ChunkThinking,
					Content:          accumulatedContent,
					ReasoningContent: accumulatedReasoning,
					Delta:            delta,
				})
			}
		}

		// Обрабатываем обычный контент
		if choice.Delta.Content != "" {
			delta := choice.Delta.Content
			accumulatedContent += delta

			// Отправляем событие только если НЕ thinkingOnly
			if callback != nil && !thinkingOnly {
				callback(llm.StreamChunk{
					Type:             llm.ChunkContent,
					Content:          accumulatedContent,
					ReasoningContent: accumulatedReasoning,
					Delta:            delta,
				})
			}
		}

		// Обрабатываем tool calls из delta (streaming mode!)
		// В streaming режиме tool calls приходят через delta, а не через message
		if len(choice.Delta.ToolCalls) > 0 {
			for _, deltaTC := range choice.Delta.ToolCalls {
				// Используем index как ключ (может быть 0, 1, 2...)
				indexKey := fmt.Sprintf("delta_%d", deltaTC.Index)

				if _, exists := pendingToolCalls[indexKey]; !exists {
					// Новый tool call
					pendingToolCalls[indexKey] = &llm.ToolCall{
						ID:   deltaTC.ID,
						Name: deltaTC.Function.Name,
						Args: deltaTC.Function.Arguments,
					}
					utils.Debug("New tool call in delta", "index", deltaTC.Index, "id", deltaTC.ID, "name", deltaTC.Function.Name)
				} else {
					// Обновляем существующий tool call (ID может прийти позже, накапливаем arguments)
					if deltaTC.ID != "" {
						pendingToolCalls[indexKey].ID = deltaTC.ID
					}
					if deltaTC.Function.Name != "" {
						pendingToolCalls[indexKey].Name = deltaTC.Function.Name
					}
					if deltaTC.Function.Arguments != "" {
						pendingToolCalls[indexKey].Args += deltaTC.Function.Arguments
					}
					utils.Debug("Accumulated tool call in delta", "index", deltaTC.Index, "args_len", len(deltaTC.Function.Arguments))
				}
			}
		}

		// Обрабатываем tool calls из message (финальные данные)
		if len(choice.Message.ToolCalls) > 0 {
			for _, tc := range choice.Message.ToolCalls {
				if _, exists := pendingToolCalls[tc.ID]; !exists {
					pendingToolCalls[tc.ID] = &llm.ToolCall{
						ID:   tc.ID,
						Name: tc.Function.Name,
						Args: tc.Function.Arguments,
					}
				} else {
					// Накапливаем аргументы
					pendingToolCalls[tc.ID].Args += tc.Function.Arguments
				}
			}
		}

		// Проверяем завершение стриминга
		if choice.FinishReason != "" {
			utils.Debug("Streaming finished",
				"reason", choice.FinishReason,
				"content_length", len(accumulatedContent),
				"reasoning_length", len(accumulatedReasoning),
				"tool_calls_count", len(pendingToolCalls))
		}
	}

	// Проверяем ошибки сканера
	if err := scanner.Err(); err != nil {
		utils.Error("Scanner error during streaming", "error", err)
		return llm.Message{}, fmt.Errorf("scanner error: %w", err)
	}

	// Собираем финальное сообщение
	result := llm.Message{
		Role:    llm.RoleAssistant,
		Content: accumulatedContent,
	}

	// Если есть reasoning_content, добавляем к контенту (как в generateWithThinking)
	if accumulatedReasoning != "" {
		if accumulatedContent == "" || strings.Contains(accumulatedReasoning, accumulatedContent) {
			result.Content = accumulatedReasoning
		} else if strings.Contains(accumulatedContent, accumulatedReasoning) {
			result.Content = accumulatedContent
		} else {
			result.Content = accumulatedReasoning + "\n\n" + accumulatedContent
		}
	}

	// Собираем tool calls
	if len(pendingToolCalls) > 0 {
		result.ToolCalls = make([]llm.ToolCall, 0, len(pendingToolCalls))
		for _, tc := range pendingToolCalls {
			result.ToolCalls = append(result.ToolCalls, *tc)
		}
	}

	// Отправляем финальный chunk
	if callback != nil {
		callback(llm.StreamChunk{
			Type:             llm.ChunkDone,
			Content:          result.Content,
			ReasoningContent: accumulatedReasoning,
			Done:             true,
		})
	}

	utils.Info("LLM streaming response received",
		"model", c.baseConfig.Model,
		"tool_calls_count", len(result.ToolCalls),
		"content_length", len(result.Content),
		"reasoning_length", len(accumulatedReasoning),
		"duration_ms", time.Since(startTime).Milliseconds())

	return result, nil
}

```

=================

# llm/options.go

```go
// Package llm provides options pattern for LLM generation parameters.
//
// This package implements functional options for runtime parameter overrides
// while maintaining backward compatibility with existing code.
package llm

// GenerateOptions holds parameters for LLM generation.
// These options can be set at initialization (from config.yaml) and
// overridden at runtime (from prompts or direct calls).
type GenerateOptions struct {
	// Model is the model identifier (e.g., "glm-4.6", "glm-4.6v-flash")
	Model string

	// Temperature controls randomness in responses (0.0 = deterministic, 1.0 = random)
	Temperature float64

	// MaxTokens limits the response length
	MaxTokens int

	// Format specifies response format (e.g., "json_object" for structured output)
	Format string

	// ParallelToolCalls controls whether LLM can call multiple tools at once.
	// nil = use model default from config.yaml
	// NOTE: Currently configured via model definition, not runtime options.
	ParallelToolCalls *bool
}

// GenerateOption is a functional option for configuring GenerateOptions.
type GenerateOption func(*GenerateOptions)

// WithModel sets the model for generation.
// Runtime override: takes precedence over config.yaml default.
func WithModel(model string) GenerateOption {
	return func(o *GenerateOptions) {
		o.Model = model
	}
}

// WithTemperature sets the temperature for generation.
// Runtime override: takes precedence over config.yaml default.
func WithTemperature(temp float64) GenerateOption {
	return func(o *GenerateOptions) {
		o.Temperature = temp
	}
}

// WithMaxTokens sets the maximum tokens for generation.
// Runtime override: takes precedence over config.yaml default.
func WithMaxTokens(tokens int) GenerateOption {
	return func(o *GenerateOptions) {
		o.MaxTokens = tokens
	}
}

// WithFormat sets the response format for generation.
// Use "json_object" for structured JSON output.
// Runtime override: takes precedence over config.yaml default.
func WithFormat(format string) GenerateOption {
	return func(o *GenerateOptions) {
		o.Format = format
	}
}

```

=================

# llm/provider.go

```go
// Интерфейс Провайдера через который работает всё приложение.

package llm

import "context"

// Пакет llm/provider.go определяет интерфейс, который должны реализовать
// все адаптеры (OpenAI, Anthropic, Ollama и т.д.).

// Provider — абстракция над LLM API.
type Provider interface {
	// Generate принимает контекст, историю сообщений и опциональные параметры.
	// Возвращает ответ модели в унифицированном формате Message.
	//
	// Параметры (variadic):
	//   - tools: []ToolDefinition или []map[string]interface{} - для Function Calling
	//   - opts: GenerateOption - для runtime переопределения параметров модели
	//
	// Примеры:
	//   Generate(ctx, messages, toolDefs)                                    // только tools
	//   Generate(ctx, messages, WithModel("glm-4.6"), WithTemperature(0.7)) // только opts
	//   Generate(ctx, messages, toolDefs, WithTemperature(0.5))             // tools + opts
	Generate(ctx context.Context, messages []Message, opts ...any) (Message, error)
}

```

=================

# llm/streaming.go

```go
// Package llm предоставляет типы и интерфейсы для работы с LLM провайдерами.
//
// Этот файл определяет абстракции для потоковой передачи (streaming) ответов от LLM.
package llm

import "context"

// StreamingProvider — интерфейс для LLM провайдеров с поддержкой стриминга.
//
// Отдельный интерфейс от Provider для обратной совместимости.
// Провайдеры могут реализовать оба интерфейса или только Provider.
//
// # Rule 4: LLM Abstraction
//
// Работаем через интерфейс, конкретные реализации (OpenAI, Anthropic и т.д.)
// скрыты за этим абстракцией.
//
// # Rule 11: Context Propagation
//
// Все методы уважают context.Context и прерывают операцию при отмене.
type StreamingProvider interface {
	// Provider — базовый интерфейс для синхронной генерации.
	// StreamingProvider расширяет Provider, сохраняя обратную совместимость.
	Provider

	// GenerateStream выполняет запрос к API с потоковой передачей ответа.
	//
	// Параметры:
	//   - ctx: контекст для отмены операции (Rule 11)
	//   - messages: история сообщений
	//   - callback: функция для обработки каждого чанка
	//   - opts: опциональные параметры (tools, GenerateOption, StreamOption)
	//
	// Возвращает финальное сообщение после завершения стриминга.
	//
	// Callback вызывается для каждой порции данных:
	//   - ChunkThinking: reasoning_content из thinking mode
	//   - ChunkContent: обычный контент ответа
	//   - ChunkError: ошибка стриминга
	//   - ChunkDone: завершение стриминга
	//
	// # Thread Safety
	//
	// Callback может вызываться из разных goroutine, должен быть thread-safe.
	GenerateStream(
		ctx context.Context,
		messages []Message,
		callback func(StreamChunk),
		opts ...any,
	) (Message, error)
}

// StreamChunk представляет одну порцию данных из потокового ответа.
//
// Структура оптимизирована для отправки через events.Event и содержит
// как инкрементальные изменения (Delta), так и накопленное состояние (Content).
type StreamChunk struct {
	// Type определяет тип чанка
	Type ChunkType

	// Content содержит накопленный текстовый контент на данный момент
	Content string

	// ReasoningContent содержит накопленный reasoning_content из thinking mode
	ReasoningContent string

	// Delta — инкрементальные изменения (для UI обновлений в реальном времени)
	Delta string

	// Done — флаг завершения стриминга
	Done bool

	// Error — ошибка если произошла (только когда Type == ChunkError)
	Error error
}

// ChunkType определяет тип стримингового чанка.
type ChunkType string

const (
	// ChunkThinking — reasoning_content из thinking mode (Zai GLM).
	// Отправляется только когда thinking параметр включен.
	ChunkThinking ChunkType = "thinking"

	// ChunkContent — обычный контент ответа.
	// Накапливается по мере поступления от LLM.
	ChunkContent ChunkType = "content"

	// ChunkError — ошибка стриминга.
	// Содержит ошибку в поле Error.
	ChunkError ChunkType = "error"

	// ChunkDone — завершение стриминга.
	// Отправляется когда все данные получены.
	ChunkDone ChunkType = "done"
)

// IsStreamingMode проверяет, включен ли стриминг в опциях.
//
// По умолчанию возвращает true (opt-out дизайн): стриминг включён,
// если явно не выключен через WithStream(false).
//
// # Usage
//
//	opts := []any{WithStream(false)}  // выключить стриминг
//	isStreaming := IsStreamingMode(opts...) // false
func IsStreamingMode(opts ...any) bool {
	for _, opt := range opts {
		if streamOpt, ok := opt.(StreamOption); ok {
			var so StreamOptions
			streamOpt(&so)
			return so.Enabled
		}
	}
	return true // Default: streaming enabled (opt-out)
}

// IsThinkingOnly проверяет, нужно ли отправлять только reasoning_content.
//
// По умолчанию возвращает true: отправляются события только для
// reasoning_content из thinking mode (требование пользователя).
func IsThinkingOnly(opts ...any) bool {
	for _, opt := range opts {
		if streamOpt, ok := opt.(StreamOption); ok {
			var so StreamOptions
			streamOpt(&so)
			return so.ThinkingOnly
		}
	}
	return true // Default: thinking only
}

```

=================

# llm/streaming_options.go

```go
// Package llm предоставляет функциональные опции для настройки стриминга.
package llm

// StreamOptions — параметры стриминга.
//
// Используется вместе с функциональными опциями StreamOption для
// настройки поведения GenerateStream.
type StreamOptions struct {
	// Enabled — включен ли стриминг (default: true, opt-out).
	//
	// true: GenerateStream будет использовать стриминг
	// false: GenerateStream fallback на обычный Generate
	Enabled bool

	// ThinkingOnly — отправлять только reasoning_content (default: true).
	//
	// true: события отправляются только для reasoning_content
	// false: события отправляются для всех чанков
	ThinkingOnly bool
}

// StreamOption — функциональная опция для настройки стриминга.
//
// Реализует pattern "Functional Options" для гибкой конфигурации.
type StreamOption func(*StreamOptions)

// WithStream включает или выключает стриминг.
//
// # Parameters
//
//   - enabled: true для включения, false для выключения
//
// # Usage
//
//	// Включить стриминг (default behaviour)
//	provider.GenerateStream(ctx, msgs, cb, WithStream(true))
//
//	// Выключить стриминг (fallback на sync)
//	provider.GenerateStream(ctx, msgs, cb, WithStream(false))
func WithStream(enabled bool) StreamOption {
	return func(o *StreamOptions) {
		o.Enabled = enabled
	}
}

// WithThinkingOnly настраивает отправку только reasoning_content.
//
// # Parameters
//
//   - thinkingOnly: true для отправки только reasoning_content
//
// # Usage
//
//	// Отправлять только reasoning_content (default)
//	provider.GenerateStream(ctx, msgs, cb, WithThinkingOnly(true))
//
//	// Отправлять все чанки (content + thinking)
//	provider.GenerateStream(ctx, msgs, cb, WithThinkingOnly(false))
func WithThinkingOnly(thinkingOnly bool) StreamOption {
	return func(o *StreamOptions) {
		o.ThinkingOnly = thinkingOnly
	}
}

// DefaultStreamOptions возвращает дефолтные значения для StreamOptions.
//
// Используется внутри GenerateStream для инициализации.
func DefaultStreamOptions() StreamOptions {
	return StreamOptions{
		Enabled:      true,  // Default: streaming enabled (opt-out)
		ThinkingOnly: true,  // Default: only reasoning_content events
	}
}

```

=================

# llm/types.go

```go
package llm

// Пакет llm содержит абстракции и типы данных для взаимодействия с языковыми моделями.
// Этот файл определяет универсальную структуру сообщений, чтобы отвязать бизнес-логику
// от конкретных SDK (OpenAI, Anthropic и т.д.).

// Role определяет, кто автор сообщения.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool" // Результат выполнения функции
)

// ToolCall описывает запрос модели на вызов инструмента.
type ToolCall struct {
	ID   string // Уникальный ID вызова (нужен для mapping'а ответа)
	Name string // Имя функции (например, "wb_update_card")
	Args string // JSON-строка с аргументами
}

// Message — универсальная единица общения в ReAct цикле.
type Message struct {
	Role    Role
	Content string

	// ToolCalls заполняется, если Role == RoleAssistant и модель хочет вызвать тулы.
	ToolCalls []ToolCall

	// ToolCallID заполняется, если Role == RoleTool (ссылка на запрос).
	ToolCallID string

	// Images — пути к файлам или base64.
	// Используется ТОЛЬКО для одноразовых Vision-запросов (analyze image).
	// В основную историю ReAct эти данные попадать НЕ должны (экономия токенов).
	Images []string
}

```

=================

# models/registry.go

```go
// Package models предоставляет централизованный реестр LLM провайдеров.
//
// Реестр позволяет зарегистрировать все модели из config.yaml при старте
// и динамически переключаться между ними во время выполнения.
//
// Rule 3: Registry pattern (similar to tools.Registry)
// Rule 5: Thread-safe via sync.RWMutex
// Rule 6: Reusable library package, no imports from internal/
package models

import (
	"fmt"
	"strings"
	"sync"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/llm"
	"github.com/ilkoid/poncho-ai/pkg/llm/openai"
)

// Registry — потокобезопасное хранилище LLM провайдеров.
//
// Rule 5: Thread-safe через sync.RWMutex.
// Rule 3: Registry pattern.
type Registry struct {
	mu     sync.RWMutex
	models map[string]ModelEntry
}

// ModelEntry — кешированный провайдер с конфигурацией.
type ModelEntry struct {
	Provider llm.Provider
	Config   config.ModelDef
}

// NewRegistry создаёт новый пустой реестр.
//
// Rule 5: Инициализирован мьютекс, карта создана.
func NewRegistry() *Registry {
	return &Registry{
		models: make(map[string]ModelEntry),
	}
}

// Register добавляет модель в реестр.
//
// Thread-safe. Возвращает ошибку если модель с таким именем уже зарегистрирована.
//
// Rule 7: Возвращает ошибку вместо panic.
func (r *Registry) Register(name string, modelDef config.ModelDef, provider llm.Provider) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.models[name]; exists {
		return fmt.Errorf("model '%s' already registered", name)
	}

	r.models[name] = ModelEntry{
		Provider: provider,
		Config:   modelDef,
	}
	return nil
}

// Get извлекает провайдер по имени модели.
//
// Thread-safe. Возвращает ошибку если модель не найдена.
func (r *Registry) Get(name string) (llm.Provider, config.ModelDef, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	entry, ok := r.models[name]
	if !ok {
		return nil, config.ModelDef{}, fmt.Errorf("model '%s' not found in registry", name)
	}
	return entry.Provider, entry.Config, nil
}

// GetWithFallback извлекает провайдер с fallback на дефолтную модель.
//
// Thread-safe. Приоритет:
// 1. Запрошенная модель (requested)
// 2. Дефолтная модель (defaultModel)
//
// Возвращает (provider, modelDef, actualModelName, error).
func (r *Registry) GetWithFallback(requested, defaultModel string) (llm.Provider, config.ModelDef, string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// 1. Пытаемся получить запрошенную модель
	if entry, ok := r.models[requested]; ok {
		return entry.Provider, entry.Config, requested, nil
	}

	// 2. Fallback на дефолтную модель
	if entry, ok := r.models[defaultModel]; ok {
		return entry.Provider, entry.Config, defaultModel, nil
	}

	// 3. Ни одна не найдена
	return nil, config.ModelDef{}, "", fmt.Errorf("neither requested model '%s' nor default '%s' found in registry", requested, defaultModel)
}

// ListNames возвращает список всех зарегистрированных имён моделей.
//
// Thread-safe. Полезно для логирования и отладки.
func (r *Registry) ListNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.models))
	for name := range r.models {
		names = append(names, name)
	}
	return names
}

// CreateProvider создаёт LLM провайдер на основе конфигурации модели.
//
// Перенесено из pkg/factory для логической связанности (models создаёт провайдеры).
// Поддерживает провайдеры: zai, openai, deepseek.
func CreateProvider(modelDef config.ModelDef) (llm.Provider, error) {
	switch modelDef.Provider {
	case "zai", "openai", "deepseek":
		return openai.NewClient(modelDef), nil
	default:
		return nil, fmt.Errorf("unknown provider type: %s", modelDef.Provider)
	}
}

// NewRegistryFromConfig создаёт и заполняет реестр из конфигурации.
//
// Итерируется через cfg.Models.Definitions и создаёт провайдеры для каждой модели.
// Возвращает ошибку если хоть одна модель не инициализируется.
//
// Rule 4: Работает через llm.Provider интерфейс.
// Rule 7: Возвращает ошибку вместо panic.
func NewRegistryFromConfig(cfg *config.AppConfig) (*Registry, error) {
	registry := NewRegistry()

	// Регистрируем все определённые модели
	for name, modelDef := range cfg.Models.Definitions {
		provider, err := CreateProvider(modelDef)
		if err != nil {
			return nil, fmt.Errorf("failed to create provider for model '%s': %w", name, err)
		}

		if err := registry.Register(name, modelDef, provider); err != nil {
			return nil, fmt.Errorf("failed to register model '%s': %w", name, err)
		}
	}

	return registry, nil
}

// IsVisionModel проверяет, является ли модель vision-моделью.
//
// Thread-safe. Проверяет:
//   1. Точное совпадение с defaultVisionModel
//   2. Явный флаг IsVision в конфигурации модели
//   3. Эвристику по названию (содержит "vision" или "v-")
func (r *Registry) IsVisionModel(modelName string, defaultVisionModel string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Точное совпадение с default_vision
	if modelName == defaultVisionModel {
		return true
	}

	// Проверяем наличие модели в реестре
	if entry, ok := r.models[modelName]; ok {
		// Приоритет явному флагу is_vision
		if entry.Config.IsVision {
			return true
		}

		// Fallback на эвристику по названию
		return strings.Contains(strings.ToLower(modelName), "vision") ||
			strings.Contains(strings.ToLower(modelName), "v-") ||
			strings.Contains(strings.ToLower(entry.Config.ModelName), "vision")
	}

	return false
}

```

=================

# prompt/agent.go

```go
// Package prompt предоставляет функции для загрузки и рендеринга промптов.
package prompt

import (
	"fmt"
	"os"

	"github.com/ilkoid/poncho-ai/pkg/config"
)

// LoadAgentSystemPrompt загружает системный промпт для AI-агента.
//
// Пытается загрузить промпт из файла {PromptsDir}/agent_system.yaml.
// Если файл не существует или ошибка загрузки — возвращает дефолтный промпт.
//
// Дефолтный промпт базовый и может быть переопределён через YAML файл
// для кастомизации поведения агента под конкретные задачи.
func LoadAgentSystemPrompt(cfg *config.AppConfig) (string, error) {
	// 1. Формируем путь к файлу промпта
	promptPath := fmt.Sprintf("%s/agent_system.yaml", cfg.App.PromptsDir)

	// 2. Проверяем существование файла
	if _, err := os.Stat(promptPath); os.IsNotExist(err) {
		// Файл не существует — возвращаем дефолтный промпт
		return getDefaultAgentPrompt(), nil
	}

	// 3. Загружаем файл
	pf, err := Load(promptPath)
	if err != nil {
		return "", fmt.Errorf("failed to load agent prompt from %s: %w", promptPath, err)
	}

	// 4. Проверяем наличие сообщений
	if len(pf.Messages) == 0 {
		return getDefaultAgentPrompt(), nil
	}

	// 5. Возвращаем контент первого сообщения (системного)
	// Предполагаем что первое сообщение — системный промпт агента
	if pf.Messages[0].Content != "" {
		return pf.Messages[0].Content, nil
	}

	return getDefaultAgentPrompt(), nil
}

// getDefaultAgentPrompt возвращает дефолтный системный промпт агента.
//
// Используется как fallback когда:
// - Файл agent_system.yaml не существует
// - Файл пустой или некорректный
func getDefaultAgentPrompt() string {
	return `Ты AI-ассистент для работы с Wildberries и анализа данных.

## Твои возможности

У тебя есть доступ к инструментам (tools) для получения актуальных данных:
- Работа с категориями Wildberries
- Работа с S3 хранилищем
- Управление задачами

## Правила работы

1. Используй tools когда нужно получить актуальные данные — не выдумывай
2. Анализируй запрос пользователя перед вызовом инструмента
3. Формируй понятные структурированные ответы
4. Если инструмент вернул ошибку — сообщи о ней пользователю
5. Если данных недостаточно — спроси уточняющий вопрос

## Примеры

Запрос: "покажи родительские категории товаров"
Действие: Вызвать get_wb_parent_categories и оформить ответ

Запрос: "какие товары в категории Женщинам?"
Действие:
  1. Вызвать get_wb_parent_categories → найти ID категории
  2. Вызвать get_wb_subjects с этим ID
  3. Оформить ответ пользователю
`
}

```

=================

# prompt/loader.go

```go
// Загрузка и Рендер - чтение файла и text/template.

package prompt

import (
	"bytes"
	"fmt"
	"os"
	"text/template"

	"gopkg.in/yaml.v3"
)

// Load загружает и парсит YAML файл промпта
func Load(path string) (*PromptFile, error) {
	// 1. Проверяем наличие
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, fmt.Errorf("prompt file not found: %s", path)
	}

	// 2. Читаем байты
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read error: %w", err)
	}

	// 3. Парсим YAML
	var pf PromptFile
	if err := yaml.Unmarshal(data, &pf); err != nil {
		return nil, fmt.Errorf("yaml parse error: %w", err)
	}

	return &pf, nil
}

// RenderMessages принимает данные (struct или map) и возвращает готовые сообщения
// где все {{.Field}} заменены на значения.
func (pf *PromptFile) RenderMessages(data interface{}) ([]Message, error) {
	rendered := make([]Message, len(pf.Messages))

	for i, msg := range pf.Messages {
		// Создаем шаблон
		tmpl, err := template.New("msg").Parse(msg.Content)
		if err != nil {
			return nil, fmt.Errorf("template parse error in message #%d (%s): %w", i, msg.Role, err)
		}

		// Рендерим в буфер
		var buf bytes.Buffer
		if err := tmpl.Execute(&buf, data); err != nil {
			return nil, fmt.Errorf("template execute error in message #%d: %w", i, err)
		}

		// Сохраняем результат
		rendered[i] = Message{
			Role:    msg.Role,
			Content: buf.String(),
		}
	}

	return rendered, nil
}

// LoadVisionSystemPrompt загружает системный промпт для Vision LLM.
//
// TODO: Реализовать загрузку из файла prompts/vision_system_prompt.yaml
// Сейчас возвращает дефолтный промпт.
func LoadVisionSystemPrompt(cfg interface{}) (string, error) {
	// STUB: Возвращаем дефолтный промпт для vision-анализа
	defaultPrompt := `Ты эксперт по анализу fashion-эскизов.

Твоя задача - проанализировать изображение и описать:
1. Тип изделия (куртка, брюки, платье и т.д.)
2. Силуэт (приталенный, прямой, свободный)
3. Детали (карманы, воротник, манжеты)
4. Цвет и материалы
5. Стиль (casual, business, sport)

Отвечай кратко и по делу, на русском языке.`

	return defaultPrompt, nil
}


```

=================

# prompt/model.go

```go
// Структуры данных - описывает формат YAML файла промпта. 
package prompt

// PromptFile описывает структуру YAML-файла с промптом
type PromptFile struct {
	Config   PromptConfig `yaml:"config"`
	Messages []Message    `yaml:"messages"`
}

// PromptConfig - настройки модели для конкретного промпта
type PromptConfig struct {
	Model       string  `yaml:"model"`       // Например "zai-vision/glm-4.5v"
	Temperature float64 `yaml:"temperature"` 
	MaxTokens   int     `yaml:"max_tokens"`
	Format      string  `yaml:"format"`      // "json_object" или text
}

// Message - одно сообщение в чате
type Message struct {
	Role    string `yaml:"role"`    // system, user, assistant
	Content string `yaml:"content"` // Шаблон с {{.Variables}}
}

// ToolPostPrompt описывает post-prompt для конкретного tool
type ToolPostPrompt struct {
	PostPrompt string `yaml:"post_prompt"` // Относительный путь к файлу промпта
	Enabled    bool   `yaml:"enabled"`     // Включён ли post-prompt
}

// ToolPostPromptConfig описывает конфигурацию связки tool → post-prompt
// Загружается из prompts/tool_postprompts.yaml
type ToolPostPromptConfig struct {
	Tools map[string]ToolPostPrompt `yaml:"tools"`
}

```

=================

# prompt/postprompt.go

```go
// Package prompt предоставляет функции для загрузки и рендеринга промптов.
//
// Этот файл реализует систему post-prompts — промптов, которые активируются
// после выполнения конкретного tool и используются для следующей LLM итерации.
package prompt

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ilkoid/poncho-ai/pkg/config"
)

// LoadToolPostPrompts загружает конфигурацию связки tool → post-prompt из основного config.yaml.
//
// Читает из секции cfg.Tools, которая содержит поле post_prompt для каждого инструмента.
// Валидирует что все referenced файлы существуют (fail-fast).
func LoadToolPostPrompts(cfg *config.AppConfig) (*ToolPostPromptConfig, error) {
	ppConfig := &ToolPostPromptConfig{
		Tools: make(map[string]ToolPostPrompt),
	}

	// Конвертируем cfg.Tools в ToolPostPromptConfig
	for toolName, toolCfg := range cfg.Tools {
		ppConfig.Tools[toolName] = ToolPostPrompt{
			PostPrompt: toolCfg.PostPrompt,
			Enabled:    toolCfg.Enabled,
		}

		// Валидация: проверяем что referenced файлы существуют
		if toolCfg.Enabled && toolCfg.PostPrompt != "" {
			promptPath := filepath.Join(cfg.App.PromptsDir, toolCfg.PostPrompt)
			if _, err := os.Stat(promptPath); os.IsNotExist(err) {
				return nil, fmt.Errorf("post-prompt file not found for tool '%s': %s (tool: %s, path: %s)",
					toolName, promptPath, toolName, toolCfg.PostPrompt)
			}
		}
	}

	return ppConfig, nil
}

// GetToolPostPrompt возвращает текст post-prompt для заданного tool.
//
// Возвращает:
//   - string: текст промпта (пустой если не настроен или отключён)
//   - error: ошибка если не удалось загрузить файл промпта
func (cfg *ToolPostPromptConfig) GetToolPostPrompt(toolName string, promptsDir string) (string, error) {
	// Проверяем есть ли конфиг для этого tool
	toolCfg, exists := cfg.Tools[toolName]
	if !exists {
		return "", nil // Не настроен — это нормально
	}

	// Проверяем включён ли
	if !toolCfg.Enabled || toolCfg.PostPrompt == "" {
		return "", nil // Отключён — это нормально
	}

	// Загружаем файл промпта
	promptPath := filepath.Join(promptsDir, toolCfg.PostPrompt)
	pf, err := Load(promptPath)
	if err != nil {
		return "", fmt.Errorf("load post-prompt file for tool '%s': %w", toolName, err)
	}

	// Если в файле есть messages — берём первое как system prompt
	if len(pf.Messages) > 0 && pf.Messages[0].Role == "system" {
		return pf.Messages[0].Content, nil
	}

	// Иначе читаем как plain text
	data, err := os.ReadFile(promptPath)
	if err != nil {
		return "", fmt.Errorf("read post-prompt file %s: %w", promptPath, err)
	}

	return string(data), nil
}

// GetToolPromptFile возвращает весь PromptFile включая Config для заданного tool.
//
// Возвращает:
//   - *PromptFile: весь файл промпта с Config и Messages (nil если не настроен)
//   - error: ошибка если не удалось загрузить файл промпта
//
// Используется для runtime переопределения параметров модели (model, temperature, max_tokens).
func (cfg *ToolPostPromptConfig) GetToolPromptFile(toolName string, promptsDir string) (*PromptFile, error) {
	// Проверяем есть ли конфиг для этого tool
	toolCfg, exists := cfg.Tools[toolName]
	if !exists {
		return nil, nil // Не настроен — это нормально
	}

	// Проверяем включён ли
	if !toolCfg.Enabled || toolCfg.PostPrompt == "" {
		return nil, nil // Отключён — это нормально
	}

	// Загружаем файл промпта
	promptPath := filepath.Join(promptsDir, toolCfg.PostPrompt)
	pf, err := Load(promptPath)
	if err != nil {
		return nil, fmt.Errorf("load post-prompt file for tool '%s': %w", toolName, err)
	}

	return pf, nil
}

```

=================

# s3storage/client.go

```go
// "Тупой" клиент. классификатор файлов будет отдельно

package s3storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	_ "path/filepath"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/ilkoid/poncho-ai/pkg/config"
)

// ClientInterface определяет интерфейс для S3 клиента.
// Используется для мокания в тестах и внедрения зависимостей.
type ClientInterface interface {
	ListFiles(ctx context.Context, prefix string) ([]StoredObject, error)
	DownloadFile(ctx context.Context, key string) ([]byte, error)
}

type Client struct {
    api    *minio.Client
    bucket string
}

// Проверка что Client реализует ClientInterface
var _ ClientInterface = (*Client)(nil)

// StoredObject - сырой объект из S3
type StoredObject struct {
	Key          string
	Size         int64
	LastModified time.Time
}

// FileMeta хранит метаданные файла с тегом классификации.
// Type может быть заполнен позже vision-моделью (например, "Платье красное").
type FileMeta struct {
	Tag               string   // Тег классификации (sketch, techpack, etc.)
	Key               string   // Полный путь в S3 (alias OriginalKey для совместимости)
	OriginalKey       string   // Оригинальный ключ в S3
	Size              int64    // Размер файла в байтах
	Type              string   // Описание типа (опционально, заполняется vision)
	Filename          string   // Имя файла без пути
	VisionDescription string   // Результат анализа vision-модели (Working Memory)
	Tags              []string // Дополнительные теги (для расширенной классификации)
}

// NewFileMeta создает новый FileMeta с базовыми метаданными.
func NewFileMeta(tag, key string, size int64, filename string) *FileMeta {
	return &FileMeta{
		Tag:         tag,
		Key:         key,
		OriginalKey: key, // Изначально Key и OriginalKey совпадают
		Size:        size,
		Filename:    filename,
		Tags:        []string{}, // Инициализируем пустой срез
	}
}

// New создает клиент, используя наш конфиг
func New(cfg config.S3Config) (*Client, error) {
    minioClient, err := minio.New(cfg.Endpoint, &minio.Options{
        Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
        Secure: cfg.UseSSL,
        Region: cfg.Region,
    })
    if err != nil {
        return nil, err
    }

    return &Client{
        api:    minioClient,
        bucket: cfg.Bucket,
    }, nil
}

// ListFiles возвращает ВСЕ файлы по префиксу (артикулу)
func (c *Client) ListFiles(ctx context.Context, prefix string) ([]StoredObject, error) {
	// Нормализация префикса (добавляем слеш, если это "папка")
	if !strings.HasSuffix(prefix, "/") && prefix != "" {
		prefix += "/"
	}

	var objects []StoredObject
	
	opts := minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: true,
	}

	for obj := range c.api.ListObjects(ctx, c.bucket, opts) {
		if obj.Err != nil {
			return nil, obj.Err
		}
		// Пропускаем саму "папку"
		if obj.Key == prefix {
			continue
		}
		objects = append(objects, StoredObject{
			Key:          obj.Key,
			Size:         obj.Size,
			LastModified: obj.LastModified,
		})
	}
	
	if len(objects) == 0 {
		// Это можно считать ошибкой или просто пустым списком - зависит от логики
		// Для утилиты лучше вернуть ошибку, чтобы пользователь сразу понял
		return nil, fmt.Errorf("path '%s' not found or empty", prefix)
	}

	return objects, nil
}

// DownloadFile скачивает объект целиком в память
func (c *Client) DownloadFile(ctx context.Context, key string) ([]byte, error) {
    obj, err := c.api.GetObject(ctx, c.bucket, key, minio.GetObjectOptions{})
    if err != nil {
        return nil, err
    }
    defer obj.Close()

    // Читаем в буфер
    buf := new(bytes.Buffer)
    if _, err := io.Copy(buf, obj); err != nil {
        return nil, err
    }

    return buf.Bytes(), nil
}

```

=================

# state/core.go

```go
// Package state предоставляет thread-safe core состояние для AI-агента.
//
// Новая CoreState реализует интерфейсы репозиториев с унифицированным хранилищем
// map[string]any, что позволяет:
// - Хранить любые данные без изменения структуры
// - Иметь единый source of truth для всего состояния
// - Быть независимой от конкретных типов зависимостей
// - Переиспользоваться в различных приложениях
//
// Соблюдение правил из dev_manifest.md:
//   - Rule 5: Thread-safe доступ через sync.RWMutex
//   - Rule 6: Library код готовый к переиспользованию, без зависимостей от internal/
//   - Rule 7: Все ошибки возвращаются, никаких panic в бизнес-логике
//   - Rule 10: Все public API имеют godoc комментарии
package state

import (
	"fmt"
	"strings"
	"sync"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/llm"
	"github.com/ilkoid/poncho-ai/pkg/s3storage"
	"github.com/ilkoid/poncho-ai/pkg/todo"
	"github.com/ilkoid/poncho-ai/pkg/tools"
	"github.com/ilkoid/poncho-ai/pkg/utils"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// CoreState представляет thread-safe core состояние AI-агента с унифицированным хранилищем.
//
// ARCHITECTURE (Refactored 2026-01-04):
//
// Использует map[string]any как единое хранилище для всех данных.
// Реализует 6 интерфейсов репозиториев для domain-specific операций.
//
// Преимущества:
// - Нет жестких зависимостей (S3, Dictionaries, etc — опциональны)
// - Унифицированный CRUD через UnifiedStore интерфейс
// - Type-safe методы через domain-specific интерфейсы
// - Thread-safe доступ к всем данным
// - Готов к переиспользованию в любых приложениях (Rule 6)
//
// Rule 5: Все изменения runtime полей защищены sync.RWMutex.
// Rule 6: Не зависит от internal/, готов к переиспользованию.
type CoreState struct {
	// Config - конфигурация приложения (Rule 2: YAML with ENV support)
	Config *config.AppConfig

	// mu защищает доступ к store (Rule 5: Thread-safe)
	mu sync.RWMutex

	// store - унифицированное хранилище данных
	// Ключи определены как константы в keys.go
	// Значения могут быть любого типа: []llm.Message, map[string][]*FileMeta, etc
	store map[string]any
}

// NewCoreState создает новое thread-safe core состояние.
//
// БЕЗ зависимостей — S3, Dictionaries и другие компоненты устанавливаются
// через соответствующие интерфейсы AFTER создания.
//
// Rule 5: Возвращает готовую к использованию thread-safe структуру.
// Rule 7: Никаких panic при nil конфигурации - валидация делегируется вызывающему.
func NewCoreState(cfg *config.AppConfig) *CoreState {
	return &CoreState{
		Config: cfg,
		store:  make(map[string]any),
	}
}

// ============================================================
// UnifiedStore Interface Implementation
// ============================================================

// Get возвращает значение по ключу.
//
// Возвращает (value, true) если ключ существует, (nil, false) иначе.
// Thread-safe: чтение защищено мьютексом.
func (s *CoreState) Get(key Key) (any, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	val, ok := s.store[string(key)]
	return val, ok
}

// Set сохраняет значение по ключу.
//
// Перезаписывает существующее значение или создает новое.
// Thread-safe: запись защищена мьютексом.
func (s *CoreState) Set(key Key, value any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.store[string(key)] = value
	return nil
}

// ============================================================
// Generic Type-Safe Helper Functions (Go 1.18+)
// ============================================================

// GetType обеспечивает type-safe доступ к значениям CoreState.
//
// Generic параметр T - тип возвращаемого значения.
// Возвращает (value, true) если ключ существует и тип совпадает, (zero, false) иначе.
// Thread-safe: чтение защищено мьютексом.
//
// Пример:
//   dicts, ok := state.GetType[*wb.Dictionaries](coreState, state.KeyDictionaries)
func GetType[T any](s *CoreState, key Key) (T, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	val, ok := s.store[string(key)]
	if !ok {
		var zero T
		return zero, false
	}

	typed, ok := val.(T)
	if !ok {
		var zero T
		return zero, false
	}

	return typed, true
}

// SetType обеспечивает type-safe сохранение значений в CoreState.
//
// Generic параметр T - тип сохраняемого значения.
// Перезаписывает существующее значение или создает новое.
// Thread-safe: запись защищена мьютексом.
//
// Пример:
//   state.SetType[*wb.Dictionaries](coreState, state.KeyDictionaries, dicts)
func SetType[T any](s *CoreState, key Key, value T) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.store[string(key)] = value
	return nil
}

// UpdateType атомарно обновляет значение в CoreState с типизацией.
//
// Generic параметр T - тип значения.
// fn получает текущее значение (или zero если ключ не существует)
// и должен вернуть новое значение.
// Thread-safe: вся операция атомарна под мьютексом.
//
// Пример:
//   state.UpdateType[*todo.Manager](coreState, state.KeyTodo, func(m *todo.Manager) *todo.Manager {
//       m.Add("new task")
//       return m
//   })
func UpdateType[T any](s *CoreState, key Key, fn func(T) T) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	current, ok := s.store[string(key)]
	var currentTyped T
	if ok {
		currentTyped = current.(T)
	}

	newValue := fn(currentTyped)
	s.store[string(key)] = newValue
	return nil
}

// Update атомарно обновляет значение по ключу.
//
// fn получает текущее значение (или nil если ключ не существует)
// и должен вернуть новое значение.
//
// Thread-safe: вся операция атомарна под мьютексом.
func (s *CoreState) Update(key Key, fn func(any) any) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	keyStr := string(key)
	current := s.store[keyStr]
	newValue := fn(current)

	if newValue == nil {
		// Если fn вернул nil - удаляем ключ
		delete(s.store, keyStr)
	} else {
		s.store[keyStr] = newValue
	}

	return nil
}

// Delete удаляет значение по ключу.
//
// Если ключ не существует — возвращает ошибку.
// Thread-safe: удаление защищено мьютексом.
func (s *CoreState) Delete(key Key) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	keyStr := string(key)
	if _, ok := s.store[keyStr]; !ok {
		return fmt.Errorf("key not found: %s", keyStr)
	}

	delete(s.store, keyStr)
	return nil
}

// Exists проверяет существование ключа.
//
// Thread-safe: чтение защищено мьютексом.
func (s *CoreState) Exists(key Key) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.store[string(key)]
	return ok
}

// List возвращает все ключи в хранилище.
//
// Порядок ключей не гарантирован.
// Thread-safe: чтение защищено мьютексом.
func (s *CoreState) List() []Key {
	s.mu.RLock()
	defer s.mu.RUnlock()

	keys := make([]Key, 0, len(s.store))
	for k := range s.store {
		keys = append(keys, Key(k))
	}
	return keys
}

// ============================================================
// MessageRepository Interface Implementation
// ============================================================

// Append добавляет сообщение в историю.
//
// Thread-safe: добавление защищено мьютексом.
func (s *CoreState) Append(msg llm.Message) error {
	return s.Update(KeyHistory, func(val any) any {
		if val == nil {
			return []llm.Message{msg}
		}
		history := val.([]llm.Message)
		return append(history, msg)
	})
}

// GetHistory возвращает копию всей истории диалога.
//
// Возвращает копию слайса для избежания race condition при изменении.
// Thread-safe: чтение защищено мьютексом.
func (s *CoreState) GetHistory() []llm.Message {
	val, ok := s.Get(KeyHistory)
	if !ok {
		return []llm.Message{}
	}

	history := val.([]llm.Message)
	dst := make([]llm.Message, len(history))
	copy(dst, history)
	return dst
}

// ============================================================
// FileRepository Interface Implementation
// ============================================================

// SetFiles сохраняет файлы для текущей сессии.
//
// Принимает map[string][]*s3storage.FileMeta и сохраняет напрямую.
// VisionDescription заполняется позже через UpdateFileAnalysis().
//
// Thread-safe: атомарная замена всей map файлов.
func (s *CoreState) SetFiles(files map[string][]*s3storage.FileMeta) error {
	return s.Set(KeyFiles, files)
}

// GetFiles возвращает копию текущих файлов.
//
// Thread-safe: чтение защищено мьютексом.
func (s *CoreState) GetFiles() map[string][]*s3storage.FileMeta {
	val, ok := s.Get(KeyFiles)
	if !ok {
		return make(map[string][]*s3storage.FileMeta)
	}

	files := val.(map[string][]*s3storage.FileMeta)
	result := make(map[string][]*s3storage.FileMeta, len(files))
	for k, v := range files {
		result[k] = append([]*s3storage.FileMeta{}, v...)
	}
	return result
}

// UpdateFileAnalysis сохраняет результат работы Vision модели.
//
// Параметры:
//   - tag: тег файла (ключ в map, например "sketch", "plm_data")
//   - filename: имя файла для поиска в слайсе
//   - description: результат анализа (текст от vision модели)
//
// Thread-safe: атомарно заменяет объект в слайсе под мьютексом.
func (s *CoreState) UpdateFileAnalysis(tag string, filename string, description string) error {
	return s.Update(KeyFiles, func(val any) any {
		if val == nil {
			utils.Error("UpdateFileAnalysis: files not found", "tag", tag, "filename", filename)
			return val
		}

		files := val.(map[string][]*s3storage.FileMeta)
		filesList, ok := files[tag]
		if !ok {
			utils.Error("UpdateFileAnalysis: tag not found", "tag", tag, "filename", filename)
			return val
		}

		// Находим индекс файла и атомарно заменяем объект
		for i := range filesList {
			if filesList[i].Filename == filename {
				// Создаем новый объект для thread-safety
				updated := &s3storage.FileMeta{
					Tag:               filesList[i].Tag,
					OriginalKey:       filesList[i].OriginalKey,
					Size:              filesList[i].Size,
					Filename:          filesList[i].Filename,
					VisionDescription: description,
					Tags:              filesList[i].Tags,
				}
				filesList[i] = updated
				utils.Debug("File analysis updated", "tag", tag, "filename", filename, "desc_length", len(description))
				return files
			}
		}

		utils.Warn("UpdateFileAnalysis: file not found", "tag", tag, "filename", filename)
		return val
	})
}

// SetCurrentArticle устанавливает текущий артикул и файлы.
//
// Параметры:
//   - articleID: ID артикула
//   - files: map файлов по тегам
//
// Thread-safe: атомарная замена файлов и articleID.
func (s *CoreState) SetCurrentArticle(articleID string, files map[string][]*s3storage.FileMeta) error {
	if err := s.SetFiles(files); err != nil {
		return err
	}
	return s.Set(KeyCurrentArticle, articleID)
}

// GetCurrentArticleID возвращает ID текущего артикула.
//
// Thread-safe: чтение защищено мьютексом.
func (s *CoreState) GetCurrentArticleID() string {
	val, ok := s.Get(KeyCurrentArticle)
	if !ok {
		return "NONE"
	}
	return val.(string)
}

// GetCurrentArticle возвращает ID и файлы текущего артикула.
//
// Thread-safe: чтение защищено мьютексом.
func (s *CoreState) GetCurrentArticle() (articleID string, files map[string][]*s3storage.FileMeta) {
	articleID = s.GetCurrentArticleID()
	files = s.GetFiles()
	return
}

// ============================================================
// TodoRepository Interface Implementation
// ============================================================

// AddTask добавляет новую задачу в план.
//
// Параметры:
//   - description: описание задачи
//   - metadata: опциональные метаданные (ключ-значение)
//
// Возвращает ID созданной задачи.
// Thread-safe: делегирует thread-safe Manager.Add.
func (s *CoreState) AddTask(description string, metadata ...map[string]interface{}) (int, error) {
	manager := s.getTodoManager()
	if manager == nil {
		// Создаем новый менеджер если не существует
		manager = todo.NewManager()
		if err := s.Set(KeyTodo, manager); err != nil {
			return 0, fmt.Errorf("failed to create todo manager: %w", err)
		}
	}

	id := manager.Add(description, metadata...)
	return id, nil
}

// CompleteTask отмечает задачу как выполненную.
//
// Возвращает ошибку если задача не найдена или уже завершена.
// Thread-safe: делегирует thread-safe Manager.Complete.
func (s *CoreState) CompleteTask(id int) error {
	manager := s.getTodoManager()
	if manager == nil {
		return fmt.Errorf("todo manager not initialized")
	}
	return manager.Complete(id)
}

// FailTask отмечает задачу как проваленную.
//
// Параметры:
//   - id: ID задачи
//   - reason: причина неудачи
//
// Возвращает ошибку если задача не найдена или уже завершена.
// Thread-safe: делегирует thread-safe Manager.Fail.
func (s *CoreState) FailTask(id int, reason string) error {
	manager := s.getTodoManager()
	if manager == nil {
		return fmt.Errorf("todo manager not initialized")
	}
	return manager.Fail(id, reason)
}

// GetTodoString возвращает строковое представление плана.
//
// Thread-safe: чтение защищено мьютексом.
func (s *CoreState) GetTodoString() string {
	manager := s.getTodoManager()
	if manager == nil {
		return ""
	}
	return manager.String()
}

// GetTodoStats возвращает статистику по задачам.
//
// Возвращает количество pending, done, failed задач.
// Thread-safe: чтение защищено мьютексом.
func (s *CoreState) GetTodoStats() (pending, done, failed int) {
	manager := s.getTodoManager()
	if manager == nil {
		return 0, 0, 0
	}
	return manager.GetStats()
}

// GetTodoManager возвращает todo.Manager для использования в tools.
//
// ВНИМАНИЕ: Это метод для backward compatibility с tools которые
// требуют *todo.Manager. Предпочтительно использовать TodoRepository методы.
//
// Thread-safe: чтение защищено мьютексом.
func (s *CoreState) GetTodoManager() *todo.Manager {
	val, _ := GetType[*todo.Manager](s, KeyTodo)
	return val
}

// SetTodoManager устанавливает todo manager в CoreState.
//
// Thread-safe: изменение защищено мьютексом.
func (s *CoreState) SetTodoManager(manager *todo.Manager) error {
	return s.Set(KeyTodo, manager)
}

// getTodoManager — вспомогательный метод для получения todo.Manager.
//
// Возвращает nil если менеджер не инициализирован.
func (s *CoreState) getTodoManager() *todo.Manager {
	val, _ := GetType[*todo.Manager](s, KeyTodo)
	return val
}

// ============================================================
// DictionaryRepository Interface Implementation
// ============================================================

// SetDictionaries устанавливает справочники маркетплейсов.
//
// Thread-safe: изменение защищено мьютексом.
func (s *CoreState) SetDictionaries(dicts *wb.Dictionaries) error {
	return s.Set(KeyDictionaries, dicts)
}

// GetDictionaries возвращает справочники маркетплейсов.
//
// Thread-safe: чтение защищено мьютексом.
func (s *CoreState) GetDictionaries() *wb.Dictionaries {
	val, _ := GetType[*wb.Dictionaries](s, KeyDictionaries)
	return val
}

// ============================================================
// StorageRepository Interface Implementation
// ============================================================

// SetStorage устанавливает S3 клиент.
//
// Если client == nil — очищает S3 из состояния.
// Thread-safe: изменение защищено мьютексом.
func (s *CoreState) SetStorage(client *s3storage.Client) error {
	if client == nil {
		return s.Delete(KeyStorage)
	}
	return s.Set(KeyStorage, client)
}

// GetStorage возвращает S3 клиент.
//
// Возвращает nil если S3 не установлен.
// Thread-safe: чтение защищено мьютексом.
func (s *CoreState) GetStorage() *s3storage.Client {
	val, _ := GetType[*s3storage.Client](s, KeyStorage)
	return val
}

// HasStorage проверяет наличие S3 клиента.
//
// Thread-safe: чтение защищено мьютексом.
func (s *CoreState) HasStorage() bool {
	return s.Exists(KeyStorage)
}

// ============================================================
// Helper Methods (backward compatibility)
// ============================================================

// SetToolsRegistry устанавливает реестр инструментов.
//
// Thread-safe метод для инициализации registry после регистрации инструментов.
//
// Rule 3: Все инструменты регистрируются через Registry.Register().
// Rule 5: Thread-safe доступ к полям структуры.
func (s *CoreState) SetToolsRegistry(registry *tools.Registry) error {
	return s.Set(KeyToolsRegistry, registry)
}

// GetToolsRegistry возвращает реестр инструментов.
//
// Thread-safe метод для получения registry для использования в агенте.
//
// Rule 3: Все инструменты вызываются через Registry.
// Rule 5: Thread-safe доступ к полям структуры.
func (s *CoreState) GetToolsRegistry() *tools.Registry {
	val, _ := GetType[*tools.Registry](s, KeyToolsRegistry)
	return val
}

// ============================================================
// Context Building
// ============================================================

// BuildAgentContext собирает полный контекст для генеративного запроса (ReAct).
//
// Объединяет:
// 1. Системный промпт
// 2. "Рабочую память" (результаты анализа файлов из VisionDescription)
// 3. Контекст плана (Todo Manager)
// 4. Историю диалога
//
// Возвращаемый массив сообщений готов для передачи в LLM.
//
// "Working Memory" паттерн: Результаты анализа vision моделей хранятся в
// FileMeta.VisionDescription и инжектируются в контекст без повторной
// отправки изображений (экономия токенов).
//
// Thread-safe: читает Files и History под защитой мьютекса.
//
// Rule 5: Thread-safe доступ к полям.
// Rule 7: Возвращает корректный массив даже при пустых данных.
func (s *CoreState) BuildAgentContext(systemPrompt string) []llm.Message {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// 1. Формируем блок знаний из проанализированных файлов
	var visualContext string
	files, hasFiles := s.store[string(KeyFiles)]
	if hasFiles {
		filesMap := files.(map[string][]*s3storage.FileMeta)
		for tag, filesList := range filesMap {
			for _, f := range filesList {
				if f.VisionDescription != "" {
					visualContext += fmt.Sprintf("- Файл [%s] %s: %s\n", tag, f.Filename, f.VisionDescription)
				}
			}
		}
	}

	knowledgeMsg := ""
	if visualContext != "" {
		knowledgeMsg = fmt.Sprintf("\nКОНТЕКСТ АРТИКУЛА (Результаты анализа файлов):\n%s", visualContext)
	}

	// 2. Формируем контекст плана
	var todoContext string
	todoVal, hasTodo := s.store[string(KeyTodo)]
	if hasTodo {
		manager := todoVal.(*todo.Manager)
		todoContext = manager.String()
	}

	// 3. Получаем историю
	var history []llm.Message
	historyVal, hasHistory := s.store[string(KeyHistory)]
	if hasHistory {
		history = historyVal.([]llm.Message)
	} else {
		history = make([]llm.Message, 0)
	}

	// 4. Собираем итоговый массив сообщений
	messages := make([]llm.Message, 0, len(history)+3)

	// Системное сообщение с инъекцией знаний
	messages = append(messages, llm.Message{
		Role:    llm.RoleSystem,
		Content: systemPrompt + knowledgeMsg,
	})

	// Добавляем контекст плана
	if todoContext != "" && !strings.Contains(todoContext, "Нет активных задач") {
		messages = append(messages, llm.Message{
			Role:    llm.RoleSystem,
			Content: todoContext,
		})
	}

	// Добавляем историю переписки
	messages = append(messages, history...)

	return messages
}


```

=================

# state/keys.go

```go
// Package state предоставляет константы ключей для унифицированного хранилища.
//
// Ключи используются для доступа к данным в CoreState.store map[string]any.
// Все ключи определены как константы для избежания опечаток и обеспечения type-safety.
//
// Rule 10: Все публичные константы имеют godoc комментарии.
package state

// Key — типизированный ключ для type-safe операций с CoreState.
//
// Используется с generic методами Get[T]/Set[T]/Update[T] для обеспечения
// compile-time проверки типов.
//
// Пример:
//   dicts, ok := coreState.Get[*wb.Dictionaries](state.KeyDictionaries)
type Key string

// String возвращает строковое представление ключа.
//
// Используется для обратной совместимости с кодом, ожидающим string.
func (k Key) String() string {
	return string(k)
}

// Ключи для унифицированного хранилища CoreState.store
//
// Используются в методах Get[T]/Set[T]/Update[T] для доступа к конкретным данным.
const (
	// KeyHistory — ключ для хранения истории диалога ([]llm.Message)
	//
	// Содержит хронологию сообщений между пользователем и агентом.
	// Используется в MessageRepository интерфейсе.
	//
	// Пример:
	//   history, ok := coreState.Get[[]llm.Message](state.KeyHistory)
	KeyHistory Key = "history"

	// KeyFiles — ключ для хранения файлов текущей сессии (map[string][]*FileMeta)
	//
	// "Рабочая память" агента — файлы и результаты их анализа.
	// Ключ map — тег файла ("sketch", "plm_data", etc).
	// Используется в FileRepository интерфейсе.
	//
	// Пример:
	//   files, ok := coreState.Get[map[string][]*s3storage.FileMeta](state.KeyFiles)
	KeyFiles Key = "files"

	// KeyCurrentArticle — ключ для хранения ID текущего артикула (string)
	//
	// ID текущего артикула для e-commerce workflow.
	// Используется в tandem с KeyFiles для отслеживания контекста.
	//
	// Пример:
	//   articleID, ok := coreState.Get[string](state.KeyCurrentArticle)
	KeyCurrentArticle Key = "current_article"

	// KeyTodo — ключ для хранения менеджера задач (*todo.Manager)
	//
	// Планировщик многошаговых задач агента.
	// Используется в TodoRepository интерфейсе.
	//
	// Пример:
	//   manager, ok := coreState.Get[*todo.Manager](state.KeyTodo)
	KeyTodo Key = "todo"

	// KeyDictionaries — ключ для хранения справочников маркетплейсов (*wb.Dictionaries)
	//
	// Данные Wildberries (цвета, страны, полы, сезоны, НДС).
	// Framework: e-commerce бизнес-логика, переиспользуемая.
	// Используется в DictionaryRepository интерфейсе.
	//
	// Пример:
	//   dicts, ok := coreState.Get[*wb.Dictionaries](state.KeyDictionaries)
	KeyDictionaries Key = "dictionaries"

	// KeyStorage — ключ для хранения S3 клиента (*s3storage.Client)
	//
	// S3-совместимый клиент для загрузки данных.
	// Является опциональной зависимостью.
	// Используется в StorageRepository интерфейсе.
	//
	// Пример:
	//   client, ok := coreState.Get[*s3storage.Client](state.KeyStorage)
	KeyStorage Key = "storage"

	// KeyToolsRegistry — ключ для хранения реестра инструментов (*tools.Registry)
	//
	// Реестр зарегистрированных инструментов агента.
	// Используется для вызова инструментов через Registry.
	//
	// Пример:
	//   registry, ok := coreState.Get[*tools.Registry](state.KeyToolsRegistry)
	KeyToolsRegistry Key = "tools_registry"
)

```

=================

# todo/manager.go

```go
// Package todo реализует потокобезопасный менеджер задач для AI-агента.
//
// Предоставляет инструменты для создания, отслеживания и управления задачами
// в рамках агentic workflow (ReAct pattern).
package todo

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// TaskStatus представляет статус задачи в плане.
type TaskStatus string

const (
	StatusPending TaskStatus = "PENDING"
	StatusDone    TaskStatus = "DONE"
	StatusFailed  TaskStatus = "FAILED"
)

// Task представляет задачу в плане действий агента.
type Task struct {
	ID          int                    `json:"id"`
	Description string                 `json:"description"`
	Status      TaskStatus             `json:"status"`
	CreatedAt   time.Time              `json:"created_at"`
	CompletedAt *time.Time             `json:"completed_at,omitempty"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

// Manager — потокобезопасное хранилище задач для агента.
//
// Используется для управления планом действий в ReAct цикле.
// Все методы thread-safe.
type Manager struct {
	mu     sync.RWMutex
	tasks  []Task
	nextID int
}

// NewManager создает новый пустой менеджер задач.
func NewManager() *Manager {
	return &Manager{
		tasks:  make([]Task, 0),
		nextID: 1,
	}
}

// Add добавляет новую задачу в план и возвращает её ID.
//
// Принимает опциональные метаданные для хранения дополнительной информации.
// Thread-safe метод.
func (m *Manager) Add(description string, metadata ...map[string]interface{}) int {
	m.mu.Lock()
	defer m.mu.Unlock()

	var meta map[string]interface{}
	if len(metadata) > 0 {
		meta = metadata[0]
	}

	task := Task{
		ID:          m.nextID,
		Description: description,
		Status:      StatusPending,
		CreatedAt:   time.Now(),
		Metadata:    meta,
	}

	m.tasks = append(m.tasks, task)
	m.nextID++
	return task.ID
}

// Complete отмечает задачу как выполненную.
//
// Возвращает ошибку если задача не найдена или уже имеет статус DONE/FAILED.
// Thread-safe метод.
func (m *Manager) Complete(id int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i := range m.tasks {
		if m.tasks[i].ID == id {
			if m.tasks[i].Status != StatusPending {
				return fmt.Errorf("задача %d уже выполнена или провалена", id)
			}
			m.tasks[i].Status = StatusDone
			now := time.Now()
			m.tasks[i].CompletedAt = &now
			return nil
		}
	}
	return fmt.Errorf("задача %d не найдена", id)
}

// Fail отмечает задачу как проваленную с указанием причины.
//
// Возвращает ошибку если задача не найдена или уже имеет статус DONE/FAILED.
// Причина провала сохраняется в Metadata["error"].
// Thread-safe метод.
func (m *Manager) Fail(id int, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i := range m.tasks {
		if m.tasks[i].ID == id {
			if m.tasks[i].Status != StatusPending {
				return fmt.Errorf("задача %d уже выполнена или провалена", id)
			}
			m.tasks[i].Status = StatusFailed
			if m.tasks[i].Metadata == nil {
				m.tasks[i].Metadata = make(map[string]interface{})
			}
			m.tasks[i].Metadata["error"] = reason
			return nil
		}
	}
	return fmt.Errorf("задача %d не найдена", id)
}

// Clear удаляет все задачи из плана и сбрасывает счётчик ID.
//
// Thread-safe метод.
func (m *Manager) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tasks = make([]Task, 0)
	m.nextID = 1
}

// String форматирует план задач для вставки в промпт (Context Injection).
//
// Возвращает текстовое представление плана с визуальными индикаторами статуса:
//   - [ ] для PENDING
//   - [✓] для DONE
//   - [✗] для FAILED
//
// Используется автоматически в BuildAgentContext для передачи контекста в LLM.
// Thread-safe метод.
func (m *Manager) String() string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.tasks) == 0 {
		return "Нет активных задач"
	}

	var result strings.Builder
	result.WriteString("ТЕКУЩИЙ ПЛАН:\n")

	pending := 0
	done := 0
	failed := 0

	for _, task := range m.tasks {
		status := "[ ]"
		switch task.Status {
		case StatusDone:
			status = "[✓]"
			done++
		case StatusFailed:
			status = "[✗]"
			failed++
		default:
			pending++
		}

		result.WriteString(fmt.Sprintf("%s %d. %s\n", status, task.ID, task.Description))

		if task.Status == StatusFailed && task.Metadata != nil {
			if err, ok := task.Metadata["error"].(string); ok {
				result.WriteString(fmt.Sprintf("    Ошибка: %s\n", err))
			}
		}
	}

	result.WriteString(fmt.Sprintf("\nСтатистика: %d выполнено, %d в работе, %d провалено",
		done, pending, failed))

	return result.String()
}

// GetTasks возвращает копию списка всех задач для использования в UI.
//
// Возвращает копию слайса, чтобы избежать race conditions при итерации.
// Thread-safe метод.
func (m *Manager) GetTasks() []Task {
	m.mu.RLock()
	defer m.mu.RUnlock()

	tasks := make([]Task, len(m.tasks))
	copy(tasks, m.tasks)
	return tasks
}

// GetStats возвращает статистику по задачам.
//
// Возвращает кортеж (pending, done, failed) с количеством задач в каждом статусе.
// Thread-safe метод.
func (m *Manager) GetStats() (pending, done, failed int) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, task := range m.tasks {
		switch task.Status {
		case StatusDone:
			done++
		case StatusFailed:
			failed++
		default:
			pending++
		}
	}
	return
}

```

=================

# tools/registry.go

```go
// Реестр для хранения и поиска инструментов.
package tools

import (
	"encoding/json"
	"fmt"
	"sync"
)

// Registry — потокобезопасное хранилище инструментов.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

// NewRegistry создает новый пустой реестр.
func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]Tool),
	}
}

// validateToolDefinition проверяет что ToolDefinition соответствует JSON Schema.
//
// Валидирует:
//   - Name не пустой
//   - Parameters является JSON объектом
//   - Parameters.type == "object" (или пустой объект для tools без параметров)
//   - Parameters.required является массивом строк (если присутствует)
//
// Инструменты без параметров могут иметь:
//   - parameters == nil (не рекомендуется, но допустимо для совместимости)
//   - parameters == {"type": "object", "properties": {}} (рекомендуется)
func validateToolDefinition(def ToolDefinition) error {
	// 1. Проверяем имя
	if def.Name == "" {
		return fmt.Errorf("tool name cannot be empty")
	}

	// 2. Разрешаем tools без параметров (nil или пустой объект)
	if def.Parameters == nil {
		// tools без параметров - допустимо для совместимости
		return nil
	}

	// 3. Сериализуем Parameters в JSON для проверки структуры
	paramsJSON, err := json.Marshal(def.Parameters)
	if err != nil {
		return fmt.Errorf("tool '%s': failed to marshal parameters: %w", def.Name, err)
	}

	// 4. Парсим как map[string]interface{}
	var params map[string]interface{}
	if err := json.Unmarshal(paramsJSON, &params); err != nil {
		return fmt.Errorf("tool '%s': parameters must be a JSON object, got: %s", def.Name, string(paramsJSON))
	}

	// 5. Проверяем что type == "object"
	typeVal, ok := params["type"]
	if !ok {
		return fmt.Errorf("tool '%s': parameters must have 'type' field", def.Name)
	}

	typeStr, ok := typeVal.(string)
	if !ok {
		return fmt.Errorf("tool '%s': parameters.type must be a string, got: %T", def.Name, typeVal)
	}

	if typeStr != "object" {
		return fmt.Errorf("tool '%s': parameters.type must be 'object', got: '%s'", def.Name, typeStr)
	}

	// 6. Проверяем что 'required' (если есть) является массивом строк
	if requiredVal, exists := params["required"]; exists {
		required, ok := requiredVal.([]interface{})
		if !ok {
			// Попробуем распарсить как []interface{} из JSON массива
			return fmt.Errorf("tool '%s': parameters.required must be an array", def.Name)
		}

		// Проверяем что все элементы - строки
		for i, item := range required {
			if _, ok := item.(string); !ok {
				return fmt.Errorf("tool '%s': parameters.required[%d] must be a string, got: %T", def.Name, i, item)
			}
		}
	}

	return nil
}

// Register добавляет инструмент в реестр с валидацией схемы.
//
// Возвращает ошибку если определение инструмента не валидно.
func (r *Registry) Register(tool Tool) error {
	def := tool.Definition()

	// Валидируем определение перед регистрацией
	if err := validateToolDefinition(def); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[def.Name] = tool
	return nil
}

// Get ищет инструмент по имени.
func (r *Registry) Get(name string) (Tool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	
	tool, ok := r.tools[name]
	if !ok {
		return nil, fmt.Errorf("tool '%s' not found", name)
	}
	return tool, nil
}

// GetDefinitions возвращает список всех определений для отправки в LLM.
func (r *Registry) GetDefinitions() []ToolDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()

	defs := make([]ToolDefinition, 0, len(r.tools))
	for _, t := range r.tools {
		defs = append(defs, t.Definition())
	}
	return defs
}

```

=================

# tools/std/planner.go

```go
// Package std содержит стандартные инструменты Poncho AI.
//
// Реализует инструменты управления планом действий (planner).
package std

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/todo"
	"github.com/ilkoid/poncho-ai/pkg/tools"
)

// PlannerTool — базовый тип для инструментов планировщика (не используется напрямую).
//
// Реальные инструменты реализованы как отдельные типы для каждого действия.
type PlannerTool struct {
	manager *todo.Manager
}

// NewPlannerTool создает базовый инструмент планировщика.
//
// Примечание: на практике используются конкретные инструменты (PlanAddTaskTool и т.д.).
func NewPlannerTool(manager *todo.Manager, cfg config.ToolConfig) *PlannerTool {
	return &PlannerTool{manager: manager}
}

// PlanAddTaskTool — инструмент для добавления задач в план действий.
//
// Позволяет агенту создавать новые задачи в Todo Manager.
type PlanAddTaskTool struct {
	manager     *todo.Manager
	description string
}

// NewPlanAddTaskTool создает инструмент для добавления задач.
func NewPlanAddTaskTool(manager *todo.Manager, cfg config.ToolConfig) *PlanAddTaskTool {
	return &PlanAddTaskTool{manager: manager, description: cfg.Description}
}

// Definition возвращает определение инструмента для function calling.
//
// Соответствует Tool interface (Rule 1).
func (t *PlanAddTaskTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "plan_add_task",
		Description: t.description, // Должен быть задан в config.yaml
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"description": map[string]interface{}{
					"type":        "string",
					"description": "Описание задачи для выполнения",
				},
				"metadata": map[string]interface{}{
					"type":        "object",
					"description": "Дополнительные метаданные (опционально)",
				},
			},
			"required": []string{"description"},
		},
	}
}

// Execute выполняет инструмент согласно контракту "Raw In, String Out".
//
// Принимает JSON строку с аргументами от LLM, возвращает результат выполнения.
// Соответствует Tool interface (Rule 1).
func (t *PlanAddTaskTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Description string                 `json:"description"`
		Metadata    map[string]interface{} `json:"metadata,omitempty"`
	}

	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("ошибка парсинга аргументов: %w", err)
	}

	if args.Description == "" {
		return "", fmt.Errorf("описание задачи не может быть пустым")
	}

	id := t.manager.Add(args.Description, args.Metadata)
	return fmt.Sprintf("✅ Задача добавлена в план (ID: %d): %s", id, args.Description), nil
}

// PlanMarkDoneTool — инструмент для отметки задач как выполненных.
//
// Позволяет агенту отмечать завершенные задачи в Todo Manager.
type PlanMarkDoneTool struct {
	manager     *todo.Manager
	description string
}

// NewPlanMarkDoneTool создает инструмент для отметки задач как выполненных.
func NewPlanMarkDoneTool(manager *todo.Manager, cfg config.ToolConfig) *PlanMarkDoneTool {
	return &PlanMarkDoneTool{manager: manager, description: cfg.Description}
}

// Definition возвращает определение инструмента для function calling.
func (t *PlanMarkDoneTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "plan_mark_done",
		Description: t.description, // Должен быть задан в config.yaml
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"task_id": map[string]interface{}{
					"type":        "integer",
					"description": "ID задачи для отметки выполнения",
				},
			},
			"required": []string{"task_id"},
		},
	}
}

// Execute выполняет инструмент согласно контракту "Raw In, String Out".
func (t *PlanMarkDoneTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		TaskID int `json:"task_id"`
	}

	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("ошибка парсинга аргументов: %w", err)
	}

	if err := t.manager.Complete(args.TaskID); err != nil {
		return "", fmt.Errorf("ошибка отметки задачи: %w", err)
	}

	return fmt.Sprintf("✅ Задача %d отмечена как выполненная", args.TaskID), nil
}

// PlanMarkFailedTool — инструмент для отметки задач как проваленных.
//
// Позволяет агенту отмечать задачи с указанием причины провала.
type PlanMarkFailedTool struct {
	manager     *todo.Manager
	description string
}

// NewPlanMarkFailedTool создает инструмент для отметки задач как проваленных.
func NewPlanMarkFailedTool(manager *todo.Manager, cfg config.ToolConfig) *PlanMarkFailedTool {
	return &PlanMarkFailedTool{manager: manager, description: cfg.Description}
}

// Definition возвращает определение инструмента для function calling.
func (t *PlanMarkFailedTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "plan_mark_failed",
		Description: t.description, // Должен быть задан в config.yaml
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"task_id": map[string]interface{}{
					"type":        "integer",
					"description": "ID задачи для отметки провала",
				},
				"reason": map[string]interface{}{
					"type":        "string",
					"description": "Причина провала задачи",
				},
			},
			"required": []string{"task_id", "reason"},
		},
	}
}

// Execute выполняет инструмент согласно контракту "Raw In, String Out".
func (t *PlanMarkFailedTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		TaskID int    `json:"task_id"`
		Reason string `json:"reason"`
	}

	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("ошибка парсинга аргументов: %w", err)
	}

	if err := t.manager.Fail(args.TaskID, args.Reason); err != nil {
		return "", fmt.Errorf("ошибка отметки задачи: %w", err)
	}

	return fmt.Sprintf("❌ Задача %d отмечена как проваленная: %s", args.TaskID, args.Reason), nil
}

// PlanClearTool — инструмент для очистки всего плана действий.
//
// Позволяет агенту удалять все задачи из Todo Manager.
type PlanClearTool struct {
	manager     *todo.Manager
	description string
}

// NewPlanClearTool создает инструмент для очистки плана.
func NewPlanClearTool(manager *todo.Manager, cfg config.ToolConfig) *PlanClearTool {
	return &PlanClearTool{manager: manager, description: cfg.Description}
}

// Definition возвращает определение инструмента для function calling.
func (t *PlanClearTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "plan_clear",
		Description: t.description, // Должен быть задан в config.yaml
		Parameters: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
			"required":   []string{}, // Нет параметров
		},
	}
}

// Execute выполняет инструмент согласно контракту "Raw In, String Out".
func (t *PlanClearTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	t.manager.Clear()
	return "🗑️ План действий очищен", nil
}

// PlanSetTasksTool — инструмент для создания/замены всего плана задач.
//
// Позволяет агенту создать полный план за один вызов вместо множественных plan_add_task.
type PlanSetTasksTool struct {
	manager     *todo.Manager
	description string
}

// NewPlanSetTasksTool создает инструмент для создания/замены плана.
func NewPlanSetTasksTool(manager *todo.Manager, cfg config.ToolConfig) *PlanSetTasksTool {
	return &PlanSetTasksTool{manager: manager, description: cfg.Description}
}

// Definition возвращает определение инструмента для function calling.
//
// Соответствует Tool interface (Rule 1).
func (t *PlanSetTasksTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "plan_set_tasks",
		Description: t.description, // Должен быть задан в config.yaml
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"tasks": map[string]interface{}{
					"type": "array",
					"items": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"description": map[string]interface{}{
								"type":        "string",
								"description": "Описание задачи",
							},
							"metadata": map[string]interface{}{
								"type":        "object",
								"description": "Дополнительные метаданные (опционально)",
							},
						},
						"required": []string{"description"},
					},
				},
			},
			"required": []string{"tasks"},
		},
	}
}

// Execute выполняет инструмент согласно контракту "Raw In, String Out".
//
// Принимает JSON строку с аргументами от LLM, возвращает результат выполнения.
// Очищает текущий план и создает новый из массива задач.
// Соответствует Tool interface (Rule 1).
func (t *PlanSetTasksTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Tasks []struct {
			Description string                 `json:"description"`
			Metadata    map[string]interface{} `json:"metadata,omitempty"`
		} `json:"tasks"`
	}

	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("ошибка парсинга аргументов: %w", err)
	}

	if len(args.Tasks) == 0 {
		return "", fmt.Errorf("список задач не может быть пустым")
	}

	// Очищаем текущий план
	t.manager.Clear()

	// Добавляем все задачи
	var addedIDs []int
	for _, task := range args.Tasks {
		id := t.manager.Add(task.Description, task.Metadata)
		addedIDs = append(addedIDs, id)
	}

	return fmt.Sprintf("✅ План создан (%d задач, ID: %v)", len(addedIDs), addedIDs), nil
}

// NewPlannerTools создает карту всех инструментов планировщика.
//
// Удобная функция для массовой регистрации инструментов planner'а.
// Возвращает map[string]tools.Tool, которую можно использовать
// для непосредственной регистрации в Registry.
//
// Параметры:
//   - manager: Todo Manager для управления задачами
//   - cfg: Конфигурация tools (используется для единообразия с другими tools)
func NewPlannerTools(manager *todo.Manager, cfg config.ToolConfig) map[string]tools.Tool {
	return map[string]tools.Tool{
		"plan_add_task":    NewPlanAddTaskTool(manager, cfg),
		"plan_mark_done":   NewPlanMarkDoneTool(manager, cfg),
		"plan_mark_failed": NewPlanMarkFailedTool(manager, cfg),
		"plan_clear":       NewPlanClearTool(manager, cfg),
		"plan_set_tasks":   NewPlanSetTasksTool(manager, cfg),
	}
}

```

=================

# tools/std/s3_batch.go

```go
// Package std предоставляет стандартные инструменты для Poncho AI.
package std

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/ilkoid/poncho-ai/pkg/classifier"
	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/s3storage"
	"github.com/ilkoid/poncho-ai/pkg/state"
	"github.com/ilkoid/poncho-ai/pkg/tools"
)

// === S3 Batch Tools ===

// ClassifyAndDownloadS3FilesTool — инструмент для классификации файлов артикула в S3.
//
// Списывает файлы из папки артикула в S3, классифицирует их по тегам (sketch, plm_data, marketing)
// используя glob-паттерны. Хранит только метаданные в state, контент не скачивает.
//
// Content-on-demand паттерн:
// - Изображения: vision анализ через read_s3_image (скачает + проанализирует)
// - JSON файлы: чтение через read_s3_object (скачает content)
//
// Это предотвращает разрастание контекста LLM (BuildAgentContext инжектит
// VisionDescription в каждый запрос).
//
// Rule 1: Реализует Tool interface ("Raw In, String Out")
// Rule 5: Thread-safe через CoreState
// Rule 7: Возвращает ошибки вместо panic
// Rule 11: Распространяет context.Context во все S3 вызовы
type ClassifyAndDownloadS3FilesTool struct {
	s3Client  *s3storage.Client
	state     *state.CoreState    // Rule 5: Thread-safe state management
	imageCfg  config.ImageProcConfig
	fileRules []config.FileRule
	toolCfg   config.ToolConfig
}

// NewClassifyAndDownloadS3Files создает инструмент для пакетной загрузки и классификации файлов.
//
// Rule 3: Регистрация через Registry.Register()
// Rule 5: Thread-safe через CoreState
func NewClassifyAndDownloadS3Files(
	s3Client *s3storage.Client,
	state *state.CoreState,
	imageCfg config.ImageProcConfig,
	fileRules []config.FileRule,
	toolCfg config.ToolConfig,
) *ClassifyAndDownloadS3FilesTool {
	return &ClassifyAndDownloadS3FilesTool{
		s3Client:  s3Client,
		state:     state,
		imageCfg:  imageCfg,
		fileRules: fileRules,
		toolCfg:   toolCfg,
	}
}

// Definition возвращает определение инструмента для function calling.
//
// Rule 1: Tool interface - Definition() не изменяется
func (t *ClassifyAndDownloadS3FilesTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name: "classify_and_download_s3_files",
		Description: "Списывает и классифицирует файлы артикула из S3 по тегам (sketch, plm_data, marketing). Хранит только метаданные. Для контента используй read_s3_image (images) или read_s3_object (JSON).",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"article_id": map[string]interface{}{
					"type":        "string",
					"description": "Артикул товара для загрузки файлов (например, '12345')",
				},
			},
			"required": []string{"article_id"},
		},
	}
}

// Execute выполняет загрузку и классификацию файлов артикула из S3.
//
// Rule 1: "Raw In, String Out" - получаем JSON строку, возвращаем строку
// Rule 7: Возвращаем ошибки вместо panic
// Rule 11: Распространяем context.Context во все S3 вызовы
func (t *ClassifyAndDownloadS3FilesTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	// 1. Парсим аргументы
	var args struct {
		ArticleID string `json:"article_id"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	// 2. Валидация
	if t.s3Client == nil {
		return "", fmt.Errorf("S3 client not initialized")
	}
	if args.ArticleID == "" {
		return "", fmt.Errorf("article_id is required")
	}

	// 3. Получаем список файлов из S3
	prefix := args.ArticleID + "/"
	objects, err := t.s3Client.ListFiles(ctx, prefix)
	if err != nil {
		return "", fmt.Errorf("failed to list files for article '%s': %w", args.ArticleID, err)
	}

	if len(objects) == 0 {
		return fmt.Sprintf(`{"article_id":"%s","status":"warning","message":"No files found in S3 for article %s"}`, args.ArticleID, args.ArticleID), nil
	}

	// 4. Классифицируем файлы используя classifier engine
	engine := classifier.New(t.fileRules)
	classifiedFiles, err := engine.Process(objects)
	if err != nil {
		return "", fmt.Errorf("classification failed: %w", err)
	}

	// 5. Загружаем и обрабатываем файлы
	processedCount := 0
	for _, files := range classifiedFiles {
		for i, file := range files {
			// Определяем тип файла (метаданные, без скачивания контента)
			if isImageFile(file.Filename) {
				files[i].Type = "image"
				// VisionDescription заполнится позже через read_s3_image
				processedCount++
			} else if isJSONFile(file.Filename) {
				// JSON файлы: только метаданные, контент не храним
				// иначе BuildAgentContext инжектит весь JSON в каждый запрос
				files[i].Type = "json"
				files[i].VisionDescription = fmt.Sprintf("JSON available at %s (use read_s3_object to read)", file.Key)
				processedCount++
			}
		}
	}

	// 6. Сохраняем в CoreState
	if err := t.state.SetCurrentArticle(args.ArticleID, classifiedFiles); err != nil {
		return "", fmt.Errorf("failed to store article in state: %w", err)
	}

	// 7. Формируем ответ для LLM
	summary := map[string]interface{}{
		"article_id":  args.ArticleID,
		"status":      "success",
		"message":     fmt.Sprintf("Found %d files for article %s (metadata only, content on-demand)", processedCount, args.ArticleID),
		"files":       buildTagSummary(classifiedFiles),
		"total_count": countTotalFiles(classifiedFiles),
		"next_steps": []string{
			"Use read_s3_image for vision analysis of specific images",
			"Use read_s3_object to read JSON files with PLM metadata",
		},
	}

	result, err := json.Marshal(summary)
	if err != nil {
		return "", fmt.Errorf("failed to marshal response: %w", err)
	}

	return string(result), nil
}

// AnalyzeArticleImagesBatchTool — заглушка для анализа изображений.
type AnalyzeArticleImagesBatchTool struct {
	state      interface{} // GlobalState - не используется в stub
	s3Client   *s3storage.Client
	visionLLM  interface{} // LLM Provider - не используется в stub
	visionPrompt string   // Vision system prompt - не используется в stub
	imageCfg   config.ImageProcConfig
	toolCfg    config.ToolConfig
}

// NewAnalyzeArticleImagesBatch создает заглушку для инструмента анализа изображений.
func NewAnalyzeArticleImagesBatch(
	state interface{},
	s3Client *s3storage.Client,
	visionLLM interface{},
	visionPrompt string,
	imageCfg config.ImageProcConfig,
	toolCfg config.ToolConfig,
) *AnalyzeArticleImagesBatchTool {
	return &AnalyzeArticleImagesBatchTool{
		state:        state,
		s3Client:     s3Client,
		visionLLM:    visionLLM,
		visionPrompt: visionPrompt,
		imageCfg:     imageCfg,
		toolCfg:      toolCfg,
	}
}

func (t *AnalyzeArticleImagesBatchTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name: "analyze_article_images_batch",
		Description: "Анализирует изображения из текущего артикула с помощью Vision LLM. Обрабатывает картинки параллельно в горутинах. Опционально фильтрует по тегу (sketch, plm_data, marketing). Используй это после classify_and_download_s3_files для анализа эскизов. [STUB - НЕ РЕАЛИЗОВАНО]",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"tag": map[string]interface{}{
					"type":        "string",
					"description": "Фильтр по тегу (sketch, plm_data, marketing). Если не указан - анализирует все изображения.",
					"enum":        []string{"sketch", "plm_data", "marketing", ""},
				},
			},
			"required": []string{},
		},
	}
}

func (t *AnalyzeArticleImagesBatchTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Tag string `json:"tag"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	// STUB: Возврат заглушки
	stub := map[string]interface{}{
		"error":   "not_implemented",
		"message": "analyze_article_images_batch tool is not implemented yet",
		"tag":     args.Tag,
		"results": []map[string]interface{}{
			{
				"file":     "example.jpg",
				"analysis": "STUB: vision analysis not implemented",
			},
		},
	}
	result, _ := json.Marshal(stub)
	return string(result), nil
}

// === Helper Functions ===

// isImageFile проверяет, является ли файл изображением по расширению.
func isImageFile(filename string) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".jpg", ".jpeg", ".png", ".webp":
		return true
	}
	return false
}

// isJSONFile проверяет, является ли файл JSON по расширению.
func isJSONFile(filename string) bool {
	return strings.ToLower(filepath.Ext(filename)) == ".json"
}

// buildTagSummary строит сводку по тегам для ответа LLM.
func buildTagSummary(files map[string][]*s3storage.FileMeta) map[string]interface{} {
	result := make(map[string]interface{})
	for tag, fileList := range files {
		filenames := make([]string, len(fileList))
		for i, f := range fileList {
			filenames[i] = f.Filename
		}
		result[tag] = map[string]interface{}{
			"count": len(fileList),
			"files": filenames,
		}
	}
	return result
}

// countTotalFiles подсчитывает общее количество файлов во всех тегах.
func countTotalFiles(files map[string][]*s3storage.FileMeta) int {
	total := 0
	for _, fileList := range files {
		total += len(fileList)
	}
	return total
}

```

=================

# tools/std/s3_tools.go

```go
/* инструменты для работы с S3 в пакете pkg/tools/std/
Нам понадобятся два инструмента:

list_s3_files: Аналог ls. Позволяет агенту "осмотреться" в бакете и найти нужные файлы (артикулы, документы).
read_s3_object: Аналог cat. Позволяет агенту прочитать содержимое файла (JSON текст или получить ссылку на картинку).
*/
package std

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/s3storage"
	"github.com/ilkoid/poncho-ai/pkg/tools"
	"github.com/ilkoid/poncho-ai/pkg/utils"
)

// --- Tool: list_s3_files ---
// Позволяет агенту узнать, какие файлы есть по указанному пути (префиксу).

type S3ListTool struct {
	client *s3storage.Client
}

func NewS3ListTool(c *s3storage.Client) *S3ListTool {
	return &S3ListTool{client: c}
}

func (t *S3ListTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "list_s3_files",
		Description: "Возвращает список файлов в S3 хранилище по указанному пути (префиксу). Используй это, чтобы найти артикулы или проверить наличие файлов.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"prefix": map[string]interface{}{
					"type":        "string",
					"description": "Путь к папке (например '12345/' или пусто для корня).",
				},
			},
			"required": []string{}, // Prefix is optional, but field must be present for LLM API compatibility
		},
	}
}

func (t *S3ListTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Prefix string `json:"prefix"`
	}
	// Если аргументы пустые или кривые, пробуем продолжить с дефолтом
	if argsJSON != "" {
		_ = json.Unmarshal([]byte(argsJSON), &args)
	}

	// Вызываем наш S3 клиент
	files, err := t.client.ListFiles(ctx, args.Prefix)
	if err != nil {
		return "", fmt.Errorf("s3 list error: %w", err)
	}

	// Упрощаем ответ для LLM (экономим токены)
	// Отдаем только имена и размеры, без метаданных
	type simpleFile struct {
		Key  string `json:"key"`
		Size string `json:"size"` // "10.5 KB" читаемее для LLM, чем байты
	}

	simpleList := make([]simpleFile, 0, len(files))
	for _, f := range files {
		simpleList = append(simpleList, simpleFile{
			Key:  f.Key,
			Size: formatSize(f.Size),
		})
	}

	data, err := json.Marshal(simpleList)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// --- Tool: read_s3_object ---
// Позволяет прочитать содержимое файла.
// Если это текст/JSON — возвращает текст.
// Если это картинка — возвращает сообщение, что это бинарный файл (или base64, если попросят).
// Для агента безопаснее читать только текст, а картинки обрабатывать через Vision-инструменты.

type S3ReadTool struct {
	client *s3storage.Client
}

func NewS3ReadTool(c *s3storage.Client) *S3ReadTool {
	return &S3ReadTool{client: c}
}

func (t *S3ReadTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "read_s3_object",
		Description: "Читает содержимое файла из S3. Поддерживает текстовые файлы (JSON, TXT, MD). Не используй для картинок.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"key": map[string]interface{}{
					"type":        "string",
					"description": "Полный путь к файлу (ключ), полученный из list_s3_files.",
				},
			},
			"required": []string{"key"},
		},
	}
}

func (t *S3ReadTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	// Простая защита от дурака (чтобы не качать гигабайтные видео)
	ext := strings.ToLower(filepath.Ext(args.Key))
	if isBinaryExt(ext) {
		return "", fmt.Errorf("file type '%s' is binary/image. Use specialized vision tools for images", ext)
	}

	// Скачиваем
	contentBytes, err := t.client.DownloadFile(ctx, args.Key)
	if err != nil {
		return "", fmt.Errorf("s3 download error: %w", err)
	}

	// Возвращаем как строку (предполагаем UTF-8)
	// Для plm.json не обрезаем - сохраняем полный контент для последующей обработки
	if strings.Contains(strings.ToLower(args.Key), "plm.json") {
		return string(contentBytes), nil
	}

	// Для остальных файлов ограничиваем длину, чтобы не забить контекст LLM
	const maxTextSize = 3000 // Снижено с 20KB до 3KB для экономии токенов
	if len(contentBytes) > maxTextSize {
		// Для JSON файлов добавляем предупреждение
		truncated := string(contentBytes[:maxTextSize])
		warning := "\n\n...[TRUNCATED - File too large for context. Use specific queries for partial data.]"
		if ext == ".json" {
			warning = "\n\n...[TRUNCATED - JSON file too large. Request specific fields you need.]"
		}
		return truncated + warning, nil
	}

	return string(contentBytes), nil
}

// --- Helpers ---

func formatSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

func isBinaryExt(ext string) bool {
	switch ext {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".zip", ".pdf", ".mp4":
		return true
	}
	return false
}

// --- Tool: read_s3_image ---
/*
реализуем инструмент read_image_base64 (или улучшим read_s3_object), который будет:

- Скачивать картинку.
- Ресайзить её согласно конфигу.
- Возвращать Base64 строку (готовую для отправки в Vision API).
- Для ресайза нам понадобится библиотека github.com/nfnt/resize или стандартная image.

Улучшенный s3_tools.go с поддержкой изображений: добавим новый инструмент S3ReadImageTool. Он будет специализированным.
*/

type S3ReadImageTool struct {
	client *s3storage.Client
	cfg    config.ImageProcConfig
}

func NewS3ReadImageTool(c *s3storage.Client, cfg config.ImageProcConfig) *S3ReadImageTool {
	return &S3ReadImageTool{
		client: c,
		cfg:    cfg,
	}
}

func (t *S3ReadImageTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "read_s3_image",
		Description: "Скачивает изображение из S3, оптимизирует его (resize) и возвращает в формате Base64. Используй это для Vision-анализа.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"key": map[string]interface{}{
					"type": "string",
				},
			},
			"required": []string{"key"},
		},
	}
}

func (t *S3ReadImageTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Key string `json:"key"`
	}
	_ = json.Unmarshal([]byte(argsJSON), &args)

	// 1. Проверяем расширение
	ext := strings.ToLower(filepath.Ext(args.Key))
	if !isImageExt(ext) {
		return "", fmt.Errorf("file '%s' is not an image", args.Key)
	}

	// 2. Скачиваем байты
	rawBytes, err := t.client.DownloadFile(ctx, args.Key)
	if err != nil {
		return "", err
	}

	// 3. Ресайзим (если включено в конфиге)
	if t.cfg.MaxWidth > 0 {
		rawBytes, err = utils.ResizeImage(rawBytes, t.cfg.MaxWidth, t.cfg.Quality)
		if err != nil {
			return "", fmt.Errorf("resize error: %w", err)
		}
	}

	// 4. Base64 encode
	b64 := base64.StdEncoding.EncodeToString(rawBytes)
	
	// Возвращаем как префикс Data URI (чтобы сразу вставлять в API)
	// Или просто raw base64, зависит от того, что ждет провайдер.
	// Обычно провайдеры (OpenAI) хотят data:image/jpeg;base64,...
	mimeType := "image/jpeg" // Мы конвертировали в jpeg при ресайзе
	if t.cfg.MaxWidth == 0 && ext == ".png" {
		mimeType = "image/png"
	}

	return fmt.Sprintf("data:%s;base64,%s", mimeType, b64), nil
}

func isImageExt(ext string) bool {
	switch ext {
	case ".jpg", ".jpeg", ".png", ".webp":
		return true
	}
	return false
}

// Регистрация в main.go: reg.Register(std.NewS3ReadImageTool(s3Client, cfg.ImageProcessing))

// --- Tool: get_plm_data ---
// Загружает plm.json из S3, очищает от лишних полей и возвращает sanitized JSON.
// Использует utils.SanitizePLMJson для удаления:
//   - Ответственные, Эскизы (загружаются отдельно)
//   - НомерСтроки, ИдентификаторСтроки, Статус
//   - Миниатюра_Файл (огромный base64)
//   - Пустые значения и технические поля
type GetPLMDataTool struct {
	client *s3storage.Client
}

func NewGetPLMDataTool(c *s3storage.Client) *GetPLMDataTool {
	return &GetPLMDataTool{client: c}
}

func (t *GetPLMDataTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name: "get_plm_data",
		Description: "Загружает PLM данные (plm.json) артикула из S3 и возвращает очищенный JSON без технических полей, миниатюр и эскизов. Используй для получения информации о товаре: артикул, категория, сезон, цвета, материалы, размерный ряд.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"article_id": map[string]interface{}{
					"type":        "string",
					"description": "ID артикула (например, '12611516'). Если не указан, используется текущий артикул из контекста.",
				},
			},
		},
	}
}

func (t *GetPLMDataTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		ArticleID string `json:"article_id"`
	}
	_ = json.Unmarshal([]byte(argsJSON), &args)

	// Если article_id не указан, возвращаем ошибку
	if args.ArticleID == "" {
		return "", fmt.Errorf("article_id is required. Specify the article ID (e.g., '12611516')")
	}

	// Формируем путь к plm.json
	key := fmt.Sprintf("%s/%s.json", args.ArticleID, args.ArticleID)

	// Скачиваем файл
	contentBytes, err := t.client.DownloadFile(ctx, key)
	if err != nil {
		return "", fmt.Errorf("failed to download plm.json: %w", err)
	}

	// Санитайзим JSON (удаляем лишние поля)
	sanitized, err := utils.SanitizePLMJson(string(contentBytes))
	if err != nil {
		return "", fmt.Errorf("failed to sanitize PLM JSON: %w", err)
	}

	return sanitized, nil
}


```

=================

# tools/std/wb_ad_analytics.go

```go
// Package std предоставляет инструменты для аналитики рекламы Wildberries.
//
// Реализует инструменты для анализа рекламных кампаний:
// - Статистика кампаний (показы, клики, заказы)
// - Статистика по ключевым фразам
// - Атрибуция: органика vs реклама
package std

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/tools"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// WbCampaignStatsTool — инструмент для получения статистики рекламных кампаний.
//
// Использует Promotion API: POST /adv/v2/fullstats
// Возвращает показы, клики, CTR, CPC, затраты, заказы.
type WbCampaignStatsTool struct {
	client      *wb.Client
	toolID      string
	endpoint    string
	rateLimit   int
	burst       int
	description string
}

// NewWbCampaignStatsTool создает инструмент для получения статистики кампаний.
//
// Параметры:
//   - c: экземпляр клиента Wildberries API
//   - cfg: конфигурация tool из YAML
//   - wbDefaults: дефолтные значения из wb секции config.yaml
//
// Возвращает инструмент, готовый к регистрации в реестре.
func NewWbCampaignStatsTool(c *wb.Client, cfg config.ToolConfig, wbDefaults config.WBConfig) *WbCampaignStatsTool {
	endpoint, rateLimit, burst := applyWbDefaults(cfg, wbDefaults)

	return &WbCampaignStatsTool{
		client:      c,
		toolID:      "get_wb_campaign_stats",
		endpoint:    endpoint,
		rateLimit:   rateLimit,
		burst:       burst,
		description: cfg.Description,
	}
}

// Definition возвращает определение инструмента для function calling.
func (t *WbCampaignStatsTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "get_wb_campaign_stats",
		Description: t.description,
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"advertIds": map[string]interface{}{
					"type": "array",
					"items": map[string]interface{}{"type": "integer"},
					"description": "Список ID рекламных кампаний",
				},
				"days": map[string]interface{}{
					"type":        "integer",
					"description": "Количество дней для анализа (1-90)",
				},
			},
			"required": []string{"advertIds", "days"},
		},
	}
}

// Execute выполняет инструмент согласно контракту "Raw In, String Out".
func (t *WbCampaignStatsTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		AdvertIds []int `json:"advertIds"`
		Days      int    `json:"days"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	// Валидация
	if len(args.AdvertIds) == 0 {
		return "", fmt.Errorf("advertIds cannot be empty")
	}
	if args.Days < 1 || args.Days > 90 {
		return "", fmt.Errorf("days must be between 1 and 90")
	}

	// Mock режим для demo ключа
	if t.client.IsDemoKey() {
		return t.executeMock(args.AdvertIds, args.Days)
	}

	// Формируем даты
	now := time.Now()
	dates := make([]string, 0, args.Days)
	for i := args.Days - 1; i >= 0; i-- {
		dates = append(dates, now.AddDate(0, 0, -i).Format("2006-01-02"))
	}

	// Формируем запрос к WB API
	reqBody := map[string]interface{}{
		" adverIds": args.AdvertIds,
		"dates":     dates,
	}

	var response []struct {
		AdvertID    int     `json:"advertId"`
		Views       int     `json:"views"`
		Clicks      int     `json:"clicks"`
		CTR         float64 `json:"ctr"`
		CPC         int     `json:"cpc"`
		Sum         float64 `json:"sum"`      // Затраты
		Orders      int     `json:"orders"`
		AtmSales    int     `json:"atmSales"` // Продажи в автоматических кампаниях
		ShksSales   int     `json:"shksSales"` // Продажи в кампаниях с ручным управлением
	}

	err := t.client.Post(ctx, t.toolID, t.endpoint, t.rateLimit, t.burst,
		"/adv/v2/fullstats", reqBody, &response)
	if err != nil {
		return "", fmt.Errorf("failed to get campaign stats: %w", err)
	}

	// Форматируем ответ для LLM
	result, _ := json.Marshal(response)
	return string(result), nil
}

// executeMock возвращает mock данные для demo режима.
func (t *WbCampaignStatsTool) executeMock(advertIds []int, days int) (string, error) {
	now := time.Now()

	results := make([]map[string]interface{}, 0, len(advertIds))

	for _, advertID := range advertIds {
		views := 5000 + advertID*100
		clicks := views / 20 // CTR ~5%
		sum := float64(clicks) * 5.0 // CPC ~5 руб

		results = append(results, map[string]interface{}{
			"advertId":  advertID,
			"views":     views,
			"clicks":    clicks,
			"ctr":       float64(clicks) / float64(views) * 100,
			"cpc":       5,
			"sum":       sum,
			"orders":    clicks / 10,
			"atmSales":  clicks / 15,
			"shksSales": clicks / 30,
			"period": map[string]interface{}{
				"begin": now.AddDate(0, 0, -days).Format("2006-01-02"),
				"end":   now.Format("2006-01-02"),
				"days":  days,
			},
			"mock": true,
		})
	}

	result, _ := json.Marshal(results)
	return string(result), nil
}

// WbKeywordStatsTool — инструмент для получения статистики по ключевым фразам.
//
// Использует Promotion API: GET /adv/v0/stats/keywords
// Возвращает статистику по фразам кампании (за 7 дней).
type WbKeywordStatsTool struct {
	client      *wb.Client
	toolID      string
	endpoint    string
	rateLimit   int
	burst       int
	description string
}

// NewWbKeywordStatsTool создает инструмент для получения статистики по фразам.
//
// Параметры:
//   - c: экземпляр клиента Wildberries API
//   - cfg: конфигурация tool из YAML
//   - wbDefaults: дефолтные значения из wb секции config.yaml
//
// Возвращает инструмент, готовый к регистрации в реестре.
func NewWbKeywordStatsTool(c *wb.Client, cfg config.ToolConfig, wbDefaults config.WBConfig) *WbKeywordStatsTool {
	endpoint, rateLimit, burst := applyWbDefaults(cfg, wbDefaults)

	return &WbKeywordStatsTool{
		client:      c,
		toolID:      "get_wb_keyword_stats",
		endpoint:    endpoint,
		rateLimit:   rateLimit,
		burst:       burst,
		description: cfg.Description,
	}
}

// Definition возвращает определение инструмента для function calling.
func (t *WbKeywordStatsTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "get_wb_keyword_stats",
		Description: t.description,
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"advertId": map[string]interface{}{
					"type":        "integer",
					"description": "ID рекламной кампании",
				},
				"days": map[string]interface{}{
					"type":        "integer",
					"description": "Количество дней для анализа (1-7, максимум 7 дней)",
				},
			},
			"required": []string{"advertId", "days"},
		},
	}
}

// Execute выполняет инструмент согласно контракту "Raw In, String Out".
func (t *WbKeywordStatsTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		AdvertID int `json:"advertId"`
		Days     int `json:"days"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	// Валидация
	if args.AdvertID <= 0 {
		return "", fmt.Errorf("advertId must be positive")
	}
	if args.Days < 1 || args.Days > 7 {
		return "", fmt.Errorf("days must be between 1 and 7 (API limit)")
	}

	// Mock режим для demo ключа
	if t.client.IsDemoKey() {
		return t.executeMock(args.AdvertID, args.Days)
	}

	// Формируем запрос к WB API (GET запрос)
	// Примечание: WB API возвращает данные за последние 7 дней
	var response []struct {
		Keyword      string  `json:"keyword"`
		Views        int     `json:"views"`
		Clicks       int     `json:"clicks"`
		CTR          float64 `json:"ctr"`
		CPC          int     `json:"cpc"`
		Sum          float64 `json:"sum"`
		Orders       int     `json:"orders"`
		Conversions  float64 `json:"conversions"` // Конверсия в заказ
	}

	// Для GET запросов используем метод Get
	err := t.client.Get(ctx, t.toolID, t.endpoint, t.rateLimit, t.burst,
		"/adv/v0/stats/keywords", nil, &response)
	if err != nil {
		return "", fmt.Errorf("failed to get keyword stats: %w", err)
	}

	// Форматируем ответ для LLM
	result, _ := json.Marshal(response)
	return string(result), nil
}

// executeMock возвращает mock данные для demo режима.
func (t *WbKeywordStatsTool) executeMock(advertID int, days int) (string, error) {
	now := time.Now()

	// Mock ключевые слова
	mockKeywords := []string{
		"платье женское", "вечернее платье", "платье черное",
		"платье длинное", "платье летнее", "сарафан",
		"платье коктейльное", "платье офисное", "платье красное",
		"платье с рукавами",
	}

	results := make([]map[string]interface{}, 0, len(mockKeywords))

	for _, kw := range mockKeywords {
		views := 500 + (len(kw) * 10)
		clicks := views / 25
		sum := float64(clicks) * 6.0

		results = append(results, map[string]interface{}{
			"keyword":     kw,
			"views":       views,
			"clicks":      clicks,
			"ctr":         float64(clicks) / float64(views) * 100,
			"cpc":         6,
			"sum":         sum,
			"orders":      clicks / 8,
			"conversions": float64(clicks/8) / float64(clicks) * 100,
			"period": map[string]interface{}{
				"begin": now.AddDate(0, 0, -days).Format("2006-01-02"),
				"end":   now.Format("2006-01-02"),
				"days":  days,
			},
			"mock": true,
		})
	}

	result, _ := json.Marshal(results)
	return string(result), nil
}

// WbAttributionSummaryTool — инструмент для атрибуции органика vs реклама.
//
// Это агрегатор, который объединяет данные из:
// - get_wb_product_funnel (общая статистика)
// - get_wb_campaign_stats (статистика рекламных кампаний)
type WbAttributionSummaryTool struct {
	funnelTool  *WbProductFunnelTool
	campaignTool *WbCampaignStatsTool
	description  string
}

// NewWbAttributionSummaryTool создает инструмент для атрибуции.
//
// Параметры:
//   - c: экземпляр клиента Wildberries API
//   - cfg: конфигурация tool из YAML
//   - wbDefaults: дефолтные значения из wb секции config.yaml
//
// Возвращает инструмент, готовый к регистрации в реестре.
func NewWbAttributionSummaryTool(c *wb.Client, cfg config.ToolConfig, wbDefaults config.WBConfig) *WbAttributionSummaryTool {
	return &WbAttributionSummaryTool{
		funnelTool:   NewWbProductFunnelTool(c, cfg, wbDefaults),
		campaignTool: NewWbCampaignStatsTool(c, cfg, wbDefaults),
		description:  cfg.Description,
	}
}

// Definition возвращает определение инструмента для function calling.
func (t *WbAttributionSummaryTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "get_wb_attribution_summary",
		Description: t.description,
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"nmIDs": map[string]interface{}{
					"type": "array",
					"items": map[string]interface{}{"type": "integer"},
					"description": "Список nmID товаров (макс. 100)",
				},
				"advertIds": map[string]interface{}{
					"type": "array",
					"items": map[string]interface{}{"type": "integer"},
					"description": "Список ID рекламных кампаний для атрибуции",
				},
				"days": map[string]interface{}{
					"type":        "integer",
					"description": "Количество дней для анализа (1-90)",
				},
			},
			"required": []string{"nmIDs", "advertIds", "days"},
		},
	}
}

// Execute выполняет инструмент согласно контракту "Raw In, String Out".
func (t *WbAttributionSummaryTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		NmIDs     []int `json:"nmIDs"`
		AdvertIds []int `json:"advertIds"`
		Days      int    `json:"days"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	// Валидация
	if len(args.NmIDs) == 0 {
		return "", fmt.Errorf("nmIDs cannot be empty")
	}
	if len(args.AdvertIds) == 0 {
		return "", fmt.Errorf("advertIds cannot be empty")
	}
	if args.Days < 1 || args.Days > 90 {
		return "", fmt.Errorf("days must be between 1 and 90")
	}

	// Получаем данные воронки продаж
	funnelArgs, _ := json.Marshal(map[string]interface{}{
		"nmIDs": args.NmIDs,
		"days":  args.Days,
	})
	funnelData, err := t.funnelTool.Execute(ctx, string(funnelArgs))
	if err != nil {
		return "", fmt.Errorf("failed to get funnel data: %w", err)
	}

	var funnelResponse struct {
		Data struct {
			Cards []struct {
				NMID  int `json:"nmID"`
				Statistics struct {
					SelectedPeriod struct {
						OpenCardCount  int `json:"openCardCount"`
						AddToCartCount int `json:"addToCartCount"`
						OrdersCount    int `json:"ordersCount"`
					} `json:"selectedPeriod"`
				} `json:"statistics"`
			} `json:"cards"`
		} `json:"data"`
	}

	if err := json.Unmarshal([]byte(funnelData), &funnelResponse); err != nil {
		return "", fmt.Errorf("failed to parse funnel response: %w", err)
	}

	// Получаем данные рекламных кампаний
	campaignArgs, _ := json.Marshal(map[string]interface{}{
		"advertIds": args.AdvertIds,
		"days":      args.Days,
	})
	campaignData, err := t.campaignTool.Execute(ctx, string(campaignArgs))
	if err != nil {
		// Если не удалось получить данные кампаний, продолжаем без них
		campaignData = "[]"
	}

	var campaignResponse []struct {
		AdvertID int     `json:"advertId"`
		Views    int     `json:"views"`
		Clicks   int     `json:"clicks"`
		Sum      float64 `json:"sum"`
		Orders   int     `json:"orders"`
	}

	json.Unmarshal([]byte(campaignData), &campaignResponse)

	// Агрегируем данные по товарам
	now := time.Now()
	begin := now.AddDate(0, 0, -args.Days)

	results := make([]map[string]interface{}, 0, len(args.NmIDs))

	// Считаем общие показатели по рекламе
	var totalAdViews, totalAdOrders int
	var totalAdSpent float64
	campaignMap := make(map[int]int) // advertID → orders

	for _, c := range campaignResponse {
		totalAdViews += c.Views
		totalAdOrders += c.Orders
		totalAdSpent += c.Sum
		campaignMap[c.AdvertID] += c.Orders
	}

	// Формируем результат по каждому товару
	for _, card := range funnelResponse.Data.Cards {
		totalViews := card.Statistics.SelectedPeriod.OpenCardCount
		totalOrders := card.Statistics.SelectedPeriod.OrdersCount

		// Предполагаем что все рекламные просмотры относятся к этим товарам
		// Это упрощение - в реальности нужно точное распределение
		organicViews := totalViews - totalAdViews
		if organicViews < 0 {
			organicViews = 0
		}

		organicOrders := totalOrders - totalAdOrders
		if organicOrders < 0 {
			organicOrders = 0
		}

		// Формируем распределение по кампаниям
		byCampaign := make([]map[string]interface{}, 0, len(campaignResponse))
		for _, c := range campaignResponse {
			byCampaign = append(byCampaign, map[string]interface{}{
				"advertId": c.AdvertID,
				"orders":   campaignMap[c.AdvertID],
				"spent":    c.Sum,
			})
		}

		results = append(results, map[string]interface{}{
			"nmID": card.NMID,
			"period": map[string]string{
				"begin": begin.Format("2006-01-02"),
				"end":   now.Format("2006-01-02"),
			},
			"summary": map[string]interface{}{
				"totalViews":   totalViews,
				"organicViews": organicViews,
				"adViews":      totalAdViews,
				"totalOrders":  totalOrders,
				"organicOrders": organicOrders,
				"adOrders":     totalAdOrders,
			},
			"byCampaign": byCampaign,
		})
	}

	data, _ := json.Marshal(results)
	return string(data), nil
}

```

=================

# tools/std/wb_analytics.go

```go
// Package std предоставляет инструменты для аналитики Wildberries.
//
// Реализует инструменты для получения статистики товаров:
// - Воронка продаж (просмотры → корзина → заказ)
// - История по дням
package std

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/tools"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// WbProductFunnelTool — инструмент для получения воронки продаж товаров.
//
// Использует Analytics API: POST /api/v2/nm-report/detail
// Возвращает просмотры, добавления в корзину, заказы и конверсии.
type WbProductFunnelTool struct {
	client      *wb.Client
	toolID      string
	endpoint    string
	rateLimit   int
	burst       int
	description string
}

// NewWbProductFunnelTool создает инструмент для получения воронки продаж.
//
// Параметры:
//   - c: экземпляр клиента Wildberries API
//   - cfg: конфигурация tool из YAML
//   - wbDefaults: дефолтные значения из wb секции config.yaml
//
// Возвращает инструмент, готовый к регистрации в реестре.
func NewWbProductFunnelTool(c *wb.Client, cfg config.ToolConfig, wbDefaults config.WBConfig) *WbProductFunnelTool {
	endpoint, rateLimit, burst := applyWbDefaults(cfg, wbDefaults)

	return &WbProductFunnelTool{
		client:      c,
		toolID:      "get_wb_product_funnel",
		endpoint:    endpoint,
		rateLimit:   rateLimit,
		burst:       burst,
		description: cfg.Description,
	}
}

// Definition возвращает определение инструмента для function calling.
func (t *WbProductFunnelTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "get_wb_product_funnel",
		Description: t.description,
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"nmIDs": map[string]interface{}{
					"type": "array",
					"items": map[string]interface{}{"type": "integer"},
					"description": "Список nmID товаров (макс. 100)",
				},
				"days": map[string]interface{}{
					"type":        "integer",
					"description": "Количество дней для анализа (1-365)",
				},
			},
			"required": []string{"nmIDs", "days"},
		},
	}
}

// Execute выполняет инструмент согласно контракту "Raw In, String Out".
func (t *WbProductFunnelTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		NmIDs []int `json:"nmIDs"`
		Days  int    `json:"days"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	// Валидация
	if len(args.NmIDs) == 0 {
		return "", fmt.Errorf("nmIDs cannot be empty")
	}
	if len(args.NmIDs) > 100 {
		return "", fmt.Errorf("nmIDs cannot exceed 100 items")
	}
	if args.Days < 1 || args.Days > 365 {
		return "", fmt.Errorf("days must be between 1 and 365")
	}

	// Mock режим для demo ключа
	if t.client.IsDemoKey() {
		return t.executeMock(args.NmIDs, args.Days)
	}

	// Формируем период
	now := time.Now()
	begin := now.AddDate(0, 0, -args.Days)

	// Формируем запрос к WB API
	reqBody := map[string]interface{}{
		"nmIDs": args.NmIDs,
		"period": map[string]string{
			"begin": begin.Format("2006-01-02"),
			"end":   now.Format("2006-01-02"),
		},
		"page": 1,
	}

	var response struct {
		Data struct {
			Cards []struct {
				NMID  int `json:"nmID"`
				Statistics struct {
					SelectedPeriod struct {
						OpenCardCount  int  `json:"openCardCount"`
						AddToCartCount int  `json:"addToCartCount"`
						OrdersCount    int  `json:"ordersCount"`
						Conversions    struct {
							AddToCartPercent     float64 `json:"addToCartPercent"`
							CartToOrderPercent   float64 `json:"cartToOrderPercent"`
							OpenCardToOrderPercent float64 `json:"openCardToOrderPercent"`
						} `json:"conversions"`
					} `json:"selectedPeriod"`
				} `json:"statistics"`
			} `json:"cards"`
		} `json:"data"`
	}

	err := t.client.Post(ctx, t.toolID, t.endpoint, t.rateLimit, t.burst,
		"/api/v2/nm-report/detail", reqBody, &response)
	if err != nil {
		return "", fmt.Errorf("failed to get product funnel: %w", err)
	}

	// Форматируем ответ для LLM
	result, _ := json.Marshal(response.Data.Cards)
	return string(result), nil
}

// executeMock возвращает mock данные для demo режима.
func (t *WbProductFunnelTool) executeMock(nmIDs []int, days int) (string, error) {
	mockCards := make([]map[string]interface{}, 0, len(nmIDs))

	now := time.Now()
	begin := now.AddDate(0, 0, -days)

	for _, nmID := range nmIDs {
		views := 1000 + (nmID % 500)
		addToCart := views / 10
		orders := addToCart / 5

		mockCards = append(mockCards, map[string]interface{}{
			"nmID": nmID,
			"period": map[string]string{
				"begin": begin.Format("2006-01-02"),
				"end":   now.Format("2006-01-02"),
			},
			"funnel": map[string]interface{}{
				"views":      views,
				"addToCart":  addToCart,
				"orders":     orders,
				"conversions": map[string]interface{}{
					"toCartPercent":   float64(addToCart) / float64(views) * 100,
					"toOrderPercent":  float64(orders) / float64(addToCart) * 100,
				},
			},
			"mock": true,
		})
	}

	result, _ := json.Marshal(mockCards)
	return string(result), nil
}

// WbProductFunnelHistoryTool — инструмент для получения истории воронки по дням.
//
// Использует Analytics API: POST /api/v2/nm-report/detail/history
// Возвращает историю по дням (до 7 дней бесплатно).
type WbProductFunnelHistoryTool struct {
	client      *wb.Client
	toolID      string
	endpoint    string
	rateLimit   int
	burst       int
	description string
}

// NewWbProductFunnelHistoryTool создает инструмент для получения истории по дням.
//
// Параметры:
//   - c: экземпляр клиента Wildberries API
//   - cfg: конфигурация tool из YAML
//   - wbDefaults: дефолтные значения из wb секции config.yaml
//
// Возвращает инструмент, готовый к регистрации в реестре.
func NewWbProductFunnelHistoryTool(c *wb.Client, cfg config.ToolConfig, wbDefaults config.WBConfig) *WbProductFunnelHistoryTool {
	endpoint, rateLimit, burst := applyWbDefaults(cfg, wbDefaults)

	return &WbProductFunnelHistoryTool{
		client:      c,
		toolID:      "get_wb_product_funnel_history",
		endpoint:    endpoint,
		rateLimit:   rateLimit,
		burst:       burst,
		description: cfg.Description,
	}
}

// Definition возвращает определение инструмента для function calling.
func (t *WbProductFunnelHistoryTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "get_wb_product_funnel_history",
		Description: t.description,
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"nmIDs": map[string]interface{}{
					"type": "array",
					"items": map[string]interface{}{"type": "integer"},
					"description": "Список nmID товаров (макс. 100)",
				},
				"days": map[string]interface{}{
					"type":        "integer",
					"description": "Количество дней для истории (1-7 бесплатно, до 365 с подпиской Джем)",
				},
			},
			"required": []string{"nmIDs", "days"},
		},
	}
}

// Execute выполняет инструмент согласно контракту "Raw In, String Out".
func (t *WbProductFunnelHistoryTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		NmIDs []int `json:"nmIDs"`
		Days  int    `json:"days"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	// Валидация
	if len(args.NmIDs) == 0 {
		return "", fmt.Errorf("nmIDs cannot be empty")
	}
	if len(args.NmIDs) > 100 {
		return "", fmt.Errorf("nmIDs cannot exceed 100 items")
	}
	if args.Days < 1 || args.Days > 365 {
		return "", fmt.Errorf("days must be between 1 and 365")
	}

	// Mock режим для demo ключа
	if t.client.IsDemoKey() {
		return t.executeMock(args.NmIDs, args.Days)
	}

	// Формируем период
	now := time.Now()
	begin := now.AddDate(0, 0, -args.Days)

	// Формируем запрос к WB API
	reqBody := map[string]interface{}{
		"nmIDs": args.NmIDs,
		"period": map[string]string{
			"begin": begin.Format("2006-01-02"),
			"end":   now.Format("2006-01-02"),
		},
		"page": 1,
	}

	var response struct {
		Data struct {
			Cards []struct {
				NMID  int `json:"nmID"`
				Statistics []struct {
					Date           string `json:"date"`
					OpenCardCount  int    `json:"openCardCount"`
					AddToCartCount int    `json:"addToCartCount"`
					OrdersCount    int    `json:"ordersCount"`
					Conversions    struct {
						AddToCartPercent     float64 `json:"addToCartPercent"`
						CartToOrderPercent   float64 `json:"cartToOrderPercent"`
						OpenCardToOrderPercent float64 `json:"openCardToOrderPercent"`
					} `json:"conversions"`
				} `json:"statistics"`
			} `json:"cards"`
		} `json:"data"`
	}

	err := t.client.Post(ctx, t.toolID, t.endpoint, t.rateLimit, t.burst,
		"/api/v2/nm-report/detail/history", reqBody, &response)
	if err != nil {
		return "", fmt.Errorf("failed to get product funnel history: %w", err)
	}

	// Форматируем ответ для LLM
	result, _ := json.Marshal(response.Data.Cards)
	return string(result), nil
}

// executeMock возвращает mock данные для demo режима.
func (t *WbProductFunnelHistoryTool) executeMock(nmIDs []int, days int) (string, error) {
	mockCards := make([]map[string]interface{}, 0, len(nmIDs))
	now := time.Now()

	for _, nmID := range nmIDs {
		history := make([]map[string]interface{}, 0, days)

		for i := days - 1; i >= 0; i-- {
			date := now.AddDate(0, 0, -i)
			views := 100 + (nmID % 50) + i*10
			addToCart := views / 10
			orders := addToCart / 5

			history = append(history, map[string]interface{}{
				"date":           date.Format("2006-01-02"),
				"openCardCount":  views,
				"addToCartCount": addToCart,
				"ordersCount":    orders,
				"conversions": map[string]interface{}{
					"addToCartPercent":     float64(addToCart) / float64(views) * 100,
					"cartToOrderPercent":   float64(orders) / float64(addToCart) * 100,
					"openCardToOrderPercent": float64(orders) / float64(views) * 100,
				},
			})
		}

		mockCards = append(mockCards, map[string]interface{}{
			"nmID":       nmID,
			"statistics": history,
			"mock":       true,
		})
	}

	result, _ := json.Marshal(mockCards)
	return string(result), nil
}

```

=================

# tools/std/wb_catalog.go

```go
// Package std содержит стандартные инструменты для работы с Wildberries.
//
// Реализует инструменты для получения каталога категорий и предметов.
package std

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/tools"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// WbParentCategoriesTool — инструмент для получения родительских категорий Wildberries.
//
// Позволяет агенту получить список верхнеуровневых категорий (Женщинам, Мужчинам, и т.д.).
type WbParentCategoriesTool struct {
	client      *wb.Client
	toolID      string
	endpoint    string
	rateLimit   int
	burst       int
	description string // Описание из YAML конфигурации
}

// NewWbParentCategoriesTool создает инструмент для получения родительских категорий.
//
// Параметры:
//   - c: экземпляр клиента Wildberries API
//   - cfg: конфигурация tool из YAML
//   - wbDefaults: дефолтные значения из wb секции config.yaml
//
// Возвращает инструмент, готовый к регистрации в реестре.
func NewWbParentCategoriesTool(c *wb.Client, cfg config.ToolConfig, wbDefaults config.WBConfig) *WbParentCategoriesTool {
	endpoint, rateLimit, burst := applyWbDefaults(cfg, wbDefaults)

	return &WbParentCategoriesTool{
		client:      c,
		toolID:      "get_wb_parent_categories",
		endpoint:    endpoint,
		rateLimit:   rateLimit,
		burst:       burst,
		description: cfg.Description,
	}
}

// Definition возвращает определение инструмента для function calling.
func (t *WbParentCategoriesTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "get_wb_parent_categories",
		Description: t.description, // Должен быть задан в config.yaml
		Parameters: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
			"required":   []string{}, // Нет обязательных параметров
		},
	}
}

// Execute выполняет инструмент согласно контракту "Raw In, String Out".
func (t *WbParentCategoriesTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	// Mock режим для demo ключа
	if t.client.IsDemoKey() {
		return t.executeMock()
	}

	// Передаем параметры из tool config в client
	cats, err := t.client.GetParentCategories(ctx, t.endpoint, t.rateLimit, t.burst)
	if err != nil {
		return "", fmt.Errorf("failed to get parent categories: %w", err)
	}

	data, err := json.Marshal(cats)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// executeMock возвращает mock данные с реальной структурой WB API.
func (t *WbParentCategoriesTool) executeMock() (string, error) {
	mockCats := []map[string]interface{}{
		{"id": 1541, "name": "Женщинам", "isVisible": true},
		{"id": 1542, "name": "Мужчинам", "isVisible": true},
		{"id": 1543, "name": "Детям", "isVisible": true},
		{"id": 1544, "name": "Обувь", "isVisible": true},
		{"id": 1545, "name": "Аксессуары", "isVisible": true},
		{"id": 1546, "name": "Белье", "isVisible": true},
		{"id": 1547, "name": "Красота", "isVisible": true},
		{"id": 1525, "name": "Дом", "isVisible": true},
		{"id": 1534, "name": "Электроника", "isVisible": true},
	}

	data, err := json.Marshal(mockCats)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// WbSubjectsTool — инструмент для получения предметов (подкатегорий) Wildberries.
//
// Позволяет агенту получить список подкатегорий для заданной родительской категории.
type WbSubjectsTool struct {
	client      *wb.Client
	toolID      string
	endpoint    string
	rateLimit   int
	burst       int
	description string
}

// NewWbSubjectsTool создает инструмент для получения предметов (подкатегорий).
//
// Параметры:
//   - c: экземпляр клиента Wildberries API
//   - cfg: конфигурация tool из YAML
//   - wbDefaults: дефолтные значения из wb секции config.yaml
//
// Возвращает инструмент, готовый к регистрации в реестре.
func NewWbSubjectsTool(c *wb.Client, cfg config.ToolConfig, wbDefaults config.WBConfig) *WbSubjectsTool {
	endpoint, rateLimit, burst := applyWbDefaults(cfg, wbDefaults)

	return &WbSubjectsTool{
		client:      c,
		toolID:      "get_wb_subjects",
		endpoint:    endpoint,
		rateLimit:   rateLimit,
		burst:       burst,
		description: cfg.Description,
	}
}

// Definition возвращает определение инструмента для function calling.
func (t *WbSubjectsTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "get_wb_subjects",
		Description: t.description, // Должен быть задан в config.yaml
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"parentID": map[string]interface{}{
					"type":        "integer",
					"description": "ID родительской категории (получи его из get_wb_parent_categories)",
				},
			},
			"required": []string{"parentID"},
		},
	}
}

// Execute выполняет инструмент согласно контракту "Raw In, String Out".
func (t *WbSubjectsTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		ParentID int `json:"parentID"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments json: %w", err)
	}

	// Mock режим для demo ключа
	if t.client.IsDemoKey() {
		return t.executeMock(args.ParentID)
	}

	// Передаем параметры из tool config в client
	subjects, err := t.client.GetAllSubjectsLazy(ctx, t.endpoint, t.rateLimit, t.burst, args.ParentID)
	if err != nil {
		return "", fmt.Errorf("failed to get subjects: %w", err)
	}

	data, err := json.Marshal(subjects)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// executeMock возвращает mock данные с реальной структурой WB API для subjects.
func (t *WbSubjectsTool) executeMock(parentID int) (string, error) {
	// Определяем родительскую категорию для более реалистичного mock
	var parentName string
	var mockSubjects []map[string]interface{}

	switch parentID {
	case 1541: // Женщинам
		parentName = "Женщинам"
		mockSubjects = []map[string]interface{}{
			{"id": 685, "name": "Платья", "objectName": "Платье", "hasParent": true, "parentID": parentID, "parentName": parentName},
			{"id": 1031, "name": "Блузки и рубашки", "objectName": "Блузка", "hasParent": true, "parentID": parentID, "parentName": parentName},
			{"id": 1571, "name": "Брюки и джинсы", "objectName": "Брюки", "hasParent": true, "parentID": parentID, "parentName": parentName},
			{"id": 1727, "name": "Юбки", "objectName": "Юбка", "hasParent": true, "parentID": parentID, "parentName": parentName},
			{"id": 1335, "name": "Костюмы", "objectName": "Костюм", "hasParent": true, "parentID": parentID, "parentName": parentName},
		}
	case 1542: // Мужчинам
		parentName = "Мужчинам"
		mockSubjects = []map[string]interface{}{
			{"id": 1171, "name": "Футболки и майки", "objectName": "Футболка", "hasParent": true, "parentID": parentID, "parentName": parentName},
			{"id": 1177, "name": "Рубашки", "objectName": "Рубашка", "hasParent": true, "parentID": parentID, "parentName": parentName},
			{"id": 1350, "name": "Брюки", "objectName": "Брюки", "hasParent": true, "parentID": parentID, "parentName": parentName},
			{"id": 1349, "name": "Джинсы", "objectName": "Джинсы", "hasParent": true, "parentID": parentID, "parentName": parentName},
			{"id": 1467, "name": "Пиджаки и жакеты", "objectName": "Пиджак", "hasParent": true, "parentID": parentID, "parentName": parentName},
		}
	case 1543: // Детям
		parentName = "Детям"
		mockSubjects = []map[string]interface{}{
			{"id": 1479, "name": "Для девочек", "objectName": "Платье", "hasParent": true, "parentID": parentID, "parentName": parentName},
			{"id": 1480, "name": "Для мальчиков", "objectName": "Брюки", "hasParent": true, "parentID": parentID, "parentName": parentName},
			{"id": 1481, "name": "Для новорожденных", "objectName": "Комбинезон", "hasParent": true, "parentID": parentID, "parentName": parentName},
		}
	case 1544: // Обувь
		parentName = "Обувь"
		mockSubjects = []map[string]interface{}{
			{"id": 642, "name": "Женская обувь", "objectName": "Босоножки", "hasParent": true, "parentID": parentID, "parentName": parentName},
			{"id": 643, "name": "Мужская обувь", "objectName": "Кроссовки", "hasParent": true, "parentID": parentID, "parentName": parentName},
			{"id": 644, "name": "Детская обувь", "objectName": "Ботинки", "hasParent": true, "parentID": parentID, "parentName": parentName},
		}
	default:
		// Для неизвестного parentID возвращаем общие данные
		parentName = "Unknown"
		mockSubjects = []map[string]interface{}{
			{"id": 1, "name": "Категория 1", "objectName": "Объект 1", "hasParent": true, "parentID": parentID, "parentName": parentName},
			{"id": 2, "name": "Категория 2", "objectName": "Объект 2", "hasParent": true, "parentID": parentID, "parentName": parentName},
		}
	}

	data, err := json.Marshal(mockSubjects)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// WbPingTool — инструмент для проверки доступности Wildberries API.
//
// Позволяет агенту проверить, доступен ли WB Content API и валиден ли API ключ.
// Возвращает детальную диагностику: статус сервиса, timestamp, тип ошибки.
type WbPingTool struct {
	client      *wb.Client
	toolID      string
	endpoint    string
	rateLimit   int
	burst       int
	description string
}

// NewWbPingTool создает инструмент для проверки доступности WB API.
//
// Параметры:
//   - c: экземпляр клиента Wildberries API
//   - cfg: конфигурация tool из YAML
//   - wbDefaults: дефолтные значения из wb секции config.yaml
//
// Возвращает инструмент, готовый к регистрации в реестре.
func NewWbPingTool(c *wb.Client, cfg config.ToolConfig, wbDefaults config.WBConfig) *WbPingTool {
	endpoint, rateLimit, burst := applyWbDefaults(cfg, wbDefaults)

	return &WbPingTool{
		client:      c,
		toolID:      "ping_wb_api",
		endpoint:    endpoint,
		rateLimit:   rateLimit,
		burst:       burst,
		description: cfg.Description,
	}
}

// Definition возвращает определение инструмента для function calling.
func (t *WbPingTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "ping_wb_api",
		Description: t.description, // Должен быть задан в config.yaml
		Parameters: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
			"required":   []string{}, // Нет обязательных параметров
		},
	}
}

// Execute выполняет инструмент согласно контракту "Raw In, String Out".
func (t *WbPingTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	// Mock режим для demo ключа
	if t.client.IsDemoKey() {
		return t.executeMock()
	}

	// Передаем параметры из tool config в client
	resp, err := t.client.Ping(ctx, t.endpoint, t.rateLimit, t.burst)

	// Формируем развернутый ответ для LLM
	result := map[string]interface{}{
		"available": err == nil,
	}

	if err != nil {
		errType := t.client.ClassifyError(err)
		result["error"] = err.Error()
		result["error_type"] = errType.String()
		result["message"] = errType.HumanMessage()
	} else {
		result["status"] = resp.Status
		result["timestamp"] = resp.TS
		result["message"] = "Wildberries Content API доступен и работает корректно."
	}

	data, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// executeMock возвращает mock данные для ping (demo режим работает).
func (t *WbPingTool) executeMock() (string, error) {
	result := map[string]interface{}{
		"available": true,
		"status":    "OK",
		"timestamp": "2026-01-01T12:00:00Z",
		"message":   "Demo режим: Wildberries Content API имитирует доступность. Для реальных запросов установите WB_API_KEY.",
	}

	data, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

```

=================

# tools/std/wb_characteristics.go

```go
// Package std содержит инструменты для работы с характеристиками и справочниками Wildberries.
//
// Реализует инструменты для получения характеристик предметов, кодов ТНВЭД и брендов.
package std

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/tools"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// WbSubjectsByNameTool — инструмент для поиска предметов по подстроке.
//
// Позволяет агенту найти предмет по названию (без учета регистра).
type WbSubjectsByNameTool struct {
	client      *wb.Client
	toolID      string
	endpoint    string
	rateLimit   int
	burst       int
	description string
}

// NewWbSubjectsByNameTool создает инструмент для поиска предметов по имени.
//
// Параметры:
//   - c: экземпляр клиента Wildberries API
//   - cfg: конфигурация tool из YAML
//   - wbDefaults: дефолтные значения из wb секции config.yaml
//
// Возвращает инструмент, готовый к регистрации в реестре.
func NewWbSubjectsByNameTool(c *wb.Client, cfg config.ToolConfig, wbDefaults config.WBConfig) *WbSubjectsByNameTool {
	endpoint, rateLimit, burst := applyWbDefaults(cfg, wbDefaults)

	return &WbSubjectsByNameTool{
		client:      c,
		toolID:      "get_wb_subjects_by_name",
		endpoint:    endpoint,
		rateLimit:   rateLimit,
		burst:       burst,
		description: cfg.Description,
	}
}

func (t *WbSubjectsByNameTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "get_wb_subjects_by_name",
		Description: t.description, // Должен быть задан в config.yaml
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"name": map[string]interface{}{
					"type":        "string",
					"description": "Подстрока для поиска (например, 'платье', 'кроссовки'). Поиск работает по подстроке и без учета регистра.",
				},
				"limit": map[string]interface{}{
					"type":        "integer",
					"description": "Максимальное количество результатов (по умолчанию 50, максимум 1000).",
				},
			},
			"required": []string{"name"},
		},
	}
}

func (t *WbSubjectsByNameTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Name  string `json:"name"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	// Дефолтный лимит
	if args.Limit <= 0 {
		args.Limit = 50
	}
	if args.Limit > 1000 {
		args.Limit = 1000
	}

	// Передаем параметры из tool config в client
	allSubjects, err := t.client.GetAllSubjectsLazy(ctx, t.endpoint, t.rateLimit, t.burst, 0)
	if err != nil {
		return "", fmt.Errorf("failed to get subjects: %w", err)
	}

	// Фильтруем по имени (case-insensitive substring)
	var matches []wb.Subject

	for _, s := range allSubjects {
		if containsIgnoreCase(s.SubjectName, args.Name) {
			matches = append(matches, s)
			if len(matches) >= args.Limit {
				break
			}
		}
	}

	data, err := json.Marshal(matches)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// WbCharacteristicsTool — инструмент для получения характеристик предмета.
//
// Возвращает обязательные и опциональные характеристики для указанного предмета.
type WbCharacteristicsTool struct {
	client      *wb.Client
	toolID      string
	endpoint    string
	rateLimit   int
	burst       int
	description string
}

// NewWbCharacteristicsTool создает инструмент для получения характеристик.
//
// Параметры:
//   - c: экземпляр клиента Wildberries API
//   - cfg: конфигурация tool из YAML
//   - wbDefaults: дефолтные значения из wb секции config.yaml
//
// Возвращает инструмент, готовый к регистрации в реестре.
func NewWbCharacteristicsTool(c *wb.Client, cfg config.ToolConfig, wbDefaults config.WBConfig) *WbCharacteristicsTool {
	endpoint, rateLimit, burst := applyWbDefaults(cfg, wbDefaults)

	return &WbCharacteristicsTool{
		client:      c,
		toolID:      "get_wb_characteristics",
		endpoint:    endpoint,
		rateLimit:   rateLimit,
		burst:       burst,
		description: cfg.Description,
	}
}

func (t *WbCharacteristicsTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "get_wb_characteristics",
		Description: t.description, // Должен быть задан в config.yaml
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"subjectID": map[string]interface{}{
					"type":        "integer",
					"description": "ID предмета (получи его из get_wb_subjects или get_wb_subjects_by_name).",
				},
			},
			"required": []string{"subjectID"},
		},
	}
}

func (t *WbCharacteristicsTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		SubjectID int `json:"subjectID"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	// Передаем параметры из tool config в client
	charcs, err := t.client.GetCharacteristics(ctx, t.endpoint, t.rateLimit, t.burst, args.SubjectID)
	if err != nil {
		return "", fmt.Errorf("failed to get characteristics: %w", err)
	}

	data, err := json.Marshal(charcs)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// WbTnvedTool — инструмент для получения кодов ТНВЭД.
//
// Возвращает список кодов ТНВЭД для указанного предмета с информацией о маркировке.
type WbTnvedTool struct {
	client      *wb.Client
	toolID      string
	endpoint    string
	rateLimit   int
	burst       int
	description string
}

// NewWbTnvedTool создает инструмент для получения кодов ТНВЭД.
//
// Параметры:
//   - c: экземпляр клиента Wildberries API
//   - cfg: конфигурация tool из YAML
//   - wbDefaults: дефолтные значения из wb секции config.yaml
//
// Возвращает инструмент, готовый к регистрации в реестре.
func NewWbTnvedTool(c *wb.Client, cfg config.ToolConfig, wbDefaults config.WBConfig) *WbTnvedTool {
	endpoint, rateLimit, burst := applyWbDefaults(cfg, wbDefaults)

	return &WbTnvedTool{
		client:      c,
		toolID:      "get_wb_tnved",
		endpoint:    endpoint,
		rateLimit:   rateLimit,
		burst:       burst,
		description: cfg.Description,
	}
}

func (t *WbTnvedTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "get_wb_tnved",
		Description: t.description, // Должен быть задан в config.yaml
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"subjectID": map[string]interface{}{
					"type":        "integer",
					"description": "ID предмета (получи его из get_wb_subjects или get_wb_subjects_by_name).",
				},
				"search": map[string]interface{}{
					"type":        "string",
					"description": "Опциональный поиск по коду ТНВЭД (например, '6106903000').",
				},
			},
			"required": []string{"subjectID"},
		},
	}
}

func (t *WbTnvedTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		SubjectID int    `json:"subjectID"`
		Search    string `json:"search"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	// Передаем параметры из tool config в client
	tnveds, err := t.client.GetTnved(ctx, t.endpoint, t.rateLimit, t.burst, args.SubjectID, args.Search)
	if err != nil {
		return "", fmt.Errorf("failed to get tnved: %w", err)
	}

	data, err := json.Marshal(tnveds)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// WbBrandsTool — инструмент для получения списка брендов.
//
// Возвращает бренды для указанного предмета (с авто-пагинацией и лимитом из конфига).
type WbBrandsTool struct {
	client      *wb.Client
	toolID      string
	endpoint    string
	rateLimit   int
	burst       int
	limit       int
	description string
}

// NewWbBrandsTool создает инструмент для получения брендов.
//
// Параметры:
//   - c: экземпляр клиента Wildberries API
//   - cfg: конфигурация tool из YAML
//   - wbDefaults: дефолтные значения из wb секции config.yaml
//
// Возвращает инструмент, готовый к регистрации в реестре.
func NewWbBrandsTool(c *wb.Client, cfg config.ToolConfig, wbDefaults config.WBConfig) *WbBrandsTool {
	endpoint, rateLimit, burst := applyWbDefaults(cfg, wbDefaults)

	// limit берется из tool config или из wb defaults
	limit := cfg.DefaultTake
	if limit == 0 {
		limit = wbDefaults.BrandsLimit // 500 по дефолту из config.go
	}

	return &WbBrandsTool{
		client:      c,
		toolID:      "get_wb_brands",
		endpoint:    endpoint,
		rateLimit:   rateLimit,
		burst:       burst,
		limit:       limit,
		description: cfg.Description,
	}
}

func (t *WbBrandsTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "get_wb_brands",
		Description: t.description, // Должен быть задан в config.yaml
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"subjectID": map[string]interface{}{
					"type":        "integer",
					"description": "ID предмета (получи его из get_wb_subjects или get_wb_subjects_by_name).",
				},
			},
			"required": []string{"subjectID"},
		},
	}
}

func (t *WbBrandsTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		SubjectID int `json:"subjectID"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	// Передаем параметры из tool config в client
	brands, err := t.client.GetBrands(ctx, t.endpoint, t.rateLimit, t.burst, args.SubjectID, t.limit)
	if err != nil {
		return "", fmt.Errorf("failed to get brands: %w", err)
	}

	// Для экономии токенов возвращаем только ID и название
	type brandPreview struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	previews := make([]brandPreview, len(brands))
	for i, b := range brands {
		previews[i] = brandPreview{ID: b.ID, Name: b.Name}
	}

	data, err := json.Marshal(previews)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// Helper функция для case-insensitive поиска (работает с Unicode)
func containsIgnoreCase(s, substr string) bool {
	// Используем strings.ToLower для корректной работы с кириллицей
	return len(s) >= len(substr) &&
		(s == substr ||
		 len(s) > len(substr) && containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	// Case-insensitive поиск с поддержкой Unicode
	sLower := strings.ToLower(s)
	substrLower := strings.ToLower(substr)
	return strings.Contains(sLower, substrLower)
}

```

=================

# tools/std/wb_common.go

```go
// Package std содержит общие вспомогательные функции для WB tools.
package std

import (
	"github.com/ilkoid/poncho-ai/pkg/config"
)

// applyWbDefaults применяет дефолтные значения из wb секции config.
//
// Параметры:
//   - cfg: конфигурация конкретного tool из YAML
//   - wbDefaults: дефолтные значения из wb секции config.yaml
//
// Возвращает endpoint, rateLimit, burst для использования в tool.
// Логика приоритета: значения из tool config перекрывают wb defaults.
func applyWbDefaults(cfg config.ToolConfig, wbDefaults config.WBConfig) (endpoint string, rateLimit int, burst int) {
	wbDefaults = wbDefaults.GetDefaults()

	endpoint = cfg.Endpoint
	if endpoint == "" {
		endpoint = wbDefaults.BaseURL
	}

	rateLimit = cfg.RateLimit
	if rateLimit == 0 {
		rateLimit = wbDefaults.RateLimit
	}

	burst = cfg.Burst
	if burst == 0 {
		burst = wbDefaults.BurstLimit
	}

	return
}

```

=================

# tools/std/wb_dictionaries.go

```go
// Package std содержит инструменты для работы со справочниками Wildberries.
//
// Инструменты принимают *wb.Dictionaries через конструктор для кэширования
// и переиспользования данных, загруженных при старте приложения.
package std

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/tools"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// WbColorsTool — инструмент для поиска цветов с fuzzy search.
//
// Использует ColorService для нечеткого поиска по справочнику цветов WB.
type WbColorsTool struct {
	colorService *wb.ColorService
	dicts        *wb.Dictionaries // Для получения топа цветов при пустом поиске
	toolID       string
	description  string
}

// NewWbColorsTool создает инструмент для поиска цветов.
//
// Параметры:
//   - dicts: Кэшированные справочники (полученные из wb.Client.LoadDictionaries)
//   - cfg: Конфигурация tool из YAML (используется для единообразия)
//
// Возвращает инструмент с инициализированным ColorService.
func NewWbColorsTool(dicts *wb.Dictionaries, cfg config.ToolConfig) *WbColorsTool {
	return &WbColorsTool{
		colorService: wb.NewColorService(dicts.Colors),
		dicts:        dicts,
		toolID:       "get_wb_colors",
		description:  cfg.Description,
	}
}

func (t *WbColorsTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "get_wb_colors",
		Description: t.description, // Должен быть задан в config.yaml
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"search": map[string]interface{}{
					"type":        "string",
					"description": "Подстрока для поиска цвета (например, 'персиковый', 'красный', 'голубой'). Поиск нечеткий, работает по подстроке и схожим названиям.",
				},
				"top": map[string]interface{}{
					"type":        "integer",
					"description": "Количество результатов (по умолчанию 10, максимум 50).",
				},
			},
			"required": []string{}, // search опционален - можно вызвать без параметров для топа
		},
	}
}

func (t *WbColorsTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Search string `json:"search"`
		Top    int    `json:"top"`
	}

	// Аргументы опциональны - если пусто, возвращаем топ цветов
	if argsJSON != "" && argsJSON != "{}" {
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return "", fmt.Errorf("invalid arguments: %w", err)
		}
	}

	// Дефолтные значения
	if args.Top <= 0 {
		args.Top = 10
	}
	if args.Top > 50 {
		args.Top = 50
	}

	// Если поиск пустой - возвращаем топ цветов из справочника
	if args.Search == "" {
		// Возвращаем первые top цветов как "популярные"
		top := args.Top
		if top > len(t.dicts.Colors) {
			top = len(t.dicts.Colors)
		}
		data, err := json.Marshal(t.dicts.Colors[:top])
		if err != nil {
			return "", err
		}
		return string(data), nil
	}

	// Fuzzy search через ColorService
	matches := t.colorService.FindTopMatches(args.Search, args.Top)

	data, err := json.Marshal(matches)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// simpleDictTool — базовый тип для простых справочников WB.
//
// Устраняет дублирование для tools, которые просто возвращают список
// значений из справочника без дополнительной логики.
type simpleDictTool[T any] struct {
	dicts        *wb.Dictionaries
	toolID       string
	description  string
	getData      func(*wb.Dictionaries) []T
}

// Definition возвращает описание инструмента для LLM.
func (t *simpleDictTool[T]) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        t.toolID,
		Description: t.description,
		Parameters: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
			"required":   []string{},
		},
	}
}

// Execute возвращает данные из справочника как JSON.
func (t *simpleDictTool[T]) Execute(ctx context.Context, argsJSON string) (string, error) {
	data, err := json.Marshal(t.getData(t.dicts))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// newSimpleDictTool создает инструмент для простого справочника.
func newSimpleDictTool[T any](
	toolID string,
	getData func(*wb.Dictionaries) []T,
	dicts *wb.Dictionaries,
	cfg config.ToolConfig,
) tools.Tool {
	return &simpleDictTool[T]{
		dicts:       dicts,
		toolID:      toolID,
		description: cfg.Description,
		getData:     getData,
	}
}

// WbCountriesTool — инструмент для получения справочника стран.
//
// Возвращает список стран производства для карточки товара.
type WbCountriesTool = simpleDictTool[wb.Country]

// NewWbCountriesTool создает инструмент для получения стран.
func NewWbCountriesTool(dicts *wb.Dictionaries, cfg config.ToolConfig) tools.Tool {
	return newSimpleDictTool("get_wb_countries", func(d *wb.Dictionaries) []wb.Country {
		return d.Countries
	}, dicts, cfg)
}

// WbGendersTool — инструмент для получения справочника полов.
//
// Возвращает список допустимых значений для характеристики "Пол".
type WbGendersTool = simpleDictTool[string]

// NewWbGendersTool создает инструмент для получения полов.
func NewWbGendersTool(dicts *wb.Dictionaries, cfg config.ToolConfig) tools.Tool {
	return newSimpleDictTool("get_wb_genders", func(d *wb.Dictionaries) []string {
		return d.Genders
	}, dicts, cfg)
}

// WbSeasonsTool — инструмент для получения справочника сезонов.
//
// Возвращает список допустимых значений для характеристики "Сезон".
type WbSeasonsTool = simpleDictTool[string]

// NewWbSeasonsTool создает инструмент для получения сезонов.
func NewWbSeasonsTool(dicts *wb.Dictionaries, cfg config.ToolConfig) tools.Tool {
	return newSimpleDictTool("get_wb_seasons", func(d *wb.Dictionaries) []string {
		return d.Seasons
	}, dicts, cfg)
}

// WbVatRatesTool — инструмент для получения справочника ставок НДС.
//
// Возвращает список допустимых значений НДС для карточки товара.
type WbVatRatesTool = simpleDictTool[string]

// NewWbVatRatesTool создает инструмент для получения ставок НДС.
func NewWbVatRatesTool(dicts *wb.Dictionaries, cfg config.ToolConfig) tools.Tool {
	return newSimpleDictTool("get_wb_vat_rates", func(d *wb.Dictionaries) []string {
		return d.Vats
	}, dicts, cfg)
}

// ReloadWbDictionariesTool — заглушка для перезагрузки справочников из API.
//
// TODO: Реализовать полную перезагрузку справочников через WB API.
type ReloadWbDictionariesTool struct {
	client *wb.Client
	dicts  *wb.Dictionaries
}

// NewReloadWbDictionariesTool создает заглушку для инструмента перезагрузки справочников.
func NewReloadWbDictionariesTool(client *wb.Client, cfg config.ToolConfig) *ReloadWbDictionariesTool {
	// dicts передается через компоненты, здесь заглушка
	return &ReloadWbDictionariesTool{
		client: client,
		dicts:  nil, // Будет установлен при инициализации
	}
}

func (t *ReloadWbDictionariesTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "reload_wb_dictionaries",
		Description: "Перезагружает справочники Wildberries из API. Возвращает количество записей в каждом справочнике. Используй для проверки доступности API или после изменения данных. ВНИМАНИЕ: не обновляет состояние агента, только возвращает данные. [STUB - НЕ РЕАЛИЗОВАНО]",
		Parameters: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
			"required":   []string{},
		},
	}
}

func (t *ReloadWbDictionariesTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	// STUB: Возврат заглушки с информацией о текущих справочниках
	stub := map[string]interface{}{
		"error":   "not_implemented",
		"message": "reload_wb_dictionaries tool is not implemented yet. Dictionaries are loaded at startup.",
		"current_counts": map[string]interface{}{
			"colors":    0,
			"countries": 0,
			"genders":   0,
			"seasons":   0,
			"vat_rates": 0,
		},
	}
	result, _ := json.Marshal(stub)
	return string(result), nil
}


```

=================

# tools/std/wb_feedbacks.go

```go
// Package std предоставляет стандартные инструменты для Poncho AI.
package std

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/tools"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// === WB Feedbacks API Tools ===
// TODO: Реализовать инструменты для работы с Feedbacks API
// https://feedbacks-api.wildberries.ru

// WbFeedbacksTool — заглушка для получения отзывов о товарах.
type WbFeedbacksTool struct {
	client *wb.Client
}

// NewWbFeedbacksTool создает заглушку для инструмента получения отзывов.
func NewWbFeedbacksTool(client *wb.Client, cfg config.ToolConfig) *WbFeedbacksTool {
	return &WbFeedbacksTool{client: client}
}

func (t *WbFeedbacksTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "get_wb_feedbacks",
		Description: "Возвращает отзывы на товары Wildberries с пагинацией. Позволяет фильтровать по отвеченности (isAnswered: true/false) и артикулу (nmID). [STUB - НЕ РЕАЛИЗОВАНО]",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"nmID": map[string]interface{}{
					"type":        "integer",
					"description": "Артикул товара (nmID)",
				},
				"isAnswered": map[string]interface{}{
					"type":        "boolean",
					"description": "Фильтр по отвеченности (true - отвеченные, false - неотвеченные)",
				},
				"take": map[string]interface{}{
					"type":        "integer",
					"description": "Количество отзывов (пагинация)",
				},
				"skip": map[string]interface{}{
					"type":        "integer",
					"description": "Пропустить записей (пагинация)",
				},
			},
			"required": []string{},
		},
	}
}

func (t *WbFeedbacksTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		NmID       int  `json:"nmID"`
		IsAnswered *bool `json:"isAnswered"`
		Take       int  `json:"take"`
		Skip       int  `json:"skip"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	// STUB: Возврат заглушки
	stub := map[string]interface{}{
		"error":   "not_implemented",
		"message": "get_wb_feedbacks tool is not implemented yet",
		"args":    args,
	}
	result, _ := json.Marshal(stub)
	return string(result), nil
}

// WbQuestionsTool — заглушка для получения вопросов о товарах.
type WbQuestionsTool struct {
	client *wb.Client
}

// NewWbQuestionsTool создает заглушку для инструмента получения вопросов.
func NewWbQuestionsTool(client *wb.Client, cfg config.ToolConfig) *WbQuestionsTool {
	return &WbQuestionsTool{client: client}
}

func (t *WbQuestionsTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "get_wb_questions",
		Description: "Возвращает вопросы о товарах Wildberries с пагинацией. Позволяет фильтровать по отвеченности (isAnswered: true/false) и артикулу (nmID). [STUB - НЕ РЕАЛИЗОВАНО]",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"nmID": map[string]interface{}{
					"type":        "integer",
					"description": "Артикул товара (nmID)",
				},
				"isAnswered": map[string]interface{}{
					"type":        "boolean",
					"description": "Фильтр по отвеченности (true - отвеченные, false - неотвеченные)",
				},
				"take": map[string]interface{}{
					"type":        "integer",
					"description": "Количество вопросов (пагинация)",
				},
				"skip": map[string]interface{}{
					"type":        "integer",
					"description": "Пропустить записей (пагинация)",
				},
			},
			"required": []string{},
		},
	}
}

func (t *WbQuestionsTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		NmID       int  `json:"nmID"`
		IsAnswered *bool `json:"isAnswered"`
		Take       int  `json:"take"`
		Skip       int  `json:"skip"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	// STUB: Возврат заглушки
	stub := map[string]interface{}{
		"error":   "not_implemented",
		"message": "get_wb_questions tool is not implemented yet",
		"args":    args,
	}
	result, _ := json.Marshal(stub)
	return string(result), nil
}

// WbNewFeedbacksQuestionsTool — заглушка для проверки новых отзывов/вопросов.
type WbNewFeedbacksQuestionsTool struct {
	client *wb.Client
}

// NewWbNewFeedbacksQuestionsTool создает заглушку для инструмента проверки новых отзывов/вопросов.
func NewWbNewFeedbacksQuestionsTool(client *wb.Client, cfg config.ToolConfig) *WbNewFeedbacksQuestionsTool {
	return &WbNewFeedbacksQuestionsTool{client: client}
}

func (t *WbNewFeedbacksQuestionsTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "get_wb_new_feedbacks_questions",
		Description: "Проверяет наличие новых отзывов и вопросов на Wildberries. Возвращает количество непрочитанных отзывов и вопросов. [STUB - НЕ РЕАЛИЗОВАНО]",
		Parameters: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
			"required":   []string{},
		},
	}
}

func (t *WbNewFeedbacksQuestionsTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	// STUB: Возврат заглушки
	stub := map[string]interface{}{
		"error":   "not_implemented",
		"message": "get_wb_new_feedbacks_questions tool is not implemented yet",
	}
	result, _ := json.Marshal(stub)
	return string(result), nil
}

// WbUnansweredFeedbacksCountsTool — заглушка для получения количества неотвеченных отзывов.
type WbUnansweredFeedbacksCountsTool struct {
	client *wb.Client
}

// NewWbUnansweredFeedbacksCountsTool создает заглушку для инструмента получения неотвеченных отзывов.
func NewWbUnansweredFeedbacksCountsTool(client *wb.Client, cfg config.ToolConfig) *WbUnansweredFeedbacksCountsTool {
	return &WbUnansweredFeedbacksCountsTool{client: client}
}

func (t *WbUnansweredFeedbacksCountsTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "get_wb_unanswered_feedbacks_counts",
		Description: "Возвращает количество неотвеченных отзывов на Wildberries (общее и за сегодня). Используй для мониторинга качества сервиса. [STUB - НЕ РЕАЛИЗОВАНО]",
		Parameters: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
			"required":   []string{},
		},
	}
}

func (t *WbUnansweredFeedbacksCountsTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	// STUB: Возврат заглушки
	stub := map[string]interface{}{
		"error":   "not_implemented",
		"message": "get_wb_unanswered_feedbacks_counts tool is not implemented yet",
	}
	result, _ := json.Marshal(stub)
	return string(result), nil
}

// WbUnansweredQuestionsCountsTool — заглушка для получения количества неотвеченных вопросов.
type WbUnansweredQuestionsCountsTool struct {
	client *wb.Client
}

// NewWbUnansweredQuestionsCountsTool создает заглушку для инструмента получения неотвеченных вопросов.
func NewWbUnansweredQuestionsCountsTool(client *wb.Client, cfg config.ToolConfig) *WbUnansweredQuestionsCountsTool {
	return &WbUnansweredQuestionsCountsTool{client: client}
}

func (t *WbUnansweredQuestionsCountsTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "get_wb_unanswered_questions_counts",
		Description: "Возвращает количество неотвеченных вопросов на Wildberries (общее и за сегодня). Используй для мониторинга качества сервиса. [STUB - НЕ РЕАЛИЗОВАНО]",
		Parameters: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
			"required":   []string{},
		},
	}
}

func (t *WbUnansweredQuestionsCountsTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	// STUB: Возврат заглушки
	stub := map[string]interface{}{
		"error":   "not_implemented",
		"message": "get_wb_unanswered_questions_counts tool is not implemented yet",
	}
	result, _ := json.Marshal(stub)
	return string(result), nil
}

```

=================

# tools/std/wb_products.go

```go
// Package std предоставляет стандартные инструменты для Poncho AI.
package std

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/tools"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// === WB Products Search ===
// TODO: Реализовать инструмент для поиска товаров Wildberries

// WbProductSearchTool — заглушка для поиска товаров по артикулам поставщика.
type WbProductSearchTool struct {
	client *wb.Client
	wbCfg  config.WBConfig
	toolCfg config.ToolConfig
}

// NewWbProductSearchTool создает заглушку для инструмента поиска товаров.
func NewWbProductSearchTool(client *wb.Client, toolCfg config.ToolConfig, wbCfg config.WBConfig) *WbProductSearchTool {
	return &WbProductSearchTool{
		client:  client,
		wbCfg:   wbCfg,
		toolCfg: toolCfg,
	}
}

func (t *WbProductSearchTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name: "search_wb_products",
		Description: "Ищет товары Wildberries по артикулам поставщика (vendor code/supplier article) и возвращает их nmID. Использует Content API (категория Promotion). ВАЖНО: видит только товары продавца, которому принадлежит API токен (до 100 карточек). Для поиска товаров других продавцов используйте get_wb_feedbacks или get_wb_questions. [STUB - НЕ РЕАЛИЗОВАНО]",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"vendor_codes": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "string"},
					"description": "Список артикулов поставщика для поиска",
				},
			},
			"required": []string{"vendor_codes"},
		},
	}
}

func (t *WbProductSearchTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		VendorCodes []string `json:"vendor_codes"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	if len(args.VendorCodes) == 0 {
		return "", fmt.Errorf("vendor_codes cannot be empty")
	}

	// STUB: Возврат заглушки
	stub := map[string]interface{}{
		"error":   "not_implemented",
		"message": "search_wb_products tool is not implemented yet",
		"vendor_codes": args.VendorCodes,
		"results": []map[string]interface{}{
			{
				"vendor_code": args.VendorCodes[0],
				"nmID":        0,
				"price":       0,
				"status":      "STUB",
			},
		},
	}
	result, _ := json.Marshal(stub)
	return string(result), nil
}

```

=================

# tools/std/wb_search_analytics.go

```go
// Package std предоставляет инструменты для аналитики поисковых запросов Wildberries.
//
// Реализует инструменты для анализа позиций товаров в поиске:
// - Средняя позиция в выдаче
// - Топ поисковых запросов
// - Топ-10 позиций по органике
package std

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/tools"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// WbSearchPositionsTool — инструмент для получения позиций товаров в поиске.
//
// Использует Analytics API: POST /api/v2/search-report/report
// Возвращает среднюю позицию, видимость, переходы из поиска.
type WbSearchPositionsTool struct {
	client      *wb.Client
	toolID      string
	endpoint    string
	rateLimit   int
	burst       int
	description string
}

// NewWbSearchPositionsTool создает инструмент для получения позиций в поиске.
//
// Параметры:
//   - c: экземпляр клиента Wildberries API
//   - cfg: конфигурация tool из YAML
//   - wbDefaults: дефолтные значения из wb секции config.yaml
//
// Возвращает инструмент, готовый к регистрации в реестре.
func NewWbSearchPositionsTool(c *wb.Client, cfg config.ToolConfig, wbDefaults config.WBConfig) *WbSearchPositionsTool {
	endpoint, rateLimit, burst := applyWbDefaults(cfg, wbDefaults)

	return &WbSearchPositionsTool{
		client:      c,
		toolID:      "get_wb_search_positions",
		endpoint:    endpoint,
		rateLimit:   rateLimit,
		burst:       burst,
		description: cfg.Description,
	}
}

// Definition возвращает определение инструмента для function calling.
func (t *WbSearchPositionsTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "get_wb_search_positions",
		Description: t.description,
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"nmIDs": map[string]interface{}{
					"type": "array",
					"items": map[string]interface{}{"type": "integer"},
					"description": "Список nmID товаров (макс. 100)",
				},
				"days": map[string]interface{}{
					"type":        "integer",
					"description": "Количество дней для анализа (1-30)",
				},
			},
			"required": []string{"nmIDs", "days"},
		},
	}
}

// Execute выполняет инструмент согласно контракту "Raw In, String Out".
func (t *WbSearchPositionsTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		NmIDs []int `json:"nmIDs"`
		Days  int    `json:"days"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	// Валидация
	if len(args.NmIDs) == 0 {
		return "", fmt.Errorf("nmIDs cannot be empty")
	}
	if len(args.NmIDs) > 100 {
		return "", fmt.Errorf("nmIDs cannot exceed 100 items")
	}
	if args.Days < 1 || args.Days > 30 {
		return "", fmt.Errorf("days must be between 1 and 30")
	}

	// Mock режим для demo ключа
	if t.client.IsDemoKey() {
		return t.executeMock(args.NmIDs, args.Days)
	}

	// Формируем период
	now := time.Now()
	begin := now.AddDate(0, 0, -args.Days)
	end := now.AddDate(0, 0, -args.Days*2) // pastPeriod для сравнения

	// Формируем запрос к WB API
	reqBody := map[string]interface{}{
		"nmIds": args.NmIDs,
		"currentPeriod": map[string]string{
			"begin": begin.Format("2006-01-02"),
			"end":   now.Format("2006-01-02"),
		},
		"pastPeriod": map[string]string{
			"begin": end.Format("2006-01-02"),
			"end":   begin.Format("2006-01-02"),
		},
		"page": 1,
	}

	var response struct {
		Data struct {
			Clusters struct {
				FirstHundred int `json:"firstHundred"` // Количество товаров в топ-100
			} `json:"clusters"`
			Items []struct {
				NMID         int     `json:"nmId"`
				AvgPosition  float64 `json:"avgPosition"`
				OpenCard     int     `json:"openCard"`
				Orders       int     `json:"orders"`
			} `json:"items"`
		} `json:"data"`
	}

	err := t.client.Post(ctx, t.toolID, t.endpoint, t.rateLimit, t.burst,
		"/api/v2/search-report/report", reqBody, &response)
	if err != nil {
		return "", fmt.Errorf("failed to get search positions: %w", err)
	}

	// Форматируем ответ для LLM
	result, _ := json.Marshal(response.Data)
	return string(result), nil
}

// executeMock возвращает mock данные для demo режима.
func (t *WbSearchPositionsTool) executeMock(nmIDs []int, days int) (string, error) {
	now := time.Now()
	begin := now.AddDate(0, 0, -days)

	items := make([]map[string]interface{}, 0, len(nmIDs))
	firstHundred := 0

	for _, nmID := range nmIDs {
		avgPos := 50.0 + float64(nmID%100)
		if avgPos <= 100 {
			firstHundred++
		}

		items = append(items, map[string]interface{}{
			"nmId":        nmID,
			"avgPosition": avgPos,
			"openCard":    100 + nmID%200,
			"orders":      10 + nmID%50,
		})
	}

	result := map[string]interface{}{
		"period": map[string]string{
			"begin": begin.Format("2006-01-02"),
			"end":   now.Format("2006-01-02"),
		},
		"clusters": map[string]interface{}{
			"firstHundred": firstHundred,
		},
		"items": items,
		"mock": true,
	}

	data, _ := json.Marshal(result)
	return string(data), nil
}

// WbTopSearchQueriesTool — инструмент для получения топ поисковых запросов.
//
// Использует Analytics API: POST /api/v2/search-report/product/search-texts
// Возвращает топ поисковых фраз по товару.
type WbTopSearchQueriesTool struct {
	client      *wb.Client
	toolID      string
	endpoint    string
	rateLimit   int
	burst       int
	description string
}

// NewWbTopSearchQueriesTool создает инструмент для получения топ поисковых запросов.
//
// Параметры:
//   - c: экземпляр клиента Wildberries API
//   - cfg: конфигурация tool из YAML
//   - wbDefaults: дефолтные значения из wb секции config.yaml
//
// Возвращает инструмент, готовый к регистрации в реестре.
func NewWbTopSearchQueriesTool(c *wb.Client, cfg config.ToolConfig, wbDefaults config.WBConfig) *WbTopSearchQueriesTool {
	endpoint, rateLimit, burst := applyWbDefaults(cfg, wbDefaults)

	return &WbTopSearchQueriesTool{
		client:      c,
		toolID:      "get_wb_top_search_queries",
		endpoint:    endpoint,
		rateLimit:   rateLimit,
		burst:       burst,
		description: cfg.Description,
	}
}

// Definition возвращает определение инструмента для function calling.
func (t *WbTopSearchQueriesTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "get_wb_top_search_queries",
		Description: t.description,
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"nmIDs": map[string]interface{}{
					"type": "array",
					"items": map[string]interface{}{"type": "integer"},
					"description": "Список nmID товаров (макс. 100)",
				},
				"days": map[string]interface{}{
					"type":        "integer",
					"description": "Количество дней для анализа (1-30)",
				},
				"limit": map[string]interface{}{
					"type":        "integer",
					"description": "Максимальное количество запросов (по умолчанию 30, макс. 100)",
				},
			},
			"required": []string{"nmIDs", "days"},
		},
	}
}

// Execute выполняет инструмент согласно контракту "Raw In, String Out".
func (t *WbTopSearchQueriesTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		NmIDs []int `json:"nmIDs"`
		Days  int    `json:"days"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	// Валидация
	if len(args.NmIDs) == 0 {
		return "", fmt.Errorf("nmIDs cannot be empty")
	}
	if len(args.NmIDs) > 100 {
		return "", fmt.Errorf("nmIDs cannot exceed 100 items")
	}
	if args.Days < 1 || args.Days > 30 {
		return "", fmt.Errorf("days must be between 1 and 30")
	}
	if args.Limit == 0 {
		args.Limit = 30 // дефолт
	}
	if args.Limit > 100 {
		args.Limit = 100 // максимум
	}

	// Mock режим для demo ключа
	if t.client.IsDemoKey() {
		return t.executeMock(args.NmIDs, args.Days, args.Limit)
	}

	// Формируем период
	now := time.Now()
	begin := now.AddDate(0, 0, -args.Days)

	// Формируем запрос к WB API
	reqBody := map[string]interface{}{
		"nmIds": args.NmIDs,
		"period": map[string]string{
			"begin": begin.Format("2006-01-02"),
			"end":   now.Format("2006-01-02"),
		},
		"topOrderBy": "orders", // Сортировка по заказам
		"limit":     args.Limit,
		"page":      1,
	}

	var response struct {
		Data struct {
			Items []struct {
				NMID      int    `json:"nmId"`
				Queries   []struct {
					Text    string  `json:"text"`
					Orders  int     `json:"orders"`
					Views   int     `json:"views"`
					Position float64 `json:"position"`
				} `json:"queries"`
			} `json:"items"`
		} `json:"data"`
	}

	err := t.client.Post(ctx, t.toolID, t.endpoint, t.rateLimit, t.burst,
		"/api/v2/search-report/product/search-texts", reqBody, &response)
	if err != nil {
		return "", fmt.Errorf("failed to get top search queries: %w", err)
	}

	// Форматируем ответ для LLM
	result, _ := json.Marshal(response.Data)
	return string(result), nil
}

// executeMock возвращает mock данные для demo режима.
func (t *WbTopSearchQueriesTool) executeMock(nmIDs []int, days int, limit int) (string, error) {
	now := time.Now()
	begin := now.AddDate(0, 0, -days)

	items := make([]map[string]interface{}, 0, len(nmIDs))

	// Mock поисковые запросы
	mockQueries := []string{
		"платье женское", "вечернее платье", "платье черное",
		"платье длинное", "платье летнее", "сарафан",
		"платье коктейльное", "платье офисное", "платье красное",
		"платье с рукавами",
	}

	for _, nmID := range nmIDs {
		queries := make([]map[string]interface{}, 0, limit)

		for i, q := range mockQueries {
			if i >= limit {
				break
			}
			queries = append(queries, map[string]interface{}{
				"text":     q,
				"orders":   10 + i*5 + nmID%20,
				"views":    100 + i*50 + nmID%200,
				"position": float64(i*3 + 1 + nmID%10),
			})
		}

		items = append(items, map[string]interface{}{
			"nmId":    nmID,
			"queries": queries,
		})
	}

	result := map[string]interface{}{
		"period": map[string]string{
			"begin": begin.Format("2006-01-02"),
			"end":   now.Format("2006-01-02"),
		},
		"items": items,
		"mock":  true,
	}

	data, _ := json.Marshal(result)
	return string(data), nil
}

// WbTopOrganicPositionsTool — инструмент для получения топ-10 позиций в органике.
//
// Это агрегатор, который использует данные из get_wb_top_search_queries
// и возвращает только позиции в топ-10 по каждому запросу.
type WbTopOrganicPositionsTool struct {
	topQueriesTool *WbTopSearchQueriesTool
	description    string
}

// NewWbTopOrganicPositionsTool создает инструмент для получения топ-10 позиций.
//
// Параметры:
//   - c: экземпляр клиента Wildberries API
//   - cfg: конфигурация tool из YAML
//   - wbDefaults: дефолтные значения из wb секции config.yaml
//
// Возвращает инструмент, готовый к регистрации в реестре.
func NewWbTopOrganicPositionsTool(c *wb.Client, cfg config.ToolConfig, wbDefaults config.WBConfig) *WbTopOrganicPositionsTool {
	return &WbTopOrganicPositionsTool{
		topQueriesTool: NewWbTopSearchQueriesTool(c, cfg, wbDefaults),
		description:    cfg.Description,
	}
}

// Definition возвращает определение инструмента для function calling.
func (t *WbTopOrganicPositionsTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "get_wb_top_organic_positions",
		Description: t.description,
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"nmIDs": map[string]interface{}{
					"type": "array",
					"items": map[string]interface{}{"type": "integer"},
					"description": "Список nmID товаров (макс. 100)",
				},
				"days": map[string]interface{}{
					"type":        "integer",
					"description": "Количество дней для анализа (1-30)",
				},
			},
			"required": []string{"nmIDs", "days"},
		},
	}
}

// Execute выполняет инструмент согласно контракту "Raw In, String Out".
func (t *WbTopOrganicPositionsTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	// Получаем данные из инструмента топ запросов
	rawData, err := t.topQueriesTool.Execute(ctx, argsJSON)
	if err != nil {
		return "", fmt.Errorf("failed to get top queries data: %w", err)
	}

	var response struct {
		Data struct {
			Items []struct {
				NMID    int `json:"nmId"`
				Queries []struct {
					Text    string  `json:"text"`
					Orders  int     `json:"orders"`
					Views   int     `json:"views"`
					Position float64 `json:"position"`
				} `json:"queries"`
			} `json:"items"`
		} `json:"data"`
	}

	if err := json.Unmarshal([]byte(rawData), &response); err != nil {
		return "", fmt.Errorf("failed to parse top queries response: %w", err)
	}

	// Фильтруем только топ-10 позиции
	result := make([]map[string]interface{}, 0, len(response.Data.Items))

	for _, item := range response.Data.Items {
		topPositions := make([]map[string]interface{}, 0)

		for _, q := range item.Queries {
			if q.Position <= 10.0 {
				topPositions = append(topPositions, map[string]interface{}{
					"query":    q.Text,
					"position": int(q.Position),
					"orders":   q.Orders,
					"views":    q.Views,
				})
			}
		}

		if len(topPositions) > 0 {
			result = append(result, map[string]interface{}{
				"nmID":         item.NMID,
				"topPositions": topPositions,
			})
		}
	}

	// Если реальных данных нет, возвращаем пустой результат с понятным сообщением
	if len(result) == 0 {
		for _, item := range response.Data.Items {
			result = append(result, map[string]interface{}{
				"nmID":         item.NMID,
				"topPositions": []map[string]interface{}{},
				"message":      "Товар не находится в топ-10 органической выдачи по анализируемым запросам",
			})
		}
	}

	data, _ := json.Marshal(result)
	return string(data), nil
}

```

=================

# tools/types.go

```go
// Интерфейс Tool и структуры определений.

package tools

import "context"

// JSONSchema представляет JSON Schema для параметров инструмента.
//
// Используется вместо interface{} для типобезопасности.
// Формат соответствует JSON Schema specification для Function Calling API.
type JSONSchema map[string]any

// ToolDefinition описывает инструмент для LLM (Function Calling API format).
type ToolDefinition struct {
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Parameters  JSONSchema `json:"parameters"` // JSON Schema объекта аргументов
}

// Tool — контракт, который должен реализовать любой инструмент.
type Tool interface {
	// Definition возвращает описание инструмента для LLM.
	Definition() ToolDefinition

	// Execute выполняет логику инструмента.
	// argsJSON — это сырой JSON с аргументами, который прислала LLM.
	// Возвращает результат (обычно JSON) или ошибку.
	Execute(ctx context.Context, argsJSON string) (string, error)
}


```

=================

# tui/adapter.go

```go
// Package tui предоставляет reusable helpers для подключения Bubble Tea TUI к агенту.
//
// Это НЕ готовый TUI (он остаётся в internal/ui/), а reusable адаптеры
// и конвертеры для удобной работы с событиями агента.
//
// Port & Adapter паттерн:
//   - pkg/events.* — Port (интерфейсы)
//   - pkg/tui.* — Adapter helpers (переиспользуемые утилиты)
//   - internal/ui.* — Конкретная реализация TUI (app-specific)
//
// # Basic Usage
//
//	client, _ := agent.New(...)
//	sub := client.Subscribe()
//
//	// Конвертируем события агента в Bubble Tea сообщения
//	cmd := tui.ReceiveEvents(sub, func(event events.Event) tea.Msg {
//	    return EventMsg(event)
//	})
//
// Rule 6: только reusable код, без app-specific логики.
package tui

import (
	"github.com/ilkoid/poncho-ai/pkg/events"
	tea "github.com/charmbracelet/bubbletea"
)

// EventMsg конвертирует events.Event в Bubble Tea сообщение.
//
// Используется в Bubble Tea Update() для обработки событий агента.
type EventMsg events.Event

// ReceiveEventCmd возвращает Bubble Tea Cmd для чтения событий из Subscriber.
//
// Функция-конвертер вызывается для каждого полученного события и должна
// возвращать Bubble Tea сообщение.
//
// Пример использования в Bubble Tea Model:
//
//	func (m model) Init() tea.Cmd {
//	    return tui.ReceiveEventCmd(subscriber, func(evt events.Event) tea.Msg {
//	        return EventMsg(evt)
//	    })
//	}
//
// Rule 11: cmd можно отменить через context.
func ReceiveEventCmd(sub events.Subscriber, converter func(events.Event) tea.Msg) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-sub.Events()
		if !ok {
			return tea.QuitMsg{}
		}
		return converter(event)
	}
}

// WaitForEvent возвращает Cmd который ждёт следующего события.
//
// Используется в Update() для продолжения чтения событий:
//
//	case EventMsg(event):
//	    // ... обработка события
//	    return m, tui.WaitForEvent(sub, converter)
func WaitForEvent(sub events.Subscriber, converter func(events.Event) tea.Msg) tea.Cmd {
	return ReceiveEventCmd(sub, converter)
}

```

=================

# tui/model.go

```go
// Package tui предоставляет базовый TUI для AI агентов на Bubble Tea.
//
// Это reusable библиотечный код (Rule 6), который может быть использован
// любым приложением на базе Poncho AI.
//
// Для специфичных функций (todo-панель, special commands) используйте
// internal/ui/ который расширяет этот базовый TUI.
//
// # Basic Usage
//
//	client, _ := agent.New(...)
//	tui.Run(client) // Готовый TUI из коробки!
//
// # Advanced Usage (с кастомизацией)
//
//	client, _ := agent.New(...)
//	emitter := events.NewChanEmitter(100)
//	client.SetEmitter(emitter)
//
//	model := tui.NewModel(client, emitter.Subscribe())
//	p := tea.NewProgram(model)
//	p.Run()
package tui

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	"github.com/ilkoid/poncho-ai/pkg/agent"
	"github.com/ilkoid/poncho-ai/pkg/events"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Model представляет базовую TUI модель для AI агента.
//
// Реализует Bubble Tea Model интерфейс. Обеспечивает:
//   - Чат-подобный интерфейс с историей сообщений
//   - Поле ввода для запросов
//   - Отображение событий агента через events.Subscriber
//   - Базовую навигацию (скролл, Ctrl+C для выхода)
//
// Thread-safe.
//
// Правило 11: хранит родительский context.Context для распространения отмены.
//
// Для расширения функционала (todo-панель, special commands)
// используйте встраивание (embedding) в internal/ui/.
type Model struct {
	// UI компоненты Bubble Tea
	viewport viewport.Model
	textarea textarea.Model

	// Dependencies
	agent    agent.Agent
	eventSub events.Subscriber

	// Состояние
	isProcessing bool // Флаг занятости агента
	mu           sync.RWMutex

	// Опции
	title   string // Заголовок приложения
	prompt  string // Приглашение ввода
	ready   bool   // Флаг первой инициализации
	timeout time.Duration // Таймаут для agent execution

	// Правило 11: родительский контекст для распространения отмены
	ctx context.Context
}

// NewModel создаёт новую TUI модель.
//
// Rule 11: Принимает родительский контекст для распространения отмены.
//
// Parameters:
//   - ctx: Родительский контекст для распространения отмены
//   - agent: AI агент (реализует agent.Agent интерфейс)
//   - eventSub: Подписчик на события агента
//
// Возвращает модель готовую к использованию с Bubble Tea.
func NewModel(ctx context.Context, agent agent.Agent, eventSub events.Subscriber) Model {
	// Настройка поля ввода
	ta := textarea.New()
	ta.Placeholder = "Введите запрос к AI агенту..."
	ta.Focus()
	ta.Prompt = "┃ "
	ta.CharLimit = 500
	ta.SetHeight(3)
	ta.ShowLineNumbers = false

	// Настройка вьюпорта для лога
	// Размеры (0,0) обновятся при первом WindowSizeMsg
	vp := viewport.New(0, 0)
	vp.SetContent(fmt.Sprintf("%s\n%s\n",
		systemStyle("AI Agent TUI initialized."),
		systemStyle("Type your query and press Enter..."),
	))

	return Model{
		viewport:     vp,
		textarea:     ta,
		agent:        agent,
		eventSub:     eventSub,
		isProcessing: false,
		title:        "AI Agent",
		prompt:       "┃ ",
		ready:        false,
		timeout:      5 * time.Minute, // дефолтный timeout
		ctx:          ctx, // Rule 11: сохраняем родительский контекст
	}
}

// Init реализует tea.Model интерфейс.
//
// Возвращает команды для:
//   - Мигания курсора
//   - Чтения событий от агента
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		textarea.Blink,
		ReceiveEventCmd(m.eventSub, func(event events.Event) tea.Msg {
			return EventMsg(event)
		}),
	)
}

// Update реализует tea.Model интерфейс.
//
// Обрабатывает:
//   - tea.WindowSizeMsg: изменение размера терминала
//   - tea.KeyMsg: нажатия клавиш
//   - EventMsg: события от агента
//
// Для расширения (добавление новых сообщений) используйте
// встраивание Model в своей структуре.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var (
		tiCmd tea.Cmd
		vpCmd tea.Cmd
	)

	m.textarea, tiCmd = m.textarea.Update(msg)
	m.viewport, vpCmd = m.viewport.Update(msg)

	switch msg := msg.(type) {
	case EventMsg:
		// События от агента
		return m.handleAgentEvent(events.Event(msg))

	case tea.WindowSizeMsg:
		return m.handleWindowSize(msg)

	case tea.KeyMsg:
		return m.handleKeyPress(msg)
	}

	return m, tea.Batch(tiCmd, vpCmd)
}

// handleAgentEvent обрабатывает события от агента.
func (m Model) handleAgentEvent(event events.Event) (tea.Model, tea.Cmd) {
	switch event.Type {
	case events.EventThinking:
		m.mu.Lock()
		m.isProcessing = true
		m.mu.Unlock()
		m.appendLog(systemStyle("Thinking..."))
		return m, WaitForEvent(m.eventSub, func(e events.Event) tea.Msg {
			return EventMsg(e)
		})

	case events.EventThinkingChunk:
		// Обработка порции reasoning_content при streaming
		if chunkData, ok := event.Data.(events.ThinkingChunkData); ok {
			m.appendThinkingChunk(chunkData.Chunk)
		}
		return m, WaitForEvent(m.eventSub, func(e events.Event) tea.Msg {
			return EventMsg(e)
		})

	case events.EventMessage:
		if msgData, ok := event.Data.(events.MessageData); ok {
			m.appendLog(aiMessageStyle("AI: ") + msgData.Content)
		}
		return m, WaitForEvent(m.eventSub, func(e events.Event) tea.Msg {
			return EventMsg(e)
		})

	case events.EventError:
		if errData, ok := event.Data.(events.ErrorData); ok {
			m.appendLog(errorStyle("ERROR: ") + errData.Err.Error())
		}
		m.mu.Lock()
		m.isProcessing = false
		m.mu.Unlock()
		m.textarea.Focus()
		return m, nil

	case events.EventDone:
		if msgData, ok := event.Data.(events.MessageData); ok {
			m.appendLog(aiMessageStyle("AI: ") + msgData.Content)
		}
		m.mu.Lock()
		m.isProcessing = false
		m.mu.Unlock()
		m.textarea.Focus()
		return m, nil
	}

	return m, nil
}

// handleWindowSize обрабатывает изменение размера терминала.
func (m Model) handleWindowSize(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
	headerHeight := 1
	footerHeight := m.textarea.Height() + 2

	// Вычисляем высоту для области контента
	vpHeight := msg.Height - headerHeight - footerHeight
	if vpHeight < 0 {
		vpHeight = 0
	}

	// Вычисляем ширину
	vpWidth := msg.Width
	if vpWidth < 20 {
		vpWidth = 20 // Минимальная ширина
	}

	m.viewport.Width = vpWidth
	m.viewport.Height = vpHeight

	// Только при первом запуске
	if !m.ready {
		m.ready = true
		dimensions := fmt.Sprintf("Window: %dx%d | Viewport: %dx%d",
			msg.Width, msg.Height, vpWidth, vpHeight)
		m.appendLog(systemStyle("INFO: ") + dimensions)
	}

	m.textarea.SetWidth(vpWidth)

	return m, nil
}

// handleKeyPress обрабатывает нажатия клавиш.
func (m Model) handleKeyPress(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC, tea.KeyEsc:
		return m, tea.Quit

	case tea.KeyEnter:
		input := m.textarea.Value()
		if input == "" {
			return m, nil
		}

		// Очищаем ввод
		m.textarea.Reset()

		// Добавляем сообщение пользователя в лог
		m.appendLog(userMessageStyle("USER: ") + input)

		// Запускаем агента
		return m, m.startAgent(input)
	}

	return m, nil
}

// startAgent запускает агента с заданным запросом.
// Правило 11: использует сохранённый родительский контекст.
func (m Model) startAgent(query string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := m.contextWithTimeout(m.ctx)
		defer cancel()

		_, err := m.agent.Run(ctx, query)
		if err != nil {
			return EventMsg{
				Type: events.EventError,
				Data: events.ErrorData{Err: err},
			}
		}
		// События придут через emitter автоматически
		return nil
	}
}

// appendLog добавляет строку в лог чата.
func (m *Model) appendLog(str string) {
	newContent := fmt.Sprintf("%s\n%s", m.viewport.View(), str)
	m.viewport.SetContent(newContent)
	m.viewport.GotoBottom()
}

// appendThinkingChunk обновляет строку с thinking content.
//
// В отличие от appendLog, этот метод обновляет последнюю строку
// вместо добавления новой (для эффекта печатающегося текста).
func (m *Model) appendThinkingChunk(chunk string) {
	currentContent := m.viewport.View()
	lines := fmt.Sprintf("%s", currentContent)

	// Разбиваем на строки
	linesList := strings.Split(lines, "\n")

	// Если последняя строка начинается с "Thinking: ", обновляем её
	if len(linesList) > 0 {
		lastLine := linesList[len(linesList)-1]
		if strings.Contains(lastLine, "Thinking") {
			// Заменяем последнюю строку с новым chunk
			linesList[len(linesList)-1] = thinkingStyle("Thinking: ") + thinkingContentStyle(chunk)
		} else {
			// Добавляем новую строку
			linesList = append(linesList, thinkingStyle("Thinking: ")+thinkingContentStyle(chunk))
		}
	} else {
		// Добавляем новую строку
		linesList = []string{thinkingStyle("Thinking: ") + thinkingContentStyle(chunk)}
	}

	// Объединяем обратно
	newContent := strings.Join(linesList, "\n")
	m.viewport.SetContent(newContent)
	m.viewport.GotoBottom()
}

// View реализует tea.Model интерфейс.
//
// Возвращает строковое представление TUI для рендеринга.
func (m Model) View() string {
	return fmt.Sprintf("%s\n%s",
		m.viewport.View(),
		m.textarea.View(),
	)
}

// contextWithTimeout создаёт контекст с таймаутом из настроек модели.
// Правило 11: принимает родительский контекст для распространения отмены.
func (m Model) contextWithTimeout(parentCtx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parentCtx, m.timeout)
}

// ===== STYLES =====

// systemStyle возвращает стиль для системных сообщений.
func systemStyle(str string) string {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("242")). // Серый
		Render(str)
}

// aiMessageStyle возвращает стиль для сообщений AI.
func aiMessageStyle(str string) string {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("86")). // Cyan
		Bold(true).
		Render(str)
}

// errorStyle возвращает стиль для ошибок.
func errorStyle(str string) string {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("196")). // Red
		Bold(true).
		Render(str)
}

// userMessageStyle возвращает стиль для сообщений пользователя.
func userMessageStyle(str string) string {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("226")). // Yellow
		Bold(true).
		Render(str)
}

// thinkingStyle возвращает стиль для заголовка thinking.
func thinkingStyle(str string) string {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("99")). // Purple
		Bold(true).
		Render(str)
}

// thinkingContentStyle возвращает стиль для контента thinking.
func thinkingContentStyle(str string) string {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("245")). // Dim gray
		Render(str)
}

// Ensure Model implements tea.Model
var _ tea.Model = (*Model)(nil)

```

=================

# tui/run.go

```go
package tui

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ilkoid/poncho-ai/pkg/agent"
	"github.com/ilkoid/poncho-ai/pkg/events"
)

// Run запускает готовый TUI для AI агента.
//
// Это главная точка входа для пользователей библиотеки.
// Создаёт emitter, подписывается на события и запускает Bubble Tea программу.
//
// Правило 11: принимает и распространяет context.Context.
//
// # Basic Usage
//
//	client, _ := agent.New(context.Background(), agent.Config{ConfigPath: "config.yaml"})
//	if err := tui.Run(context.Background(), client); err != nil {
//	    log.Fatal(err)
//	}
//
// # Advanced Usage (с кастомным emitter)
//
//	client, _ := agent.New(context.Background(), ...)
//	emitter := events.NewChanEmitter(100)
//	client.SetEmitter(emitter)
//
//	model := tui.NewModel(client, emitter.Subscribe())
//	p := tea.NewProgram(model)
//	if _, err := p.Run(); err != nil {
//	    log.Fatal(err)
//	}
//
// Thread-safe.
func Run(ctx context.Context, client *agent.Client) error {
	if client == nil {
		return fmt.Errorf("client is nil")
	}

	// Создаём emitter для событий
	emitter := events.NewChanEmitter(100)
	client.SetEmitter(emitter)

	// Получаем subscriber для TUI
	sub := client.Subscribe()

	// Создаём модель с контекстом (Rule 11)
	model := NewModel(ctx, client, sub)

	// Запускаем Bubble Tea программу
	p := tea.NewProgram(model)

	if _, err := p.Run(); err != nil {
		return fmt.Errorf("TUI error: %w", err)
	}

	return nil
}

// RunWithOpts запускает TUI с опциями.
//
// Позволяет кастомизировать поведение TUI через опции.
//
// Правило 11: принимает и распространяет context.Context.
//
// Пример:
//
//	client, _ := agent.New(context.Background(), ...)
//	err := tui.RunWithOpts(context.Background(), client,
//	    tui.WithTitle("My AI App"),
//	    tui.WithPrompt("> "),
//	)
func RunWithOpts(ctx context.Context, client *agent.Client, opts ...Option) error {
	if client == nil {
		return fmt.Errorf("client is nil")
	}

	// Создаём emitter
	emitter := events.NewChanEmitter(100)
	client.SetEmitter(emitter)
	sub := client.Subscribe()

	// Создаём модель с опциями (Rule 11)
	model := NewModel(ctx, client, sub)
	for _, opt := range opts {
		opt(&model)
	}

	// Запускаем
	p := tea.NewProgram(model)
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("TUI error: %w", err)
	}

	return nil
}

// Option - функция для кастомизации TUI.
type Option func(*Model)

// WithTitle устанавливает заголовок TUI.
func WithTitle(title string) Option {
	return func(m *Model) {
		m.title = title
	}
}

// WithPrompt устанавливает текст приглашения ввода.
func WithPrompt(prompt string) Option {
	return func(m *Model) {
		m.prompt = prompt
	}
}

// WithTimeout устанавливает таймаут для выполнения запросов агента.
//
// По умолчанию используется 5 минут.
//
// Пример:
//
//	client, _ := agent.New(context.Background(), ...)
//	err := tui.RunWithOpts(context.Background(), client,
//	    tui.WithTimeout(10 * time.Minute),
//	)
func WithTimeout(timeout time.Duration) Option {
	return func(m *Model) {
		m.timeout = timeout
	}
}

```

=================

# utils/clean.go

```go
// Package utils предоставляет вспомогательные функции для обработки данных.
//
// Включает утилиты для очистки ответов LLM от markdown-обёртки,
// санитизации JSON и других форматирующих операций.
package utils

import (
	"strings"
)

// CleanJsonBlock удаляет markdown-обёртку вокруг JSON.
//
// LLM часто возвращает JSON обёрнутым в markdown кодовые блоки:
//   ```json
//   {"key": "value"}
//   ```
//
// Эта функция очищает такие обёртки, возвращая чистый JSON.
//
// Примеры:
//   ```json {"a": 1} ``` → {"a": 1}
//   `{"a": 1}` → {"a": 1}
//   ``` {"a": 1} ``` → {"a": 1}
func CleanJsonBlock(s string) string {
	s = strings.TrimSpace(s)

	// Удаляем ```json в начале
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```JSON")
	s = strings.TrimPrefix(s, "```Json")

	// Удаляем ``` в начале
	s = strings.TrimPrefix(s, "```")

	// Удаляем ``` в конце
	s = strings.TrimSuffix(s, "```")

	return strings.TrimSpace(s)
}

// CleanMarkdownCode удаляет все markdown code blocks из текста.
//
// В отличие от CleanJsonBlock, эта функция работает с полным текстом,
// содержащим несколько code blocks, и удаляет их все, оставляя только
// обычный текст.
//
// Примеры:
//   "Пример:\n```json\n{"a": 1}\n```\nКонец" → "Пример:\nКонец"
func CleanMarkdownCode(s string) string {
	lines := strings.Split(s, "\n")
	var result []string

	inCodeBlock := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Проверяем начало/конец code block
		if strings.HasPrefix(trimmed, "```") {
			inCodeBlock = !inCodeBlock
			continue
		}

		// Добавляем строку только если не внутри code block
		if !inCodeBlock {
			result = append(result, line)
		}
	}

	return strings.Join(result, "\n")
}

// TrimCommonPrefixes удаляет общие префиксы из строк текста.
//
// Полезно для очистки цитат или numbered lists от лишних пробелов
// и символов, которые LLM может добавить при форматировании.
func TrimCommonPrefixes(s string) string {
	lines := strings.Split(s, "\n")
	if len(lines) == 0 {
		return s
	}

	// Находим общий префикс (пробелы, табы, маркировка, нумерация)
	var commonPrefix string
	for i := 0; i < len(lines[0]); i++ {
		c := lines[0][i]
		// Префикс может содержать: пробелы, табы, '-', '*', '.', цифры
		if c != ' ' && c != '\t' && c != '-' && c != '*' && c != '.' &&
			(c < '0' || c > '9') {
			break
		}

		isCommon := true
		for _, line := range lines {
			if i >= len(line) || line[i] != c {
				isCommon = false
				break
			}
		}

		if !isCommon {
			break
		}
		commonPrefix += string(c)
	}

	// Удаляем общий префикс из всех строк
	result := make([]string, len(lines))
	for i, line := range lines {
		if strings.HasPrefix(line, commonPrefix) {
			result[i] = strings.TrimPrefix(line, commonPrefix)
			// Дополнительно удаляем оставшиеся пробелы после префикса (например, "1. " → "1.")
			result[i] = strings.TrimLeft(result[i], " \t")
		} else {
			result[i] = line
		}
	}

	return strings.Join(result, "\n")
}

// SanitizeLLMOutput выполняет комплексную очистку вывода LLM.
//
// Применяет несколько шагов очистки:
// 1. Удаляет markdown code blocks
// 2. Удаляет лишние пробелы в начале/конце строк
// 3. Удаляет пустые строки
// 4. Нормализует переносы строк
//
// Используется как финальный шаг перед отображением ответа пользователю.
func SanitizeLLMOutput(s string) string {
	// 1. Удаляем markdown code blocks
	s = CleanMarkdownCode(s)

	// 2. Разбиваем на строки и обрезаем пробелы
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSpace(line)
	}

	// 3. Удаляем пустые строки (включая середину)
	var nonEmpty []string
	for _, line := range lines {
		if line != "" {
			nonEmpty = append(nonEmpty, line)
		}
	}

	// 4. Собираем результат
	return strings.Join(nonEmpty, "\n")
}

// ExtractJSON пытается извлечь JSON объект из строки.
//
// LLM часто возвращает JSON вместе с пояснительным текстом.
// Эта функция находит первый валидный JSON-объект в тексте.
//
// Возвращает пустую строку если JSON-объект не найден.
//
// ВНИМАНИЕ: Не валидирует JSON, только извлекает его по эвристикам.
// Для валидации используйте json.Unmarshal().
func ExtractJSON(s string) string {
	// Ищем первый { и последний }
	start := strings.Index(s, "{")
	if start == -1 {
		return ""
	}

	// Проверяем что это не массив (пропускаем [{)
	if start > 0 && s[start-1] == '[' {
		// Это элемент массива, не извлекаем
		return ""
	}

	// Ищем соответствующую закрывающую скобку
	depth := 0
	for i := start; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}

	return s[start:]
}

// SplitChunks разбивает текст на части по разделителю.
//
// Полезно для обработки многострочных ответов LLM,
// где разные части разделены пустыми строками или разделителями.
//
// Примеры:
//   SplitChunks("a\n\nb\n\nc", "\n\n") → ["a", "b", "c"]
//   SplitChunks("a---b---c", "---") → ["a", "b", "c"]
func SplitChunks(s string, separator string) []string {
	if separator == "" {
		return []string{s}
	}

	chunks := strings.Split(s, separator)
	result := make([]string, 0, len(chunks))

	for _, chunk := range chunks {
		trimmed := strings.TrimSpace(chunk)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}

	return result
}

// WrapText переносит текст по словам с учетом заданной ширины.
//
// Сохраняет существующие переносы строк и не разрывает слова.
// Если ширина меньше 1, возвращает исходный текст без изменений.
//
// Параметры:
//   s - исходный текст
//   width - максимальная ширина строки в символах
//
// Примеры:
//   WrapText("hello world", 5) → "hello\nworld"
//   WrapText("a\nb\nc", 10) → "a\nb\nc" (сохраняет переносы)
func WrapText(s string, width int) string {
	if width < 1 {
		return s
	}

	// Разбиваем на исходные строки
	lines := strings.Split(s, "\n")
	var result []string

	for _, line := range lines {
		// Пропускаем пустые строки
		if strings.TrimSpace(line) == "" {
			result = append(result, "")
			continue
		}

		// Разбиваем строку на слова
		words := strings.Fields(line)

		if len(words) == 0 {
			result = append(result, "")
			continue
		}

		currentLine := words[0]
		for _, word := range words[1:] {
			// Проверяем, поместится ли следующее слово
			testLine := currentLine + " " + word
			if len(testLine) <= width {
				currentLine = testLine
			} else {
				// Слово не помещается - переносим строку
				result = append(result, currentLine)
				currentLine = word
			}
		}
		result = append(result, currentLine)
	}

	return strings.Join(result, "\n")
}

```

=================

# utils/image.go

```go
// Package utils предоставляет утилиты для обработки изображений.
package utils

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png" // Регистрируем PNG декодер

	"github.com/nfnt/resize"
)

// ResizeImage ресайзит изображение до указанной ширины, сохраняя пропорции.
//
// Параметры:
//   - data: байты исходного изображения (JPEG, PNG)
//   - maxWidth: целевая ширина в пикселях. Если 0 или меньше исходной ширины — ресайз не применяется.
//   - quality: качество JPEG при кодировании (1-100). Рекомендуется 85.
//
// Возвращает байты JPEG изображения (для LLM и base64).
func ResizeImage(data []byte, maxWidth int, quality int) ([]byte, error) {
	// 1. Декодируем изображение
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode image: %w", err)
	}

	originalBounds := img.Bounds()
	originalWidth := originalBounds.Dx()

	// 2. Проверяем нужен ли ресайз
	if maxWidth <= 0 || originalWidth <= maxWidth {
		// Ресайз не нужен, но конвертируем в JPEG для консистентности
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
			return nil, fmt.Errorf("encode to jpeg: %w", err)
		}
		return buf.Bytes(), nil
	}

	// 3. Вычисляем новую высоту сохраняя aspect ratio
	aspectRatio := float64(originalBounds.Dy()) / float64(originalWidth)
	newHeight := uint(float64(maxWidth) * aspectRatio)

	// 4. Ресайзим используя Lanczos3 (качественный алгоритм)
	resized := resize.Resize(uint(maxWidth), newHeight, img, resize.Lanczos3)

	// 5. Кодируем в JPEG
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, resized, &jpeg.Options{Quality: quality}); err != nil {
		return nil, fmt.Errorf("encode resized image: %w", err)
	}

	return buf.Bytes(), nil
}

```

=================

# utils/sanitize.go

```go
// Package utils предоставляет утилиты для санитайза и оптимизации данных.
package utils

import (
	"encoding/json"
	"fmt"
	"strings"
)

// SanitizePLMJson убирает лишние поля из PLM JSON для экономии токенов.
//
// Удаляет:
//   - Блок "Ответственные" (служебная информация)
//   - Блок "Эскизы" (загружаются отдельно)
//   - Поля с "НомерСтроки" (технические)
//   - Поля со "Статус" (служебные)
//   - Поля с "ИдентификаторСтроки" (технические)
//   - **ВАЖНО**: Поля "Миниатюра", "Миниатюра_Файл" (огромные base64 данные)
//   - Поля "НавигационнаяСсылкаМиниатюра", "ФайлКартинки", "ВидЭскизаФайл" (ссылки на файлы)
//   - Пустые строки и пустые значения
//   - Технические поля: "ДобавленАвтоматически", "ДатаВыгрузкиВУТ", "ВыгружатьВУТ",
//     "АртикулПрототипа", "Прототип", "ВидПереноса", "СостояниеСогласования",
//     "СезоннаяКоллекцияПервогоПроизводства", "ТНВЭД", "НаименованиеПоКлассификатору",
//     "ПодлежитМаркировкеЧЗ", "ВнешнееОформление", "ИдентификаторСтрокиКонструкции"
//
// Сохраняет только полезную информацию для описания товара:
//   - Артикул, Наименование, Категория, Сезон
//   - Цвета, Материалы, РазмерныйРяд
//   - РекомендацииПоУходу, Комментарий
func SanitizePLMJson(jsonStr string) (string, error) {
	// Парсим JSON
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
		return "", fmt.Errorf("failed to parse JSON: %w", err)
	}

	// Поля для полного удаления (целые блоки)
	blocksToRemove := []string{
		"Ответственные",
		"Эскизы",
		"ДополнительныеРеквизиты",
		"ДополнительныеРеквизитыКомплектующих",
		"ДополнительныеРеквизитыМаркетинг",
	}

	// Поля для удаления (на всех уровнях вложенности)
	// **ВАЖНО**: "Миниатюра_Файл" содержит огромный base64 массив - его нужно удалять в первую очередь
	fieldsToRemove := []string{
		"НомерСтроки",
		"ИдентификаторСтроки",
		"Статус",
		"Миниатюра",
		"Миниатюра_Файл",      // ВАЖНО: enormous base64 data
		"НавигационнаяСсылкаМиниатюра",
		"ФайлКартинки",
		"ВидЭскизаФайл",
		"ДобавленАвтоматически",
		"ДатаВыгрузкиВУТ",
		"ВыгружатьВУТ",
		"АртикулПрототипа",
		"Прототип",
		"ВидПереноса",
		"СостояниеСогласования",
		"СезоннаяКоллекцияПервогоПроизводства",
		"ТНВЭД",
		"НаименованиеПоКлассификатору",
		"ПодлежитМаркировкеЧЗ",
		"ВнешнееОформление",
		"ИдентификаторСтрокиКонструкции",
		"Код", // Технический код, не нужен для описания
	}

	// Удаляем целые блоки
	for _, block := range blocksToRemove {
		delete(data, block)
	}

	// Рекурсивно очищаем ВСЕ данные от ненужных полей и пустых значений
	// Это гарантирует удаление "Миниатюра_Файл" на любом уровне вложенности
	data = cleanData(data, fieldsToRemove)

	// Конвертируем обратно в JSON с отступами для читаемости
	result, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal sanitized JSON: %w", err)
	}

	return string(result), nil
}

// cleanData рекурсивно очищает данные от технических полей и пустых значений
func cleanData(data map[string]interface{}, fieldsToRemove []string) map[string]interface{} {
	result := make(map[string]interface{})

	for key, value := range data {
		// Пропускаем поля для удаления
		if shouldRemove(key, fieldsToRemove) {
			continue
		}

		// Рекурсивно обрабатываем вложенные map и слайсы
		switch v := value.(type) {
		case map[string]interface{}:
			cleaned := cleanData(v, fieldsToRemove)
			if len(cleaned) > 0 {
				result[key] = cleaned
			}
		case []interface{}:
			cleaned := cleanSlice(v, fieldsToRemove)
			if len(cleaned) > 0 {
				result[key] = cleaned
			}
		case string:
			// Пропускаем пустые строки
			if strings.TrimSpace(v) != "" {
				result[key] = v
			}
		case nil:
			// Пропускаем nil значения
		default:
			result[key] = v
		}
	}

	return result
}

// cleanSlice очищает слайсы от технических полей и пустых значений
func cleanSlice(slice []interface{}, fieldsToRemove []string) []interface{} {
	result := make([]interface{}, 0, len(slice))

	for _, item := range slice {
		switch v := item.(type) {
		case map[string]interface{}:
			cleaned := cleanData(v, fieldsToRemove)
			if len(cleaned) > 0 {
				result = append(result, cleaned)
			}
		case []interface{}:
			cleaned := cleanSlice(v, fieldsToRemove)
			if len(cleaned) > 0 {
				result = append(result, cleaned)
			}
		case string:
			// Пропускаем пустые строки
			if strings.TrimSpace(v) != "" {
				result = append(result, v)
			}
		case nil:
			// Пропускаем nil значения
		default:
			result = append(result, v)
		}
	}

	return result
}

// shouldRemove проверяет, нужно ли удалить поле
func shouldRemove(key string, fieldsToRemove []string) bool {
	for _, field := range fieldsToRemove {
		if key == field || strings.Contains(key, field) {
			return true
		}
	}
	return false
}

// removeEmptyValues удаляет пустые значения из map
func removeEmptyValues(m map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{})

	for key, value := range m {
		switch v := value.(type) {
		case string:
			if strings.TrimSpace(v) != "" {
				result[key] = v
			}
		case nil:
			// Пропускаем
		case []interface{}:
			if len(v) > 0 {
				result[key] = v
			}
		case map[string]interface{}:
			if len(v) > 0 {
				result[key] = v
			}
		default:
			result[key] = v
		}
	}

	return result
}

```

=================

# utils/sanitize_test.go

```go
package utils

import (
	"strings"
	"testing"
)

func TestSanitizePLMJson(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		contains []string // Must contain these substrings
		notContains []string // Must NOT contain these substrings
	}{
		{
			name: "removes Ответственные block",
			input: `{"Реквизиты":{"Артикул":"12345"},"Ответственные":[{"НомерСтроки":1}]}`,
			contains: []string{"Артикул", "12345"},
			notContains: []string{"Ответственные", "НомерСтроки"},
		},
		{
			name: "removes Эскизы block",
			input: `{"Реквизиты":{"Артикул":"12345"},"Эскизы":[{"НомерСтроки":1}]}`,
			contains: []string{"Артикул", "12345"},
			notContains: []string{"Эскизы", "НомерСтроки"},
		},
		{
			name: "removes Миниатюра_Файл (base64 data)",
			input: `{"Реквизиты":{"Артикул":"12345","Миниатюра_Файл":"iVBORw0KGgoAAAANSUhEUg..."}}`,
			contains: []string{"Артикул", "12345"},
			notContains: []string{"Миниатюра_Файл", "iVBORw0KG"},
		},
		{
			name: "removes multiple technical fields",
			input: `{
				"Реквизиты": {
					"Артикул": "12345",
					"Наименование": "Test Product",
					"НомерСтроки": 1,
					"ИдентификаторСтроки": "abc",
					"Статус": "Active",
					"Код": 123
				}
			}`,
			contains: []string{"Артикул", "12345", "Наименование", "Test Product"},
			notContains: []string{"НомерСтроки", "ИдентификаторСтроки", "Статус", `"Код"`},
		},
		{
			name: "keeps useful product data",
			input: `{
				"Реквизиты": {
					"Артикул": "12611516",
					"Наименование": "Куртка джинсовая",
					"СезоннаяКоллекция": "Весна-Лето 2026",
					"РазмерныйРяд": "128,134,140",
					"Цвета": [{"Пантон": "Blue"}]
				}
			}`,
			contains: []string{
				"Артикул", "12611516",
				"Наименование", "Куртка джинсовая",
				"СезоннаяКоллекция", "Весна-Лето 2026",
				"РазмерныйРяд", "128,134,140",
				"Пантон", "Blue",
			},
			notContains: []string{},
		},
		{
			name: "removes empty values",
			input: `{"Реквизиты":{"Артикул":"12345","Конструкция":"","Комментарий":"   "}}`,
			contains: []string{"Артикул", "12345"},
			notContains: []string{"Конструкция", "Комментарий"},
		},
		{
			name: "removes technical fields from nested arrays",
			input: `{
				"Цвета": [
					{"НомерСтроки":1,"Пантон":"Blue","Основной":true},
					{"НомерСтроки":2,"Пантон":"Red","Основной":false}
				]
			}`,
			contains: []string{"Пантон", "Blue", "Red", "Основной"},
			notContains: []string{"НомерСтроки"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := SanitizePLMJson(tt.input)
			if err != nil {
				t.Fatalf("SanitizePLMJson() error = %v", err)
			}

			// Check required substrings
			for _, mustContain := range tt.contains {
				if !strings.Contains(result, mustContain) {
					t.Errorf("Result must contain %q, but got:\n%s", mustContain, result)
				}
			}

			// Check forbidden substrings
			for _, mustNotContain := range tt.notContains {
				if strings.Contains(result, mustNotContain) {
					t.Errorf("Result must NOT contain %q, but got:\n%s", mustNotContain, result)
				}
			}
		})
	}
}

func TestSanitizePLMJsonRealExample(t *testing.T) {
	// Test with a subset of real PLM JSON structure
	input := `{
		"Реквизиты": {
			"Код": 12588,
			"Наименование": "12611516 Куртка джинсовая Мужской Tween",
			"Артикул": "12611516",
			"СезоннаяКоллекция": "Весна-Лето 2026",
			"ТоварнаяПодгруппа": "Куртка джинсовая",
			"РазмерныйРяд": "128,134,140,146,152,158,164,170,176",
			"Комментарий": "Fit Oversize",
			"Миниатюра_Файл": "iVBORw0KGgoAAAANSUhEUgAAANQAAACWCAYAAACmTqZ/AAAAIGNIUk0AA...",
			"Код": 12588
		},
		"Ответственные": [
			{"НомерСтроки":1,"Роль":"Designer","Сотрудник":"John"},
			{"НомерСтроки":2,"Роль":"Merch","Сотрудник":"Jane"}
		],
		"Эскизы": [
			{"НомерСтроки":1,"Файл":"sketch1.jpg"}
		]
	}`

	result, err := SanitizePLMJson(input)
	if err != nil {
		t.Fatalf("SanitizePLMJson() error = %v", err)
	}

	// Must keep important data
	mustContain := []string{
		"Артикул",
		"12611516",
		"Наименование",
		"Куртка джинсовая",
		"СезоннаяКоллекция",
		"Весна-Лето 2026",
		"ТоварнаяПодгруппа",
		"РазмерныйРяд",
		"128,134,140",
		"Комментарий",
		"Fit Oversize",
	}

	// Must remove technical/noise data
	mustNotContain := []string{
		"Ответственные",
		"Эскизы",
		"НомерСтроки",
		"Миниатюра_Файл",
		"iVBORw0KG",
		"Роль",
		"Сотрудник",
		"Файл",
		`"Код": 12588`, // Technical code field
	}

	for _, s := range mustContain {
		if !strings.Contains(result, s) {
			t.Errorf("Result must contain %q\nGot:\n%s", s, result)
		}
	}

	for _, s := range mustNotContain {
		if strings.Contains(result, s) {
			t.Errorf("Result must NOT contain %q\nGot:\n%s", s, result)
		}
	}

	// Check that result is significantly smaller than input
	inputLen := len(input)
	resultLen := len(result)
	if resultLen > inputLen*80/100 { // Should be at least 20% smaller
		t.Errorf("Sanitized JSON (%d bytes) should be significantly smaller than input (%d bytes)", resultLen, inputLen)
	}
}

```

=================

# utils/simplelogger.go

```go
// Package utils предоставляет простой файловый логгер для TUI приложений.
//
// Логгер создаёт .log файл в текущей директории с timestamp в имени.
// Thread-safe через sync.Mutex.
package utils

import (
	"fmt"
	"os"
	"sync"
	"time"
)

var (
	logFile    *os.File
	logMutex   sync.Mutex
	initialized bool
)

// InitLogger создает/открывает .log файл в текущей директории.
//
// Имя файла: poncho-YYYY-MM-DD-HH-MM.log (например, poncho-2025-12-27-15-30.log)
// Файл создаётся в той же директории, откуда запущена утилита.
func InitLogger() error {
	logMutex.Lock()
	defer logMutex.Unlock()

	if initialized {
		return nil
	}

	// Имя файла: poncho-2025-12-27-15-30.log
	timestamp := time.Now().Format("2006-01-02-15-04")
	filename := fmt.Sprintf("poncho-%s.log", timestamp)

	var err error
	logFile, err = os.OpenFile(filename, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to open log file: %w", err)
	}

	initialized = true
	// Пишем напрямую без Info чтобы избежать deadlock (мьютекс уже захвачен)
	timestampNow := time.Now().Format("2006-01-02 15:04:05")
	initLine := fmt.Sprintf("[%s] INFO: Logger initialized file=%s\n", timestampNow, filename)

	if _, err := logFile.WriteString(initLine); err != nil {
		// Fallback на stderr при ошибке
		fmt.Fprintf(os.Stderr, "%s", initLine)
		fmt.Fprintf(os.Stderr, "[LOGGER ERROR: WriteString failed: %v]\n", err)
	}

	if err := logFile.Sync(); err != nil {
		fmt.Fprintf(os.Stderr, "[LOGGER WARNING: Sync failed: %v]\n", err)
	}

	return nil
}

// Info - информационное сообщение.
func Info(msg string, keyvals ...any) {
	log("INFO", msg, keyvals...)
}

// Error - сообщение об ошибке.
func Error(msg string, keyvals ...any) {
	log("ERROR", msg, keyvals...)
}

// Debug - отладочное сообщение.
func Debug(msg string, keyvals ...any) {
	log("DEBUG", msg, keyvals...)
}

// Warn - предупреждение.
func Warn(msg string, keyvals ...any) {
	log("WARN", msg, keyvals...)
}

// log - внутренняя функция записи в лог.
//
// Формат: [YYYY-MM-DD HH:MM:SS] LEVEL: message key1=value1 key2=value2
// При ошибке записи в файл, fallback на stderr.
func log(level, msg string, keyvals ...any) {
	logMutex.Lock()
	defer logMutex.Unlock()

	if logFile == nil {
		return
	}

	timestamp := time.Now().Format("2006-01-02 15:04:05")
	line := fmt.Sprintf("[%s] %s: %s", timestamp, level, msg)

	for i := 0; i < len(keyvals); i += 2 {
		if i+1 < len(keyvals) {
			line += fmt.Sprintf(" %v=%v", keyvals[i], keyvals[i+1])
		}
	}

	line += "\n"

	// Пишем в файл с обработкой ошибок
	if _, err := logFile.WriteString(line); err != nil {
		// Fallback: если файл недоступен, пишем в stderr
		fmt.Fprintf(os.Stderr, "%s", line)
		fmt.Fprintf(os.Stderr, "[LOGGER ERROR: WriteString failed: %v]\n", err)
		return
	}

	if err := logFile.Sync(); err != nil {
		// Sync failed - warning в stderr, но лог уже записан
		fmt.Fprintf(os.Stderr, "[LOGGER WARNING: Sync failed: %v]\n", err)
	}
}

// Close закрывает лог-файл.
//
// Вызывается через defer в main().
func Close() {
	logMutex.Lock()
	defer logMutex.Unlock()

	if logFile != nil {
		if err := logFile.Close(); err != nil {
			// Логгер уже закрывается, только stderr
			fmt.Fprintf(os.Stderr, "[LOGGER WARNING: Close failed: %v]\n", err)
		}
		logFile = nil
	}
}

```

=================

# wb/client.go

```go
// Package wb provides a reusable SDK for Wildberries API.
//
// Architecture:
//
// This is an **API SDK**, not just a "dumb" HTTP client. It provides:
//   - HTTP client with retry, rate limiting, and error classification
//   - High-level methods that handle WB API-specific response wrappers
//   - Auto-pagination for endpoints that return partial data
//   - Client-side filtering for API limitations (workarounds)
//
// Comparison with S3 client:
//   - S3 client (pkg/s3storage) is a "dumb" client - S3 API is simple and standardized
//   - WB client is an SDK - WB API is complex with custom response formats and quirks
//
// Usage pattern:
//   - pkg/wb - reusable SDK (can be used in any project)
//   - pkg/tools/std - thin wrappers for LLM function calling
//
// Design rationale:
// Auto-pagination and filtering are NOT business logic - they are technical workarounds
// for WB API limitations. Moving them to tools would violate DRY and make code harder
// to maintain.
package wb

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"golang.org/x/time/rate"
)

// Константы удалены - все параметры теперь из config.yaml
// Defaults для tools задаются в wb секции config.yaml

// ErrorType представляет тип ошибки при работе с WB API.
type ErrorType int

const (
	ErrUnknown ErrorType = iota
	ErrAuthFailed
	ErrTimeout
	ErrNetwork
	ErrRateLimit
)

// String возвращает строковое представление типа ошибки.
func (e ErrorType) String() string {
	switch e {
	case ErrAuthFailed:
		return "authentication_failed"
	case ErrTimeout:
		return "timeout"
	case ErrNetwork:
		return "network_error"
	case ErrRateLimit:
		return "rate_limit"
	default:
		return "unknown"
	}
}

// HumanMessage возвращает человекочитаемое сообщение для типа ошибки.
func (e ErrorType) HumanMessage() string {
	switch e {
	case ErrAuthFailed:
		return "API ключ недействителен или отсутствует. Проверьте WB_API_KEY в конфигурации."
	case ErrTimeout:
		return "Превышено время ожидания. Сервер WB не отвечает или проблемы с сетью."
	case ErrNetwork:
		return "Сервер WB недоступен. Проверьте подключение к интернету."
	case ErrRateLimit:
		return "Превышен лимит запросов. Подождите перед следующей попыткой."
	default:
		return "Неизвестная ошибка при подключении к WB API."
	}
}

// HTTPClient интерфейс для выполнения HTTP запросов.
//
// Позволяет мокировать HTTP клиент в тестах (Rule 9).
// Стандартный *http.Client реализует этот интерфейс.
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

type Client struct {
	apiKey        string
	httpClient    HTTPClient // Интерфейс вместо конкретного типа для testability
	retryAttempts int        // Количество retry попыток

	mu       sync.RWMutex
	limiters map[string]*rate.Limiter // tool ID → limiter
}

// IsDemoKey проверяет что используется demo ключ (для mock режима).
func (c *Client) IsDemoKey() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.apiKey == "demo_key"
}

// New создает новый клиент для работы с Wildberries API.
//
// Параметры:
//   - apiKey: API ключ для авторизации в Wildberries
//
// Возвращает настроенный клиент с дефолтными значениями из WBConfig.GetDefaults().
// Рекомендуется использовать NewFromConfig для конфигурируемого клиента.
//
// DEPRECATED: Используйте NewFromConfig для явного указания всех параметров.
func New(apiKey string) *Client {
	// Используем дефолтную конфигурацию для согласованности с NewFromConfig
	defaultCfg := config.WBConfig{
		APIKey:        apiKey,
		RateLimit:     100,  // дефолтный rate limit
		BurstLimit:    5,    // дефолтный burst
		RetryAttempts: 3,    // дефолтный retry
		Timeout:       "30s", // дефолтный timeout
	}
	cfg := defaultCfg.GetDefaults()

	// Парсим timeout (заведомо валидный, но на всякий случай)
	timeout, _ := time.ParseDuration(cfg.Timeout)

	return &Client{
		apiKey:        cfg.APIKey,
		retryAttempts: cfg.RetryAttempts,
		httpClient: &http.Client{
			Timeout: timeout,
		},
		limiters: make(map[string]*rate.Limiter),
	}
}

// NewFromConfig создает новый клиент из конфигурации.
//
// Параметры:
//   - cfg: Конфигурация WB API с настройками timeout
//
// Возвращает настроенный клиент с параметрами из конфига.
// Лимитеры создаются динамически при вызове Get().
// Поля с нулевыми значениями используют дефолтные значения через GetDefaults().
func NewFromConfig(cfg config.WBConfig) (*Client, error) {
	// Применяем дефолтные значения
	cfg = cfg.GetDefaults()

	if cfg.APIKey == "" {
		return nil, fmt.Errorf("wb.api_key is required")
	}

	// Парсим timeout
	timeout, err := time.ParseDuration(cfg.Timeout)
	if err != nil {
		return nil, fmt.Errorf("invalid wb.timeout format: %w", err)
	}

	return &Client{
		apiKey:        cfg.APIKey,
		retryAttempts: cfg.RetryAttempts,
		httpClient: &http.Client{
			Timeout: timeout,
		},
		limiters: make(map[string]*rate.Limiter),
	}, nil
}

// ClassifyError классифицирует ошибку по типу для лучшей диагностики.
//
// Анализирует текст ошибки и возвращает соответствующий тип:
//   - ErrAuthFailed: ошибки 401, unauthorized, Forbidden
//   - ErrTimeout: timeout, deadline exceeded
//   - ErrNetwork: connection refused, no such host
//   - ErrRateLimit: ошибки 429, Too Many Requests
//   - ErrUnknown: все остальные ошибки
func (c *Client) ClassifyError(err error) ErrorType {
	if err == nil {
		return ErrUnknown
	}

	errMsg := err.Error()
	errMsgLower := strings.ToLower(errMsg)

	// Проверка ошибок авторизации
	if strings.Contains(errMsg, "401") ||
		strings.Contains(errMsgLower, "unauthorized") ||
		strings.Contains(errMsg, "Forbidden") {
		return ErrAuthFailed
	}

	// Проверка таймаутов
	if strings.Contains(errMsgLower, "timeout") ||
		strings.Contains(errMsg, "deadline exceeded") {
		return ErrTimeout
	}

	// Проверка сетевых ошибок
	if strings.Contains(errMsg, "connection refused") ||
		strings.Contains(errMsg, "no such host") {
		return ErrNetwork
	}

	// Проверка rate limiting
	if strings.Contains(errMsg, "429") ||
		strings.Contains(errMsg, "Too Many Requests") {
		return ErrRateLimit
	}

	return ErrUnknown
}

// httpRequest описывает параметры HTTP запроса.
type httpRequest struct {
	method string
	url    string
	body   io.Reader
}

// doRequest выполняет HTTP запрос с retry логикой и rate limiting.
//
// Общий метод для Get() и Post(), реализующий retry loop, rate limiting
// и обработку 429 ответов.
func (c *Client) doRequest(ctx context.Context, toolID string, rateLimit int, burst int, req httpRequest, dest interface{}) error {
	// Получаем или создаём limiter для этого tool
	limiter := c.getOrCreateLimiter(toolID, rateLimit, burst)

	var lastErr error

	// Retry loop
	for i := 0; i < c.retryAttempts; i++ {
		// 1. Ждем разрешения от лимитера (блокирует горутину, если превысили лимит)
		if err := limiter.Wait(ctx); err != nil {
			return fmt.Errorf("rate limiter wait: %w", err)
		}

		httpReq, err := http.NewRequestWithContext(ctx, req.method, req.url, req.body)
		if err != nil {
			return err
		}

		httpReq.Header.Set("Authorization", c.apiKey)
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Accept", "application/json")

		resp, err := c.httpClient.Do(httpReq)
		if err != nil {
			lastErr = err
			continue // Сетевая ошибка, пробуем еще
		}
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)

		// Обработка 429 (Too Many Requests)
		if resp.StatusCode == http.StatusTooManyRequests {
			// Читаем заголовок X-Ratelimit-Retry или Retry-After
			retryAfter := 1 * time.Second // Дефолт
			if s := resp.Header.Get("X-Ratelimit-Retry"); s != "" {
				if sec, err := strconv.Atoi(s); err == nil {
					retryAfter = time.Duration(sec) * time.Second
				}
			}

			// Ждем и ретраем
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(retryAfter):
				continue
			}
		}

		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("wb api error: status %d, body: %s", resp.StatusCode, string(body))
		}

		if err := json.Unmarshal(body, dest); err != nil {
			return fmt.Errorf("unmarshal error: %w", err)
		}

		return nil // Успех
	}

	return fmt.Errorf("max retries exceeded, last error: %v", lastErr)
}

// Get выполняет GET запрос к Wildberries API с поддержкой Rate Limit и Retries.
//
// Параметры передаются при каждом вызове, что позволяет каждому tool иметь
// свой endpoint и rate limit.
//
// Параметры:
//   - ctx: контекст для отмены
//   - toolID: идентификатор tool для выбора limiter (например, "get_wb_parent_categories")
//   - baseURL: базовый URL API (например, "https://content-api.wildberries.ru")
//   - rateLimit: лимит запросов в минуту
//   - burst: burst для rate limiter
//   - path: путь к endpoint (например, "/api/v1/directory/parent-categories")
//   - params: query параметры (может быть nil)
//   - dest: указатель на структуру для unmarshal результата
//
// Возвращает ошибку если запрос не удался.
func (c *Client) Get(ctx context.Context, toolID string, baseURL string, rateLimit int, burst int, path string, params url.Values, dest interface{}) error {
	// Валидация обязательных параметров - client "тупой", ожидает что их предоставит tool
	if baseURL == "" {
		return fmt.Errorf("baseURL is required (tool should provide value from config)")
	}
	if rateLimit <= 0 {
		return fmt.Errorf("rateLimit must be positive (tool should provide value from config)")
	}
	if burst <= 0 {
		return fmt.Errorf("burst must be positive (tool should provide value from config)")
	}

	u, err := url.Parse(baseURL + path)
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}
	if params != nil {
		u.RawQuery = params.Encode()
	}

	return c.doRequest(ctx, toolID, rateLimit, burst, httpRequest{
		method: "GET",
		url:    u.String(),
		body:   nil,
	}, dest)
}

// getOrCreateLimiter возвращает существующий limiter для toolID или создаёт новый.
//
// Параметры:
//   - toolID: идентификатор tool (ключ для map)
//   - rateLimit: запросов в минуту
//   - burst: burst для rate limiter
//
// Возвращает *rate.Limiter для этого tool.
func (c *Client) getOrCreateLimiter(toolID string, rateLimit int, burst int) *rate.Limiter {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Если limiter уже существует - возвращаем
	if limiter, exists := c.limiters[toolID]; exists {
		return limiter
	}

	// Создаём новый limiter
	// rateLimit в запросах/минуту → rate.Limit в запросах/секунду
	ratePerSec := float64(rateLimit) / 60.0
	limiter := rate.NewLimiter(rate.Limit(ratePerSec), burst)
	c.limiters[toolID] = limiter

	return limiter
}

// Post выполняет POST запрос к Wildberries API с поддержкой Rate Limit и Retries.
//
// Параметры передаются при каждом вызове, что позволяет каждому tool иметь
// свой endpoint и rate limit.
//
// Параметры:
//   - ctx: контекст для отмены
//   - toolID: идентификатор tool для выбора limiter (например, "search_wb_products")
//   - baseURL: базовый URL API (например, "https://content-api.wildberries.ru")
//   - rateLimit: лимит запросов в минуту
//   - burst: burst для rate limiter
//   - path: путь к endpoint (например, "/api/v2/list/goods")
//   - body: тело запроса (будет сериализовано в JSON)
//   - dest: указатель на структуру для unmarshal результата
//
// Возвращает ошибку если запрос не удался.
func (c *Client) Post(ctx context.Context, toolID string, baseURL string, rateLimit int, burst int, path string, body interface{}, dest interface{}) error {
	// Валидация обязательных параметров - client "тупой", ожидает что их предоставит tool
	if baseURL == "" {
		return fmt.Errorf("baseURL is required (tool should provide value from config)")
	}
	if rateLimit <= 0 {
		return fmt.Errorf("rateLimit must be positive (tool should provide value from config)")
	}
	if burst <= 0 {
		return fmt.Errorf("burst must be positive (tool should provide value from config)")
	}

	u, err := url.Parse(baseURL + path)
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}

	// Сериализуем body
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal body: %w", err)
	}

	return c.doRequest(ctx, toolID, rateLimit, burst, httpRequest{
		method: "POST",
		url:    u.String(),
		body:   strings.NewReader(string(bodyJSON)),
	}, dest)
}
// PingResponse представляет ответ от ping endpoint Wildberries Content API.
//
// Поля:
//   - Status: Статус сервиса (обычно "OK" при успешном ответе)
//   - TS: Timestamp ответа сервера
type PingResponse struct {
	Status string `json:"Status"`
	TS     string `json:"TS"`
}

// Ping проверяет связь именно с сервисом Content API.
//
// Параметры:
//   - ctx: контекст для отмены
//   - baseURL: базовый URL API (например, "https://content-api.wildberries.ru")
//   - rateLimit: лимит запросов в минуту
//   - burst: burst для rate limiter
//
// Возвращает ответ от API или ошибку. Полезен для диагностики:
// - проверка доступности сервиса
// - проверка валидности API ключа (401 = unauthorized)
// - определение сетевых проблем
func (c *Client) Ping(ctx context.Context, baseURL string, rateLimit int, burst int) (*PingResponse, error) {
	var resp PingResponse

	// Ping возвращает простой JSON без обертки APIResponse[T]
	err := c.Get(ctx, "ping_wb_api", baseURL, rateLimit, burst, "/ping", nil, &resp)
	if err != nil {
		return nil, fmt.Errorf("ping failed: %w", err)
	}

	if resp.Status != "OK" {
		return nil, fmt.Errorf("ping status not OK: %s", resp.Status)
	}

	return &resp, nil
}

```

=================

# wb/colors.go

```go
/* 
Для реализации нечеткого поиска (Fuzzy Search) по названиям цветов в Go подойдет библиотека, вычисляющая расстояние Левенштейна или использующая триграммы. Для простоты  github.com/lithammer/fuzzysearch или простая реализация на базе стандартной библиотеки (если не хотим лишних зависимостей).

Учитывая, что справочник цветов не такой уж огромный (не миллионы, а тысячи), простой перебор с ранжированием по сходству будет работать мгновенно.

Как это интегрировать в Flow?
Загрузка при старте:
В main.go или при первом обращении к тулу загружаем цвета:

go
colors, err := wbClient.GetColors(ctx)
colorService := wb.NewColorService(colors)
Использование в Tool:
Когда LLM просит найти цвет, мы вызываем colorService.FindTopMatches("персиковый", 5).
Это вернет:

"персиковый"

"персиковый джем"

"персиковый мелок"

И этот короткий список мы отдаем LLM, чтобы она выбрала лучший вариант.
*/
package wb

import (
    "sort"
    "strings"
    _ "unicode"
)

// SearchMatch - результат поиска
type SearchMatch struct {
    Color Color
    Score float64 // Чем больше, тем лучше (0.0 - 1.0)
}

// ColorService - обертка над списком цветов для поиска
type ColorService struct {
    colors []Color
}

func NewColorService(colors []Color) *ColorService {
    return &ColorService{colors: colors}
}

// FindTopMatches ищет топ-N похожих цветов
func (s *ColorService) FindTopMatches(query string, topN int) []Color {
    query = strings.ToLower(strings.TrimSpace(query))
    var matches []SearchMatch

    for _, c := range s.colors {
        target := strings.ToLower(c.Name)
        
        // 1. Точное совпадение - высший приоритет
        if target == query {
            matches = append(matches, SearchMatch{Color: c, Score: 1.0})
            continue
        }

        // 2. Вхождение (substring)
        if strings.Contains(target, query) {
            // Штраф за лишние символы: len(query) / len(target)
            // Если ищем "красный", то "темно-красный" получит меньший скор, чем "красный"
            score := float64(len(query)) / float64(len(target)) * 0.9 
            matches = append(matches, SearchMatch{Color: c, Score: score})
            continue
        }

        // 3. Обратное вхождение (если запрос длиннее: "ярко-красный" ищем "красный")
        if strings.Contains(query, target) {
            score := float64(len(target)) / float64(len(query)) * 0.8
            matches = append(matches, SearchMatch{Color: c, Score: score})
            continue
        }
        
        // 4. (Опционально) Расстояние Левенштейна для опечаток
        // Можно добавить, если нужно, но для цветов обычно хватает substring
    }

    // Сортируем по убыванию Score
    sort.Slice(matches, func(i, j int) bool {
        return matches[i].Score > matches[j].Score
    })

    // Берем топ-N
    result := make([]Color, 0, topN)
    for i := 0; i < len(matches) && i < topN; i++ {
        result = append(result, matches[i].Color)
    }

    return result
}

```

=================

# wb/content.go

```go
// High-level API methods for Wildberries Content API.
//
// This file contains methods that wrap the raw HTTP client with WB-specific logic:
//   - Response wrapper handling (APIResponse[T])
//   - Auto-pagination for paginated endpoints
//   - Client-side filtering for API limitations
//
// Why this logic is in the SDK and not in tools:
//
// 1. Auto-pagination (GetAllSubjectsLazy, GetBrands):
//    These methods handle WB API's pagination mechanism internally.
//    This is a technical detail of the API, not business logic.
//    Moving pagination to tools would cause code duplication across tools.
//
// 2. Response wrapper handling (GetParentCategories, GetColors, etc.):
//    WB API wraps all responses in APIResponse[T] with Error/ErrorText fields.
//    Unwrapping is a concern of the SDK, not individual tools.
//
// 3. Client-side filtering (GetProductsByArticles):
//    WB Content API with Promotion token doesn't support filtering by multiple articles.
//    This is a workaround for API limitations, not business logic.
//
// The pattern:
//   - pkg/wb SDK: handles API quirks, pagination, response formats
//   - pkg/tools/std: thin wrappers for LLM function calling (just SDK calls + JSON marshaling)
package wb

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
)

// GetParentCategories возвращает список родительских категорий.
// Параметры baseURL, rateLimit, burst передаются из tool config.
func (c *Client) GetParentCategories(ctx context.Context, baseURL string, rateLimit int, burst int) ([]ParentCategory, error) {
	var resp APIResponse[[]ParentCategory]

	err := c.Get(ctx, "get_wb_parent_categories", baseURL, rateLimit, burst, "/content/v2/object/parent/all", nil, &resp)
	if err != nil {
		return nil, err
	}

	if resp.Error {
		return nil, fmt.Errorf("wb logic error: %s", resp.ErrorText)
	}

	return resp.Data, nil
}

// GetSubjects возвращает список предметов (подкатегорий).
// Можно фильтровать по parentID, name и т.д. (см. доку).
// Для простоты пока без фильтров или с опциональными.
// func (c *Client) GetSubjects(ctx context.Context, parentID int) ([]Subject, error) {
// 	params := url.Values{}
// 	if parentID > 0 {
// 		params.Set("parentID", fmt.Sprintf("%d", parentID))
// 	}
	
// 	// Лимит WB может отдавать много данных, возможно нужна пагинация (offset/limit)
// 	// Но в API /object/all пагинация делается через top/limit? 
// 	// В доке написано: "limit: int, offset: int". 
// 	// Давай добавим дефолтные лимиты, чтобы не качать всё
// 	params.Set("limit", "1000") 

// 	var resp APIResponse[[]Subject]
	
// 	err := c.get(ctx, "/content/v2/object/all", params, &resp)
// 	if err != nil {
// 		return nil, err
// 	}

// 	if resp.Error {
// 		return nil, fmt.Errorf("wb logic error: %s", resp.ErrorText)
// 	}

// 	return resp.Data, nil
// }

// // GetAllSubjects выкачивает ВСЕ предметы, автоматически листая страницы. deprecated?
// func (c *Client) GetAllSubjects(ctx context.Context, parentID int) ([]Subject, error) {
//     var allSubjects []Subject
//     limit := 1000
//     offset := 0

//     for {
//         params := url.Values{}
//         params.Set("limit", strconv.Itoa(limit))
//         params.Set("offset", strconv.Itoa(offset))
//         if parentID > 0 {
//             params.Set("parentID", strconv.Itoa(parentID))
//         }

//         var resp APIResponse[[]Subject]
//         // Наш умный .get() сам подождет лимиты
//         err := c.get(ctx, "/content/v2/object/all", params, &resp)
//         if err != nil {
//             return nil, err
//         }
//         if resp.Error {
//             return nil, fmt.Errorf("wb error: %s", resp.ErrorText)
//         }

//         // Добавляем полученное
//         allSubjects = append(allSubjects, resp.Data...)

//         // Если вернулось меньше лимита, значит это последняя страница
//         if len(resp.Data) < limit {
//             break
//         }

//         // Готовимся к следующей странице
//         offset += limit
//     }

//     return allSubjects, nil
// }

// FetchSubjectsPage - низкоуровневый запрос одной страницы предметов.
// Параметры baseURL, rateLimit, burst передаются из tool config.
func (c *Client) FetchSubjectsPage(ctx context.Context, baseURL string, rateLimit int, burst int, parentID, limit, offset int) ([]Subject, error) {
	params := url.Values{}
	params.Set("limit", strconv.Itoa(limit))
	params.Set("offset", strconv.Itoa(offset))
	if parentID > 0 {
		params.Set("parentID", strconv.Itoa(parentID))
	}

	var resp APIResponse[[]Subject]
	err := c.Get(ctx, "get_wb_subjects", baseURL, rateLimit, burst, "/content/v2/object/all", params, &resp)
	if err != nil {
		return nil, err
	}
	if resp.Error {
		return nil, fmt.Errorf("wb logic error: %s", resp.ErrorText)
	}
	return resp.Data, nil
}

// GetAllSubjectsLazy - "ленивый" метод, который выкачивает всё используя FetchSubjectsPage.
// Параметры baseURL, rateLimit, burst передаются из tool config.
func (c *Client) GetAllSubjectsLazy(ctx context.Context, baseURL string, rateLimit int, burst int, parentID int) ([]Subject, error) {
	var all []Subject
	limit := 1000
	offset := 0

	for {
		batch, err := c.FetchSubjectsPage(ctx, baseURL, rateLimit, burst, parentID, limit, offset)
		if err != nil {
			return nil, err
		}

		all = append(all, batch...)

		if len(batch) < limit {
			break
		}
		offset += limit
	}
	return all, nil
}

// GetCharacteristics получает характеристики для предмета.
// Параметры baseURL, rateLimit, burst передаются из tool config.
func (c *Client) GetCharacteristics(ctx context.Context, baseURL string, rateLimit int, burst int, subjectID int) ([]Characteristic, error) {
	path := fmt.Sprintf("/content/v2/object/charcs/%d", subjectID)

	var resp APIResponse[[]Characteristic]

	err := c.Get(ctx, "get_wb_characteristics", baseURL, rateLimit, burst, path, nil, &resp)
	if err != nil {
		return nil, err
	}

	if resp.Error {
		return nil, fmt.Errorf("wb logic error: %s", resp.ErrorText)
	}

	return resp.Data, nil
}

/* добавляем цвет wb. URL: /content/v2/directory/colors. Это справочник ("directory"), а не объект.
Внимание! 
Использование в AI-агенте (Tool для LLM)
Это классический кейс для RAG (Retrieval Augmented Generation).
Список цветов может быть на 5000+ строк. Мы не можем запихнуть его весь в контекст LLM.

Стратегия:

При старте приложения (или раз в сутки) скачиваем GetColors() и кэшируем в памяти (в GlobalState).

Когда нужно определить цвет товара, мы используем Fuzzy Search (нечеткий поиск) внутри Go, а не спрашиваем LLM "выбери из 5000 вариантов".

Пример сценария:

LLM проанализировала эскиз: "Цвет платья: светло-персиковый".

Мы (Go-код) ищем в справочнике colors что-то похожее на "светло-персиковый".

Находим: "персиковый", "персиковый мелок", "светло-персиковый".

Отдаем LLM эти 3 варианта: "Выбери точный цвет WB из: [...]".

LLM выбирает "персиковый мелок".

Иметь в виду, что использовать его надо с кэшированием, а не дергать каждый раз.
*/

// GetColors возвращает справочник всех допустимых цветов WB.
// Параметры baseURL, rateLimit, burst передаются из tool config.
func (c *Client) GetColors(ctx context.Context, baseURL string, rateLimit int, burst int) ([]Color, error) {
	var resp APIResponse[[]Color]
	err := c.Get(ctx, "get_wb_colors", baseURL, rateLimit, burst, "/content/v2/directory/colors", nil, &resp)
	if err != nil {
		return nil, err
	}
	if resp.Error {
		return nil, fmt.Errorf("wb logic error: %s", resp.ErrorText)
	}
	return resp.Data, nil
}

// GetGenders возвращает справочник полов/видов.
// В API называется "Kinds". Пример: "Мужской", "Женский", "Детский".
// Параметры baseURL, rateLimit, burst передаются из tool config.
func (c *Client) GetGenders(ctx context.Context, baseURL string, rateLimit int, burst int) ([]string, error) {
	var resp APIResponse[[]string]

	err := c.Get(ctx, "get_wb_genders", baseURL, rateLimit, burst, "/content/v2/directory/kinds", nil, &resp)
	if err != nil {
		return nil, err
	}

	if resp.Error {
		return nil, fmt.Errorf("wb logic error: %s", resp.ErrorText)
	}

	return resp.Data, nil
}

// GetSeasons возвращает справочник сезонов.
// Параметры baseURL, rateLimit, burst передаются из tool config.
func (c *Client) GetSeasons(ctx context.Context, baseURL string, rateLimit int, burst int) ([]string, error) {
	var resp APIResponse[[]string]

	err := c.Get(ctx, "get_wb_seasons", baseURL, rateLimit, burst, "/content/v2/directory/seasons", nil, &resp)
	if err != nil {
		return nil, err
	}

	if resp.Error {
		return nil, fmt.Errorf("wb logic error: %s", resp.ErrorText)
	}

	return resp.Data, nil
}

// pkg/wb/types.go
type Tnved struct {
    Tnved string `json:"tnved"` // Код (строка, т.к. может начинаться с 0)
    IsKiz bool   `json:"isKiz"` // Требует ли маркировки КИЗ
}

// GetTnved возвращает список кодов ТНВЭД для конкретного предмета.
// Параметры baseURL, rateLimit, burst передаются из tool config.
func (c *Client) GetTnved(ctx context.Context, baseURL string, rateLimit int, burst int, subjectID int, search string) ([]Tnved, error) {
	params := url.Values{}
	params.Set("subjectID", fmt.Sprintf("%d", subjectID))
	if search != "" {
		params.Set("search", search)
	}

	var resp APIResponse[[]Tnved]

	err := c.Get(ctx, "get_wb_tnved", baseURL, rateLimit, burst, "/content/v2/directory/tnved", params, &resp)
	if err != nil {
		return nil, err
	}

	if resp.Error {
		return nil, fmt.Errorf("wb logic error: %s", resp.ErrorText)
	}

	return resp.Data, nil
}

/* 
Сценарий использования GetTnved (Flow)
Вот как это будет выглядеть в диалоге с агентом:

Пользователь: "Заведи карточку на шелковую блузку".
LLM: (Анализ...) "Блузка" -> это SubjectID 123 (нашла через поиск предметов).
LLM: "Мне нужно выбрать код ТНВЭД для блузки. Вызываю get_tnved(subjectID=123)".
Tool: Возвращает список:
6206100000 (из шелка)
6206200000 (из шерсти)
...
LLM: "Ага, раз блузка шелковая, беру код 6206100000".
Это подтверждает, что ТНВЭД должен быть инструментом (Tool), а не частью предзагруженного словаря.
=============================
*/

// GetVats возвращает список ставок НДС. Пример: ["22%", "Без НДС", "10%"].
// Параметры baseURL, rateLimit, burst передаются из tool config.
func (c *Client) GetVats(ctx context.Context, baseURL string, rateLimit int, burst int) ([]string, error) {
	var resp APIResponse[[]string]

	params := url.Values{}
	params.Set("locale", "ru")

	err := c.Get(ctx, "get_wb_vats", baseURL, rateLimit, burst, "/content/v2/directory/vat", params, &resp)
	if err != nil {
		return nil, err
	}

	if resp.Error {
		return nil, fmt.Errorf("wb logic error: %s", resp.ErrorText)
	}

	return resp.Data, nil
}

// GetCountries возвращает список стран производства.
// Параметры baseURL, rateLimit, burst передаются из tool config.
func (c *Client) GetCountries(ctx context.Context, baseURL string, rateLimit int, burst int) ([]Country, error) {
	var resp APIResponse[[]Country]

	params := url.Values{}
	params.Set("locale", "ru")

	err := c.Get(ctx, "get_wb_countries", baseURL, rateLimit, burst, "/content/v2/directory/countries", params, &resp)
	if err != nil {
		return nil, err
	}

	if resp.Error {
		return nil, fmt.Errorf("wb logic error: %s", resp.ErrorText)
	}

	return resp.Data, nil
}

/*
Резюме по справочникам
Мы собрали фулл-хаус статических справочников:
Цвета (Colors) -> Номенклатура (nmID)
Пол (Genders) -> Обязательное поле карточки
Страна (Countries) -> Обязательное поле
Сезон (Seasons) -> Обязательное поле (часто)
НДС (Vats) -> Финансы
Динамический: ТНВЭД (по запросу).

Теперь у нас есть всё, чтобы AI-агент мог "собрать" JSON карточки товара, опираясь на реальные, валидные значения WB, а не галлюцинируя "Страна: Поднебесная" или "Сезон: Дождливый".
*/

// BrandsResponse представляет ответ от API брендов
// Структура отличается от стандартной APIResponse
type BrandsResponse struct {
    Brands []Brand `json:"brands"`
    Next   int     `json:"next"`   // 0 если это последняя страница
    Total  int     `json:"total"`  // Общее количество брендов
}

// GetBrands возвращает список брендов для указанного предмета с авто-пагинацией.
//
// Параметры:
//   - baseURL: базовый URL API (из tool config)
//   - rateLimit: лимит запросов в минуту (из tool config)
//   - burst: burst для rate limiter (из tool config)
//   - subjectID: ID предмета для фильтрации брендов
//   - limit: максимальное количество брендов для возврата (0 = все доступные)
//
// Возвращает список брендов отсортированных по популярности.
func (c *Client) GetBrands(ctx context.Context, baseURL string, rateLimit int, burst int, subjectID int, limit int) ([]Brand, error) {
	var allBrands []Brand
	next := 0

	for {
		params := url.Values{}
		params.Set("subjectId", fmt.Sprintf("%d", subjectID))
		if next > 0 {
			params.Set("next", fmt.Sprintf("%d", next))
		}

		var brandsResp BrandsResponse

		err := c.Get(ctx, "get_wb_brands", baseURL, rateLimit, burst,
			"/api/content/v1/brands", params, &brandsResp)
		if err != nil {
			return nil, err
		}

		allBrands = append(allBrands, brandsResp.Brands...)

		if brandsResp.Next == 0 {
			break
		}
		if limit > 0 && len(allBrands) >= limit {
			allBrands = allBrands[:limit]
			break
		}

		next = brandsResp.Next
	}

	return allBrands, nil
}

// ============================================================================
// Product Search Methods (supplierArticle -> nmID mapping)
// ============================================================================

// GetProductsByArticles ищет товары по артикулам поставщика (supplierArticle).
//
// Этот метод используется для конвертации артикулов поставщика в nmID Wildberries.
// Пользователи обычно знают свой артикул (vendor code/supplier article), а не nmID.
//
// Использует Content API: POST /content/v2/get/cards/list с textSearch.
// Требует токен с категорией Promotion (бит 6).
//
// Параметры:
//   - ctx: контекст для отмены
//   - toolID: идентификатор tool для rate limiting
//   - baseURL: базовый URL API (content-api.wildberries.ru)
//   - rateLimit: лимит запросов в минуту
//   - burst: burst для rate limiter
//   - articles: список артикулов поставщика (поиск по одному за раз)
//
// Возвращает список найденных товаров с их nmID и другой информацией.
//
// Правило 8: добавляем функциональность через новые методы, не меняя существующие.
func (c *Client) GetProductsByArticles(ctx context.Context, toolID string, baseURL string, rateLimit int, burst int, articles []string) ([]ProductInfo, error) {
	if len(articles) == 0 {
		return []ProductInfo{}, nil
	}

	var results []ProductInfo

	// Content API не поддерживает поиск по нескольким артикулам одновременно
	// API с токеном Promotion видит только карточки продавца
	// Получаем все карточки и фильтруем на стороне клиента

	// Сначала получаем все карточки (без фильтра)
	reqBody := CardsListRequest{
		Settings: CardsSettings{
			Cursor: CardsCursor{
				Limit: 100, // Максимум
			},
		},
	}

	var resp CardsListResponse
	err := c.Post(ctx, toolID, baseURL, rateLimit, burst, "/content/v2/get/cards/list", reqBody, &resp)
	if err != nil {
		return nil, err
	}

	// Создаём map для быстрого поиска
	articleMap := make(map[string]bool)
	for _, a := range articles {
		articleMap[a] = true
	}

	// Фильтруем по vendorCode
	for _, card := range resp.Cards {
		if articleMap[card.VendorCode] {
			results = append(results, ProductInfo{
				NmID:    card.NmID,
				Article: card.VendorCode,
				Name:    card.Title,
				Price:   0,
			})
			delete(articleMap, card.VendorCode) // Удаляем из map чтобы не дублировать
		}
	}

	return results, nil
}

```

=================

# wb/dictionaries.go

```go
package wb

import (
	"context"
	_ "fmt"
)

// Dictionaries - контейнер для всех справочников
type Dictionaries struct {
    Colors  []Color
    Genders []string
	Countries []Country
    Seasons []string
	Vats    []string // <--- Добавили НДС
}

// LoadDictionaries загружает все необходимые справочники.
// Параметры baseURL, rateLimit, burst передаются из конфигурации.
func (c *Client) LoadDictionaries(ctx context.Context, baseURL string, rateLimit int, burst int) (*Dictionaries, error) {
	colors, err := c.GetColors(ctx, baseURL, rateLimit, burst)
	if err != nil {
		return nil, err
	}

	genders, err := c.GetGenders(ctx, baseURL, rateLimit, burst)
	if err != nil {
		return nil, err
	}

	seasons, err := c.GetSeasons(ctx, baseURL, rateLimit, burst)
	if err != nil {
		return nil, err
	}

	vats, err := c.GetVats(ctx, baseURL, rateLimit, burst)
	if err != nil {
		return nil, err
	}

	countries, err := c.GetCountries(ctx, baseURL, rateLimit, burst)
	if err != nil {
		return nil, err
	}

	return &Dictionaries{
		Colors:    colors,
		Genders:   genders,
		Seasons:   seasons,
		Vats:      vats,
		Countries: countries,
	}, nil
}

/* 
===
Использование в main.go
// ... внутри main
fmt.Print("📚 Loading WB dictionaries... ")
dicts, err := wbClient.LoadDictionaries(context.Background())
if err != nil {
    log.Fatal(err)
}
// Сохраняем в State
state.Dictionaries = dicts 
fmt.Printf("OK (%d colors, %d genders)\n", len(dicts.Colors), len(dicts.Genders))
===
Это решит проблему "разрозненных сущностей". Все справочные данные будут лежать в одном месте state.Dictionaries и будут доступны для Tools и LLM.

Пример Tool для пола:
LLM: "Пол: для мальчика"
Tool match_gender: Ищет "для мальчика" в state.Dictionaries.Genders. Находит "Детский" (если он там есть) или возвращает список доступных: ["Мужской", "Женский", "Детский", "Унисекс"].
*/

// ================

```

=================

# wb/types.go

```go
// Модели данных

package wb

// Common Response Wrapper
type APIResponse[T any] struct {
	Data      T           `json:"data"`
	Error     bool        `json:"error"`
	ErrorText string      `json:"errorText"`
	// AdditionalErrors игнорируем, так как тип плавает (string/null)
}

// 1. Parent Category
type ParentCategory struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	IsVisible bool   `json:"isVisible"`
}

// 2. Subject (Предмет)
type Subject struct {
	SubjectID   int    `json:"subjectID"`
	ParentID    int    `json:"parentID"`
	SubjectName string `json:"subjectName"`
	ParentName  string `json:"parentName"`
}

// 3. Characteristic (Характеристика)
type Characteristic struct {
	CharcID     int    `json:"charcID"`
	SubjectName string `json:"subjectName"`
	SubjectID   int    `json:"subjectID"`
	Name        string `json:"name"`
	Required    bool   `json:"required"`
	UnitName    string `json:"unitName"`
	MaxCount    int    `json:"maxCount"`
	Popular     bool   `json:"popular"`
	CharcType   int    `json:"charcType"` // 1: string, 4: number? Нужно уточнять в доке, но int безопасен
}

type Color struct {
    Name       string `json:"name"`       // "персиковый мелок"
    ParentName string `json:"parentName"` // "оранжевый"
}

type Country struct {
    Name     string `json:"name"`     // "Китай"
    FullName string `json:"fullName"` // "Китайская Народная Республика"
}

// Brand представляет бренд в справочнике WB
type Brand struct {
    ID      int    `json:"id"`      // Уникальный ID бренда
    LogoURL string `json:"logoUrl"` // URL логотипа бренда
    Name    string `json:"name"`    // Название бренда
}

// ============================================================================
// Feedbacks API Types
// ============================================================================

// FeedbacksResponse представляет ответ от API отзывов.
type FeedbacksResponse struct {
	Data struct {
		Feedbacks       []Feedback `json:"feedbacks"`
		CountUnanswered int        `json:"countUnanswered"`
		CountArchive    int        `json:"countArchive"`
	} `json:"data"`
	Error     bool   `json:"error"`
	ErrorText string `json:"errorText"`
}

// Feedback представляет отзыв на товар.
type Feedback struct {
	ID              string           `json:"id"`
	Text            string           `json:"text"`
	ProductValuation int              `json:"productValuation"`
	CreatedDate     string           `json:"createdDate"`
	Answer          *FeedbackAnswer  `json:"answer,omitempty"`
	ProductDetails  FeedbackProduct  `json:"productDetails"`
	UserName        string           `json:"userName"`
	PhotoLinks      []FeedbackPhoto  `json:"photoLinks,omitempty"`
}

// FeedbackAnswer представляет ответ продавца на отзыв.
type FeedbackAnswer struct {
	Text     string `json:"text"`
	State    string `json:"state"`
	Editable bool   `json:"editable"`
}

// FeedbackProduct представляет информацию о товаре в отзыве.
type FeedbackProduct struct {
	ImtID           int    `json:"imtId"`
	NmId            int    `json:"nmId"`
	ProductName     string `json:"productName"`
	SupplierArticle string `json:"supplierArticle"`
	BrandName       string `json:"brandName"`
}

// FeedbackPhoto представляет фото в отзыве.
type FeedbackPhoto struct {
	FullSize string `json:"fullSize"`
	MiniSize string `json:"miniSize"`
}

// QuestionsResponse представляет ответ от API вопросов.
type QuestionsResponse struct {
	Data struct {
		Questions        []Question `json:"questions"`
		CountUnanswered  int        `json:"countUnanswered"`
		CountArchive     int        `json:"countArchive"`
	} `json:"data"`
	Error     bool   `json:"error"`
	ErrorText string `json:"errorText"`
}

// Question представляет вопрос о товаре.
type Question struct {
	ID            string           `json:"id"`
	Text          string           `json:"text"`
	CreatedDate   string           `json:"createdDate"`
	State         string           `json:"state"`
	Answer        *QuestionAnswer  `json:"answer,omitempty"`
	ProductDetails QuestionProduct  `json:"productDetails"`
	WasViewed     bool             `json:"wasViewed"`
}

// QuestionAnswer представляет ответ на вопрос.
type QuestionAnswer struct {
	Text string `json:"text"`
}

// QuestionProduct представляет информацию о товаре в вопросе.
type QuestionProduct struct {
	ImtID           int    `json:"imtId"`
	NmId            int    `json:"nmId"`
	ProductName     string `json:"productName"`
	SupplierArticle string `json:"supplierArticle"`
	SupplierName    string `json:"supplierName"`
	BrandName       string `json:"brandName"`
}

// UnansweredFeedbacksCountsResponse представляет ответ с количеством неотвеченных отзывов.
type UnansweredFeedbacksCountsResponse struct {
	Data struct {
		CountUnanswered      int    `json:"countUnanswered"`
		CountUnansweredToday int    `json:"countUnansweredToday"`
		Valuation            string `json:"valuation"` // Средняя оценка (будет удалена WB после 11 декабря)
	} `json:"data"`
	Error     bool   `json:"error"`
	ErrorText string `json:"errorText"`
}

// UnansweredQuestionsCountsResponse представляет ответ с количеством неотвеченных вопросов.
type UnansweredQuestionsCountsResponse struct {
	Data struct {
		CountUnanswered      int `json:"countUnanswered"`
		CountUnansweredToday int `json:"countUnansweredToday"`
	} `json:"data"`
	Error     bool   `json:"error"`
	ErrorText string `json:"errorText"`
}

// NewFeedbacksQuestionsResponse представляет ответ о наличии новых отзывов/вопросов.
type NewFeedbacksQuestionsResponse struct {
	Data struct {
		HasNewQuestions bool `json:"hasNewQuestions"`
		HasNewFeedbacks  bool `json:"hasNewFeedbacks"`
	} `json:"data"`
	Error     bool   `json:"error"`
	ErrorText string `json:"errorText"`
}

// ============================================================================
// Product Search API Types (for supplierArticle -> nmID mapping)
// ============================================================================

// ProductsSearchRequest представляет запрос к API поиска товаров.
type ProductsSearchRequest struct {
	Filter struct {
		ArticleInList []string `json:"article_in_list"` // Артикулы поставщика (max 1000)
	} `json:"filter"`
}

// ProductsSearchResponse представляет ответ от API поиска товаров.
type ProductsSearchResponse struct {
	Data struct {
		Products []ProductInfo `json:"products"`
	} `json:"data"`
	Status struct {
		ErrorCode    *int    `json:"error_code,omitempty"`
		ErrorMessage *string `json:"error_message,omitempty"`
		Message      *string `json:"message,omitempty"`
	} `json:"status"`
}

// ProductInfo представляет информацию о товаре.
type ProductInfo struct {
	NmID          int    `json:"nmID"`
	Article       string `json:"article"`        // Артикул поставщика (vendor code)
	Name          string `json:"name"`
	Price         int    `json:"price"`
	SalePriceUfact int   `json:"salePriceUfact"`
}

// ProductSearchResult представляет результат поиска товара для LLM.
type ProductSearchResult struct {
	NmID            int    `json:"nmId"`            // WB ID товара
	SupplierArticle string `json:"supplierArticle"` // Артикул поставщика
	Name            string `json:"name"`            // Название товара
	Price           int    `json:"price"`           // Цена
	Found           bool   `json:"found"`           // Найден ли товар
}

// ============================================================================
// Content API Cards List Types (for Promotion category tokens)
// ============================================================================

// CardsListRequest представляет запрос для получения списка карточек товаров.
type CardsListRequest struct {
	Settings CardsSettings `json:"settings"`
}

// CardsSettings содержит настройки запроса карточек.
type CardsSettings struct {
	Cursor CardsCursor `json:"cursor"`
	Filter *CardsFilter `json:"filter,omitempty"`
	Sort   *CardsSort   `json:"sort,omitempty"`
}

// CardsCursor содержит параметры пагинации.
type CardsCursor struct {
	Limit    int    `json:"limit"`              // Максимум 100
	UpdatedAt string `json:"updatedAt,omitempty"` // Для пагинации
	NmID      int    `json:"nmID,omitempty"`      // Для пагинации
}

// CardsFilter содержит параметры фильтрации карточек.
type CardsFilter struct {
	TextSearch string `json:"textSearch,omitempty"` // Поиск по артикулу/названию
	// Другие поля фильтра можно добавить по необходимости
}

// CardsSort содержит параметры сортировки.
type CardsSort struct {
	Ascending bool `json:"ascending,omitempty"`
}

// CardsListResponse представляет ответ от Content API с карточками товаров.
type CardsListResponse struct {
	Cards []ProductCard `json:"cards"`
	Cursor *CardsCursorResponse `json:"cursor,omitempty"`
	Error  bool          `json:"error"`
	ErrorText string      `json:"errorText,omitempty"`
}

// CardsCursorResponse содержит информацию о пагинации в ответе.
type CardsCursorResponse struct {
	UpdatedAt string `json:"updatedAt"`
	NmID      int    `json:"nmID"`
	Total     int    `json:"total"`
}

// ProductCard представляет карточку товара от Content API.
type ProductCard struct {
	NmID       int    `json:"nmID"`
	ImtID      int    `json:"imtID"`
	NmUUID     string `json:"nmUUID"`
	SubjectID  int    `json:"subjectID"`
	SubjectName string `json:"subjectName"`
	VendorCode string `json:"vendorCode"` // Артикул поставщика!
	Brand      string `json:"brand"`
	Title      string `json:"title"`
	Description string `json:"description"`
	Photos     []ProductPhoto `json:"photos"`
	CreatedAt  string `json:"createdAt"`
	UpdatedAt  string `json:"updatedAt"`
}

// ProductPhoto представляет фото товара.
type ProductPhoto struct {
	Big      string `json:"big"`
	C246x328 string `json:"c246x328"`
	Square   string `json:"square"`
}


```

=================

