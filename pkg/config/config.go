package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// AppConfig — корневая структура конфигурации.
// Она зеркалит структуру твоего config.yaml.
type AppConfig struct {
	Models          ModelsConfig          `yaml:"models"`
	Tools           map[string]ToolConfig `yaml:"tools"`
	S3              S3Config              `yaml:"s3"`
	ImageProcessing ImageProcConfig       `yaml:"image_processing"`
	App             AppSpecific           `yaml:"app"`
    FileRules 		[]FileRule            `yaml:"file_rules"` // Новая секция
	WB				 WBConfig             `yaml:"wb"`
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
	DefaultVision string              `yaml:"default_vision"` // Алиас по умолчанию (например, "glm-4.6v-flash")
	DefaultChat   string              `yaml:"default_chat"`   // Алиас для чата по умолчанию (например, "glm-4.5")
	Definitions   map[string]ModelDef `yaml:"definitions"`    // Словарь определений моделей
}

// ModelDef — параметры конкретной модели.
type ModelDef struct {
	Provider    string        `yaml:"provider"`   // "zai", "openai" и т.д.
	ModelName   string        `yaml:"model_name"` // Реальное имя в API
	APIKey      string        `yaml:"api_key"`    // Поддерживает ${VAR}
	MaxTokens   int           `yaml:"max_tokens"`
	Temperature float64       `yaml:"temperature"`
	Timeout     time.Duration `yaml:"timeout"` // Go умеет парсить строки вида "60s", "1m"
    BaseURL string `yaml:"base_url"` // <--- Добавить
}

// ToolConfig — настройки инструментов (импорт, поиск и т.д.).
type ToolConfig struct {
	Enabled    bool   `yaml:"enabled"`
	PostPrompt string `yaml:"post_prompt"` // Путь к post-prompt файлу (относительно prompts_dir)
	Timeout    time.Duration `yaml:"timeout"`
	RetryCount int           `yaml:"retry_count"`
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
	Debug      bool   `yaml:"debug"`
	PromptsDir string `yaml:"prompts_dir"`
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
	// Можно добавить проверку наличия дефолтной модели
	if c.Models.DefaultVision != "" {
		if _, ok := c.Models.Definitions[c.Models.DefaultVision]; !ok {
			return fmt.Errorf("default_vision model '%s' is not defined in definitions", c.Models.DefaultVision)
		}
	}
	return nil
}

// Helper методы для удобства доступа (Syntactic sugar)

// GetVisionModel возвращает конфигурацию модели по умолчанию или по имени.
func (c *AppConfig) GetVisionModel(name string) (ModelDef, bool) {
	if name == "" {
		name = c.Models.DefaultVision
	}
	m, ok := c.Models.Definitions[name]
	return m, ok
}
