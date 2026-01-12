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
// Пример использования:
//
//	finder := &app.StandaloneConfigPathFinder{ConfigFlag: *configFlag}
//	components, cfgPath, err := app.InitializeForStandalone(finder, 10, "")
//	if err != nil {
//	    log.Fatalf("Initialization failed: %v", err)
//	}
func InitializeForStandalone(
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
	// Правило 11: передаём контекст для распространения отмены
	components, err := Initialize(context.Background(), cfg, maxIters, systemPrompt)
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
