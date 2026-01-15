// Package chain предоставляет Chain Pattern для AI агента.
package chain

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoadInterruptionPrompt тестирует загрузку interruption handler промпта.
func TestLoadInterruptionPrompt(t *testing.T) {
	tests := []struct {
		name           string
		promptsDir     string
		promptPath     string
		expectFallback bool
		expectConfig   bool
		setupFS        bool   // Создавать тестовую файловую систему
	}{
		{
			name:           "empty path uses fallback",
			promptsDir:     "",
			promptPath:     "",
			expectFallback: true,
			expectConfig:   false,
		},
		{
			name:           "non-existent file uses fallback",
			promptsDir:     "./non_existent_dir",
			promptPath:     "non_existent.yaml",
			expectFallback: true,
			expectConfig:   false,
		},
		{
			name:       "valid YAML file loads",
			promptsDir: "test_prompts",
			promptPath: "interruption_handler.yaml",
			setupFS:    true,
			// При наличии файла, fallback не используется
			expectFallback: false,
			expectConfig:   true,
		},
		{
			name:           "absolute path uses fallback when not exists",
			promptsDir:     "",
			promptPath:     "/absolute/non/existent.yaml",
			expectFallback: true,
			expectConfig:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Создаём тестовую файловую систему если нужно
			if tt.setupFS {
				// Создаём временную директорию
				tmpDir := t.TempDir()
				tt.promptsDir = tmpDir

				// Создаём тестовый YAML файл
				yamlPath := filepath.Join(tmpDir, "interruption_handler.yaml")
				yamlContent := `version: "1.0"
config:
  model: "test-model"
  temperature: 0.7
messages:
  - role: system
    content: "Test interruption handler prompt"
`
				if err := os.WriteFile(yamlPath, []byte(yamlContent), 0644); err != nil {
					t.Fatalf("Failed to create test YAML file: %v", err)
				}
			}

			// Вызываем тестируемую функцию
			prompt, config := loadInterruptionPrompt(tt.promptsDir, tt.promptPath)

			// Проверяем результат
			if tt.expectFallback && prompt != defaultInterruptionPrompt {
				t.Errorf("Expected default prompt, got: %s", prompt[:50])
			}

			if !tt.expectFallback && prompt == defaultInterruptionPrompt {
				t.Error("Expected YAML prompt, got default fallback")
			}

			if tt.expectConfig && config == nil {
				t.Error("Expected prompt config, got nil")
			}

			if !tt.expectConfig && config != nil {
				t.Errorf("Expected nil config, got: %+v", config)
			}
		})
	}
}

// TestLoadInterruptionPrompt_InvalidYAML тестирует обработку невалидного YAML.
func TestLoadInterruptionPrompt_InvalidYAML(t *testing.T) {
	tmpDir := t.TempDir()

	// Создаём невалидный YAML файл
	invalidYAMLPath := filepath.Join(tmpDir, "invalid.yaml")
	invalidYAMLContent := `invalid: yaml: content: [unclosed`

	if err := os.WriteFile(invalidYAMLPath, []byte(invalidYAMLContent), 0644); err != nil {
		t.Fatalf("Failed to create invalid YAML file: %v", err)
	}

	// Должен использоваться fallback вместо panic
	prompt, config := loadInterruptionPrompt(tmpDir, "invalid.yaml")

	if prompt != defaultInterruptionPrompt {
		t.Error("Expected default fallback prompt for invalid YAML")
	}

	if config != nil {
		t.Error("Expected nil config for invalid YAML")
	}
}

// TestLoadInterruptionPrompt_EmptyMessages тестирует YAML без messages.
func TestLoadInterruptionPrompt_EmptyMessages(t *testing.T) {
	tmpDir := t.TempDir()

	// Создаём YAML без messages
	emptyYAMLPath := filepath.Join(tmpDir, "empty.yaml")
	emptyYAMLContent := `version: "1.0"
config:
  model: "test-model"
messages: []
`

	if err := os.WriteFile(emptyYAMLPath, []byte(emptyYAMLContent), 0644); err != nil {
		t.Fatalf("Failed to create empty YAML file: %v", err)
	}

	// Должен использоваться fallback
	prompt, _ := loadInterruptionPrompt(tmpDir, "empty.yaml")

	if prompt != defaultInterruptionPrompt {
		t.Error("Expected default fallback prompt for empty messages")
	}

	// При пустых messages возвращается fallback prompt, config игнорируется
}

// TestLoadInterruptionPrompt_RelativePath тестирует относительные пути.
func TestLoadInterruptionPrompt_RelativePath(t *testing.T) {
	tmpDir := t.TempDir()

	// Создаём поддиректорию
	subDir := filepath.Join(tmpDir, "subdir")
	if err := os.Mkdir(subDir, 0755); err != nil {
		t.Fatalf("Failed to create subdir: %v", err)
	}

	// Создаём YAML файл в поддиректории
	yamlPath := filepath.Join(subDir, "handler.yaml")
	yamlContent := `version: "1.0"
config:
  model: "test-model"
messages:
  - role: system
    content: "Subdir prompt"
`

	if err := os.WriteFile(yamlPath, []byte(yamlContent), 0644); err != nil {
		t.Fatalf("Failed to create YAML file: %v", err)
	}

	// Тестируем относительный путь
	prompt, config := loadInterruptionPrompt(tmpDir, "subdir/handler.yaml")

	if prompt == defaultInterruptionPrompt {
		t.Error("Expected YAML prompt, got default fallback")
	}

	if config == nil {
		t.Error("Expected prompt config, got nil")
	}

	if config.Model != "test-model" {
		t.Errorf("Expected model 'test-model', got '%s'", config.Model)
	}
}
