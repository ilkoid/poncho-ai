//go:build short

package classifier

import (
	"path/filepath"
	"strings"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/s3storage"
)

// ClassifiedFile - файл с присвоенным тегом
type ClassifiedFile struct {
	Tag         string // "sketch", "plm" и т.д.
	OriginalKey string
	Size        int64
	Filename    string // Извлеченное имя файла из OriginalKey
}

// Engine выполняет классификацию
type Engine struct {
	rules []config.FileRule
}

func New(rules []config.FileRule) *Engine {
	return &Engine{rules: rules}
}

// Process принимает список сырых объектов и возвращает карту [Tag] -> Список файлов
func (e *Engine) Process(objects []s3storage.StoredObject) (map[string][]ClassifiedFile, error) {
	// TODO: Iterate through objects and classify them based on rules
	// TODO: For each object, check if filename matches any pattern in rules
	// TODO: If matched, add to result map with corresponding tag
	// TODO: If not matched, add to "other" category
	// TODO: Return the result map
	return nil, nil
}