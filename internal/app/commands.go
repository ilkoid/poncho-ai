package app

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// CommandResultMsg - сообщение, которое возвращает worker после работы
// Дублируем тип из ui пакета для избежания циклических зависимостей
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
	return &CommandRegistry{
		commands: make(map[string]CommandHandler),
	}
}

// Register регистрирует новую команду
func (r *CommandRegistry) Register(name string, handler CommandHandler) {
	r.commands[name] = handler
}

// Execute выполняет команду и возвращает tea.Cmd для асинхронного выполнения
func (r *CommandRegistry) Execute(input string, state *GlobalState) tea.Cmd {
	parts := strings.Fields(input)
	if len(parts) == 0 {
		return nil
	}

	cmd := parts[0]
	args := parts[1:]

	handler, exists := r.commands[cmd]
	if !exists {
		return func() tea.Msg {
			return CommandResultMsg{Err: fmt.Errorf("неизвестная команда: '%s'", cmd)}
		}
	}

	return handler(state, args)
}

// GetCommands возвращает список зарегистрированных команд
func (r *CommandRegistry) GetCommands() []string {
	var cmds []string
	for name := range r.commands {
		cmds = append(cmds, name)
	}
	return cmds
}

// SetupTodoCommands регистрирует команды для управления Todo
func SetupTodoCommands(registry *CommandRegistry, state *GlobalState) {
	registry.Register("todo", func(state *GlobalState, args []string) tea.Cmd {
		return func() tea.Msg {
			if len(args) == 0 {
				// Показать текущий план
				return CommandResultMsg{Output: state.Todo.String()}
			}

			subcommand := args[0]

			switch subcommand {
			case "add":
				if len(args) < 2 {
					return CommandResultMsg{Err: fmt.Errorf("использование: todo add <description>")}
				}
				description := strings.Join(args[1:], " ")
				id := state.AddTodoTask(description)
				return CommandResultMsg{Output: fmt.Sprintf("✅ Добавлена задача %d: %s", id, description)}

			case "done":
				if len(args) < 2 {
					return CommandResultMsg{Err: fmt.Errorf("использование: todo done <id>")}
				}
				id, err := strconv.Atoi(args[1])
				if err != nil {
					return CommandResultMsg{Err: fmt.Errorf("неверный ID задачи: %w", err)}
				}
				if err := state.CompleteTodoTask(id); err != nil {
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
				if err := state.FailTodoTask(id, reason); err != nil {
					return CommandResultMsg{Err: err}
				}
				return CommandResultMsg{Output: fmt.Sprintf("❌ Задача %d провалена: %s", id, reason)}

			case "clear":
				state.Todo.Clear()
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
	registry.Register("t", func(state *GlobalState, args []string) tea.Cmd {
		return registry.Execute("todo "+strings.Join(args, " "), state)
	})
}
