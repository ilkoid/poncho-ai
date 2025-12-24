//go:build short

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
    APIKey string `yaml:"api_key"`
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
	Enabled    bool          `yaml:"enabled"`
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
	// TODO: Check if config file exists
	// TODO: Read the entire file
	// TODO: Substitute environment variables
	// TODO: Parse YAML into structure
	// TODO: Validate critical settings
	// TODO: Return the configuration
	return nil, nil
}

// validate проверяет обязательные поля.
func (c *AppConfig) validate() error {
	// TODO: Validate S3 bucket is not empty
	// TODO: Validate S3 endpoint is not empty
	// TODO: Validate default vision model exists in definitions
	return nil
}

// Helper методы для удобства доступа (Syntactic sugar)

// GetVisionModel возвращает конфигурацию модели по умолчанию или по имени.
func (c *AppConfig) GetVisionModel(name string) (ModelDef, bool) {
	// TODO: If name is empty, use default vision model
	// TODO: Return model definition and existence flag
	return ModelDef{}, false
}