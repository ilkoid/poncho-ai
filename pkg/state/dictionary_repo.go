// Package state предоставляет реализацию DictionaryRepository.
//
// DictionaryRepo инкапсулирует работу со справочниками маркетплейсов,
// используя Store как thread-safe хранилище.
package state

import (
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// DictionaryRepo реализует DictionaryRepository.
//
// Использует Store для thread-safe хранения справочников Wildberries
// (цвета, страны, полы, сезоны, НДС).
type DictionaryRepo struct {
	store *Store
}

// NewDictionaryRepo создаёт новый репозиторий справочников.
func NewDictionaryRepo(store *Store) *DictionaryRepo {
	return &DictionaryRepo{store: store}
}

// SetDictionaries устанавливает справочники маркетплейсов.
//
// Thread-safe: делегирует в Store.Set.
func (r *DictionaryRepo) SetDictionaries(dicts *wb.Dictionaries) error {
	return r.store.Set(KeyDictionaries, dicts)
}

// GetDictionaries возвращает справочники маркетплейсов.
//
// Возвращает nil если справочники не установлены.
// Thread-safe: делегирует в Store.Get.
func (r *DictionaryRepo) GetDictionaries() *wb.Dictionaries {
	val, ok := r.store.Get(KeyDictionaries)
	if !ok {
		return nil
	}

	dicts, ok := val.(*wb.Dictionaries)
	if !ok {
		return nil
	}
	return dicts
}
