// Package main предоставляет утилиту interruption-test для тестирования механизма прерываний.
//
// Утилита запускает TUI с поддержкой прерываний.
//
// Usage:
//   cd cmd/interruption-test
//   go run main.go
//
// Features:
//   - TUI с панелью отладочной информации
//   - Ручные прерывания (пользователь сам вводит команды)
//   - Debug-logs по Ctrl-G (сохраняются в debug_logs/)
//   - Собственный config.yaml (автономность согласно dev_manifest)
//
// Примеры прерываний:
//   - "todo: add test task"     - Добавить задачу
//   - "todo: complete 1"        - Завершить задачу
//   - "stop"                    - Остановить выполнение
//   - "What are you doing?"     - Спросить статус
//
// Debug-logs:
//   - Ctrl-G сохраняет полный debug-лог текущего выполнения
//   - Логи сохраняются в debug_logs/debug_*.json
//   - Содержат полные LLM запросы/ответы, tool calls, результаты
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ilkoid/poncho-ai/pkg/agent"
	"github.com/ilkoid/poncho-ai/pkg/chain"
	"github.com/ilkoid/poncho-ai/pkg/events"
	"github.com/ilkoid/poncho-ai/pkg/tui"
	"github.com/ilkoid/poncho-ai/pkg/utils"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()

	// 0. Инициализируем логгер для debug output
	_ = utils.InitLogger()

	// 1. Определяем путь к конфигу
	configPath := "openrouter_conf.yaml"
	if len(os.Args) > 1 && os.Args[1] != "" {
		configPath = os.Args[1]
	}

	// 2. Создаём агент
	client, err := agent.New(ctx, agent.Config{
		ConfigPath: configPath,
	})
	if err != nil {
		return fmt.Errorf("agent creation failed: %w", err)
	}

	// 3. Создаём emitter и подписываемся на события
	emitter := events.NewChanEmitter(100)
	client.SetEmitter(emitter)
	sub := emitter.Subscribe()

	// 4. Канал для прерываний
	inputChan := make(chan string, 10)

	// 5. Создаём ChainConfig на основе дефолтной (из pkg/tui)
	chainCfg := tui.DefaultChainConfig()

	// Кастомизируем для interruption-test (debug logging включен)
	chainCfg.Debug.Enabled = true
	chainCfg.Debug.SaveLogs = true
	chainCfg.Debug.LogsDir = "./debug_logs"
	chainCfg.Debug.IncludeToolArgs = true
	chainCfg.Debug.IncludeToolResults = true
	chainCfg.Debug.MaxResultSize = 10000
	chainCfg.MaxIterations = 30  // Увеличено для сложных multi-step задач
	chainCfg.PostPromptsDir = "./prompts"
	chainCfg.InterruptionPrompt = "./prompts/interruption_handler.yaml"

	// 6. Approach 2: получаем CoreState из client
	coreState := client.GetState()

	// 7. Создаём базовую InterruptionModel
	baseModel := tui.NewInterruptionModel(ctx, client, coreState, sub, inputChan, chainCfg)

	// 8. Устанавливаем callback для запуска агента (Rule 6: бизнес-логика в cmd/)
	baseModel.SetOnInput(createAgentLauncher(client, chainCfg, inputChan, true))

	// 9. Включаем полное логирование LLM запросов для отладки
	baseModel.SetFullLLMLogging(true)

	// 10. Устанавливаем заголовок для TUI
	baseModel.SetTitle("🧪 Interruption Test Utility")

	// 11. Запускаем Bubble Tea с AltScreen для очистки экрана
	p := tea.NewProgram(baseModel, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("TUI error: %w", err)
	}

	return nil
}

// createAgentLauncher создаёт callback для запуска агента с прерываниями.
//
// Эта функция содержит бизнес-логику запуска агента, которая теперь вынесена
// из pkg/tui в cmd/ слой для соответствия Rule 6 (pkg/ должен быть reusable).
//
// Parameters:
//   - client: AI клиент для выполнения запросов
//   - chainCfg: Конфигурация ReAct цикла
//   - inputChan: Канал для пользовательских прерываний
//   - fullLLMLogging: Включить полное логирование LLM запросов
//
// Returns callback функцию которая запускает агента и возвращает Bubble Tea Cmd.
func createAgentLauncher(
	client *agent.Client,
	chainCfg chain.ChainConfig,
	inputChan chan string,
	fullLLMLogging bool,
) func(query string) tea.Cmd {
	return func(query string) tea.Cmd {
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()

			// Создаём ChainInput с каналом прерываний
			chainInput := chain.ChainInput{
				UserQuery:      query,
				State:          client.GetState(),
				Registry:       client.GetToolsRegistry(),
				Config:         chainCfg,
				UserInputChan:  inputChan,
				FullLLMLogging:  fullLLMLogging,
			}

			// Выполняем через Execute (поддерживает прерывания)
			output, err := client.Execute(ctx, chainInput)

			// Отправляем событие завершения
			if err != nil {
				return tui.EventMsg(events.Event{
					Type:      events.EventError,
					Data:      events.ErrorData{Err: err},
					Timestamp: time.Now(),
				})
			}

			return tui.EventMsg(events.Event{
				Type:      events.EventDone,
				Data:      events.MessageData{Content: output.Result},
				Timestamp: time.Now(),
			})
		}
	}
}

