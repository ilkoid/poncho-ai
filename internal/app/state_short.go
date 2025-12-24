//go:build short

package app

import (
	"sync"

	"github.com/ilkoid/poncho-ai/pkg/classifier"
	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/llm"
	"github.com/ilkoid/poncho-ai/pkg/s3storage"
	"github.com/ilkoid/poncho-ai/pkg/todo"
	"github.com/ilkoid/poncho-ai/pkg/tools"
	"github.com/ilkoid/poncho-ai/pkg/wb"
)

// FileMeta расширяет базовую классификацию файла результатами анализа.
type FileMeta struct {
	classifier.ClassifiedFile
	VisionDescription string
	Tags              []string
}

// GlobalState хранит данные сессии, конфигурацию и историю.
type GlobalState struct {
	Config          *config.AppConfig
	S3              *s3storage.Client
	Dictionaries    *wb.Dictionaries
	Todo            *todo.Manager
	CommandRegistry *CommandRegistry
	ToolsRegistry   *tools.Registry
	mu              sync.RWMutex
	History         []llm.Message
	Files           map[string][]*FileMeta
	CurrentArticleID string
	CurrentModel    string
	IsProcessing    bool
}

// NewState создает начальное состояние.
func NewState(cfg *config.AppConfig, s3Client *s3storage.Client) *GlobalState {
	// Инициализация состояния с базовыми значениями
	return &GlobalState{
		Config:           cfg,
		S3:               s3Client,
		Todo:             todo.NewManager(),
		CommandRegistry:  NewCommandRegistry(),
		ToolsRegistry:    tools.NewRegistry(),
		CurrentArticleID: "NONE",
		CurrentModel:     cfg.Models.DefaultVision,
		IsProcessing:     false,
		Files:            make(map[string][]*FileMeta),
		History:          make([]llm.Message, 0),
	}
}

// AppendMessage безопасно добавляет сообщение в историю.
func (s *GlobalState) AppendMessage(msg llm.Message) {
	// Потокобезопасное добавление сообщения в историю
}

// GetHistory возвращает копию истории для рендера в UI или отправки в LLM.
func (s *GlobalState) GetHistory() []llm.Message {
	// Возвращает копию истории для избежания race conditions
	return nil
}

// UpdateFileAnalysis сохраняет результат работы Vision модели в "память" файла.
func (s *GlobalState) UpdateFileAnalysis(tag string, filename string, description string) {
	// Обновляет описание файла после анализа Vision моделью
}

// SetProcessing меняет статус занятости (для спиннера в UI).
func (s *GlobalState) SetProcessing(busy bool) {
	// Устанавливает флаг обработки для UI индикатора
}

// BuildAgentContext собирает полный контекст для генеративного запроса (ReAct).
func (s *GlobalState) BuildAgentContext(systemPrompt string) []llm.Message {
	// Собирает системный промпт, рабочую память, контекст плана и историю
	return nil
}

// AddTodoTask безопасно добавляет новую задачу в план
func (s *GlobalState) AddTodoTask(description string, metadata ...map[string]interface{}) int {
	// Добавляет новую задачу в Todo менеджер
	return 0
}

// CompleteTodoTask безопасно отмечает задачу как выполненную
func (s *GlobalState) CompleteTodoTask(id int) error {
	// Отмечает задачу как выполненную
	return nil
}

// FailTodoTask безопасно отмечает задачу как проваленную
func (s *GlobalState) FailTodoTask(id int, reason string) error {
	// Отмечает задачу как проваленную с указанием причины
	return nil
}

// GetCommandRegistry возвращает реестр команд для использования в UI
func (s *GlobalState) GetCommandRegistry() *CommandRegistry {
	return s.CommandRegistry
}

// GetToolsRegistry возвращает реестр инструментов для использования в агенте
func (s *GlobalState) GetToolsRegistry() *tools.Registry {
	return s.ToolsRegistry
}