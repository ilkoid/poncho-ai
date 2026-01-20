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
	"github.com/ilkoid/poncho-ai/pkg/questions"
	"github.com/ilkoid/poncho-ai/pkg/tools/std"
	"github.com/ilkoid/poncho-ai/pkg/tui"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()

	// 0. НЕ инициализируем файловый логгер (poncho-*.log не создаётся)
	// utils.InitLogger() // Закомментировано: логи создаются только при debug mode (Ctrl+G)
	// Utils логи всё равно выводятся в stderr как fallback

	// 1. Определяем путь к конфигу
	configPath := "openrouter_conf.yaml"
	if len(os.Args) > 1 && os.Args[1] != "" {
		configPath = os.Args[1]
	}

	// 2. Создаём QuestionManager для координации ask_user_question tool
	// Shared state между tool и TUI (Polling Pattern)
	// maxOptions: 5 вариантов, timeout: 5 минут
	questionManager := questions.NewQuestionManager(5, 5*time.Minute)

	// 3. Создаём агент
	client, err := agent.New(ctx, agent.Config{
		ConfigPath: configPath,
	})
	if err != nil {
		return fmt.Errorf("agent creation failed: %w", err)
	}

	// 4. Настраиваем ask_user_question tool с QuestionManager
	// Получаем tool из registry и передаём QuestionManager
	toolsRegistry := client.GetToolsRegistry()
	if askTool, err := toolsRegistry.Get("ask_user_question"); err == nil {
		if typedTool, ok := askTool.(*std.AskUserQuestionTool); ok {
			typedTool.SetQuestionManager(questionManager)
			fmt.Fprintf(os.Stderr, "[INIT] ✓ ask_user_question tool configured with QuestionManager\n")
		} else {
			fmt.Fprintf(os.Stderr, "[INIT] ✗ Type assertion failed for ask_user_question\n")
		}
	} else {
		fmt.Fprintf(os.Stderr, "[INIT] ✗ ask_user_question tool not found: %v\n", err)
	}

	// 5. Создаём emitter и подписываемся на события
	emitter := events.NewChanEmitter(100)
	client.SetEmitter(emitter)
	sub := emitter.Subscribe()

	// 6. Канал для прерываний
	inputChan := make(chan string, 10)

	// 5. Создаём ChainConfig на основе дефолтной (из pkg/chain)
	chainCfg := chain.DefaultChainConfig()

	// Кастомизируем для interruption-test
	// Примечание: Debug-логирование настраивается в openrouter_conf.yaml (app.debug_logs)
	chainCfg.MaxIterations = 30 // Увеличено для сложных multi-step задач

	// 6. Approach 2: получаем CoreState из client
	coreState := client.GetState()

	// 7. Создаём базовую InterruptionModel
	// ⚠️ REFACTORED (Phase 3B): NewInterruptionModel больше не принимает *agent.Client (Rule 6 compliance)
	// client передается только в createAgentLauncher callback
	baseModel := tui.NewInterruptionModel(ctx, coreState, sub, inputChan)

	// 7.1. Передаём QuestionManager в InterruptionModel для polling
	baseModel.SetQuestionManager(questionManager)

	// 8. Устанавливаем callback для запуска агента (Rule 6: бизнес-логика в cmd/)
	baseModel.SetOnInput(createAgentLauncher(client, chainCfg, inputChan, baseModel))

	// 9. Устанавливаем заголовок для TUI
	baseModel.SetTitle("🧪 Interruption Test Utility")

	// 10. Запускаем Bubble Tea с AltScreen и поддержкой мыши
	p := tea.NewProgram(baseModel, tea.WithAltScreen(), tea.WithMouseAllMotion())
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
//   - model: InterruptionModel для проверки debug mode
//
// Returns callback функцию которая запускает агента и возвращает Bubble Tea Cmd.
func createAgentLauncher(
	client *agent.Client,
	chainCfg chain.ChainConfig,
	inputChan chan string,
	model *tui.InterruptionModel,
) func(query string) tea.Cmd {
	return func(queryCaptured string) tea.Cmd {
		return func() tea.Msg {
			// DEBUG LOG только если debug mode включён
			if model.GetDebugManager().IsEnabled() {
				logToDebugFile("[CALLBACK] START: query=%q", queryCaptured)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()

			// Создаём ChainInput с каналом прерываний
			chainInput := chain.ChainInput{
				UserQuery:     queryCaptured,
				State:         client.GetState(),
				Registry:      client.GetToolsRegistry(),
				Config:        chainCfg,
				UserInputChan: inputChan,
			}

			if model.GetDebugManager().IsEnabled() {
				logToDebugFile("[CALLBACK] ChainInput created, calling client.Execute()...")
			}

			// Выполняем через Execute (поддерживает прерывания)
			output, err := client.Execute(ctx, chainInput)

			if model.GetDebugManager().IsEnabled() {
				logToDebugFile("[CALLBACK] Execute returned: err=%v, result len=%d", err, len(output.Result))
			}

			// Отправляем событие завершения
			if err != nil {
				return tui.EventMsg(events.Event{
					Type: events.EventError,
					Data: events.ErrorData{Err: err},
				})
			}

			return tui.EventMsg(events.Event{
				Type: events.EventDone,
				Data: events.MessageData{Content: output.Result},
			})
		}
	}
}

// logToDebugFile пишет сообщение в debug файл
func logToDebugFile(format string, args ...interface{}) {
	f, err := os.OpenFile("callback_debug.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return
	}
	defer f.Close()

	timestamp := time.Now().Format("15:04:05.000")
	fmt.Fprintf(f, "[%s] %s\n", timestamp, fmt.Sprintf(format, args...))
}

