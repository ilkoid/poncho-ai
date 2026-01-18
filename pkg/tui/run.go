package tui

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ilkoid/poncho-ai/pkg/agent"
	"github.com/ilkoid/poncho-ai/pkg/chain"
	"github.com/ilkoid/poncho-ai/pkg/events"
)

// Run запускает готовый TUI для AI агента.
//
// Это главная точка входа для пользователей библиотеки.
// Создаёт emitter, подписывается на события и запускает Bubble Tea программу.
//
// Правило 11: принимает и распространяет context.Context.
// Approach 2: CoreState передаётся как явная зависимость.
//
// # Basic Usage
//
//	client, _ := agent.New(context.Background(), agent.Config{ConfigPath: "config.yaml"})
//	if err := tui.Run(context.Background(), client); err != nil {
//	    log.Fatal(err)
//	}
//
// Thread-safe.
func Run(ctx context.Context, client *agent.Client) error {
	if client == nil {
		return fmt.Errorf("client is nil")
	}

	// Создаём emitter для событий
	emitter := events.NewChanEmitter(100)
	client.SetEmitter(emitter)

	// Получаем subscriber для TUI
	sub := client.Subscribe()

	// Approach 2: получаем CoreState из client
	coreState := client.GetState()

	// Создаём модель с контекстом (Rule 11)
	model := NewModel(ctx, client, coreState, sub)

	// Запускаем Bubble Tea программу
	p := tea.NewProgram(model)

	if _, err := p.Run(); err != nil {
		return fmt.Errorf("TUI error: %w", err)
	}

	return nil
}

// RunWithOpts запускает TUI с опциями.
//
// Позволяет кастомизировать поведение TUI через опции.
//
// Правило 11: принимает и распространяет context.Context.
// Approach 2: CoreState передаётся как явная зависимость.
//
// Пример:
//
//	client, _ := agent.New(context.Background(), ...)
//	err := tui.RunWithOpts(context.Background(), client,
//	    tui.WithTitle("My AI App"),
//	    tui.WithPrompt("> "),
//	)
func RunWithOpts(ctx context.Context, client *agent.Client, opts ...Option) error {
	if client == nil {
		return fmt.Errorf("client is nil")
	}

	// Создаём emitter
	emitter := events.NewChanEmitter(100)
	client.SetEmitter(emitter)
	sub := client.Subscribe()

	// Approach 2: получаем CoreState из client
	coreState := client.GetState()

	// Создаём модель с опциями (Rule 11)
	model := NewModel(ctx, client, coreState, sub)
	for _, opt := range opts {
		opt(model)
	}

	// Запускаем
	p := tea.NewProgram(model)
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("TUI error: %w", err)
	}

	return nil
}

// Option - функция для кастомизации TUI.
type Option func(*Model)

// WithTitle устанавливает заголовок TUI.
func WithTitle(title string) Option {
	return func(m *Model) {
		m.title = title
	}
}

// WithPrompt устанавливает текст приглашения ввода.
func WithPrompt(prompt string) Option {
	return func(m *Model) {
		m.prompt = prompt
	}
}

// WithTimeout устанавливает таймаут для выполнения запросов агента.
//
// По умолчанию используется 5 минут.
//
// Пример:
//
//	client, _ := agent.New(context.Background(), ...)
//	err := tui.RunWithOpts(context.Background(), client,
//	    tui.WithTimeout(10 * time.Minute),
//	)
func WithTimeout(timeout time.Duration) Option {
	return func(m *Model) {
		m.timeout = timeout
	}
}

// RunWithInterruptions запускает TUI с поддержкой прерываний.
//
// ⚠️ DEPRECATED: Эта функция оставлена для обратной совместимости,
// но она БУДЕТ ВОЗВРАЩАТЬ ОШИБКУ при попытке ввода, так как не устанавливает
// обязательный callback через SetOnInput().
//
// Рекомендуемый подход:
//
//	client, _ := agent.New(context.Background(), agent.Config{ConfigPath: "config.yaml"})
//	inputChan := make(chan string, 10)
//	chainCfg := tui.DefaultChainConfig()
//	coreState := client.GetState()
//	emitter := events.NewChanEmitter(100)
//	client.SetEmitter(emitter)
//	sub := emitter.Subscribe()
//
//	model := tui.NewInterruptionModel(ctx, client, coreState, sub, inputChan, chainCfg)
//	model.SetOnInput(createAgentLauncher(client, chainCfg, inputChan, true)) // MANDATORY
//
//	p := tea.NewProgram(model, tea.WithAltScreen())
//	p.Run()
//
// Пользователь может прервать выполнение агента, набрав команду и нажав Enter:
//   - "todo: add <task>"     - Добавить задачу
//   - "todo: complete <N>"   - Завершить задачу
//   - "stop"                 - Остановить выполнение
//   - Любой текст           - Задать вопрос агенту
//
// Правило 11: принимает и распространяет context.Context.
// Approach 2: CoreState передаётся как явная зависимость.
func RunWithInterruptions(ctx context.Context, client *agent.Client) error {
	if client == nil {
		return fmt.Errorf("client is nil")
	}

	// Создаём emitter
	emitter := events.NewChanEmitter(100)
	client.SetEmitter(emitter)
	sub := emitter.Subscribe()

	// Approach 2: получаем CoreState из client
	coreState := client.GetState()

	// Канал для прерываний
	inputChan := make(chan string, 10)

	// Создаём модель с поддержкой прерываний
	// ⚠️ DEPRECATED (Phase 3B): RunWithInterruptions нарушает Rule 6 (pkg/tui imports pkg/agent)
	// Рекомендуется использовать NewInterruptionModel напрямую из cmd/ слоя
	model := NewInterruptionModel(ctx, coreState, sub, inputChan)

	// Запускаем Bubble Tea
	p := tea.NewProgram(model)
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("TUI error: %w", err)
	}

	return nil
}

// createDefaultChainConfig создаёт дефолтную конфигурацию ReAct цикла.
func createDefaultChainConfig() chain.ChainConfig {
	return chain.ChainConfig{
		Type:               "react",
		MaxIterations:      10,
		PostPromptsDir:     "./prompts",
		InterruptionPrompt: "interruption_handler.yaml",
	}
}

// DefaultChainConfig возвращает дефолтную конфигурацию ReAct цикла.
//
// Эта функция экспортирована для использования в cmd/ приложениях,
// что позволяет избежать дублирования кода конфигурации.
//
// Returns базовую конфигурацию ChainConfig с дефолтными значениями.
func DefaultChainConfig() chain.ChainConfig {
	return createDefaultChainConfig()
}
