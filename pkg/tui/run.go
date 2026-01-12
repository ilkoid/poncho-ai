package tui

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ilkoid/poncho-ai/pkg/agent"
	"github.com/ilkoid/poncho-ai/pkg/events"
)

// Run запускает готовый TUI для AI агента.
//
// Это главная точка входа для пользователей библиотеки.
// Создаёт emitter, подписывается на события и запускает Bubble Tea программу.
//
// Правило 11: принимает и распространяет context.Context.
//
// # Basic Usage
//
//	client, _ := agent.New(agent.Config{ConfigPath: "config.yaml"})
//	if err := tui.Run(context.Background(), client); err != nil {
//	    log.Fatal(err)
//	}
//
// # Advanced Usage (с кастомным emitter)
//
//	client, _ := agent.New(...)
//	emitter := events.NewChanEmitter(100)
//	client.SetEmitter(emitter)
//
//	model := tui.NewModel(client, emitter.Subscribe())
//	p := tea.NewProgram(model)
//	if _, err := p.Run(); err != nil {
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

	// Создаём модель с контекстом
	model := NewModel(client, sub)
	model.ctx = ctx // Правило 11: сохраняем родительский контекст

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
//
// Пример:
//
//	client, _ := agent.New(...)
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

	// Создаём модель с опциями
	model := NewModel(client, sub)
	model.ctx = ctx // Правило 11: сохраняем родительский контекст
	for _, opt := range opts {
		opt(&model)
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
//	client, _ := agent.New(...)
//	err := tui.RunWithOpts(client,
//	    tui.WithTimeout(10 * time.Minute),
//	)
func WithTimeout(timeout time.Duration) Option {
	return func(m *Model) {
		m.timeout = timeout
	}
}
