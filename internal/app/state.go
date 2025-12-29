/*
Обновленный файл.
Теперь это не просто конфиг-контейнер, а потокобезопасное хранилище (Store).
Ключевые изменения:
sync.RWMutex — защита от паники при одновременной записи агентом и чтении UI.
ClassifiedFile расширен полями VisionDescription — это и есть ваша "Рабочая память" для результатов анализа.
Метод BuildAgentContext — реализует логику "сжатия" знаний перед отправкой в LLM.
*/

package app

import (
	"fmt"
	"sync"

	"github.com/ilkoid/poncho-ai/pkg/agent"
	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/llm"
	"github.com/ilkoid/poncho-ai/pkg/s3storage"
	"github.com/ilkoid/poncho-ai/pkg/state"
	"github.com/ilkoid/poncho-ai/pkg/todo"
	"github.com/ilkoid/poncho-ai/pkg/tools"
	"github.com/ilkoid/poncho-ai/pkg/utils"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// Пакет app хранит глобальное состояние приложения (GlobalState).
// Он выступает "Single Source of Truth" для UI, Агента и системных утилит.

// GlobalState хранит данные сессии, конфигурацию и историю.
// Доступ к полям, которые меняются в runtime (History, Files, IsProcessing),
// должен идти через методы с мьютексом.
type GlobalState struct {
	Config          *config.AppConfig
	S3              *s3storage.Client
	Dictionaries    *wb.Dictionaries
	Todo            *todo.Manager
	CommandRegistry *CommandRegistry
	ToolsRegistry   *tools.Registry
	Orchestrator    agent.Agent // AI-агент для обработки запросов (интерфейс)

	// mu защищает доступ к History, Files, UserChoice и IsProcessing
	mu sync.RWMutex

	// History — хронология общения (User <-> Agent).
	// Сюда НЕ попадают тяжелые base64, только текст и tool calls.
	History []llm.Message

	// Files — "Рабочая память" (Working Memory).
	// Хранит файлы текущего артикула и результаты их анализа.
	// Ключ: тег (например, "sketch", "plm_data").
	// Использует state.FileMeta как единый тип для метаданных.
	Files map[string][]*state.FileMeta

	// UserChoice — данные для интерактивного выбора пользователя
	UserChoice *userChoiceData

	// Данные текущей сессии
	CurrentArticleID string
	CurrentModel     string
	IsProcessing     bool
}

// NewState создает начальное состояние.
func NewState(cfg *config.AppConfig, s3Client *s3storage.Client) *GlobalState {
	return &GlobalState{
		Config:           cfg,
		S3:               s3Client,
		Todo:             todo.NewManager(),
		CommandRegistry:  NewCommandRegistry(),
		ToolsRegistry:    tools.NewRegistry(),
		CurrentArticleID: "NONE",
		CurrentModel:     cfg.Models.DefaultVision,
		IsProcessing:     false,
		Files:            make(map[string][]*state.FileMeta),
		History:          make([]llm.Message, 0),
	}
}

// --- Thread-Safe Methods (Методы для работы с данными) ---

// AppendMessage безопасно добавляет сообщение в историю.
func (s *GlobalState) AppendMessage(msg llm.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.History = append(s.History, msg)
}

// GetHistory возвращает копию истории для рендера в UI или отправки в LLM.
// Возвращаем копию, чтобы избежать race condition при чтении слайса.
func (s *GlobalState) GetHistory() []llm.Message {
	s.mu.RLock()
	defer s.mu.RUnlock()

	dst := make([]llm.Message, len(s.History))
	copy(dst, s.History)
	return dst
}

// ClearHistory очищает историю диалога.
// Thread-safe метод для сброса истории сообщений.
func (s *GlobalState) ClearHistory() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.History = make([]llm.Message, 0)
}

// UpdateFileAnalysis сохраняет результат работы Vision модели в "память" файла.
// path — путь к файлу (ключ поиска), description — результат анализа.
//
// Thread-safe: атомарно заменяет объект в слайсе под мьютексом.
func (s *GlobalState) UpdateFileAnalysis(tag string, filename string, description string) {
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
			// Обновляем VisionDescription (создаём новый объект для thread-safety)
			updated := &state.FileMeta{
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

// SetProcessing меняет статус занятости (для спиннера в UI).
func (s *GlobalState) SetProcessing(busy bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.IsProcessing = busy
}

// SetCurrentArticle потокобезопасно обновляет текущий артикул и файлы.
//
// Принимает map[string][]*state.FileMeta и сохраняет напрямую без конвертации.
// VisionDescription заполняется позже через UpdateFileAnalysis().
func (s *GlobalState) SetCurrentArticle(articleID string, files map[string][]*state.FileMeta) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.CurrentArticleID = articleID
	// Прямое присваивание без конвертации - state.FileMeta используется везде
	s.Files = files
}

// GetCurrentArticle потокобезопасно возвращает текущий артикул и файлы.
//
// Возвращает map[string][]*state.FileMeta напрямую без конвертации.
func (s *GlobalState) GetCurrentArticle() (articleID string, files map[string][]*state.FileMeta) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	// Прямое возвращение без конвертации
	return s.CurrentArticleID, s.Files
}

// GetCurrentArticleID потокобезопасно возвращает только ID текущего артикула.
func (s *GlobalState) GetCurrentArticleID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.CurrentArticleID
}

// BuildAgentContext собирает полный контекст для генеративного запроса (ReAct).
// Он объединяет:
// 1. Системный промпт.
// 2. "Рабочую память" (результаты анализа файлов).
// 3. Контекст плана (Todo Manager).
// 4. Историю диалога.
func (s *GlobalState) BuildAgentContext(systemPrompt string) []llm.Message {
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

// --- Вспомогательные методы для работы с Todo ---

// AddTodoTask безопасно добавляет новую задачу в план
func (s *GlobalState) AddTodoTask(description string, metadata ...map[string]interface{}) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Todo.Add(description, metadata...)
}

// CompleteTodoTask безопасно отмечает задачу как выполненную
func (s *GlobalState) CompleteTodoTask(id int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Todo.Complete(id)
}

// FailTodoTask безопасно отмечает задачу как проваленную
func (s *GlobalState) FailTodoTask(id int, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Todo.Fail(id, reason)
}

/*
Как это использовать (Пример логики)
Теперь в коде вашего агента (или в команде analyze) вы делаете так:

Vision этап (отдельно):

Берете файл, отправляете в LLM (без истории).

Получаете текст.

Вызываете state.UpdateFileAnalysis("sketch", "img1.jpg", "Платье красное...").

Генерация карточки (ReAct):

Вызываете state.BuildAgentContext("Ты менеджер WB...").

Этот метод сам склеит ("Платье красное...") и контекст плана в системный промпт.

Отправляете результат в LLM.

LLM "видит" описание картинки и текущий план, но не тратит токены на vision.

Управление задачами:

state.AddTodoTask("Проанализировать эскиз платья") - добавить задачу
state.CompleteTodoTask(1) - отметить задачу как выполненную
state.FailTodoTask(1, "Ошибка загрузки файла") - отметить задачу как проваленную
*/

// GetCommandRegistry возвращает реестр команд для использования в UI
func (s *GlobalState) GetCommandRegistry() *CommandRegistry {
	return s.CommandRegistry
}

// GetToolsRegistry возвращает реестр инструментов для использования в агенте
func (s *GlobalState) GetToolsRegistry() *tools.Registry {
	return s.ToolsRegistry
}

// --- User Choice Support (для интерактивных сценариев) ---

// userChoiceData хранит данные для выбора пользователя
type userChoiceData struct {
	question string
	options  []string
}

// SetUserChoice сохраняет данные для выбора пользователя.
// Thread-safe метод.
func (s *GlobalState) SetUserChoice(question string, options []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.UserChoice = &userChoiceData{
		question: question,
		options:  options,
	}
}

// GetUserChoice возвращает данные для выбора пользователя.
// Thread-safe метод. Возвращает question и options.
func (s *GlobalState) GetUserChoice() (string, []string) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.UserChoice == nil {
		return "", nil
	}
	return s.UserChoice.question, s.UserChoice.options
}

// ClearUserChoice очищает данные выбора пользователя.
func (s *GlobalState) ClearUserChoice() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.UserChoice = nil
}
