// Package state предоставляет константы ключей для унифицированного хранилища.
//
// Ключи используются для доступа к данным в CoreState.store map[string]any.
// Все ключи определены как константы для избежания опечаток и обеспечения type-safety.
//
// Rule 10: Все публичные константы имеют godoc комментарии.
package state

// Key — типизированный ключ для type-safe операций с CoreState.
//
// Используется с generic методами Get[T]/Set[T]/Update[T] для обеспечения
// compile-time проверки типов.
//
// Пример:
//   dicts, ok := coreState.Get[*wb.Dictionaries](state.KeyDictionaries)
type Key string

// String возвращает строковое представление ключа.
//
// Используется для обратной совместимости с кодом, ожидающим string.
func (k Key) String() string {
	return string(k)
}

// Ключи для унифицированного хранилища CoreState.store
//
// Используются в методах Get[T]/Set[T]/Update[T] для доступа к конкретным данным.
const (
	// KeyHistory — ключ для хранения истории диалога ([]llm.Message)
	//
	// Содержит хронологию сообщений между пользователем и агентом.
	// Используется в MessageRepository интерфейсе.
	//
	// Пример:
	//   history, ok := coreState.Get[[]llm.Message](state.KeyHistory)
	KeyHistory Key = "history"

	// KeyFiles — ключ для хранения файлов текущей сессии (map[string][]*FileMeta)
	//
	// "Рабочая память" агента — файлы и результаты их анализа.
	// Ключ map — тег файла ("sketch", "plm_data", etc).
	// Используется в FileRepository интерфейсе.
	//
	// Пример:
	//   files, ok := coreState.Get[map[string][]*s3storage.FileMeta](state.KeyFiles)
	KeyFiles Key = "files"

	// KeyCurrentArticle — ключ для хранения ID текущего артикула (string)
	//
	// ID текущего артикула для e-commerce workflow.
	// Используется в tandem с KeyFiles для отслеживания контекста.
	//
	// Пример:
	//   articleID, ok := coreState.Get[string](state.KeyCurrentArticle)
	KeyCurrentArticle Key = "current_article"

	// KeyTodo — ключ для хранения менеджера задач (*todo.Manager)
	//
	// Планировщик многошаговых задач агента.
	// Используется в TodoRepository интерфейсе.
	//
	// Пример:
	//   manager, ok := coreState.Get[*todo.Manager](state.KeyTodo)
	KeyTodo Key = "todo"

	// KeyDictionaries — ключ для хранения справочников маркетплейсов (*wb.Dictionaries)
	//
	// Данные Wildberries (цвета, страны, полы, сезоны, НДС).
	// Framework: e-commerce бизнес-логика, переиспользуемая.
	// Используется в DictionaryRepository интерфейсе.
	//
	// Пример:
	//   dicts, ok := coreState.Get[*wb.Dictionaries](state.KeyDictionaries)
	KeyDictionaries Key = "dictionaries"

	// KeyStorage — ключ для хранения S3 клиента (*s3storage.Client)
	//
	// S3-совместимый клиент для загрузки данных.
	// Является опциональной зависимостью.
	// Используется в StorageRepository интерфейсе.
	//
	// Пример:
	//   client, ok := coreState.Get[*s3storage.Client](state.KeyStorage)
	KeyStorage Key = "storage"

	// KeyToolsRegistry — ключ для хранения реестра инструментов (*tools.Registry)
	//
	// Реестр зарегистрированных инструментов агента.
	// Используется для вызова инструментов через Registry.
	//
	// Пример:
	//   registry, ok := coreState.Get[*tools.Registry](state.KeyToolsRegistry)
	KeyToolsRegistry Key = "tools_registry"
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
func ReservedKeys() []Key {
	return []Key{
		KeyHistory,
		KeyFiles,
		KeyCurrentArticle,
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
func IsReservedKey(key Key) bool {
	for _, reserved := range ReservedKeys() {
		if key == reserved {
			return true
		}
	}
	return false
}
