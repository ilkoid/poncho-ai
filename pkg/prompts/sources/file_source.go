package sources

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// PromptData — сырые данные промпта без импорта pkg/prompts.
type PromptData struct {
	System    string            `yaml:"system"`
	Template  string            `yaml:"template"`
	Variables map[string]string `yaml:"variables"`
	Metadata  map[string]any    `yaml:"metadata"`
}

// FileSource — загрузка промптов из YAML файлов.
//
// Использует baseDir для поиска файлов: <baseDir>/<promptID>.yaml
type FileSource struct {
	baseDir string
}

// NewFileSource создаёт FileSource с указанной базовой директорией.
//
// baseDir обычно берётся из cfg.App.PromptsDir (YAML-first философия).
func NewFileSource(baseDir string) *FileSource {
	return &FileSource{
		baseDir: baseDir,
	}
}

// Load загружает промпт из YAML файла.
//
// Возвращает *PromptData для избежания циклического импорта.
func (s *FileSource) Load(promptID string) (*PromptData, error) {
	// Construct file path: <baseDir>/<promptID>.yaml
	path := filepath.Join(s.baseDir, promptID+".yaml")

	// Read file
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("file not found: %s", path)
		}
		return nil, fmt.Errorf("failed to read prompt file: %w", err)
	}

	// Parse YAML
	var file PromptData
	if err := yaml.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("failed to parse prompt YAML: %w", err)
	}

	return &file, nil
}
