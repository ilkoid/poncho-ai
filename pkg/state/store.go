// Package state предоставляет thread-safe хранилище для AI-агента.
//
// Store — чистое key-value хранилище без domain-зависимостей.
// Используется как основа для domain-specific репозиториев.
//
// Соблюдение правил из dev_manifest.md:
//   - Rule 5: Thread-safe доступ через sync.RWMutex
//   - Rule 6: Library код без внешних зависимостей (только fmt, sync)
//   - Rule 7: Все ошибки возвращаются, никаких panic
package state

import (
	"fmt"
	"sync"
)

// Store — thread-safe generic key-value хранилище.
//
// Чистый library code без domain зависимостей.
// Использует Key тип для type-safe операций.
//
// Rule 5: Все операции thread-safe через sync.RWMutex.
// Rule 6: Только стандартная библиотека (fmt, sync).
type Store struct {
	mu    sync.RWMutex
	store map[string]any
}

// NewStore создаёт новое хранилище.
func NewStore() *Store {
	return &Store{store: make(map[string]any)}
}

// Get возвращает значение по ключу.
//
// Возвращает (value, true) если ключ существует, (nil, false) иначе.
// Thread-safe: чтение защищено RLock.
func (s *Store) Get(key Key) (any, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	val, ok := s.store[string(key)]
	return val, ok
}

// Set сохраняет значение по ключу.
//
// Перезаписывает существующее значение или создает новое.
// Thread-safe: запись защищена Lock.
func (s *Store) Set(key Key, value any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.store[string(key)] = value
	return nil
}

// Update атомарно обновляет значение по ключу.
//
// fn получает текущее значение (или nil если ключ не существует)
// и должен вернуть новое значение.
//
// Если fn возвращает nil — ключ удаляется из хранилища.
// Thread-safe: вся операция атомарна под Lock.
func (s *Store) Update(key Key, fn func(any) any) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	keyStr := string(key)
	current := s.store[keyStr]
	newValue := fn(current)

	if newValue == nil {
		delete(s.store, keyStr)
	} else {
		s.store[keyStr] = newValue
	}

	return nil
}

// Delete удаляет значение по ключу.
//
// Возвращает ошибку если ключ не существует.
// Thread-safe: удаление защищено Lock.
func (s *Store) Delete(key Key) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	keyStr := string(key)
	if _, ok := s.store[keyStr]; !ok {
		return fmt.Errorf("key not found: %s", keyStr)
	}

	delete(s.store, keyStr)
	return nil
}

// Exists проверяет существование ключа.
//
// Thread-safe: чтение защищено RLock.
func (s *Store) Exists(key Key) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.store[string(key)]
	return ok
}

// List возвращает все ключи в хранилище.
//
// Порядок ключей не гарантирован.
// Thread-safe: чтение защищено RLock.
func (s *Store) List() []Key {
	s.mu.RLock()
	defer s.mu.RUnlock()

	keys := make([]Key, 0, len(s.store))
	for k := range s.store {
		keys = append(keys, Key(k))
	}
	return keys
}

// Len возвращает количество элементов в хранилище.
//
// Thread-safe: чтение защищено RLock.
func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.store)
}

// Clear очищает хранилище.
//
// Thread-safe: операция защищена Lock.
func (s *Store) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.store = make(map[string]any)
}
