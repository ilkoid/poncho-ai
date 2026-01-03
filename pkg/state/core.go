// Package state предоставляет thread-safe core состояние для AI-агента.
//
// CoreState содержит переиспользуемую бизнес-логику фреймворка:
// - Конфигурацию приложения
// - S3 клиент для хранения данных
// - Справочники маркетплейсов (WB, future Ozon)
// - Реестр инструментов (tools registry)
// - Менеджер задач (todo manager)
// - Историю диалога
// - "Рабочую память" (файлы и результаты их анализа)
//
// Package state следует правилам из dev_manifest.md:
//   - Rule 5: Thread-safe доступ через sync.RWMutex, никаких глобальных переменных
//   - Rule 6: Library code готовый к переиспользованию, без зависимостей от internal/
//   - Rule 7: Все ошибки возвращаются, никаких panic в бизнес-логике
//   - Rule 10: Все public API имеют godoc комментарии
package state

import (
	"fmt"
	"sync"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/llm"
	"github.com/ilkoid/poncho-ai/pkg/s3storage"
	"github.com/ilkoid/poncho-ai/pkg/todo"
	"github.com/ilkoid/poncho-ai/pkg/tools"
	"github.com/ilkoid/poncho-ai/pkg/utils"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// CoreState представляет thread-safe core состояние AI-агента.
//
// Содержит переиспользуемую бизнес-логику e-commerce фреймворка.
// Может использоваться в различных приложениях: CLI, TUI, HTTP API, gRPC.
//
// Rule 5: Все изменения runtime полей (History, Files) защищены мьютексом.
// Rule 6: Не зависит от internal/, готов к переиспользованию.
type CoreState struct {
	// Config - конфигурация приложения (Rule 2: YAML with ENV support)
	Config *config.AppConfig

	// S3 - S3-совместимый клиент для хранения данных
	// Используется для загрузки PLM данных, эскизов, изображений
	S3 *s3storage.Client

	// Dictionaries - справочники маркетплейсов
	// Содержит данные WB (цвета, страны, полы, сезоны, НДС)
	// Framework: e-commerce бизнес-логика, переиспользуемая
	Dictionaries *wb.Dictionaries

	// Todo - менеджер задач для планирования
	// Используется агентом для отслеживания многошаговых задач
	Todo *todo.Manager

	// ToolsRegistry - реестр инструментов (Rule 3)
	// Все инструменты регистрируются через Registry.Register()
	ToolsRegistry *tools.Registry

	// mu защищает доступ к History и Files (Rule 5: Thread-safe)
	mu sync.RWMutex

	// History - хронология диалога (User <-> Agent)
	// Сюда НЕ попадают тяжелые base64, только текст и tool calls
	History []llm.Message

	// Files - "Рабочая память" (Working Memory)
	// Хранит файлы текущего артикула и результаты их анализа
	// Ключ: тег (например, "sketch", "plm_data")
	// Использует s3storage.FileMeta как единый тип для метаданных
	Files map[string][]*s3storage.FileMeta
}

// NewCoreState создает новое thread-safe core состояние.
//
// Инициализирует базовую структуру с пустой историей и файлами.
// Dictionaries и ToolsRegistry должны быть установлены после создания.
//
// Rule 5: Возвращает готовую к использованию thread-safe структуру.
// Rule 7: Никаких panic при nil конфигурации - валидация делегируется вызывающему.
func NewCoreState(cfg *config.AppConfig, s3Client *s3storage.Client) *CoreState {
	return &CoreState{
		Config: cfg,
		S3:     s3Client,
		Todo:   todo.NewManager(),
		// ToolsRegistry и Dictionaries устанавливаются после создания
		Files:   make(map[string][]*s3storage.FileMeta),
		History: make([]llm.Message, 0),
	}
}

// SetDictionaries устанавливает справочники маркетплейсов.
//
// Thread-safe метод для инициализации словарей после загрузки из API.
//
// Rule 5: Thread-safe доступ к полям структуры.
func (s *CoreState) SetDictionaries(dicts *wb.Dictionaries) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Dictionaries = dicts
}

// GetDictionaries возвращает справочники маркетплейсов.
//
// Thread-safe метод для чтения словарей.
//
// Rule 5: Thread-safe доступ к полям структуры.
func (s *CoreState) GetDictionaries() *wb.Dictionaries {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Dictionaries
}

// GetS3 возвращает S3 клиент.
//
// Thread-safe метод для чтения S3 клиента.
//
// Rule 5: Thread-safe доступ к полям структуры.
func (s *CoreState) GetS3() *s3storage.Client {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.S3
}

// GetTodo возвращает менеджер задач.
//
// Thread-safe метод для чтения Todo менеджера.
//
// Rule 5: Thread-safe доступ к полям структуры.
func (s *CoreState) GetTodo() *todo.Manager {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Todo
}

// SetToolsRegistry устанавливает реестр инструментов.
//
// Thread-safe метод для инициализации registry после регистрации инструментов.
//
// Rule 3: Все инструменты регистрируются через Registry.Register().
// Rule 5: Thread-safe доступ к полям структуры.
func (s *CoreState) SetToolsRegistry(registry *tools.Registry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ToolsRegistry = registry
}

// GetToolsRegistry возвращает реестр инструментов.
//
// Thread-safe метод для получения registry для использования в агенте.
//
// Rule 3: Все инструменты вызываются через Registry.
// Rule 5: Thread-safe доступ к полям структуры.
func (s *CoreState) GetToolsRegistry() *tools.Registry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ToolsRegistry
}

// --- Thread-Safe History Methods (Rule 5) ---

// AppendMessage безопасно добавляет сообщение в историю диалога.
//
// Thread-safe: защищен мьютексом от конкурентной записи.
// Используется агентом для сохранения вопросов пользователя и ответов.
//
// Rule 5: Thread-safe доступ к History.
// Rule 7: Никаких panic при некорректных данных.
func (s *CoreState) AppendMessage(msg llm.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.History = append(s.History, msg)
}

// GetHistory возвращает копию истории диалога.
//
// Thread-safe: защищен мьютексом от конкурентного чтения/записи.
// Возвращает копию слайса, чтобы избежать race condition при изменении.
//
// Используется для:
// - Рендера в UI
// - Отправки в LLM
// - Отладки и логирования
//
// Rule 5: Thread-safe доступ к History.
func (s *CoreState) GetHistory() []llm.Message {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Возвращаем копию, чтобы избежать race condition
	dst := make([]llm.Message, len(s.History))
	copy(dst, s.History)
	return dst
}

// ClearHistory очищает историю диалога.
//
// Thread-safe метод для сброса истории сообщений.
// Используется для начала новой сессии или сброса контекста.
//
// Rule 5: Thread-safe доступ к History.
func (s *CoreState) ClearHistory() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.History = make([]llm.Message, 0)
}

// --- Thread-Safe File Management (Working Memory Pattern) ---

// UpdateFileAnalysis сохраняет результат работы Vision модели в "память" файла.
//
// Параметры:
//   - tag: тег файла (ключ в map Files, например "sketch", "plm_data")
//   - filename: имя файла для поиска в слайсе
//   - description: результат анализа (текстовое описание от vision модели)
//
// Thread-safe: атомарно заменяет объект в слайсе под мьютексом.
//
// "Working Memory" паттерн: Vision модель анализирует изображение один раз,
// результат сохраняется в FileMeta.VisionDescription и переиспользуется
// в последующих вызовах LLM без повторной отправки изображения.
//
// Rule 5: Thread-safe доступ к Files.
// Rule 7: Возвращает ошибку вместо panic при проблеме с поиском файла.
func (s *CoreState) UpdateFileAnalysis(tag string, filename string, description string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	files, ok := s.Files[tag]
	if !ok {
		utils.Error("UpdateFileAnalysis: tag not found", "tag", tag, "filename", filename)
		return
	}

	// Находим индекс файла и атомарно заменяем объект (thread-safe)
	for i := range files {
		if files[i].Filename == filename {
			// Создаем новый объект для thread-safety
			updated := &s3storage.FileMeta{
				Tag:               files[i].Tag,
				OriginalKey:       files[i].OriginalKey,
				Size:              files[i].Size,
				Filename:          files[i].Filename,
				VisionDescription: description,
				Tags:              files[i].Tags,
			}
			files[i] = updated
			utils.Debug("File analysis updated", "tag", tag, "filename", filename, "desc_length", len(description))
			return
		}
	}

	// Файл не найден - логируем предупреждение
	utils.Warn("UpdateFileAnalysis: file not found in state",
		"tag", tag,
		"filename", filename,
		"available_files", len(files))
}

// SetFiles устанавливает файлы для текущей сессии.
//
// Принимает map[string][]*s3storage.FileMeta и сохраняет напрямую.
// VisionDescription заполняется позже через UpdateFileAnalysis().
//
// Thread-safe: атомарная замена всей map Files.
//
// Используется при смене артикула или начале новой сессии анализа.
//
// Rule 5: Thread-safe доступ к Files.
func (s *CoreState) SetFiles(files map[string][]*s3storage.FileMeta) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Files = files
}

// GetFiles возвращает копию текущих файлов.
//
// Thread-safe метод для чтения файлов.
//
// Rule 5: Thread-safe доступ к Files.
func (s *CoreState) GetFiles() map[string][]*s3storage.FileMeta {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Возвращаем копию map для thread-safety
	result := make(map[string][]*s3storage.FileMeta, len(s.Files))
	for k, v := range s.Files {
		result[k] = append([]*s3storage.FileMeta{}, v...)
	}
	return result
}

// --- Context Building ---

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
	for tag, files := range s.Files {
		for _, f := range files {
			if f.VisionDescription != "" {
				visualContext += fmt.Sprintf("- Файл [%s] %s: %s\n", tag, f.Filename, f.VisionDescription)
			}
		}
	}

	knowledgeMsg := ""
	if visualContext != "" {
		knowledgeMsg = fmt.Sprintf("\nКОНТЕКСТ АРТИКУЛА (Результаты анализа файлов):\n%s", visualContext)
	}

	// 2. Формируем контекст плана
	todoContext := s.Todo.String()

	// 3. Собираем итоговый массив сообщений
	messages := make([]llm.Message, 0, len(s.History)+3)

	// Системное сообщение с инъекцией знаний
	messages = append(messages, llm.Message{
		Role:    llm.RoleSystem,
		Content: systemPrompt + knowledgeMsg,
	})

	// Добавляем контекст плана
	messages = append(messages, llm.Message{
		Role:    llm.RoleSystem,
		Content: todoContext,
	})

	// Добавляем историю переписки
	messages = append(messages, s.History...)

	return messages
}

// --- Todo Management (delegates to todo.Manager) ---

// AddTodoTask безопасно добавляет новую задачу в план.
//
// Параметры:
//   - description: описание задачи
//   - metadata: опциональные метаданные (ключ-значение)
//
// Возвращает ID созданной задачи.
//
// Thread-safe: делегирует thread-safe Manager.Add под защитой мьютекса.
//
// Rule 5: Thread-safe доступ к Todo.
func (s *CoreState) AddTodoTask(description string, metadata ...map[string]interface{}) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Todo.Add(description, metadata...)
}

// CompleteTodoTask безопасно отмечает задачу как выполненную.
//
// Параметры:
//   - id: ID задачи для завершения
//
// Возвращает ошибку если задача не найдена или уже завершена.
//
// Thread-safe: делегирует thread-safe Manager.Complete под защитой мьютекса.
//
// Rule 5: Thread-safe доступ к Todo.
// Rule 7: Возвращает ошибку вместо panic.
func (s *CoreState) CompleteTodoTask(id int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Todo.Complete(id)
}

// FailTodoTask безопасно отмечает задачу как проваленную.
//
// Параметры:
//   - id: ID задачи
//   - reason: причина неудачи
//
// Возвращает ошибку если задача не найдена или уже завершена.
//
// Thread-safe: делегирует thread-safe Manager.Fail под защитой мьютекса.
//
// Rule 5: Thread-safe доступ к Todo.
// Rule 7: Возвращает ошибку вместо panic.
func (s *CoreState) FailTodoTask(id int, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Todo.Fail(id, reason)
}

// GetTodoString возвращает строковое представление плана задач.
//
// Thread-safe метод для чтения состояния todo листа.
//
// Rule 5: Thread-safe доступ к Todo.
func (s *CoreState) GetTodoString() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Todo.String()
}

// GetTodoStats возвращает статистику по задачам.
//
// Возвращает количество pending, done, failed задач.
//
// Thread-safe метод для чтения статистики.
//
// Rule 5: Thread-safe доступ к Todo.
func (s *CoreState) GetTodoStats() (pending, done, failed int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Todo.GetStats()
}

// ClearTodo очищает все задачи.
//
// Thread-safe метод для сброса todo листа.
//
// Rule 5: Thread-safe доступ к Todo.
func (s *CoreState) ClearTodo() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Todo.Clear()
}
