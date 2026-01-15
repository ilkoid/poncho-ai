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
	ToolBundles     map[string]ToolBundle `yaml:"tool_bundles"`  // NEW: Группы инструментов
	EnableBundles   []string             `yaml:"enable_bundles"`  // NEW: Какие bundles включить
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

// ToolBundle — группа связанных инструментов для токен-оптимизации.
//
// Bundles позволяют сгруппировать инструменты по бизнес-контексту,
// что снижает количество токенов в system prompt (100 tools → 10 bundles).
//
// Пример использования:
//   tool_bundles:
//     wb-tools:
//       description: "Wildberries API: категории, бренды, отзывы"
//       tools:
//         - get_wb_parent_categories
//         - get_wb_brands
//         - get_wb_feedbacks
//   enable_bundles:
//     - wb-tools
type ToolBundle struct {
	Description string   `yaml:"description"` // Описание bundle для LLM
	Tools       []string `yaml:"tools"`       // Список инструментов в bundle
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
