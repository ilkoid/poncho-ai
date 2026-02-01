package prompts

import (
	"fmt"
	"os"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/prompts/sources"
)

// CreateSourceRegistry создаёт реестр источников промптов из конфигурации.
//
// OCP Principle: Добавление новых источников через YAML конфигурацию
// без изменения этого кода.
//
// Fallback Chain:
// 1. File sources из YAML конфигурации (в порядке добавления)
// 2. Default source (Go defaults) — всегда добавляется как fallback
//
// YAML-first философия: Файлы приоритетны, Go defaults — резерв.
func CreateSourceRegistry(cfg *config.AppConfig) (*SourceRegistry, error) {
	registry := NewSourceRegistry()

	// 1. Добавляем источники из YAML конфигурации
	for _, sourceCfg := range cfg.PromptSources {
		source, err := createSource(sourceCfg, cfg)
		if err != nil {
			return nil, fmt.Errorf("failed to create prompt source type '%s': %w", sourceCfg.Type, err)
		}
		if source != nil {
			registry.AddSource(source)
		}
	}

	// 2. Добавляем Default source (Go defaults) как fallback
	// Всегда добавляется ПОСЛЕ пользовательских источников
	defaultSrc := sources.NewDefaultSource()
	defaultSrc.PopulateDefaults() // Заполняем стандартными промптами
	registry.AddSource(&defaultSourceAdapter{src: defaultSrc})

	return registry, nil
}

// createSource создаёт источник промптов по типу.
//
// Factory pattern для расширяемости (OCP).
// Для добавления нового типа источника:
// 1. Добавьте case сюда
// 2. Реализуйте adapter (см. ниже)
func createSource(cfg config.PromptSourceConfig, appCfg *config.AppConfig) (PromptSource, error) {
	switch cfg.Type {
	case "file":
		// File source: YAML файлы из base_dir
		baseDir := cfg.Config["base_dir"]

		// Если не указан, используем cfg.App.PromptsDir (YAML-first)
		if baseDir == "" || baseDir == "${PROMPTS_DIR:-./prompts}" {
			baseDir = appCfg.App.PromptsDir
		}

		// Поддержка ENV переменной PROMPTS_DIR
		if baseDir == "${PROMPTS_DIR:-./prompts}" {
			if envDir := os.Getenv("PROMPTS_DIR"); envDir != "" {
				baseDir = envDir
			} else {
				baseDir = "./prompts"
			}
		}

		fileSrc := sources.NewFileSource(baseDir)
		return &fileSourceAdapter{src: fileSrc}, nil

	case "database":
		// Database source: SQL база данных (пример)
		// В продакшене требуется connection_string и table
		connString, ok := cfg.Config["connection_string"]
		if !ok || connString == "" {
			return nil, fmt.Errorf("database source requires 'connection_string' config")
		}

		table := cfg.Config["table"]
		if table == "" {
			table = "prompts"
		}

		// TODO: Создать sql.DB connection
		// Для примера возвращаем nil (требует драйвер PostgreSQL)
		_ = connString
		_ = table
		return nil, fmt.Errorf("database source not implemented (requires PostgreSQL driver)")

	case "api":
		// API source: HTTP REST API (пример)
		endpoint, ok := cfg.Config["endpoint"]
		if !ok || endpoint == "" {
			return nil, fmt.Errorf("api source requires 'endpoint' config")
		}

		token := cfg.Config["auth_token"]
		apiSrc := sources.NewAPISource(endpoint, token)
		return &apiSourceAdapter{src: apiSrc}, nil

	default:
		return nil, fmt.Errorf("unknown prompt source type: '%s'", cfg.Type)
	}
}

// === Adapters: sources.PromptData → prompts.PromptFile ===

// fileSourceAdapter адаптирует sources.FileSource к PromptSource.
type fileSourceAdapter struct {
	src *sources.FileSource
}

func (a *fileSourceAdapter) Load(promptID string) (*PromptFile, error) {
	data, err := a.src.Load(promptID)
	if err != nil {
		return nil, err
	}
	return &PromptFile{
		System:    data.System,
		Template:  data.Template,
		Variables: data.Variables,
		Metadata:  data.Metadata,
	}, nil
}

// apiSourceAdapter адаптирует sources.APISource к PromptSource.
type apiSourceAdapter struct {
	src *sources.APISource
}

func (a *apiSourceAdapter) Load(promptID string) (*PromptFile, error) {
	data, err := a.src.Load(promptID)
	if err != nil {
		return nil, err
	}
	return &PromptFile{
		System:    data.System,
		Template:  data.Template,
		Variables: data.Variables,
		Metadata:  data.Metadata,
	}, nil
}

// defaultSourceAdapter адаптирует sources.DefaultSource к PromptSource.
type defaultSourceAdapter struct {
	src *sources.DefaultSource
}

func (a *defaultSourceAdapter) Load(promptID string) (*PromptFile, error) {
	data, err := a.src.Load(promptID)
	if err != nil {
		return nil, err
	}
	return &PromptFile{
		System:    data.System,
		Template:  data.Template,
		Variables: data.Variables,
		Metadata:  data.Metadata,
	}, nil
}

// LoadAgentSystemPrompt загружает системный промпт агента через SourceRegistry.
//
// Fallback chain:
// 1. File sources (из YAML)
// 2. Default source (Go defaults)
//
// Возвращает системный промпт или ошибку если все источники failed.
func LoadAgentSystemPrompt(cfg *config.AppConfig) (string, error) {
	registry, err := CreateSourceRegistry(cfg)
	if err != nil {
		return "", fmt.Errorf("failed to create source registry: %w", err)
	}

	// Пробуем загрузить из источников
	file, err := registry.Load("agent_system")
	if err != nil {
		return "", fmt.Errorf("failed to load agent_system prompt: %w", err)
	}

	if file.System == "" {
		return "", fmt.Errorf("agent_system prompt is empty")
	}

	return file.System, nil
}

// LoadToolPostprompts загружает tool post-prompts через SourceRegistry.
//
// Возвращает map[string]string где ключ = tool name, значение = post-prompt.
func LoadToolPostprompts(cfg *config.AppConfig) (map[string]string, error) {
	registry, err := CreateSourceRegistry(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create source registry: %w", err)
	}

	// Пробуем загрузить tool_postprompts
	file, err := registry.Load("tool_postprompts")
	if err != nil {
		// Не критично если не загружено — возвращаем пустой map
		return make(map[string]string), nil
	}

	return file.Variables, nil
}
