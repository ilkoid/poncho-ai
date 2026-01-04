// Package state предоставляет thread-safe core состояние для AI-агента.
//
// Новая CoreState реализует интерфейсы репозиториев с унифицированным хранилищем
// map[string]any, что позволяет:
// - Хранить любые данные без изменения структуры
// - Иметь единый source of truth для всего состояния
// - Быть независимой от конкретных типов зависимостей
// - Переиспользоваться в различных приложениях
//
// Соблюдение правил из dev_manifest.md:
//   - Rule 5: Thread-safe доступ через sync.RWMutex
//   - Rule 6: Library код готовый к переиспользованию, без зависимостей от internal/
//   - Rule 7: Все ошибки возвращаются, никаких panic в бизнес-логике
//   - Rule 10: Все public API имеют godoc комментарии
package state

import (
	"fmt"
	"strings"
	"sync"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/llm"
	"github.com/ilkoid/poncho-ai/pkg/s3storage"
	"github.com/ilkoid/poncho-ai/pkg/todo"
	"github.com/ilkoid/poncho-ai/pkg/tools"
	"github.com/ilkoid/poncho-ai/pkg/utils"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// CoreState представляет thread-safe core состояние AI-агента с унифицированным хранилищем.
//
// ARCHITECTURE (Refactored 2026-01-04):
//
// Использует map[string]any как единое хранилище для всех данных.
// Реализует 6 интерфейсов репозиториев для domain-specific операций.
//
// Преимущества:
// - Нет жестких зависимостей (S3, Dictionaries, etc — опциональны)
// - Унифицированный CRUD через UnifiedStore интерфейс
// - Type-safe методы через domain-specific интерфейсы
// - Thread-safe доступ к всем данным
// - Готов к переиспользованию в любых приложениях (Rule 6)
//
// Rule 5: Все изменения runtime полей защищены sync.RWMutex.
// Rule 6: Не зависит от internal/, готов к переиспользованию.
type CoreState struct {
	// Config - конфигурация приложения (Rule 2: YAML with ENV support)
	Config *config.AppConfig

	// mu защищает доступ к store (Rule 5: Thread-safe)
	mu sync.RWMutex

	// store - унифицированное хранилище данных
	// Ключи определены как константы в keys.go
	// Значения могут быть любого типа: []llm.Message, map[string][]*FileMeta, etc
	store map[string]any
}

// NewCoreState создает новое thread-safe core состояние.
//
// БЕЗ зависимостей — S3, Dictionaries и другие компоненты устанавливаются
// через соответствующие интерфейсы AFTER создания.
//
// Rule 5: Возвращает готовую к использованию thread-safe структуру.
// Rule 7: Никаких panic при nil конфигурации - валидация делегируется вызывающему.
func NewCoreState(cfg *config.AppConfig) *CoreState {
	return &CoreState{
		Config: cfg,
		store:  make(map[string]any),
	}
}

// ============================================================
// UnifiedStore Interface Implementation
// ============================================================

// Get возвращает значение по ключу.
//
// Возвращает (value, true) если ключ существует, (nil, false) иначе.
// Thread-safe: чтение защищено мьютексом.
func (s *CoreState) Get(key string) (any, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	val, ok := s.store[key]
	return val, ok
}

// Set сохраняет значение по ключу.
//
// Перезаписывает существующее значение или создает новое.
// Thread-safe: запись защищена мьютексом.
func (s *CoreState) Set(key string, value any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.store[key] = value
	return nil
}

// Update атомарно обновляет значение по ключу.
//
// fn получает текущее значение (или nil если ключ не существует)
// и должен вернуть новое значение.
//
// Thread-safe: вся операция атомарна под мьютексом.
func (s *CoreState) Update(key string, fn func(any) any) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	current := s.store[key]
	newValue := fn(current)

	if newValue == nil {
		// Если fn вернул nil - удаляем ключ
		delete(s.store, key)
	} else {
		s.store[key] = newValue
	}

	return nil
}

// Delete удаляет значение по ключу.
//
// Если ключ не существует — возвращает ошибку.
// Thread-safe: удаление защищено мьютексом.
func (s *CoreState) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.store[key]; !ok {
		return fmt.Errorf("key not found: %s", key)
	}

	delete(s.store, key)
	return nil
}

// Exists проверяет существование ключа.
//
// Thread-safe: чтение защищено мьютексом.
func (s *CoreState) Exists(key string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.store[key]
	return ok
}

// List возвращает все ключи в хранилище.
//
// Порядок ключей не гарантирован.
// Thread-safe: чтение защищено мьютексом.
func (s *CoreState) List() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	keys := make([]string, 0, len(s.store))
	for k := range s.store {
		keys = append(keys, k)
	}
	return keys
}

// ============================================================
// MessageRepository Interface Implementation
// ============================================================

// Append добавляет сообщение в историю.
//
// Thread-safe: добавление защищено мьютексом.
func (s *CoreState) Append(msg llm.Message) error {
	return s.Update(KeyHistory, func(val any) any {
		if val == nil {
			return []llm.Message{msg}
		}
		history := val.([]llm.Message)
		return append(history, msg)
	})
}

// GetHistory возвращает копию всей истории диалога.
//
// Возвращает копию слайса для избежания race condition при изменении.
// Thread-safe: чтение защищено мьютексом.
func (s *CoreState) GetHistory() []llm.Message {
	val, ok := s.Get(KeyHistory)
	if !ok {
		return []llm.Message{}
	}

	history := val.([]llm.Message)
	dst := make([]llm.Message, len(history))
	copy(dst, history)
	return dst
}

// ============================================================
// FileRepository Interface Implementation
// ============================================================

// SetFiles сохраняет файлы для текущей сессии.
//
// Принимает map[string][]*s3storage.FileMeta и сохраняет напрямую.
// VisionDescription заполняется позже через UpdateFileAnalysis().
//
// Thread-safe: атомарная замена всей map файлов.
func (s *CoreState) SetFiles(files map[string][]*s3storage.FileMeta) error {
	return s.Set(KeyFiles, files)
}

// GetFiles возвращает копию текущих файлов.
//
// Thread-safe: чтение защищено мьютексом.
func (s *CoreState) GetFiles() map[string][]*s3storage.FileMeta {
	val, ok := s.Get(KeyFiles)
	if !ok {
		return make(map[string][]*s3storage.FileMeta)
	}

	files := val.(map[string][]*s3storage.FileMeta)
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
// Thread-safe: атомарно заменяет объект в слайсе под мьютексом.
func (s *CoreState) UpdateFileAnalysis(tag string, filename string, description string) error {
	return s.Update(KeyFiles, func(val any) any {
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

// ============================================================
// TodoRepository Interface Implementation
// ============================================================

// AddTask добавляет новую задачу в план.
//
// Параметры:
//   - description: описание задачи
//   - metadata: опциональные метаданные (ключ-значение)
//
// Возвращает ID созданной задачи.
// Thread-safe: делегирует thread-safe Manager.Add.
func (s *CoreState) AddTask(description string, metadata ...map[string]interface{}) (int, error) {
	manager := s.getTodoManager()
	if manager == nil {
		// Создаем новый менеджер если не существует
		manager = todo.NewManager()
		if err := s.Set(KeyTodo, manager); err != nil {
			return 0, fmt.Errorf("failed to create todo manager: %w", err)
		}
	}

	id := manager.Add(description, metadata...)
	return id, nil
}

// CompleteTask отмечает задачу как выполненную.
//
// Возвращает ошибку если задача не найдена или уже завершена.
// Thread-safe: делегирует thread-safe Manager.Complete.
func (s *CoreState) CompleteTask(id int) error {
	manager := s.getTodoManager()
	if manager == nil {
		return fmt.Errorf("todo manager not initialized")
	}
	return manager.Complete(id)
}

// FailTask отмечает задачу как проваленную.
//
// Параметры:
//   - id: ID задачи
//   - reason: причина неудачи
//
// Возвращает ошибку если задача не найдена или уже завершена.
// Thread-safe: делегирует thread-safe Manager.Fail.
func (s *CoreState) FailTask(id int, reason string) error {
	manager := s.getTodoManager()
	if manager == nil {
		return fmt.Errorf("todo manager not initialized")
	}
	return manager.Fail(id, reason)
}

// GetTodoString возвращает строковое представление плана.
//
// Thread-safe: чтение защищено мьютексом.
func (s *CoreState) GetTodoString() string {
	manager := s.getTodoManager()
	if manager == nil {
		return ""
	}
	return manager.String()
}

// GetTodoStats возвращает статистику по задачам.
//
// Возвращает количество pending, done, failed задач.
// Thread-safe: чтение защищено мьютексом.
func (s *CoreState) GetTodoStats() (pending, done, failed int) {
	manager := s.getTodoManager()
	if manager == nil {
		return 0, 0, 0
	}
	return manager.GetStats()
}

// GetTodoManager возвращает todo.Manager для использования в tools.
//
// ВНИМАНИЕ: Это метод для backward compatibility с tools которые
// требуют *todo.Manager. Предпочтительно использовать TodoRepository методы.
//
// Thread-safe: чтение защищено мьютексом.
func (s *CoreState) GetTodoManager() *todo.Manager {
	val, ok := s.Get(KeyTodo)
	if !ok {
		return nil
	}
	return val.(*todo.Manager)
}

// getTodoManager — вспомогательный метод для получения todo.Manager.
//
// Возвращает nil если менеджер не инициализирован.
func (s *CoreState) getTodoManager() *todo.Manager {
	val, ok := s.Get(KeyTodo)
	if !ok {
		return nil
	}
	return val.(*todo.Manager)
}

// ============================================================
// DictionaryRepository Interface Implementation
// ============================================================

// SetDictionaries устанавливает справочники маркетплейсов.
//
// Thread-safe: изменение защищено мьютексом.
func (s *CoreState) SetDictionaries(dicts *wb.Dictionaries) error {
	return s.Set(KeyDictionaries, dicts)
}

// GetDictionaries возвращает справочники маркетплейсов.
//
// Thread-safe: чтение защищено мьютексом.
func (s *CoreState) GetDictionaries() *wb.Dictionaries {
	val, ok := s.Get(KeyDictionaries)
	if !ok {
		return nil
	}
	return val.(*wb.Dictionaries)
}

// ============================================================
// StorageRepository Interface Implementation
// ============================================================

// SetStorage устанавливает S3 клиент.
//
// Если client == nil — очищает S3 из состояния.
// Thread-safe: изменение защищено мьютексом.
func (s *CoreState) SetStorage(client *s3storage.Client) error {
	if client == nil {
		return s.Delete(KeyStorage)
	}
	return s.Set(KeyStorage, client)
}

// GetStorage возвращает S3 клиент.
//
// Возвращает nil если S3 не установлен.
// Thread-safe: чтение защищено мьютексом.
func (s *CoreState) GetStorage() *s3storage.Client {
	val, ok := s.Get(KeyStorage)
	if !ok {
		return nil
	}
	return val.(*s3storage.Client)
}

// HasStorage проверяет наличие S3 клиента.
//
// Thread-safe: чтение защищено мьютексом.
func (s *CoreState) HasStorage() bool {
	return s.Exists(KeyStorage)
}

// ============================================================
// Helper Methods (backward compatibility)
// ============================================================

// SetToolsRegistry устанавливает реестр инструментов.
//
// Thread-safe метод для инициализации registry после регистрации инструментов.
//
// Rule 3: Все инструменты регистрируются через Registry.Register().
// Rule 5: Thread-safe доступ к полям структуры.
func (s *CoreState) SetToolsRegistry(registry *tools.Registry) error {
	return s.Set(KeyToolsRegistry, registry)
}

// GetToolsRegistry возвращает реестр инструментов.
//
// Thread-safe метод для получения registry для использования в агенте.
//
// Rule 3: Все инструменты вызываются через Registry.
// Rule 5: Thread-safe доступ к полям структуры.
func (s *CoreState) GetToolsRegistry() *tools.Registry {
	val, ok := s.Get(KeyToolsRegistry)
	if !ok {
		return nil
	}
	return val.(*tools.Registry)
}

// ============================================================
// Context Building
// ============================================================

// BuildAgentContext собирает полный контекст для генеративного запроса (ReAct).
//
// Объединяет:
// 1. Системный промпт
// 2. "Рабочую память" (результаты анализа файлов из VisionDescription)
// 3. Контекст плана (Todo Manager)
// 4. Историю диалога
//
// Возвращаемый массив сообщений готов для передачи в LLM.
//
// "Working Memory" паттерн: Результаты анализа vision моделей хранятся в
// FileMeta.VisionDescription и инжектируются в контекст без повторной
// отправки изображений (экономия токенов).
//
// Thread-safe: читает Files и History под защитой мьютекса.
//
// Rule 5: Thread-safe доступ к полям.
// Rule 7: Возвращает корректный массив даже при пустых данных.
func (s *CoreState) BuildAgentContext(systemPrompt string) []llm.Message {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// 1. Формируем блок знаний из проанализированных файлов
	var visualContext string
	files, hasFiles := s.store[KeyFiles]
	if hasFiles {
		filesMap := files.(map[string][]*s3storage.FileMeta)
		for tag, filesList := range filesMap {
			for _, f := range filesList {
				if f.VisionDescription != "" {
					visualContext += fmt.Sprintf("- Файл [%s] %s: %s\n", tag, f.Filename, f.VisionDescription)
				}
			}
		}
	}

	knowledgeMsg := ""
	if visualContext != "" {
		knowledgeMsg = fmt.Sprintf("\nКОНТЕКСТ АРТИКУЛА (Результаты анализа файлов):\n%s", visualContext)
	}

	// 2. Формируем контекст плана
	var todoContext string
	todoVal, hasTodo := s.store[KeyTodo]
	if hasTodo {
		manager := todoVal.(*todo.Manager)
		todoContext = manager.String()
	}

	// 3. Получаем историю
	var history []llm.Message
	historyVal, hasHistory := s.store[KeyHistory]
	if hasHistory {
		history = historyVal.([]llm.Message)
	} else {
		history = make([]llm.Message, 0)
	}

	// 4. Собираем итоговый массив сообщений
	messages := make([]llm.Message, 0, len(history)+3)

	// Системное сообщение с инъекцией знаний
	messages = append(messages, llm.Message{
		Role:    llm.RoleSystem,
		Content: systemPrompt + knowledgeMsg,
	})

	// Добавляем контекст плана
	if todoContext != "" && !strings.Contains(todoContext, "Нет активных задач") {
		messages = append(messages, llm.Message{
			Role:    llm.RoleSystem,
			Content: todoContext,
		})
	}

	// Добавляем историю переписки
	messages = append(messages, history...)

	return messages
}

// ============================================================
// Compile-time Interface Compliance Checks
// ============================================================

// Проверки вызовут ошибку компиляции если CoreState не реализует интерфейсы.
//
// Rule 10: Interface compliance проверки должны быть в коде.
var (
	_ UnifiedStore        = (*CoreState)(nil) // ✅ CoreState implements UnifiedStore
	_ MessageRepository   = (*CoreState)(nil) // ✅ CoreState implements MessageRepository
	_ FileRepository      = (*CoreState)(nil) // ✅ CoreState implements FileRepository
	_ TodoRepository      = (*CoreState)(nil) // ✅ CoreState implements TodoRepository
	_ DictionaryRepository = (*CoreState)(nil) // ✅ CoreState implements DictionaryRepository
	_ StorageRepository   = (*CoreState)(nil) // ✅ CoreState implements StorageRepository
)
