// Package app реализует реестр команд TUI.
//
// Позволяет регистрировать обработчики команд и выполнять их асинхронно.
package app

import (
	"fmt"
	"strconv"
	"strings"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ilkoid/poncho-ai/pkg/todo"
)

// CommandHandler — тип функции-обработчика команды.
//
// Принимает AppState и аргументы команды, возвращает tea.Cmd
// для асинхронного выполнения в Bubble Tea.
type CommandHandler func(state *AppState, args []string) tea.Cmd

// CommandRegistry — реестр зарегистрированных команд TUI.
//
// Позволяет динамически регистрировать и выполнять команды.
// Thread-safe: одновременные вызовы безопасны.
type CommandRegistry struct {
	mu       sync.RWMutex
	commands map[string]CommandHandler
}

// NewCommandRegistry создает новый пустой реестр команд.
func NewCommandRegistry() *CommandRegistry {
	return &CommandRegistry{
		commands: make(map[string]CommandHandler),
	}
}

// Register регистрирует новую команду в реестре.
//
// Если команда с таким именем уже существует, она будет перезаписана.
func (r *CommandRegistry) Register(name string, handler CommandHandler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commands[name] = handler
}

// Execute выполняет команду и возвращает tea.Cmd для асинхронного выполнения.
//
// Парсит ввод на имя команды и аргументы, находит соответствующий handler
// и возвращает tea.Cmd для выполнения в Bubble Tea.
// Если команда не найдена, возвращает команду с ошибкой.
func (r *CommandRegistry) Execute(input string, state *AppState) tea.Cmd {
	parts := strings.Fields(input)
	if len(parts) == 0 {
		return nil
	}

	cmd := parts[0]
	args := parts[1:]

	// Получаем handler под read lock
	r.mu.RLock()
	handler, exists := r.commands[cmd]
	r.mu.RUnlock()

	if !exists {
		return func() tea.Msg {
			return CommandResultMsg{Err: fmt.Errorf("неизвестная команда: '%s'", cmd)}
		}
	}

	return handler(state, args)
}

// GetCommands возвращает список имен зарегистрированных команд.
func (r *CommandRegistry) GetCommands() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	cmds := make([]string, 0, len(r.commands))
	for name := range r.commands {
		cmds = append(cmds, name)
	}
	return cmds
}

// SetupTodoCommands регистрирует команды для управления Todo в реестре.
//
// Регистрирует команду "todo" с подкомандами:
//   - todo add <description>  — добавить задачу
//   - todo done <id>          — отметить как выполненную
//   - todo fail <id> <reason> — отметить как проваленную
//   - todo clear              — очистить план
//   - todo help               — показать справку
//
// Также регистрирует псевдоним "t" для быстрого доступа.
func SetupTodoCommands(registry *CommandRegistry, state *AppState) {
	registry.Register("todo", func(state *AppState, args []string) tea.Cmd {
		return func() tea.Msg {
			if len(args) == 0 {
				// Показать текущий план
				return CommandResultMsg{Output: state.GetTodoString()}
			}

			subcommand := args[0]

			switch subcommand {
			case "add":
				if len(args) < 2 {
					return CommandResultMsg{Err: fmt.Errorf("использование: todo add <description>")}
				}
				description := strings.Join(args[1:], " ")
				// REFACTORED 2026-01-04: AddTodoTask → AddTask, теперь возвращает (int, error)
				id, err := state.AddTask(description)
				if err != nil {
					return CommandResultMsg{Err: fmt.Errorf("ошибка добавления задачи: %w", err)}
				}
				return CommandResultMsg{Output: fmt.Sprintf("✅ Добавлена задача %d: %s", id, description)}

			case "done":
				if len(args) < 2 {
					return CommandResultMsg{Err: fmt.Errorf("использование: todo done <id>")}
				}
				id, err := strconv.Atoi(args[1])
				if err != nil {
					return CommandResultMsg{Err: fmt.Errorf("неверный ID задачи: %w", err)}
				}
				// REFACTORED 2026-01-04: CompleteTodoTask → CompleteTask
				if err := state.CompleteTask(id); err != nil {
					return CommandResultMsg{Err: err}
				}
				return CommandResultMsg{Output: fmt.Sprintf("✅ Задача %d выполнена", id)}

			case "fail":
				if len(args) < 3 {
					return CommandResultMsg{Err: fmt.Errorf("использование: todo fail <id> <reason>")}
				}
				id, err := strconv.Atoi(args[1])
				if err != nil {
					return CommandResultMsg{Err: fmt.Errorf("неверный ID задачи: %w", err)}
				}
				reason := strings.Join(args[2:], " ")
				// REFACTORED 2026-01-04: FailTodoTask → FailTask
				if err := state.FailTask(id, reason); err != nil {
					return CommandResultMsg{Err: err}
				}
				return CommandResultMsg{Output: fmt.Sprintf("❌ Задача %d провалена: %s", id, reason)}

			case "clear":
				// REFACTORED 2026-01-04: ClearTodo() удален, используем Set напрямую
				if err := state.Set("todo", todo.NewManager()); err != nil {
					return CommandResultMsg{Err: err}
				}
				return CommandResultMsg{Output: "🗑️ План очищен"}

			case "help":
				helpText := `Команды управления Todo:
  todo                    - Показать текущий план
  todo add <description>  - Добавить новую задачу
  todo done <id>          - Отметить задачу как выполненную
  todo fail <id> <reason> - Отметить задачу как проваленную
  todo clear              - Очистить весь план
  todo help               - Показать эту справку`
				return CommandResultMsg{Output: helpText}

			default:
				return CommandResultMsg{Err: fmt.Errorf("неизвестная подкоманда: %s. Используйте 'todo help' для справки", subcommand)}
			}
		}
	})

	// Регистрируем короткие псевдонимы для удобства
	registry.Register("t", func(state *AppState, args []string) tea.Cmd {
		return registry.Execute("todo "+strings.Join(args, " "), state)
	})
}
