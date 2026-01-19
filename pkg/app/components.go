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

	// 2. Инициализируем WB клиент ТОЛЬКО если включены WB tools
	// Rule 13: Автономные утилиты могут работать без WB (только S3, Ozon и т.д.)
	var wbClient *wb.Client
	var dicts *wb.Dictionaries

	if hasWBTools(cfg) {
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
			log.Println("Warning: WB tools enabled but API key not set")
			utils.Error("WB tools enabled but API key not set - WB tools will fail")
		}

		utils.Info("WB client initialized",
			"api_key_set", cfg.WB.APIKey != "",
			"rate_limit", cfg.WB.RateLimit,
			"burst_limit", cfg.WB.BurstLimit)

		// 3. Загружаем справочники WB (кэшируем при старте)
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
	} else {
		utils.Info("WB tools not enabled - skipping WB initialization")
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
	if err := SetupTools(state, wbClient, visionLLM, cfg, modelRegistry); err != nil {
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

	// Инициализируем BundleResolver для токен-оптимизации (Phase 3)
	bundleResolver := chain.NewBundleResolver(
		cfg,
		state.GetToolsRegistry(),
		chain.ResolutionMode(cfg.ToolResolutionMode),
	)
	reactCycle.SetBundleResolver(bundleResolver)

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
		// utils.Error("Failed to create debug recorder", "error", err)
	} else if debugRecorder.Enabled() {
		// utils.Info("Debug recorder attached", "logs_dir", cfg.App.DebugLogs.LogsDir)
		reactCycle.AttachDebug(debugRecorder)
	} else {
		// utils.Info("Debug recorder NOT enabled", "enabled", cfg.App.DebugLogs.Enabled, "save_logs", cfg.App.DebugLogs.SaveLogs)
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

// hasWBTools проверяет, включён ли хотя бы один WB tool в конфиге.
//
// Rule 13: Автономные утилиты могут работать только с S3 или другими сервисами.
// В таких случаях WB инициализация не нужна.
func hasWBTools(cfg *config.AppConfig) bool {
	enabledTools := getEnabledTools(cfg)
	wbTools := []string{
		"search_wb_products",
		"get_wb_parent_categories",
		"get_wb_subjects",
		"ping_wb_api",
		"get_wb_feedbacks",
		"get_wb_product_feedbacks",
		"get_wb_product_detail",
	}

	for _, toolName := range wbTools {
		if enabledTools[toolName] {
			return true
		}
	}
	return false
}

// getEnabledTools возвращает карту включенных инструментов с учетом bundles.
//
// Гибридный режим:
// 1. Если enable_bundles не пустой: сначала включаются инструменты из bundles,
//    затем применяются individual overrides из tools секции.
// 2. Если enable_bundles пустой: используются individual enabled флаги (backward compatible).
//
// Возвращает map[toolName]enabled для быстрой проверки.
func getEnabledTools(cfg *config.AppConfig) map[string]bool {
	enabled := make(map[string]bool)

	// Если bundles не включены, используем backward compatible режим
	if len(cfg.EnableBundles) == 0 {
		for toolName, toolCfg := range cfg.Tools {
			enabled[toolName] = toolCfg.Enabled
		}
		return enabled
	}

	// === Bundle mode: bundles + individual overrides ===

	// 1. Сначала включаем инструменты из bundles
	for _, bundleName := range cfg.EnableBundles {
		bundle, exists := cfg.ToolBundles[bundleName]
		if !exists {
			continue // Логируем предупреждение? Пока пропускаем
		}
		for _, toolName := range bundle.Tools {
			enabled[toolName] = true
		}
	}

	// 2. Затем применяем individual overrides из tools секции
	// Это позволяет точно настроить: включить или выключить конкретный tool
	for toolName, toolCfg := range cfg.Tools {
		if toolCfg.Enabled {
			enabled[toolName] = true // Явно включаем
		} else {
			enabled[toolName] = false // Явно выключаем (override bundle)
		}
	}

	return enabled
}

// setupWBTools регистрирует все Wildberries API инструменты.
// Зависимости: wb.Client, cfg.WB
func setupWBTools(registry *tools.Registry, cfg *config.AppConfig, wbClient *wb.Client) error {
	// Helper для регистрации
	register := func(name string, tool tools.Tool) error {
		if err := registry.Register(tool); err != nil {
			return fmt.Errorf("failed to register tool '%s': %w", name, err)
		}
		return nil
	}

	// Helper для получения конфигурации tool
	getToolCfg := func(name string) (config.ToolConfig, bool) {
		tc, exists := cfg.Tools[name]
		if !exists {
			return config.ToolConfig{Enabled: false}, false
		}
		return tc, true
	}

	// Получаем карту включенных инструментов (с учетом bundles)
	enabledTools := getEnabledTools(cfg)

	// Helper для проверки включен ли tool
	isEnabled := func(name string) bool {
		return enabledTools[name]
	}

	// === WB Content API Tools ===
	if toolCfg, exists := getToolCfg("search_wb_products"); exists && isEnabled("search_wb_products") {
		if err := register("search_wb_products", std.NewWbProductSearchTool(wbClient, toolCfg, cfg.WB)); err != nil {
			return err
		}
	}

	if toolCfg, exists := getToolCfg("get_wb_parent_categories"); exists && isEnabled("get_wb_parent_categories") {
		if err := register("get_wb_parent_categories", std.NewWbParentCategoriesTool(wbClient, toolCfg, cfg.WB)); err != nil {
			return err
		}
	}

	if toolCfg, exists := getToolCfg("get_wb_subjects"); exists && isEnabled("get_wb_subjects") {
		if err := register("get_wb_subjects", std.NewWbSubjectsTool(wbClient, toolCfg, cfg.WB)); err != nil {
			return err
		}
	}

	if toolCfg, exists := getToolCfg("ping_wb_api"); exists && isEnabled("ping_wb_api") {
		if err := register("ping_wb_api", std.NewWbPingTool(wbClient, toolCfg, cfg.WB)); err != nil {
			return err
		}
	}

	// === WB Feedbacks API Tools ===
	if toolCfg, exists := getToolCfg("get_wb_feedbacks"); exists && isEnabled("get_wb_feedbacks") {
		if err := register("get_wb_feedbacks", std.NewWbFeedbacksTool(wbClient, toolCfg, cfg.WB)); err != nil {
			return err
		}
	}

	if toolCfg, exists := getToolCfg("get_wb_questions"); exists && isEnabled("get_wb_questions") {
		if err := register("get_wb_questions", std.NewWbQuestionsTool(wbClient, toolCfg, cfg.WB)); err != nil {
			return err
		}
	}

	if toolCfg, exists := getToolCfg("get_wb_new_feedbacks_questions"); exists && isEnabled("get_wb_new_feedbacks_questions") {
		if err := register("get_wb_new_feedbacks_questions", std.NewWbNewFeedbacksQuestionsTool(wbClient, toolCfg, cfg.WB)); err != nil {
			return err
		}
	}

	if toolCfg, exists := getToolCfg("get_wb_unanswered_feedbacks_counts"); exists && isEnabled("get_wb_unanswered_feedbacks_counts") {
		if err := register("get_wb_unanswered_feedbacks_counts", std.NewWbUnansweredFeedbacksCountsTool(wbClient, toolCfg, cfg.WB)); err != nil {
			return err
		}
	}

	if toolCfg, exists := getToolCfg("get_wb_unanswered_questions_counts"); exists && isEnabled("get_wb_unanswered_questions_counts") {
		if err := register("get_wb_unanswered_questions_counts", std.NewWbUnansweredQuestionsCountsTool(wbClient, toolCfg, cfg.WB)); err != nil {
			return err
		}
	}

	// === WB Characteristics Tools ===
	if toolCfg, exists := getToolCfg("get_wb_subjects_by_name"); exists && isEnabled("get_wb_subjects_by_name") {
		if err := register("get_wb_subjects_by_name", std.NewWbSubjectsByNameTool(wbClient, toolCfg, cfg.WB)); err != nil {
			return err
		}
	}

	if toolCfg, exists := getToolCfg("get_wb_characteristics"); exists && isEnabled("get_wb_characteristics") {
		if err := register("get_wb_characteristics", std.NewWbCharacteristicsTool(wbClient, toolCfg, cfg.WB)); err != nil {
			return err
		}
	}

	if toolCfg, exists := getToolCfg("get_wb_tnved"); exists && isEnabled("get_wb_tnved") {
		if err := register("get_wb_tnved", std.NewWbTnvedTool(wbClient, toolCfg, cfg.WB)); err != nil {
			return err
		}
	}

	if toolCfg, exists := getToolCfg("get_wb_brands"); exists && isEnabled("get_wb_brands") {
		if err := register("get_wb_brands", std.NewWbBrandsTool(wbClient, toolCfg, cfg.WB)); err != nil {
			return err
		}
	}

	// === WB Service Tools ===
	if toolCfg, exists := getToolCfg("reload_wb_dictionaries"); exists && isEnabled("reload_wb_dictionaries") {
		if err := register("reload_wb_dictionaries", std.NewReloadWbDictionariesTool(wbClient, toolCfg)); err != nil {
			return err
		}
	}

	// === WB Analytics Tools ===
	if toolCfg, exists := getToolCfg("get_wb_product_funnel"); exists && isEnabled("get_wb_product_funnel") {
		if err := register("get_wb_product_funnel", std.NewWbProductFunnelTool(wbClient, toolCfg, cfg.WB)); err != nil {
			return err
		}
	}

	if toolCfg, exists := getToolCfg("get_wb_product_funnel_history"); exists && isEnabled("get_wb_product_funnel_history") {
		if err := register("get_wb_product_funnel_history", std.NewWbProductFunnelHistoryTool(wbClient, toolCfg, cfg.WB)); err != nil {
			return err
		}
	}

	if toolCfg, exists := getToolCfg("get_wb_search_positions"); exists && isEnabled("get_wb_search_positions") {
		if err := register("get_wb_search_positions", std.NewWbSearchPositionsTool(wbClient, toolCfg, cfg.WB)); err != nil {
			return err
		}
	}

	if toolCfg, exists := getToolCfg("get_wb_top_search_queries"); exists && isEnabled("get_wb_top_search_queries") {
		if err := register("get_wb_top_search_queries", std.NewWbTopSearchQueriesTool(wbClient, toolCfg, cfg.WB)); err != nil {
			return err
		}
	}

	if toolCfg, exists := getToolCfg("get_wb_top_organic_positions"); exists && isEnabled("get_wb_top_organic_positions") {
		if err := register("get_wb_top_organic_positions", std.NewWbTopOrganicPositionsTool(wbClient, toolCfg, cfg.WB)); err != nil {
			return err
		}
	}

	if toolCfg, exists := getToolCfg("get_wb_campaign_stats"); exists && isEnabled("get_wb_campaign_stats") {
		if err := register("get_wb_campaign_stats", std.NewWbCampaignStatsTool(wbClient, toolCfg, cfg.WB)); err != nil {
			return err
		}
	}

	if toolCfg, exists := getToolCfg("get_wb_keyword_stats"); exists && isEnabled("get_wb_keyword_stats") {
		if err := register("get_wb_keyword_stats", std.NewWbKeywordStatsTool(wbClient, toolCfg, cfg.WB)); err != nil {
			return err
		}
	}

	if toolCfg, exists := getToolCfg("get_wb_attribution_summary"); exists && isEnabled("get_wb_attribution_summary") {
		if err := register("get_wb_attribution_summary", std.NewWbAttributionSummaryTool(wbClient, toolCfg, cfg.WB)); err != nil {
			return err
		}
	}

	return nil
}

// setupDictionaryTools регистрирует инструменты WB словарей.
// Зависимости: state.CoreState (для GetDictionaries)
func setupDictionaryTools(registry *tools.Registry, cfg *config.AppConfig, state *state.CoreState) error {
	dicts := state.GetDictionaries()
	if dicts == nil {
		return nil // Нет словарей — нечего регистрировать
	}

	register := func(name string, tool tools.Tool) error {
		if err := registry.Register(tool); err != nil {
			return fmt.Errorf("failed to register tool '%s': %w", name, err)
		}
		return nil
	}

	getToolCfg := func(name string) (config.ToolConfig, bool) {
		tc, exists := cfg.Tools[name]
		if !exists {
			return config.ToolConfig{Enabled: false}, false
		}
		return tc, true
	}

	enabledTools := getEnabledTools(cfg)
	isEnabled := func(name string) bool {
		return enabledTools[name]
	}

	// === WB Dictionary Tools ===
	if toolCfg, exists := getToolCfg("wb_colors"); exists && isEnabled("wb_colors") {
		if err := register("wb_colors", std.NewWbColorsTool(dicts, toolCfg)); err != nil {
			return err
		}
	}

	if toolCfg, exists := getToolCfg("wb_countries"); exists && isEnabled("wb_countries") {
		if err := register("wb_countries", std.NewWbCountriesTool(dicts, toolCfg)); err != nil {
			return err
		}
	}

	if toolCfg, exists := getToolCfg("wb_genders"); exists && isEnabled("wb_genders") {
		if err := register("wb_genders", std.NewWbGendersTool(dicts, toolCfg)); err != nil {
			return err
		}
	}

	if toolCfg, exists := getToolCfg("wb_seasons"); exists && isEnabled("wb_seasons") {
		if err := register("wb_seasons", std.NewWbSeasonsTool(dicts, toolCfg)); err != nil {
			return err
		}
	}

	if toolCfg, exists := getToolCfg("wb_vat_rates"); exists && isEnabled("wb_vat_rates") {
		if err := register("wb_vat_rates", std.NewWbVatRatesTool(dicts, toolCfg)); err != nil {
			return err
		}
	}

	return nil
}

// setupLLMTools регистрирует инструменты LLM провайдеров.
// Зависимости: models.Registry, cfg
func setupLLMTools(registry *tools.Registry, cfg *config.AppConfig, modelRegistry *models.Registry) error {
	register := func(name string, tool tools.Tool) error {
		if err := registry.Register(tool); err != nil {
			return fmt.Errorf("failed to register tool '%s': %w", name, err)
		}
		return nil
	}

	enabledTools := getEnabledTools(cfg)
	isEnabled := func(name string) bool {
		return enabledTools[name]
	}

	getToolCfg := func(name string) (config.ToolConfig, bool) {
		cfg, exists := cfg.Tools[name]
		return cfg, exists
	}

	// === LLM Provider Ping Tool ===
	if toolCfg, exists := getToolCfg("ping_llm_provider"); exists && isEnabled("ping_llm_provider") {
		if err := register("ping_llm_provider", std.NewLLMPingTool(modelRegistry, cfg, toolCfg)); err != nil {
			return err
		}
	}

	// === Ask User Question Tool ===
	// QuestionManager will be set later via SetQuestionManager() in cmd/ layer
	if toolCfg, exists := getToolCfg("ask_user_question"); exists && isEnabled("ask_user_question") {
		if err := register("ask_user_question", std.NewAskUserQuestionTool(nil, toolCfg)); err != nil {
			return err
		}
	}

	return nil
}

// setupS3Tools регистрирует инструменты S3 хранения.
// Зависимости: s3storage.Client, state.CoreState (для классификации), cfg
func setupS3Tools(registry *tools.Registry, cfg *config.AppConfig, s3Client *s3storage.Client, state *state.CoreState) error {
	register := func(name string, tool tools.Tool) error {
		if err := registry.Register(tool); err != nil {
			return fmt.Errorf("failed to register tool '%s': %w", name, err)
		}
		return nil
	}

	enabledTools := getEnabledTools(cfg)
	isEnabled := func(name string) bool {
		return enabledTools[name]
	}

	// === S3 Basic Tools ===
	// Для S3 tools конфигурация обычно не требуется, проверяем только enabled
	if isEnabled("list_s3_files") {
		if _, exists := cfg.Tools["list_s3_files"]; exists {
			if err := register("list_s3_files", std.NewS3ListTool(s3Client)); err != nil {
				return err
			}
		}
	}

	if _, exists := cfg.Tools["read_s3_object"]; exists && isEnabled("read_s3_object") {
		if err := register("read_s3_object", std.NewS3ReadTool(s3Client)); err != nil {
			return err
		}
	}

	if _, exists := cfg.Tools["read_s3_image"]; exists && isEnabled("read_s3_image") {
		tool := std.NewS3ReadImageTool(s3Client, cfg.ImageProcessing)
		if err := register("read_s3_image", tool); err != nil {
			return err
		}
	}

	if _, exists := cfg.Tools["get_plm_data"]; exists && isEnabled("get_plm_data") {
		tool := std.NewGetPLMDataTool(s3Client)
		if err := register("get_plm_data", tool); err != nil {
			return err
		}
	}

	if _, exists := cfg.Tools["download_s3_files"]; exists && isEnabled("download_s3_files") {
		tool := std.NewDownloadS3FilesTool(s3Client)
		if err := register("download_s3_files", tool); err != nil {
			return err
		}
	}

	// === S3 Batch Tools ===
	if toolCfg, exists := cfg.Tools["classify_and_download_s3_files"]; exists && isEnabled("classify_and_download_s3_files") {
		tool := std.NewClassifyAndDownloadS3Files(
			s3Client,
			state,
			cfg.ImageProcessing,
			cfg.FileRules,
			toolCfg,
		)
		if err := register("classify_and_download_s3_files", tool); err != nil {
			return err
		}
	}

	return nil
}

// setupVisionTools регистрирует инструменты анализа изображений.
// Зависимости: llm.Provider (vision), s3storage.Client, state.CoreState, cfg
func setupVisionTools(registry *tools.Registry, cfg *config.AppConfig, state *state.CoreState, visionLLM llm.Provider) error {
	register := func(name string, tool tools.Tool) error {
		if err := registry.Register(tool); err != nil {
			return fmt.Errorf("failed to register tool '%s': %w", name, err)
		}
		return nil
	}

	enabledTools := getEnabledTools(cfg)
	isEnabled := func(name string) bool {
		return enabledTools[name]
	}

	// === Vision Tools ===
	if toolCfg, exists := cfg.Tools["analyze_article_images_batch"]; exists && isEnabled("analyze_article_images_batch") {
		// Rule 4: Vision LLM не должен знать про инструменты
		// Создаём отдельный provider БЕЗ tools через factory (не через ModelRegistry)
		// Это предотвращает проблему когда LLM пытается вызвать tool вместо возврата текста

		// Получаем ModelDef для vision модели из конфига
		visionModelDef, exists := cfg.Models.Definitions[cfg.Models.DefaultVision]
		if !exists {
			return fmt.Errorf("vision model '%s' not found in config definitions", cfg.Models.DefaultVision)
		}

		// ОТКЛЮЧАЕМ thinking mode для vision задач!
		// GLM-4.6 с thinking mode генерирует 335+ chunk'ов и может возвращать malformed tool calls
		// Vision analysis должен быть быстрым и предсказуемым
		originalThinking := visionModelDef.Thinking
		visionModelDef.Thinking = "disabled" // Принудительно отключаем

		// Создаём отдельный Vision LLM provider БЕЗ tools через factory
		// Rule 4: Используем CreateProvider() вместо ModelRegistry.Get()
		visionLLMNoTools, err := models.CreateProvider(visionModelDef)
		if err != nil {
			return fmt.Errorf("failed to create vision LLM provider: %w", err)
		}

		// Восстанавливаем оригинальное значение (не меняем конфиг)
		visionModelDef.Thinking = originalThinking

		utils.Debug("Created vision LLM provider with thinking mode disabled",
			"model", cfg.Models.DefaultVision,
			"original_thinking", originalThinking)

		// Load vision system prompt
		visionPrompt, err := prompt.LoadVisionSystemPrompt(cfg)
		if err != nil {
			return fmt.Errorf("failed to load vision prompt: %w", err)
		}

		tool := std.NewAnalyzeArticleImagesBatch(
			state,
			state.GetStorage(), // S3 клиент для скачивания изображений
			visionLLMNoTools,  // Vision LLM БЕЗ tools и БЕЗ thinking mode
			visionPrompt,
			cfg.ImageProcessing,
			toolCfg,
		)
		if err := register("analyze_article_images_batch", tool); err != nil {
			return err
		}
	}

	return nil
}

// setupTodoTools регистрирует инструменты планировщика задач.
// Зависимости: todo.Manager
func setupTodoTools(registry *tools.Registry, cfg *config.AppConfig, tm *todo.Manager) error {
	register := func(name string, tool tools.Tool) error {
		if err := registry.Register(tool); err != nil {
			return fmt.Errorf("failed to register tool '%s': %w", name, err)
		}
		return nil
	}

	getToolCfg := func(name string) (config.ToolConfig, bool) {
		tc, exists := cfg.Tools[name]
		if !exists {
			return config.ToolConfig{Enabled: false}, false
		}
		return tc, true
	}

	enabledTools := getEnabledTools(cfg)
	isEnabled := func(name string) bool {
		return enabledTools[name]
	}

	// === Planner Tools ===
	if toolCfg, exists := getToolCfg("plan_add_task"); exists && isEnabled("plan_add_task") {
		if err := register("plan_add_task", std.NewPlanAddTaskTool(tm, toolCfg)); err != nil {
			return err
		}
	}

	if toolCfg, exists := getToolCfg("plan_mark_done"); exists && isEnabled("plan_mark_done") {
		if err := register("plan_mark_done", std.NewPlanMarkDoneTool(tm, toolCfg)); err != nil {
			return err
		}
	}

	if toolCfg, exists := getToolCfg("plan_mark_failed"); exists && isEnabled("plan_mark_failed") {
		if err := register("plan_mark_failed", std.NewPlanMarkFailedTool(tm, toolCfg)); err != nil {
			return err
		}
	}

	if toolCfg, exists := getToolCfg("plan_clear"); exists && isEnabled("plan_clear") {
		if err := register("plan_clear", std.NewPlanClearTool(tm, toolCfg)); err != nil {
			return err
		}
	}

	if toolCfg, exists := getToolCfg("plan_set_tasks"); exists && isEnabled("plan_set_tasks") {
		if err := register("plan_set_tasks", std.NewPlanSetTasksTool(tm, toolCfg)); err != nil {
			return err
		}
	}

	return nil
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
func SetupTools(state *state.CoreState, wbClient *wb.Client, visionLLM llm.Provider, cfg *config.AppConfig, modelRegistry *models.Registry) error {
	registry := state.GetToolsRegistry()

	// Регистрируем инструменты по категориям
	if err := setupWBTools(registry, cfg, wbClient); err != nil {
		return err
	}

	if err := setupLLMTools(registry, cfg, modelRegistry); err != nil {
		return err
	}

	if err := setupDictionaryTools(registry, cfg, state); err != nil {
		return err
	}

	if err := setupS3Tools(registry, cfg, state.GetStorage(), state); err != nil {
		return err
	}

	if err := setupVisionTools(registry, cfg, state, visionLLM); err != nil {
		return err
	}

	if err := setupTodoTools(registry, cfg, state.GetTodoManager()); err != nil {
		return err
	}

	// Валидация на неизвестные tools
	for toolName := range cfg.Tools {
		if !isKnownTool(toolName) {
			return fmt.Errorf("unknown tool '%s' in config. Known tools: %v",
				toolName, getAllKnownToolNames())
		}
	}

	// Собираем список зарегистрированных tools для логирования
	var registered []string
	for toolName := range cfg.Tools {
		if cfg.Tools[toolName].Enabled && isKnownTool(toolName) {
			registered = append(registered, toolName)
		}
	}

	utils.Debug("Tools registered", "count", len(registered), "tools", registered)
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
		// LLM Provider Tools
		"ping_llm_provider",
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
		"download_s3_files",
		// S3 Batch Tools
		"classify_and_download_s3_files",
		"analyze_article_images_batch",
		// Planner Tools
		"plan_add_task",
		"plan_mark_done",
		"plan_mark_failed",
		"plan_clear",
		"plan_set_tasks",
		// Question Tool
		"ask_user_question",
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
