package classifier

import (
	"path/filepath"
	"strings"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/s3storage"
	"github.com/ilkoid/poncho-ai/pkg/state"
)

// Deprecated: используйте state.FileMeta напрямую.
// Оставлен для обратной совместимости.
type ClassifiedFile = state.FileMeta

// Engine выполняет классификацию
type Engine struct {
	rules []config.FileRule
}

func New(rules []config.FileRule) *Engine {
	return &Engine{rules: rules}
}

// Process принимает список сырых объектов и возвращает карту [Tag] -> Список файлов.
//
// Возвращает map[string][]*state.FileMeta - классифицированные файлы
// с базовыми метаданными. Vision описание заполняется позже через
// state.Writer.UpdateFileAnalysis().
func (e *Engine) Process(objects []s3storage.StoredObject) (map[string][]*state.FileMeta, error) {
	result := make(map[string][]*state.FileMeta)

	for _, obj := range objects {
		filename := filepath.Base(obj.Key) // Смотрим только на имя файла, не на путь

		matched := false
		var matchedTag string

		for _, rule := range e.rules {
			for _, pattern := range rule.Patterns {
				// Используем Case-insensitive сравнение для расширений
				isMatch, _ := filepath.Match(strings.ToLower(pattern), strings.ToLower(filename))

				if isMatch {
					matchedTag = rule.Tag
					matched = true
					break
				}
			}
			if matched {
				break
			}
		}

		// Определяем тег (matched или "other")
		tag := matchedTag
		if !matched {
			tag = "other"
		}

		// Создаём FileMeta через конструктор
		fileMeta := state.NewFileMeta(tag, obj.Key, obj.Size, filename)
		result[tag] = append(result[tag], fileMeta)
	}

	return result, nil
}
