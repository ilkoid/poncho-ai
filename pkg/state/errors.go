// Package state предоставляет ошибки для работы с репозиториями состояния.
//
// Все ошибки следуют принципам из dev_manifest.md:
//   - Rule 7: Возвращаются вверх по стеку, никаких panic
//   - Явные типы ошибок для обработки на верхних уровнях
//   - Поддержка errors.Is() и errors.As() для error wrapping
//
// Rule 10: Все публичные типы имеют godoc комментарии.
package state

import "fmt"

// Ошибки репозиториев.

// ErrKeyNotFound возвращается когда ключ не найден в хранилище.
//
// Используется в методах Get, Delete.
//
// Пример использования:
//   val, ok := state.Get(KeyHistory)
//   if !ok {
//       return fmt.Errorf("%w: %s", ErrKeyNotFound, KeyHistory)
//   }
var ErrKeyNotFound = fmt.Errorf("key not found")

// ErrKeyReserved возвращается когда пытается использовать зарезервированный ключ.
//
// Зарезервированные ключи используются системой (history, files, todo, etc).
// Пользователь не может перезаписать их через Set().
//
// Пример использования:
//   if IsReservedKey(key) {
//       return fmt.Errorf("%w: %s", ErrKeyReserved, key)
//   }
var ErrKeyReserved = fmt.Errorf("key is reserved")

// ErrInvalidType возвращается когда тип значения не соответствует ожидаемому.
//
// Происходит при type assertion когда значение в store имеет другой тип.
//
// Пример использования:
//   history, ok := val.([]llm.Message)
//   if !ok {
//       return fmt.Errorf("%w: expected []llm.Message, got %T", ErrInvalidType, val)
//   }
var ErrInvalidType = fmt.Errorf("invalid type")

// ErrNilManager возвращается когда менеджер не инициализирован.
//
// Используется в TodoRepository когда пытается вызвать методы
// на несуществующем todo.Manager.
//
// Пример использования:
//   if manager == nil {
//       return fmt.Errorf("%w: todo manager", ErrNilManager)
//   }
var ErrNilManager = fmt.Errorf("manager not initialized")

// ErrNilStorage возвращается когда S3 клиент не установлен.
//
// Используется в StorageRepository когда пытается получить
// несуществующий S3 клиент.
//
// Пример использования:
//   if !s.HasStorage() {
//       return fmt.Errorf("%w: S3 client not available", ErrNilStorage)
//   }
var ErrNilStorage = fmt.Errorf("storage not initialized")

// ErrNotFound возвращается когда сущность не найдена.
//
// Универсальная ошибка для ситуаций когда конкретная сущность
// (задача, файл, словарь) не существует.
//
// Пример использования:
//   if task == nil {
//       return fmt.Errorf("%w: task with id %d", ErrNotFound, id)
//   }
var ErrNotFound = fmt.Errorf("entity not found")

// ErrAlreadyCompleted возвращается когда операция невозможна.
//
// Используется когда пытается завершить уже завершенную задачу
// или выполнить другую недопустимую операцию.
//
// Пример использования:
//   if task.Status == StatusDone {
//       return fmt.Errorf("%w: task %d is already done", ErrAlreadyCompleted, id)
//   }
var ErrAlreadyCompleted = fmt.Errorf("already completed")

// Ошибки с контекстом.

// KeyNotFoundError — ошибка с контекстом ключа.
//
// Предоставляет информацию о каком именно ключе идет речь.
// Поддерживает errors.Is() для проверки.
type KeyNotFoundError struct {
	Key string
}

func (e *KeyNotFoundError) Error() string {
	return fmt.Sprintf("key not found: %s", e.Key)
}

// Is проверяет что ошибка является ErrKeyNotFound.
//
// Реализует интерфейс для errors.Is().
func (e *KeyNotFoundError) Is(target error) bool {
	return target == ErrKeyNotFound
}

// InvalidTypeError — ошибка с контекстом типа.
//
// Предоставляет информацию о ожидаемом и фактическом типе.
type InvalidTypeError struct {
	Key          string
	ExpectedType string
	ActualType   string
}

func (e *InvalidTypeError) Error() string {
	return fmt.Sprintf("invalid type for key %s: expected %s, got %s",
		e.Key, e.ExpectedType, e.ActualType)
}

// Is проверяет что ошибка является ErrInvalidType.
func (e *InvalidTypeError) Is(target error) bool {
	return target == ErrInvalidType
}

// ManagerNotFoundError — ошибка с контекстом менеджера.
//
// Используется когда конкретный менеджер не найден.
type ManagerNotFoundError struct {
	ManagerType string // "todo", "storage", etc
}

func (e *ManagerNotFoundError) Error() string {
	return fmt.Sprintf("manager not initialized: %s", e.ManagerType)
}

// Is проверяет что ошибка является ErrNilManager.
func (e *ManagerNotFoundError) Is(target error) bool {
	return target == ErrNilManager
}

// Вспомогательные функции для работы с ошибками.

// IsKeyNotFound проверяет что ошибка связана с отсутствующим ключом.
//
// Удобная обертка над errors.Is().
//
// Пример использования:
//   if err != nil && IsKeyNotFound(err) {
//       // Создаем новую запись
//   }
func IsKeyNotFound(err error) bool {
	return err != nil && (err == ErrKeyNotFound ||
		fmt.Errorf("%w", ErrKeyNotFound).Error() == err.Error())
}

// IsReservedKey проверяет что ошибка связана с зарезервированным ключом.
//
// Удобная обертка над errors.Is().
func IsReservedKeyError(err error) bool {
	return err != nil && err == ErrKeyReserved
}

// IsInvalidType проверяет что ошибка связана с неверным типом.
//
// Удобная обертка над errors.Is().
func IsInvalidTypeError(err error) bool {
	return err != nil && err == ErrInvalidType
}

// WrapKeyNotFound оборачивает ошибку с контекстом ключа.
//
// Возвращает ошибку которая поддерживает errors.Is() с ErrKeyNotFound.
//
// Пример использования:
//   return WrapKeyNotFound(KeyHistory)
func WrapKeyNotFound(key string) error {
	return &KeyNotFoundError{Key: key}
}

// WrapInvalidType оборачивает ошибку с контекстом типа.
//
// Возвращает ошибку которая поддерживает errors.Is() с ErrInvalidType.
//
// Пример использования:
//   return WrapInvalidType(KeyHistory, "[]llm.Message", fmt.Sprintf("%T", val))
func WrapInvalidType(key, expectedType, actualType string) error {
	return &InvalidTypeError{
		Key:          key,
		ExpectedType: expectedType,
		ActualType:   actualType,
	}
}

// WrapManagerNotFound оборачивает ошибку с контекстом менеджера.
//
// Возвращает ошибку которая поддерживает errors.Is() с ErrNilManager.
//
// Пример использования:
//   return WrapManagerNotFound("todo")
func WrapManagerNotFound(managerType string) error {
	return &ManagerNotFoundError{ManagerType: managerType}
}
