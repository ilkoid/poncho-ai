// Package app предоставляет состояние приложения (AppState).
//
// AppState содержит application-specific логику TUI приложения и
// встраивает CoreState для переиспользуемой бизнес-логики фреймворка.
//
// Package app следует правилам из dev_manifest.md:
//   - Rule 5: Thread-safe доступ через sync.RWMutex
//   - Rule 6: Application-specific логика, может импортировать pkg/
//   - Rule 7: Все ошибки возвращаются, никаких panic
package app

import (
	"sync"

	"github.com/ilkoid/poncho-ai/pkg/agent"
	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/s3storage"
	"github.com/ilkoid/poncho-ai/pkg/state"
	"github.com/ilkoid/poncho-ai/pkg/tools"
)

// AppState представляет состояние приложения (TUI/CLI specific).
//
// Встраивает CoreState (композиция) для framework логики и
// добавляет application-specific поля для TUI интерфейса.
//
// Rule 6: Application-specific логика, использует framework через pkg/state.
type AppState struct {
	// CoreState - встроенное framework core состояние
	// Содержит переиспользуемую бизнес-логику (WB, S3, tools, etc.)
	// Rule 6: Композиция вместо наследования для четкого разделения
	*state.CoreState

	// CommandRegistry - реестр TUI команд
	// Application-specific: команды для интерактивного интерфейса
	CommandRegistry *CommandRegistry

	// Orchestrator - конкретная реализация AI-агента
	// Application-specific: выбор реализации (internal/agent или pkg/chain)
	Orchestrator agent.Agent

	// mu защищает доступ к UserChoice, CurrentArticleID, CurrentModel, IsProcessing
	mu sync.RWMutex

	// UserChoice - данные для интерактивного выбора пользователя
	// Application-specific:交互ные UI scenarios
	UserChoice *userChoiceData

	// CurrentArticleID - ID текущего артикула WB
	// Application-specific: состояние workflow для WB
	CurrentArticleID string

	// CurrentModel - текущая модель для отображения в UI
	// Application-specific: UI state
	CurrentModel string

	// IsProcessing - флаг занятости агента
	// Application-specific: UI spinner
	IsProcessing bool
}

// userChoiceData хранит данные для выбора пользователя.
// Application-specific: интерактивные UI scenarios.
type userChoiceData struct {
	question string
	options  []string
}

// NewAppState создает новое состояние приложения.
//
// REFACTORED 2026-01-04: Конструктор больше не требует s3Client.
// S3 client может быть установлен позже через CoreState.SetStorage() если нужен.
//
// Инициализирует CoreState через state.NewCoreState() и
// устанавливает application-specific значения по умолчанию.
//
// Rule 5: Thread-safe структура с инициализированными мьютексами.
// Rule 6: Использует framework core через pkg/state.
func NewAppState(cfg *config.AppConfig) *AppState {
	coreState := state.NewCoreState(cfg)

	return &AppState{
		CoreState:       coreState,
		CommandRegistry: NewCommandRegistry(),
		// ToolsRegistry будет установлен позже через pkg/app/components.Initialize()
		// S3 client может быть установлен позже через CoreState.SetStorage()
		// Orchestrator будет установлен позже
		CurrentArticleID: "NONE",
		CurrentModel:     cfg.Models.DefaultVision,
		IsProcessing:     false,
	}
}

// --- Orchestrator Management (Application-Specific) ---

// SetOrchestrator устанавливает реализацию AI-агента.
//
// Application-specific: выбор между internal/agent.Orchestrator
// или pkg/chain.ReActChain (или других реализаций).
//
// Rule 4: Работает через agent.Agent интерфейс.
func (s *AppState) SetOrchestrator(orch agent.Agent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Orchestrator = orch
}

// GetOrchestrator возвращает текущую реализацию AI-агента.
//
// Thread-safe метод для получения агента.
func (s *AppState) GetOrchestrator() agent.Agent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Orchestrator
}

// --- UI-Specific Processing (Application-Specific) ---

// SetProcessing меняет статус занятости (для спиннера в UI).
//
// Thread-safe метод для обновления UI state.
//
// Application-specific: только для TUI с spinner.
func (s *AppState) SetProcessing(busy bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.IsProcessing = busy
}

// GetProcessing возвращает текущий статус занятости.
//
// Thread-safe метод для чтения UI state.
func (s *AppState) GetProcessing() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.IsProcessing
}

// --- Article Management (WB Workflow - Application-Specific) ---

// SetCurrentArticle потокобезопасно обновляет текущий артикул и файлы.
//
// Принимает map[string][]*s3storage.FileMeta и сохраняет в CoreState.
// VisionDescription заполняется позже через UpdateFileAnalysis().
//
// Thread-safe: защищен мьютексом AppState.
//
// Application-specific: WB workflow state.
func (s *AppState) SetCurrentArticle(articleID string, files map[string][]*s3storage.FileMeta) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.CurrentArticleID = articleID
	// Делегируем в CoreState
	s.CoreState.SetFiles(files)
}

// GetCurrentArticle потокобезопасно возвращает текущий артикул и файлы.
//
// Thread-safe метод для чтения WB workflow state.
//
// Application-specific: WB workflow state.
func (s *AppState) GetCurrentArticle() (articleID string, files map[string][]*s3storage.FileMeta) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	// Читаем articleID из AppState, files из CoreState
	return s.CurrentArticleID, s.CoreState.GetFiles()
}

// GetCurrentArticleID потокобезопасно возвращает только ID текущего артикула.
//
// Thread-safe метод для чтения WB workflow state.
func (s *AppState) GetCurrentArticleID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.CurrentArticleID
}

// SetCurrentModel устанавливает текущую модель для отображения в UI.
//
// Thread-safe метод для обновления UI state.
//
// Application-specific: UI display.
func (s *AppState) SetCurrentModel(model string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.CurrentModel = model
}

// GetCurrentModel возвращает текущую модель для отображения в UI.
//
// Thread-safe метод для чтения UI state.
func (s *AppState) GetCurrentModel() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.CurrentModel
}

// --- User Choice (Interactive UI - Application-Specific) ---

// SetUserChoice сохраняет данные для выбора пользователя.
//
// Thread-safe метод для интерактивных UI scenarios.
//
// Application-specific: только для интерактивных UI.
func (s *AppState) SetUserChoice(question string, options []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.UserChoice = &userChoiceData{
		question: question,
		options:  options,
	}
}

// GetUserChoice возвращает данные для выбора пользователя.
//
// Thread-safe метод. Возвращает question и options.
//
// Application-specific: только для интерактивных UI.
func (s *AppState) GetUserChoice() (string, []string) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.UserChoice == nil {
		return "", nil
	}
	return s.UserChoice.question, s.UserChoice.options
}

// ClearUserChoice очищает данные выбора пользователя.
//
// Thread-safe метод для сброса интерактивного состояния.
func (s *AppState) ClearUserChoice() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.UserChoice = nil
}

// --- Registry Getters ---

// GetCommandRegistry возвращает реестр команд для использования в UI.
//
// Application-specific: TUI commands registry.
func (s *AppState) GetCommandRegistry() *CommandRegistry {
	return s.CommandRegistry
}

// GetToolsRegistry возвращает реестр инструментов для использования в агенте.
//
// Делегирует в CoreState для доступа к framework tools registry.
//
// Rule 3: Все инструменты вызываются через Registry.
func (s *AppState) GetToolsRegistry() *tools.Registry {
	return s.CoreState.GetToolsRegistry()
}
