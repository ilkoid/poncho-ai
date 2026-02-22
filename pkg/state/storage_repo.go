// Package state предоставляет реализацию StorageRepository.
//
// StorageRepo инкапсулирует работу с S3 клиентом,
// используя Store как thread-safe хранилище.
package state

import (
	"github.com/ilkoid/poncho-ai/pkg/s3storage"
)

// StorageRepo реализует StorageRepository.
//
// Использует Store для thread-safe хранения S3-совместимого клиента.
// S3 является опциональной зависимостью — не все приложения используют его.
type StorageRepo struct {
	store *Store
}

// NewStorageRepo создаёт новый репозиторий хранилища.
func NewStorageRepo(store *Store) *StorageRepo {
	return &StorageRepo{store: store}
}

// SetStorage устанавливает S3 клиент.
//
// Если client == nil — очищает S3 из состояния.
// Thread-safe: делегирует в Store.Set/Delete.
func (r *StorageRepo) SetStorage(client *s3storage.Client) error {
	if client == nil {
		// Игнорируем ошибку "key not found" при удалении
		_ = r.store.Delete(KeyStorage)
		return nil
	}
	return r.store.Set(KeyStorage, client)
}

// GetStorage возвращает S3 клиент.
//
// Возвращает nil если S3 не установлен.
// Thread-safe: делегирует в Store.Get.
func (r *StorageRepo) GetStorage() *s3storage.Client {
	val, ok := r.store.Get(KeyStorage)
	if !ok {
		return nil
	}

	client, ok := val.(*s3storage.Client)
	if !ok {
		return nil
	}
	return client
}

// HasStorage проверяет наличие S3 клиента.
//
// Thread-safe: делегирует в Store.Exists.
func (r *StorageRepo) HasStorage() bool {
	return r.store.Exists(KeyStorage)
}
