// Package state предоставляет интерфейсы репозиториев для унифицированного
// управления состоянием AI-агента.
//
// Интерфейсы следуют паттерну Repository с единым базовым CRUD (UnifiedStore)
// и domain-specific расширениями для каждого типа данных.
//
// Thread-safety: Все реализации должны гарантировать thread-safe доступ
// через sync.RWMutex или аналогичные механизмы.
//
// Rule 10: Все публичные API имеют godoc комментарии.
// Rule 7: Все ошибки возвращаются, никаких panic.
// Rule 11: Все долгосрочные операции принимают context.Context.
package state

import (
	"github.com/ilkoid/poncho-ai/pkg/llm"
	"github.com/ilkoid/poncho-ai/pkg/s3storage"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// UnifiedStore — базовый интерфейс для всех репозиториев.
//
// Предоставляет унифицированный CRUD API для работы с произвольными данными,
// хранящимися в состоянии. Все domain-specific интерфейсы расширяют этот базовый.
//
// Thread-safety: Все методы должны быть thread-safe.
// Rule 7: Все методы возвращают ошибку вместо panic.
type UnifiedStore interface {
	// Get возвращает значение по ключу.
	//
	// Возвращает (value, true) если ключ существует, (nil, false) иначе.
	// Thread-safe: чтение защищено мьютексом.
	Get(key string) (any, bool)

	// Set сохраняет значение по ключу.
	//
	// Перезаписывает существующее значение или создает новое.
	// Thread-safe: запись защищена мьютексом.
	Set(key string, value any) error

	// Update атомарно обновляет значение по ключу.
	//
	// fn получает текущее значение (или nil если ключ не существует)
	// и должен вернуть новое значение. Если fn возвращает nil — ключ удаляется.
	//
	// Пример:
	//   state.Update("counter", func(val any) any {
	//       if val == nil { return 1 }
	//       return val.(int) + 1
	//   })
	//
	// Thread-safe: вся операция атомарна под мьютексом.
	Update(key string, fn func(any) any) error

	// Delete удаляет значение по ключу.
	//
	// Если ключ не существует — возвращает ошибку.
	// Thread-safe: удаление защищено мьютексом.
	Delete(key string) error

	// Exists проверяет существование ключа.
	//
	// Thread-safe: чтение защищено мьютексом.
	Exists(key string) bool

	// List возвращает все ключи в хранилище.
	//
	// Порядок ключей не гарантирован.
	// Thread-safe: чтение защищено мьютексом.
	List() []string
}

// MessageRepository — репозиторий для истории диалога (User <-> Agent).
//
// Расширяет UnifiedStore с методами специфичными для работы с сообщениями LLM.
// Используется для хранения хронологии диалога без тяжелых base64 данных.
//
// Rule 5: Единый source of truth для истории диалога.
type MessageRepository interface {
	UnifiedStore

	// Append добавляет сообщение в историю.
	//
	// Thread-safe: добавление защищено мьютексом.
	Append(msg llm.Message) error

	// GetHistory возвращает копию всей истории диалога.
	//
	// Возвращает копию слайса для избежания race condition при изменении.
	// Thread-safe: чтение защищено мьютексом.
	GetHistory() []llm.Message
}

// FileRepository — репозиторий для "рабочей памяти" (Working Memory).
//
// Хранит файлы текущей сессии и результаты их анализа (vision descriptions).
// Использует теги для группировки файлов (например, "sketch", "plm_data").
//
// "Working Memory" паттерн: Vision модель анализирует изображение один раз,
// результат сохраняется в FileMeta.VisionDescription и переиспользуется
// в последующих вызовах LLM без повторной отправки изображения.
type FileRepository interface {
	UnifiedStore

	// SetFiles сохраняет файлы для текущей сессии.
	//
	// Принимает map[string][]*s3storage.FileMeta и сохраняет напрямую.
	// VisionDescription заполняется позже через UpdateFileAnalysis().
	//
	// Thread-safe: атомарная замена всей map файлов.
	SetFiles(files map[string][]*s3storage.FileMeta) error

	// GetFiles возвращает копию текущих файлов.
	//
	// Thread-safe: чтение защищено мьютексом.
	GetFiles() map[string][]*s3storage.FileMeta

	// UpdateFileAnalysis сохраняет результат работы Vision модели.
	//
	// Параметры:
	//   - tag: тег файла (ключ в map, например "sketch", "plm_data")
	//   - filename: имя файла для поиска в слайсе
	//   - description: результат анализа (текст от vision модели)
	//
	// Thread-safe: атомарно заменяет объект в слайсе под мьютексом.
	UpdateFileAnalysis(tag string, filename string, description string) error
}

// TodoRepository — репозиторий для управления планом задач.
//
// Предоставляет интерфейс для работы с todo.Manager через унифицированное хранилище.
// Все операции делегируют thread-safe Manager методы.
type TodoRepository interface {
	UnifiedStore

	// AddTask добавляет новую задачу в план.
	//
	// Параметры:
	//   - description: описание задачи
	//   - metadata: опциональные метаданные (ключ-значение)
	//
	// Возвращает ID созданной задачи.
	// Thread-safe: делегирует thread-safe Manager.Add.
	AddTask(description string, metadata ...map[string]interface{}) (int, error)

	// CompleteTask отмечает задачу как выполненную.
	//
	// Возвращает ошибку если задача не найдена или уже завершена.
	// Thread-safe: делегирует thread-safe Manager.Complete.
	CompleteTask(id int) error

	// FailTask отмечает задачу как проваленную.
	//
	// Параметры:
	//   - id: ID задачи
	//   - reason: причина неудачи
	//
	// Возвращает ошибку если задача не найдена или уже завершена.
	// Thread-safe: делегирует thread-safe Manager.Fail.
	FailTask(id int, reason string) error

	// GetTodoString возвращает строковое представление плана.
	//
	// Thread-safe: чтение защищено мьютексом.
	GetTodoString() string

	// GetTodoStats возвращает статистику по задачам.
	//
	// Возвращает количество pending, done, failed задач.
	// Thread-safe: чтение защищено мьютексом.
	GetTodoStats() (pending, done, failed int)
}

// DictionaryRepository — репозиторий для справочников маркетплейсов.
//
// Хранит данные Wildberries (цвета, страны, полы, сезоны, НДС).
// Framework: e-commerce бизнес-логика, переиспользуемая.
type DictionaryRepository interface {
	UnifiedStore

	// SetDictionaries устанавливает справочники маркетплейсов.
	//
	// Thread-safe: изменение защищено мьютексом.
	SetDictionaries(dicts *wb.Dictionaries) error

	// GetDictionaries возвращает справочники маркетплейсов.
	//
	// Thread-safe: чтение защищено мьютексом.
	GetDictionaries() *wb.Dictionaries
}

// StorageRepository — репозиторий для S3/файлового клиента.
//
// Предоставляет доступ к S3-совместимому хранилищу для загрузки данных.
// Является опциональной зависимостью — некоторые приложения могут не использовать S3.
//
// Thread-safety: S3 клиент сам должен быть thread-safe (обычно через HTTP client с connection pool).
type StorageRepository interface {
	UnifiedStore

	// SetStorage устанавливает S3 клиент.
	//
	// Если client == nil — очищает S3 из состояния.
	// Thread-safe: изменение защищено мьютексом.
	SetStorage(client *s3storage.Client) error

	// GetStorage возвращает S3 клиент.
	//
	// Возвращает nil если S3 не установлен.
	// Thread-safe: чтение защищено мьютексом.
	GetStorage() *s3storage.Client

	// HasStorage проверяет наличие S3 клиента.
	//
	// Thread-safe: чтение защищено мьютексом.
	HasStorage() bool
}

// Compile-time checks для обеспечения реализации интерфейсов.
//
// ПРИМЕЧАНИЕ: Проверки будут добавлены в Phase 2 после создания новой CoreState.
//
// После реализации CoreState в core.go добавить:
//
//	var (
//	    _ UnifiedStore        = (*CoreState)(nil)
//	    _ MessageRepository   = (*CoreState)(nil)
//	    _ FileRepository      = (*CoreState)(nil)
//	    _ TodoRepository      = (*CoreState)(nil)
//	    _ DictionaryRepository = (*CoreState)(nil)
//	    _ StorageRepository   = (*CoreState)(nil)
//	)
//
// Rule 10: Interface compliance проверки должны быть в коде.
// Это вызовет ошибку компиляции если CoreState не реализует все интерфейсы.
