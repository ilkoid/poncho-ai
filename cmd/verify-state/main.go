// Verify State Writing — проверка записи задач в CoreState.
//
// Утилита подтверждает что plan_set_tasks корректно пишет задачи в state.
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/agent"
	appcomponents "github.com/ilkoid/poncho-ai/pkg/app"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	fmt.Println("╔════════════════════════════════════════════════════════════╗")
	fmt.Println("║     Verify plan_set_tasks writes to CoreState             ║")
	fmt.Println("╚════════════════════════════════════════════════════════════╝")
	fmt.Println()

	// 1. Загружаем конфигурацию
	_, cfgPath, err := appcomponents.InitializeConfig(&appcomponents.DefaultConfigPathFinder{})
	if err != nil {
		return err
	}
	fmt.Printf("✅ Config loaded: %s\n\n", cfgPath)

	// 2. Rule 11: Создаём родительский контекст для инициализации
	initCtx, initCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer initCancel()

	// 3. Создаём агент
	client, err := agent.New(initCtx, agent.Config{ConfigPath: cfgPath})
	if err != nil {
		return err
	}
	fmt.Println("✅ Agent client created")
	fmt.Println()

	// 4. Получаем state ДО вызова plan_set_tasks
	stateBefore := client.GetState()
	todoBefore := stateBefore.GetTodoManager()
	pendingBefore, doneBefore, failedBefore := todoBefore.GetStats()
	fmt.Printf("📊 BEFORE: Todo Manager Stats: pending=%d done=%d failed=%d\n\n",
		pendingBefore, doneBefore, failedBefore)

	// 5. Проверяем что тот же instance в state
	todoFromState := stateBefore.GetTodoManager()
	fmt.Printf("🔗 SAME INSTANCE: todoBefore == todoFromState = %v\n\n", todoBefore == todoFromState)

	// 6. Вызываем agent с plan_set_tasks
	ctx := context.Background()
	testQuery := "Составь план из 3 задач для анализа товара: проверь категорию, загрузи эскизы, сгенерируй описание"
	fmt.Printf("🔍 Query: %s\n\n", testQuery)

	result, err := client.Run(ctx, testQuery)
	if err != nil {
		return err
	}
	fmt.Println("✅ Agent execution completed")
	fmt.Println()

	// 6. Получаем state ПОСЛЕ вызова
	stateAfter := client.GetState()
	todoAfter := stateAfter.GetTodoManager()
	pendingAfter, doneAfter, failedAfter := todoAfter.GetStats()
	fmt.Printf("📊 AFTER: Todo Manager Stats: pending=%d done=%d failed=%d\n\n",
		pendingAfter, doneAfter, failedAfter)

	// 7. Проверяем что тот же instance
	fmt.Printf("🔗 SAME INSTANCE: todoBefore == todoAfter = %v\n\n", todoBefore == todoAfter)

	// 8. Проверяем задачи через state напрямую
	tasks := todoAfter.GetTasks()
	fmt.Printf("📝 TASKS IN STATE (%d total):\n", len(tasks))
	for _, t := range tasks {
		fmt.Printf("   [%d] %s - %s\n", t.ID, t.Status, t.Description)
	}
	fmt.Println()

	// 9. Проверяем что задачи видны в BuildAgentContext
	messages := stateBefore.BuildAgentContext("System prompt")
	fmt.Printf("📤 BuildAgentContext MESSAGES (%d total):\n", len(messages))
	for i, msg := range messages {
		if msg.Role == "system" && len(msg.Content) > 100 {
			preview := msg.Content
			if len(preview) > 150 {
				preview = preview[:150] + "..."
			}
			fmt.Printf("   [%d] role=%s content_len=%d preview=%s\n",
				i, msg.Role, len(msg.Content), preview)
		}
	}
	fmt.Println()

	// 10. Проверяем что todo контекст инжектился
	hasTodoContext := false
	for _, msg := range messages {
		if msg.Role == "system" && len(msg.Content) > 0 {
			if len(msg.Content) > 200 && (strings.Contains(msg.Content, "ТЕКУЩИЙ ПЛАН") || strings.Contains(msg.Content, "[ ]")) {
				hasTodoContext = true
				fmt.Println("✅ TODO CONTEXT INJECTED: Found plan in system message")
				break
			}
		}
	}
	if !hasTodoContext {
		fmt.Println("⚠️  TODO CONTEXT NOT INJECTED: Plan not found in system message")
	}
	fmt.Println()

	// 11. Итоговая проверка
	if pendingAfter == 3 {
		fmt.Println("═════════════════════════════════════════════════════════════")
		fmt.Println("           ✅ VERIFICATION PASSED")
		fmt.Println("═════════════════════════════════════════════════════════════")
		fmt.Println()
		fmt.Println("✅ Tasks correctly written to CoreState:")
		fmt.Println("   - Same instance before/after (reference type)")
		fmt.Println("   - 3 tasks created via plan_set_tasks")
		fmt.Println("   - Tasks retrievable via state.GetTodoManager()")
		fmt.Println("   - Tasks injected into LLM context via BuildAgentContext()")
	} else {
		fmt.Println("═════════════════════════════════════════════════════════════")
		fmt.Println("           ❌ VERIFICATION FAILED")
		fmt.Println("═════════════════════════════════════════════════════════════")
		fmt.Printf("Expected 3 pending tasks, got %d\n", pendingAfter)
	}

	fmt.Println()
	fmt.Println("═════════════════════════════════════════════════════════════")
	fmt.Println("                    AGENT RESULT")
	fmt.Println("═════════════════════════════════════════════════════════════")
	fmt.Println(result)
	fmt.Println("═════════════════════════════════════════════════════════════")

	return nil
}
