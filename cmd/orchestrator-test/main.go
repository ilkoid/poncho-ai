// Orchestrator Test - утилита для проверки работы оркестратора с todo листом
//
// Запускает агент с пользовательским запросом и выводит:
// - Ответ оркестратора
// - Состояние todo листа
// - Статистику задач
// - Историю сообщений (verbose mode)
//
// Использует pkg/app для переиспользования логики инициализации
// с другими entry points (TUI, HTTP и т.д.).
package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/app"
	"github.com/ilkoid/poncho-ai/pkg/llm"
	"github.com/ilkoid/poncho-ai/pkg/utils"
)

// CLI flags
var (
	flagConfig       = flag.String("config", "", "Path to config.yaml (default: auto-detect)")
	flagQuery        = flag.String("query", "", "User query for the agent (default: read from stdin)")
	flagSystemPrompt = flag.String("system", "", "Custom system prompt (default: use agent prompt from config)")
	flagMaxIters     = flag.Int("iters", 10, "Maximum ReAct iterations")
	flagTimeout      = flag.Duration("timeout", 2*time.Minute, "Execution timeout")
	flagVerbose      = flag.Bool("verbose", false, "Show message history")
	flagNoClear      = flag.Bool("no-clear", false, "Don't clear todo list after execution")
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	flag.Parse()

	// 0. Инициализируем логгер
	if err := utils.InitLogger(); err != nil {
		log.Printf("Warning: failed to init logger: %v", err)
	}
	defer utils.Close()

	utils.Info("orchestrator-test started", "query", *flagQuery, "verbose", *flagVerbose)

	// 1. Инициализируем конфигурацию (переиспользуем из pkg/app)
	cfg, cfgPath, err := app.InitializeConfig(&app.DefaultConfigPathFinder{ConfigFlag: *flagConfig})
	if err != nil {
		utils.Error("Failed to load config", "error", err)
		return err
	}
	log.Printf("Config loaded from %s", cfgPath)

	// 2. Инициализируем компоненты (переиспользуем из pkg/app)
	components, err := app.Initialize(cfg, *flagMaxIters, *flagSystemPrompt, app.ToolsAll)
	if err != nil {
		utils.Error("Components initialization failed", "error", err)
		return fmt.Errorf("initialization failed: %w", err)
	}
	log.Println("Components initialized successfully")

	// 3. Получаем запрос
	userQuery := getUserQuery()

	// 4. Выполняем запрос (переиспользуем из pkg/app)
	result, err := app.Execute(components, userQuery, *flagTimeout)
	if err != nil {
		utils.Error("Execution failed", "error", err, "query", userQuery)
		return fmt.Errorf("execution failed: %w", err)
	}

	// 5. Очищаем todo лист после выполнения (если не отключено)
	if !*flagNoClear {
		components.ClearTodos()
	}

	// 6. Выводим результаты (CLI-specific)
	printResults(result, *flagVerbose)

	utils.Info("orchestrator-test completed", "duration", result.Duration)

	return nil
}

// getUserQuery получает запрос пользователя из флага или stdin.
//
// Для TUI эта функция не нужна - запрос приходит из UI.
func getUserQuery() string {
	if *flagQuery != "" {
		return *flagQuery
	}

	// Читаем из stdin (поддерживает pipe и интерактивный ввод)
	fmt.Fprint(os.Stderr, "Enter query (press Ctrl+D when done):\n")

	// Используем bufio для корректного чтения из pipe и stdin
	var input strings.Builder
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		if input.Len() > 0 {
			input.WriteString(" ")
		}
		input.WriteString(scanner.Text())
	}

	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "Error reading input: %v\n", err)
		return ""
	}

	result := strings.TrimSpace(input.String())
	if result == "" {
		fmt.Fprintln(os.Stderr, "No input provided")
		return ""
	}

	return result
}

// printResults выводит результаты выполнения.
//
// Это CLI-specific функция. Для TUI будет создана аналогичная функция,
// которая рендерит результаты в UI компоненты.
func printResults(result *app.ExecutionResult, verbose bool) {
	separator := strings.Repeat("=", 70)

	fmt.Println()
	fmt.Println(separator)
	fmt.Println("ORCHESTRATOR RESPONSE")
	fmt.Println(separator)
	fmt.Println(result.Response)

	fmt.Println()
	fmt.Println(separator)
	fmt.Println("TODO LIST STATE")
	fmt.Println(separator)
	if result.TodoStats.Total == 0 {
		fmt.Println("(empty)")
	} else {
		fmt.Println(result.TodoString)
	}

	fmt.Println()
	fmt.Println(separator)
	fmt.Println("STATISTICS")
	fmt.Println(separator)
	fmt.Printf("Tasks: %d total, %d pending, %d done, %d failed\n",
		result.TodoStats.Total, result.TodoStats.Pending,
		result.TodoStats.Done, result.TodoStats.Failed)
	fmt.Printf("Execution time: %v\n", result.Duration)

	if verbose {
		fmt.Println()
		fmt.Println(separator)
		fmt.Println("MESSAGE HISTORY")
		fmt.Println(separator)
		printMessageHistory(result.History)
	}

	fmt.Println(separator)
}

// printMessageHistory выводит историю сообщений в читаемом формате.
//
// Для TUI история будет отображаться в отдельной панели.
func printMessageHistory(messages []llm.Message) {
	for i, msg := range messages {
		roleLabel := "UNKNOWN"
		switch msg.Role {
		case llm.RoleUser:
			roleLabel = "USER"
		case llm.RoleAssistant:
			roleLabel = "ASSISTANT"
		case llm.RoleSystem:
			roleLabel = "SYSTEM"
		case llm.RoleTool:
			roleLabel = "TOOL"
		}

		fmt.Printf("\n[%d] %s\n", i+1, roleLabel)

		if msg.ToolCallID != "" {
			fmt.Printf("  ToolCallID: %s\n", msg.ToolCallID)
		}

		// Выводим контент
		content := msg.Content
		if len(content) > 500 {
			content = content[:500] + "... (truncated)"
		}
		fmt.Printf("  Content: %s\n", content)

		// Выводим tool calls если есть
		if len(msg.ToolCalls) > 0 {
			fmt.Printf("  Tool Calls: %d\n", len(msg.ToolCalls))
			for _, tc := range msg.ToolCalls {
				fmt.Printf("    - %s: %s\n", tc.Name, tc.Args)
			}
		}
	}
}
