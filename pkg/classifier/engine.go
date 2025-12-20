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
	result := make(map[string][]ClassifiedFile)

	for _, obj := range objects {
		filename := filepath.Base(obj.Key) // Смотрим только на имя файла, не на путь

		matched := false
		for _, rule := range e.rules {
			for _, pattern := range rule.Patterns {
				// Используем Case-insensitive сравнение для расширений
				// (на самом деле filepath.Match в Linux чувствителен к регистру,
				// для надежности лучше приводить к нижнему регистру оба)
				isMatch, _ := filepath.Match(strings.ToLower(pattern), strings.ToLower(filename))

				if isMatch {
					filename := filepath.Base(obj.Key)
					result[rule.Tag] = append(result[rule.Tag], ClassifiedFile{
						Tag:         rule.Tag,
						OriginalKey: obj.Key,
						Size:        obj.Size,
						Filename:    filename,
					})
					matched = true
					break // Файл попал в категорию, дальше не проверяем (или проверяем, если нужен мульти-тег?)
				}
			}
			if matched {
				break
			}
		}

		// Если файл не попал ни под одно правило, можно сохранить его в "other"
		if !matched {
			filename := filepath.Base(obj.Key)
			result["other"] = append(result["other"], ClassifiedFile{
				Tag:         "other",
				OriginalKey: obj.Key,
				Size:        obj.Size,
				Filename:    filename,
			})
		}
	}

	return result, nil
}
