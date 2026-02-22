// Package state предоставляет generic type-safe функции для работы со Store.
//
// Generic функции обеспечивают compile-time проверку типов при работе с Store.
// Используют Go 1.18+ generics для type-safe доступа к значениям.
//
// Примеры:
//
//	dicts, ok := state.GetType[*wb.Dictionaries](store, state.KeyDictionaries)
//	state.SetType[*wb.Dictionaries](store, state.KeyDictionaries, dicts)
package state

// GetType обеспечивает type-safe доступ к значениям Store.
//
// Generic параметр T - тип возвращаемого значения.
// Возвращает (value, true) если ключ существует и тип совпадает, (zero, false) иначе.
// Thread-safe: чтение защищено мьютексом Store.
//
// Пример:
//
//	dicts, ok := state.GetType[*wb.Dictionaries](store, state.KeyDictionaries)
func GetType[T any](s *Store, key Key) (T, bool) {
	val, ok := s.Get(key)
	if !ok {
		var zero T
		return zero, false
	}

	typed, ok := val.(T)
	if !ok {
		var zero T
		return zero, false
	}

	return typed, true
}

// SetType обеспечивает type-safe сохранение значений в Store.
//
// Generic параметр T - тип сохраняемого значения.
// Перезаписывает существующее значение или создает новое.
// Thread-safe: запись защищена мьютексом Store.
//
// Пример:
//
//	state.SetType[*wb.Dictionaries](store, state.KeyDictionaries, dicts)
func SetType[T any](s *Store, key Key, value T) error {
	return s.Set(key, value)
}

// UpdateType атомарно обновляет значение в Store с типизацией.
//
// Generic параметр T - тип значения.
// fn получает текущее значение (или zero если ключ не существует)
// и должен вернуть новое значение.
// Thread-safe: вся операция атомарна под мьютексом Store.
//
// Пример:
//
//	state.UpdateType[*todo.Manager](store, state.KeyTodo, func(m *todo.Manager) *todo.Manager {
//	    m.Add("new task")
//	    return m
//	})
func UpdateType[T any](s *Store, key Key, fn func(T) T) error {
	return s.Update(key, func(val any) any {
		var currentTyped T
		if val != nil {
			currentTyped = val.(T)
		}
		return fn(currentTyped)
	})
}

// GetTypeFromCoreState обеспечивает type-safe доступ к значениям через CoreState.
//
// Удобная обёртка для работы напрямую с CoreState вместо Store.
// Возвращает (value, true) если ключ существует и тип совпадает, (zero, false) иначе.
// Thread-safe: чтение защищено мьютексом.
//
// Пример:
//
//	dicts, ok := state.GetTypeFromCoreState[*wb.Dictionaries](coreState, state.KeyDictionaries)
func GetTypeFromCoreState[T any](s *CoreState, key Key) (T, bool) {
	return GetType[T](s.Store, key)
}

// SetTypeToCoreState обеспечивает type-safe сохранение значений через CoreState.
//
// Удобная обёртка для работы напрямую с CoreState вместо Store.
// Thread-safe: запись защищена мьютексом.
//
// Пример:
//
//	state.SetTypeToCoreState[*wb.Dictionaries](coreState, state.KeyDictionaries, dicts)
func SetTypeToCoreState[T any](s *CoreState, key Key, value T) error {
	return SetType[T](s.Store, key, value)
}
