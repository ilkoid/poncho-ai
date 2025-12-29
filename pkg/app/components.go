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

	"github.com/ilkoid/poncho-ai/internal/agent"
	"github.com/ilkoid/poncho-ai/internal/app"
	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/factory"
	"github.com/ilkoid/poncho-ai/pkg/llm"
	"github.com/ilkoid/poncho-ai/pkg/prompt"
	"github.com/ilkoid/poncho-ai/pkg/s3storage"
	"github.com/ilkoid/poncho-ai/pkg/tools"
	"github.com/ilkoid/poncho-ai/pkg/tools/std"
	"github.com/ilkoid/poncho-ai/pkg/utils"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Components содержит все компоненты приложения для переиспользования.
//
// Эта структура может быть использована в TUI для избежания дублирования
// кода инициализации между CLI и GUI версиями.
type Components struct {
	Config       *config.AppConfig
	State        *app.GlobalState
	LLM          llm.Provider      // Chat модель (для ReAct loop)
	VisionLLM    llm.Provider      // Vision модель (для batch image analysis)
	WBClient     *wb.Client
	Orchestrator *agent.Orchestrator
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
// Порядок поиска:
// 1. Флаг -config (если указан)
// 2. Текущая директория (./config.yaml)
// 3. Директория бинарника
// 4. Директория утилиты (cmd/orchestrator-test/)
// 5. Родительская директория (для запуска из cmd/)
type DefaultConfigPathFinder struct {
	// ConfigFlag - значение флага -config, если указан
	ConfigFlag string
}

// FindConfigPath находит путь к config.yaml.
func (f *DefaultConfigPathFinder) FindConfigPath() string {
	var cfgPath string

	// 1. Флаг имеет приоритет
	if f.ConfigFlag != "" {
		cfgPath = f.ConfigFlag
		return resolveAbsPath(cfgPath)
	}

	// 2. Текущая директория
	cfgPath = "config.yaml"
	if _, err := os.Stat(cfgPath); err == nil {
		return resolveAbsPath(cfgPath)
	}

	// 3. Директория бинарника
	if execPath, err := os.Executable(); err == nil {
		binDir := filepath.Dir(execPath)
		cfgPath = filepath.Join(binDir, "config.yaml")
		if _, err := os.Stat(cfgPath); err == nil {
			return cfgPath
		}
	}

	// 4. Директория утилиты (относительно текущей)
	cfgPath = filepath.Join("cmd", "orchestrator-test", "config.yaml")
	if _, err := os.Stat(cfgPath); err == nil {
		return resolveAbsPath(cfgPath)
	}

	// 5. Родительская директория (для запуска из cmd/orchestrator-test/)
	cfgPath = filepath.Join("..", "..", "config.yaml")
	if _, err := os.Stat(cfgPath); err == nil {
		return resolveAbsPath(cfgPath)
	}

	// 6. Родительская директория напрямую
	cfgPath = filepath.Join("..", "config.yaml")
	if _, err := os.Stat(cfgPath); err == nil {
		return resolveAbsPath(cfgPath)
	}

	// Возвращаем дефолтный путь (даже если не существует)
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
//
// Параметры:
//   - cfg: конфигурация приложения
//   - maxIters: максимальное количество итераций оркестратора
//   - systemPrompt: опциональный системный промпт (если пустой, загружается из промптов)
func Initialize(cfg *config.AppConfig, maxIters int, systemPrompt string) (*Components, error) {
	utils.Info("Initializing components", "maxIters", maxIters)

	// 1. Инициализируем S3 клиент
	s3Client, err := s3storage.New(cfg.S3)
	if err != nil {
		utils.Error("S3 client creation failed", "error", err)
		return nil, fmt.Errorf("failed to create S3 client: %w", err)
	}
	utils.Info("S3 client initialized", "bucket", cfg.S3.Bucket)

	// 2. Инициализируем WB клиент из конфигурации
	var wbClient *wb.Client
	if cfg.WB.APIKey != "" {
		var err error
		wbClient, err = wb.NewFromConfig(cfg.WB)
		if err != nil {
			utils.Error("WB client creation failed", "error", err)
			return nil, fmt.Errorf("failed to create WB client: %w", err)
		}
		ctx := context.Background()
		if _, err := wbClient.Ping(ctx); err != nil {
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
		ctx := context.Background()
		dicts, err = wbClient.LoadDictionaries(ctx)
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

	// 4. Создаём глобальное состояние (thread-safe)
	state := app.NewState(cfg, s3Client)
	state.Dictionaries = dicts // Сохраняем справочники в state

	// 5. Регистрируем Todo команды
	app.SetupTodoCommands(state.CommandRegistry, state)

	// 6. Создаём LLM провайдер (ОДИН для reasoning и chat, правило 4)
	// Получаем reasoning модель (или fallback на chat если не указана)
	reasoningModelDef, ok := cfg.GetReasoningModel("")
	if !ok {
		utils.Error("Reasoning model not found", "model", cfg.Models.DefaultReasoning)
		return nil, fmt.Errorf("reasoning model not found - configure default_reasoning or default_chat in config.yaml")
	}

	llmProvider, err := factory.NewLLMProvider(reasoningModelDef)
	if err != nil {
		utils.Error("LLM provider creation failed", "error", err)
		return nil, fmt.Errorf("failed to create LLM provider: %w", err)
	}
	utils.Info("LLM provider created", "provider", reasoningModelDef.Provider, "model", reasoningModelDef.ModelName)

	// Формируем reasoningConfig из config.yaml
	reasoningConfig := llm.GenerateOptions{
		Model:       reasoningModelDef.ModelName,
		Temperature: reasoningModelDef.Temperature,
		MaxTokens:   reasoningModelDef.MaxTokens,
		Format:      "",
	}

	// Формируем chatConfig из config.yaml (используется как fallback для post-prompts)
	chatConfig := llm.GenerateOptions{}
	if chatModelDef, ok := cfg.GetChatModel(""); ok {
		chatConfig = llm.GenerateOptions{
			Model:       chatModelDef.ModelName,
			Temperature: chatModelDef.Temperature,
			MaxTokens:   chatModelDef.MaxTokens,
			Format:      "",
		}
		utils.Info("Chat config loaded", "model", chatModelDef.ModelName)
	} else {
		// Fallback на reasoning config если chat не настроен
		chatConfig = reasoningConfig
		utils.Info("Chat config not set, using reasoning config as fallback")
	}

	// Vision LLM (для batch image analysis) - отдельный провайдер
	var visionLLM llm.Provider
	if cfg.Models.DefaultVision != "" {
		visionModelDef, ok := cfg.Models.Definitions[cfg.Models.DefaultVision]
		if !ok {
			utils.Error("Default vision model not found", "model", cfg.Models.DefaultVision)
			return nil, fmt.Errorf("default_vision model '%s' not found in definitions", cfg.Models.DefaultVision)
		}

		visionLLM, err = factory.NewLLMProvider(visionModelDef)
		if err != nil {
			utils.Error("Vision LLM provider creation failed", "error", err)
			return nil, fmt.Errorf("failed to create vision LLM provider: %w", err)
		}
		utils.Info("Vision LLM provider created", "provider", visionModelDef.Provider, "model", visionModelDef.ModelName)
	} else {
		utils.Info("No vision model configured, batch image analysis will be unavailable")
	}

	// 7. Регистрируем инструменты из YAML конфигурации
	if err := SetupTools(state, wbClient, visionLLM, cfg); err != nil {
		utils.Error("Tools registration failed", "error", err)
		return nil, fmt.Errorf("failed to register tools: %w", err)
	}

	// 8. Загружаем системный промпт
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

	// 9. Загружаем post-prompts для tools
	toolPostPrompts, err := prompt.LoadToolPostPrompts(cfg)
	if err != nil {
		utils.Error("Failed to load tool post-prompts", "error", err)
		return nil, fmt.Errorf("failed to load tool post-prompts: %w", err)
	}
	if len(toolPostPrompts.Tools) > 0 {
		utils.Info("Tool post-prompts loaded", "count", len(toolPostPrompts.Tools))
	}

	// 10. Создаём Orchestrator
	orchestrator, err := agent.New(agent.Config{
		LLM:              llmProvider,
		Registry:         state.GetToolsRegistry(),
		State:            state,
		MaxIters:         maxIters,
		SystemPrompt:     agentSystemPrompt,
		ToolPostPrompts:  toolPostPrompts,
		ReasoningConfig:  reasoningConfig,
		ChatConfig:       chatConfig,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create orchestrator: %w", err)
	}

	return &Components{
		Config:       cfg,
		State:        state,
		LLM:          llmProvider,
		VisionLLM:    visionLLM,
		WBClient:     wbClient,
		Orchestrator: orchestrator,
	}, nil
}

// Execute выполняет запрос через оркестратор.
//
// Эта функция является переиспользуемой - она может быть вызвана
// из TUI версии для выполнения запросов с тем же кодом.
//
// Правило 4: работает только через llm.Provider интерфейс.
// Правило 3: использует tools.Registry для инструментов.
func Execute(c *Components, query string, timeout time.Duration) (*ExecutionResult, error) {
	startTime := time.Now()
	utils.Info("Executing query", "query", query, "timeout", timeout)

	// Создаём контекст с timeout
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
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
		TodoString: c.State.Todo.String(),
		History:    c.Orchestrator.GetHistory(),
		Duration:   time.Since(startTime),
	}

	// Получаем статистику задач
	pending, done, failed := c.State.Todo.GetStats()
	result.TodoStats = TodoStats{
		Pending: pending,
		Done:    done,
		Failed:  failed,
		Total:   pending + done + failed,
	}

	return result, nil
}

// ClearTodos очищает todo лист после выполнения.
func (c *Components) ClearTodos() {
	c.State.Todo.Clear()
}

// SetupTools регистрирует инструменты из YAML конфигурации.
//
// Правило 3: все инструменты регистрируются через Registry.Register().
// Правило 1: инструменты реализуют Tool интерфейс.
//
// Конфигурация читается из cfg.Tools, где ключом является имя tool.
// Инструмент регистрируется только если cfg.Tools[toolName].Enabled == true.
//
// Возвращает ошибку если:
//   - инструмент не найден в коде (unknown tool)
//   - валидация схемы инструмента не прошла
func SetupTools(state *app.GlobalState, wbClient *wb.Client, visionLLM llm.Provider, cfg *config.AppConfig) error {
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
		if err := register("search_wb_products", std.NewWbProductSearchTool(wbClient, toolCfg)); err != nil {
			return err
		}
	}

	if toolCfg, exists := getToolCfg("get_wb_parent_categories"); exists && toolCfg.Enabled {
		if err := register("get_wb_parent_categories", std.NewWbParentCategoriesTool(wbClient, toolCfg)); err != nil {
			return err
		}
	}

	if toolCfg, exists := getToolCfg("get_wb_subjects"); exists && toolCfg.Enabled {
		if err := register("get_wb_subjects", std.NewWbSubjectsTool(wbClient, toolCfg)); err != nil {
			return err
		}
	}

	if toolCfg, exists := getToolCfg("ping_wb_api"); exists && toolCfg.Enabled {
		if err := register("ping_wb_api", std.NewWbPingTool(wbClient, toolCfg)); err != nil {
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
	if state.Dictionaries != nil {
		if toolCfg, exists := getToolCfg("wb_colors"); exists && toolCfg.Enabled {
			if err := register("wb_colors", std.NewWbColorsTool(state.Dictionaries, toolCfg)); err != nil {
				return err
			}
		}

		if toolCfg, exists := getToolCfg("wb_countries"); exists && toolCfg.Enabled {
			if err := register("wb_countries", std.NewWbCountriesTool(state.Dictionaries, toolCfg)); err != nil {
				return err
			}
		}

		if toolCfg, exists := getToolCfg("wb_genders"); exists && toolCfg.Enabled {
			if err := register("wb_genders", std.NewWbGendersTool(state.Dictionaries, toolCfg)); err != nil {
				return err
			}
		}

		if toolCfg, exists := getToolCfg("wb_seasons"); exists && toolCfg.Enabled {
			if err := register("wb_seasons", std.NewWbSeasonsTool(state.Dictionaries, toolCfg)); err != nil {
				return err
			}
		}

		if toolCfg, exists := getToolCfg("wb_vat_rates"); exists && toolCfg.Enabled {
			if err := register("wb_vat_rates", std.NewWbVatRatesTool(state.Dictionaries, toolCfg)); err != nil {
				return err
			}
		}
	}

	// === WB Service Tools ===
	if toolCfg, exists := getToolCfg("reload_wb_dictionaries"); exists && toolCfg.Enabled {
		if err := register("reload_wb_dictionaries", std.NewReloadWbDictionariesTool(wbClient, toolCfg)); err != nil {
			return err
		}
	}

	// === S3 Basic Tools ===
	if toolCfg, exists := getToolCfg("list_s3_files"); exists && toolCfg.Enabled {
		if err := register("list_s3_files", std.NewS3ListTool(state.S3)); err != nil {
			return err
		}
	}

	if toolCfg, exists := getToolCfg("read_s3_object"); exists && toolCfg.Enabled {
		if err := register("read_s3_object", std.NewS3ReadTool(state.S3)); err != nil {
			return err
		}
	}

	if toolCfg, exists := getToolCfg("read_s3_image"); exists && toolCfg.Enabled {
		tool := std.NewS3ReadImageTool(state.S3, cfg.ImageProcessing)
		if err := register("read_s3_image", tool); err != nil {
			return err
		}
	}

	// === S3 Batch Tools ===
	if toolCfg, exists := getToolCfg("classify_and_download_s3_files"); exists && toolCfg.Enabled {
		tool := std.NewClassifyAndDownloadS3Files(
			state.S3,
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
			state.S3, // S3 клиент для скачивания изображений
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
		if err := register("plan_add_task", std.NewPlanAddTaskTool(state.Todo, toolCfg)); err != nil {
			return err
		}
	}

	if toolCfg, exists := getToolCfg("plan_mark_done"); exists && toolCfg.Enabled {
		if err := register("plan_mark_done", std.NewPlanMarkDoneTool(state.Todo, toolCfg)); err != nil {
			return err
		}
	}

	if toolCfg, exists := getToolCfg("plan_mark_failed"); exists && toolCfg.Enabled {
		if err := register("plan_mark_failed", std.NewPlanMarkFailedTool(state.Todo, toolCfg)); err != nil {
			return err
		}
	}

	if toolCfg, exists := getToolCfg("plan_clear"); exists && toolCfg.Enabled {
		if err := register("plan_clear", std.NewPlanClearTool(state.Todo, toolCfg)); err != nil {
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
		// WB Service Tools
		"reload_wb_dictionaries",
		// S3 Basic Tools
		"list_s3_files",
		"read_s3_object",
		"read_s3_image",
		// S3 Batch Tools
		"classify_and_download_s3_files",
		"analyze_article_images_batch",
		// Planner Tools
		"plan_add_task",
		"plan_mark_done",
		"plan_mark_failed",
		"plan_clear",
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
