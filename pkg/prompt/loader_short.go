//go:build short

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
	// TODO: Check if prompt file exists
	// TODO: Read file bytes
	// TODO: Parse YAML into PromptFile structure
	// TODO: Return parsed prompt file
	return nil, nil
}

// RenderMessages принимает данные (struct или map) и возвращает готовые сообщения
// где все {{.Field}} заменены на значения.
func (pf *PromptFile) RenderMessages(data interface{}) ([]Message, error) {
	// TODO: Create result slice with same length as messages
	// TODO: For each message, create template from content
	// TODO: Execute template with provided data
	// TODO: Store rendered message in result slice
	// TODO: Return rendered messages
	return nil, nil
}