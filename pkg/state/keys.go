// Package state предоставляет константы ключей для унифицированного хранилища.
//
// Ключи используются для доступа к данным в CoreState.store map[string]any.
// Все ключи определены как константы для избежания опечаток и обеспечения type-safety.
//
// Rule 10: Все публичные константы имеют godoc комментарии.
package state

// Ключи для унифицированного хранилища CoreState.store
//
// Используются в методах Get/Set/Update для доступа к конкретным данным.
const (
	// KeyHistory — ключ для хранения истории диалога ([]llm.Message)
	//
	// Содержит хронологию сообщений между пользователем и агентом.
	// Используется в MessageRepository интерфейсе.
	//
	// Пример:
	//   history := state.Get(KeyHistory).([]llm.Message)
	KeyHistory = "history"

	// KeyFiles — ключ для хранения файлов текущей сессии (map[string][]*FileMeta)
	//
	// "Рабочая память" агента — файлы и результаты их анализа.
	// Ключ map — тег файла ("sketch", "plm_data", etc).
	// Используется в FileRepository интерфейсе.
	//
	// Пример:
	//   files := state.Get(KeyFiles).(map[string][]*s3storage.FileMeta)
	KeyFiles = "files"

	// KeyTodo — ключ для хранения менеджера задач (*todo.Manager)
	//
	// Планировщик многошаговых задач агента.
	// Используется в TodoRepository интерфейсе.
	//
	// Пример:
	//   manager := state.Get(KeyTodo).(*todo.Manager)
	KeyTodo = "todo"

	// KeyDictionaries — ключ для хранения справочников маркетплейсов (*wb.Dictionaries)
	//
	// Данные Wildberries (цвета, страны, полы, сезоны, НДС).
	// Framework: e-commerce бизнес-логика, переиспользуемая.
	// Используется в DictionaryRepository интерфейсе.
	//
	// Пример:
	//   dicts := state.Get(KeyDictionaries).(*wb.Dictionaries)
	KeyDictionaries = "dictionaries"

	// KeyStorage — ключ для хранения S3 клиента (*s3storage.Client)
	//
	// S3-совместимый клиент для загрузки данных.
	// Является опциональной зависимостью.
	// Используется в StorageRepository интерфейсе.
	//
	// Пример:
	//   client := state.Get(KeyStorage).(*s3storage.Client)
	KeyStorage = "storage"

	// KeyToolsRegistry — ключ для хранения реестра инструментов (*tools.Registry)
	//
	// Реестр зарегистрированных инструментов агента.
	// Используется для вызова инструментов через Registry.
	//
	// Пример:
	//   registry := state.Get(KeyToolsRegistry).(*tools.Registry)
	KeyToolsRegistry = "tools_registry"
)

// ReservedKeys возвращает список зарезервированных ключей.
//
// Вспомогательная функция для валидации — проверяет что пользователь
// не пытается использовать системный ключ для своих данных.
//
// Пример использования:
//   if contains(ReservedKeys(), userKey) {
//       return error("key is reserved")
//   }
func ReservedKeys() []string {
	return []string{
		KeyHistory,
		KeyFiles,
		KeyTodo,
		KeyDictionaries,
		KeyStorage,
		KeyToolsRegistry,
	}
}

// IsReservedKey проверяет что ключ является зарезервированным.
//
// Возвращает true если ключ используется системой.
//
// Rule 7: Возвращает bool вместо panic при несуществующем ключе.
func IsReservedKey(key string) bool {
	for _, reserved := range ReservedKeys() {
		if key == reserved {
			return true
		}
	}
	return false
}
