//go:build short

package app

import (
	tea "github.com/charmbracelet/bubbletea"
)

// CommandResultMsg - сообщение, которое возвращает worker после работы
type CommandResultMsg struct {
	Output string
	Err    error
}

// CommandHandler - тип функции-обработчика команды
type CommandHandler func(state *GlobalState, args []string) tea.Cmd

// CommandRegistry - реестр команд
type CommandRegistry struct {
	commands map[string]CommandHandler
}

// NewCommandRegistry создает новый реестр команд
func NewCommandRegistry() *CommandRegistry {
	// Создает новый пустой реестр команд
	return &CommandRegistry{
		commands: make(map[string]CommandHandler),
	}
}

// Register регистрирует новую команду
func (r *CommandRegistry) Register(name string, handler CommandHandler) {
	// Регистрирует обработчик для указанной команды
}

// Execute выполняет команду и возвращает tea.Cmd для асинхронного выполнения
func (r *CommandRegistry) Execute(input string, state *GlobalState) tea.Cmd {
	// Парсит ввод и выполняет соответствующую команду
	return nil
}

// GetCommands возвращает список зарегистрированных команд
func (r *CommandRegistry) GetCommands() []string {
	// Возвращает список всех зарегистрированных команд
	return nil
}

// SetupTodoCommands регистрирует команды для управления Todo
func SetupTodoCommands(registry *CommandRegistry, state *GlobalState) {
	// Регистрирует команды: todo add, todo done, todo fail, todo clear, todo help
	// Также регистрирует короткий псевдоним 't'
}