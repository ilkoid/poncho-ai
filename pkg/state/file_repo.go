// Package state предоставляет реализацию FileRepository.
//
// FileRepo инкапсулирует работу с файлами и результатами их анализа,
// используя Store как thread-safe хранилище.
package state

import (
	"github.com/ilkoid/poncho-ai/pkg/s3storage"
	"github.com/ilkoid/poncho-ai/pkg/utils"
)

// FileRepo реализует FileRepository.
//
// Использует Store для thread-safe хранения файлов и результатов анализа.
// Поддерживает группировку файлов по тегам (sketch, plm_data, marketing).
type FileRepo struct {
	store *Store
}

// NewFileRepo создаёт новый репозиторий файлов.
func NewFileRepo(store *Store) *FileRepo {
	return &FileRepo{store: store}
}

// SetFiles сохраняет файлы для текущей сессии.
//
// Принимает map[string][]*s3storage.FileMeta и сохраняет напрямую.
// VisionDescription заполняется позже через UpdateFileAnalysis().
//
// Thread-safe: делегирует в Store.Set.
func (r *FileRepo) SetFiles(files map[string][]*s3storage.FileMeta) error {
	return r.store.Set(KeyFiles, files)
}

// GetFiles возвращает копию текущих файлов.
//
// Thread-safe: делегирует в Store.Get.
func (r *FileRepo) GetFiles() map[string][]*s3storage.FileMeta {
	val, ok := r.store.Get(KeyFiles)
	if !ok {
		return make(map[string][]*s3storage.FileMeta)
	}

	files, ok := val.(map[string][]*s3storage.FileMeta)
	if !ok {
		utils.Error("GetFiles: type assertion failed", "key", KeyFiles)
		return make(map[string][]*s3storage.FileMeta)
	}

	// Возвращаем глубокую копию для thread-safety
	result := make(map[string][]*s3storage.FileMeta, len(files))
	for k, v := range files {
		result[k] = append([]*s3storage.FileMeta{}, v...)
	}
	return result
}

// UpdateFileAnalysis сохраняет результат работы Vision модели.
//
// Параметры:
//   - tag: тег файла (ключ в map, например "sketch", "plm_data")
//   - filename: имя файла для поиска в слайсе
//   - description: результат анализа (текст от vision модели)
//
// Thread-safe: делегирует в Store.Update.
func (r *FileRepo) UpdateFileAnalysis(tag string, filename string, description string) error {
	return r.store.Update(KeyFiles, func(val any) any {
		if val == nil {
			utils.Error("UpdateFileAnalysis: files not found", "tag", tag, "filename", filename)
			return val
		}

		files := val.(map[string][]*s3storage.FileMeta)
		filesList, ok := files[tag]
		if !ok {
			utils.Error("UpdateFileAnalysis: tag not found", "tag", tag, "filename", filename)
			return val
		}

		// Находим индекс файла и атомарно заменяем объект
		for i := range filesList {
			if filesList[i].Filename == filename {
				// Создаем новый объект для thread-safety
				updated := &s3storage.FileMeta{
					Tag:               filesList[i].Tag,
					OriginalKey:       filesList[i].OriginalKey,
					Size:              filesList[i].Size,
					Filename:          filesList[i].Filename,
					VisionDescription: description,
					Tags:              filesList[i].Tags,
				}
				filesList[i] = updated
				utils.Debug("File analysis updated", "tag", tag, "filename", filename, "desc_length", len(description))
				return files
			}
		}

		utils.Warn("UpdateFileAnalysis: file not found", "tag", tag, "filename", filename)
		return val
	})
}

// SetCurrentArticle устанавливает текущий артикул и файлы.
//
// Thread-safe: атомарная замена файлов и articleID через два вызова Store.
func (r *FileRepo) SetCurrentArticle(articleID string, files map[string][]*s3storage.FileMeta) error {
	if err := r.SetFiles(files); err != nil {
		return err
	}
	return r.store.Set(KeyCurrentArticle, articleID)
}

// GetCurrentArticleID возвращает ID текущего артикула.
//
// Thread-safe: делегирует в Store.Get.
func (r *FileRepo) GetCurrentArticleID() string {
	val, ok := r.store.Get(KeyCurrentArticle)
	if !ok {
		return "NONE"
	}

	articleID, ok := val.(string)
	if !ok {
		utils.Error("GetCurrentArticleID: type assertion failed", "key", KeyCurrentArticle)
		return "NONE"
	}
	return articleID
}

// GetCurrentArticle возвращает ID и файлы текущего артикула.
//
// Thread-safe: комбинирует GetCurrentArticleID и GetFiles.
func (r *FileRepo) GetCurrentArticle() (articleID string, files map[string][]*s3storage.FileMeta) {
	articleID = r.GetCurrentArticleID()
	files = r.GetFiles()
	return
}
