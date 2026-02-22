// Package state предоставляет CoreState — композицию Store + Domain Repositories.
//
// CoreState является главным entry point для работы с состоянием AI-агента.
// Внутренне делегирует операции в специализированные репозитории.
//
// Соблюдение правил из dev_manifest.md:
//   - Rule 5: Thread-safe через Store.mu
//   - Rule 6: Library code — Config единственная внешняя зависимость
//   - Rule 7: Все ошибки возвращаются, никаких panic в бизнес-логике
//   - Rule 10: Все public API имеют godoc комментарии
package state

import (
	"sync"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/llm"
	"github.com/ilkoid/poncho-ai/pkg/s3storage"
	"github.com/ilkoid/poncho-ai/pkg/todo"
	"github.com/ilkoid/poncho-ai/pkg/tools"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// CoreState — композиция Store + Domain Repositories.
//
// ARCHITECTURE (Refactored 2026-02-21):
//
// CoreState делегирует операции в специализированные репозитории:
// - Store — чистое thread-safe хранилище (0 внешних зависимостей)
// - MessageRepo — история сообщений
// - FileRepo — файлы с результатами анализа
// - TodoRepo — планировщик задач
// - DictionaryRepo — справочники маркетплейсов
// - StorageRepo — S3 клиент
// - ToolsRepo — реестр инструментов
// - ContextBuilder — построение контекста LLM
//
// Преимущества новой архитектуры:
// - SRP: Каждый репозиторий отвечает за одну область
// - OCP: Можно добавлять новые репозитории без изменения CoreState
// - ISP: Клиенты могут зависеть только от нужных интерфейсов
// - DIP: CoreState зависит от абстракций (интерфейсов)
//
// Rule 5: Все изменения runtime полей защищены через Store.
// Rule 6: Не зависит от internal/, готов к переиспользованию.
type CoreState struct {
	// Config - конфигурация приложения (Rule 2: YAML with ENV support)
	Config *config.AppConfig

	// Store — чистое thread-safe хранилище
	// Все репозитории используют его для хранения данных
	Store *Store

	// Domain repositories (lazy initialization)
	// Создаются по требованию для экономии памяти
	messages   *MessageRepo
	files      *FileRepo
	todos      *TodoRepo
	dicts      *DictionaryRepo
	storage    *StorageRepo
	tools      *ToolsRepo
	context    *ContextBuilderImpl

	// mu защищает lazy initialization репозиториев
	mu sync.RWMutex
}

// NewCoreState создает новое thread-safe core состояние.
//
// Создаёт Store и конфигурирует CoreState для использования.
// Репозитории создаются лениво при первом обращении.
//
// Rule 5: Возвращает готовую к использованию thread-safe структуру.
// Rule 7: Никаких panic при nil конфигурации - валидация делегируется вызывающему.
func NewCoreState(cfg *config.AppConfig) *CoreState {
	store := NewStore()
	return &CoreState{
		Config: cfg,
		Store:  store,
	}
}

// ============================================================
// Repository Accessors (Lazy Initialization)
// ============================================================

// Messages возвращает MessageRepository.
//
// Repository создаётся при первом вызове (lazy initialization).
// Thread-safe: создание защищено мьютексом.
func (s *CoreState) Messages() MessageRepository {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.messages == nil {
		s.messages = NewMessageRepo(s.Store)
	}
	return s.messages
}

// Files возвращает FileRepository.
//
// Repository создаётся при первом вызове (lazy initialization).
// Thread-safe: создание защищено мьютексом.
func (s *CoreState) Files() FileRepository {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.files == nil {
		s.files = NewFileRepo(s.Store)
	}
	return s.files
}

// Todos возвращает TodoRepository.
//
// Repository создаётся при первом вызове (lazy initialization).
// Thread-safe: создание защищено мьютексом.
func (s *CoreState) Todos() TodoRepository {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.todos == nil {
		s.todos = NewTodoRepo(s.Store)
	}
	return s.todos
}

// Dictionaries возвращает DictionaryRepository.
//
// Repository создаётся при первом вызове (lazy initialization).
// Thread-safe: создание защищено мьютексом.
func (s *CoreState) Dictionaries() DictionaryRepository {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.dicts == nil {
		s.dicts = NewDictionaryRepo(s.Store)
	}
	return s.dicts
}

// Storage возвращает StorageRepository.
//
// Repository создаётся при первом вызове (lazy initialization).
// Thread-safe: создание защищено мьютексом.
func (s *CoreState) Storage() StorageRepository {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.storage == nil {
		s.storage = NewStorageRepo(s.Store)
	}
	return s.storage
}

// Tools возвращает ToolsRepository.
//
// Repository создаётся при первом вызове (lazy initialization).
// Thread-safe: создание защищено мьютексом.
func (s *CoreState) Tools() ToolsRepository {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.tools == nil {
		s.tools = NewToolsRepo(s.Store)
	}
	return s.tools
}

// ContextBuilder возвращает ContextBuilder.
//
// Builder создаётся при первом вызове (lazy initialization).
// Thread-safe: создание защищено мьютексом.
func (s *CoreState) ContextBuilder() ContextBuilder {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.context == nil {
		s.context = NewContextBuilder(s.Store)
	}
	return s.context
}

// ============================================================
// Convenience Methods (Direct Store Access)
// ============================================================

// Get возвращает значение по ключу.
//
// Делегирует в Store.Get.
// Thread-safe: чтение защищено мьютексом Store.
func (s *CoreState) Get(key Key) (any, bool) {
	return s.Store.Get(key)
}

// Update атомарно обновляет значение по ключу.
//
// Делегирует в Store.Update.
// Thread-safe: вся операция атомарна под мьютексом Store.
func (s *CoreState) Update(key Key, fn func(any) any) error {
	return s.Store.Update(key, fn)
}

// Delete удаляет значение по ключу.
//
// Делегирует в Store.Delete.
// Thread-safe: удаление защищено мьютексом Store.
func (s *CoreState) Delete(key Key) error {
	return s.Store.Delete(key)
}

// Exists проверяет существование ключа.
//
// Делегирует в Store.Exists.
// Thread-safe: чтение защищено мьютексом Store.
func (s *CoreState) Exists(key Key) bool {
	return s.Store.Exists(key)
}

// List возвращает все ключи в хранилище.
//
// Делегирует в Store.List.
// Thread-safe: чтение защищено мьютексом Store.
func (s *CoreState) List() []Key {
	return s.Store.List()
}

// ============================================================
// Backward Compatibility Layer
// ============================================================

// Append добавляет сообщение в историю.
//
// Deprecated: Use state.Messages().Append() instead.
func (s *CoreState) Append(msg llm.Message) error {
	return s.Messages().Append(msg)
}

// GetHistory возвращает копию всей истории диалога.
//
// Deprecated: Use state.Messages().GetHistory() instead.
func (s *CoreState) GetHistory() []llm.Message {
	return s.Messages().GetHistory()
}

// SetFiles сохраняет файлы для текущей сессии.
//
// Deprecated: Use state.Files().SetFiles() instead.
func (s *CoreState) SetFiles(files map[string][]*s3storage.FileMeta) error {
	return s.Files().SetFiles(files)
}

// GetFiles возвращает копию текущих файлов.
//
// Deprecated: Use state.Files().GetFiles() instead.
func (s *CoreState) GetFiles() map[string][]*s3storage.FileMeta {
	return s.Files().GetFiles()
}

// UpdateFileAnalysis сохраняет результат работы Vision модели.
//
// Deprecated: Use state.Files().UpdateFileAnalysis() instead.
func (s *CoreState) UpdateFileAnalysis(tag string, filename string, description string) error {
	return s.Files().UpdateFileAnalysis(tag, filename, description)
}

// SetCurrentArticle устанавливает текущий артикул и файлы.
//
// Deprecated: Use state.Files().SetCurrentArticle() instead.
func (s *CoreState) SetCurrentArticle(articleID string, files map[string][]*s3storage.FileMeta) error {
	return s.Files().SetCurrentArticle(articleID, files)
}

// GetCurrentArticleID возвращает ID текущего артикула.
//
// Deprecated: Use state.Files().GetCurrentArticleID() instead.
func (s *CoreState) GetCurrentArticleID() string {
	return s.Files().GetCurrentArticleID()
}

// GetCurrentArticle возвращает ID и файлы текущего артикула.
//
// Deprecated: Use state.Files().GetCurrentArticle() instead.
func (s *CoreState) GetCurrentArticle() (articleID string, files map[string][]*s3storage.FileMeta) {
	return s.Files().GetCurrentArticle()
}

// AddTask добавляет новую задачу в план.
//
// Deprecated: Use state.Todos().AddTask() instead.
func (s *CoreState) AddTask(description string, metadata ...map[string]any) (int, error) {
	return s.Todos().AddTask(description, metadata...)
}

// CompleteTask отмечает задачу как выполненную.
//
// Deprecated: Use state.Todos().CompleteTask() instead.
func (s *CoreState) CompleteTask(id int) error {
	return s.Todos().CompleteTask(id)
}

// FailTask отмечает задачу как проваленную.
//
// Deprecated: Use state.Todos().FailTask() instead.
func (s *CoreState) FailTask(id int, reason string) error {
	return s.Todos().FailTask(id, reason)
}

// GetTodoString возвращает строковое представление плана.
//
// Deprecated: Use state.Todos().GetTodoString() instead.
func (s *CoreState) GetTodoString() string {
	return s.Todos().GetTodoString()
}

// GetTodoStats возвращает статистику по задачам.
//
// Deprecated: Use state.Todos().GetTodoStats() instead.
func (s *CoreState) GetTodoStats() (pending, done, failed int) {
	return s.Todos().GetTodoStats()
}

// GetTodoManager возвращает todo.Manager для использования в tools.
//
// Deprecated: Use state.Todos().GetTodoManager() instead.
func (s *CoreState) GetTodoManager() *todo.Manager {
	return s.Todos().GetTodoManager()
}

// SetTodoManager устанавливает todo manager в CoreState.
//
// Deprecated: Use state.Todos().SetTodoManager() instead.
func (s *CoreState) SetTodoManager(manager *todo.Manager) error {
	return s.Todos().SetTodoManager(manager)
}

// SetDictionaries устанавливает справочники маркетплейсов.
//
// Deprecated: Use state.Dictionaries().SetDictionaries() instead.
func (s *CoreState) SetDictionaries(dicts *wb.Dictionaries) error {
	return s.Dictionaries().SetDictionaries(dicts)
}

// GetDictionaries возвращает справочники маркетплейсов.
//
// Deprecated: Use state.Dictionaries().GetDictionaries() instead.
func (s *CoreState) GetDictionaries() *wb.Dictionaries {
	return s.Dictionaries().GetDictionaries()
}

// SetStorage устанавливает S3 клиент.
//
// Deprecated: Use state.Storage().SetStorage() instead.
func (s *CoreState) SetStorage(client *s3storage.Client) error {
	return s.Storage().SetStorage(client)
}

// GetStorage возвращает S3 клиент.
//
// Deprecated: Use state.Storage().GetStorage() instead.
func (s *CoreState) GetStorage() *s3storage.Client {
	return s.Storage().GetStorage()
}

// HasStorage проверяет наличие S3 клиента.
//
// Deprecated: Use state.Storage().HasStorage() instead.
func (s *CoreState) HasStorage() bool {
	return s.Storage().HasStorage()
}

// SetToolsRegistry устанавливает реестр инструментов.
//
// Deprecated: Use state.Tools().SetToolsRegistry() instead.
func (s *CoreState) SetToolsRegistry(registry *tools.Registry) error {
	return s.Tools().SetToolsRegistry(registry)
}

// GetToolsRegistry возвращает реестр инструментов.
//
// Deprecated: Use state.Tools().GetToolsRegistry() instead.
func (s *CoreState) GetToolsRegistry() *tools.Registry {
	return s.Tools().GetToolsRegistry()
}

// BuildAgentContext собирает полный контекст для генеративного запроса (ReAct).
//
// Deprecated: Use state.ContextBuilder().BuildAgentContext() instead.
func (s *CoreState) BuildAgentContext(systemPrompt string) []llm.Message {
	return s.ContextBuilder().BuildAgentContext(systemPrompt)
}
