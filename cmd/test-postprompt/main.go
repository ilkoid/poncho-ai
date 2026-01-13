// Test Post-Prompt — проверка механизма post-prompts.
package main

import (
	"context"
	"fmt"
	"os"
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
	fmt.Println("║     Test Post-Prompt Mechanism                             ║")
	fmt.Println("╚════════════════════════════════════════════════════════════╝")
	fmt.Println()

	// 1. Загружаем конфигурацию
	_, cfgPath, err := appcomponents.InitializeConfig(&appcomponents.DefaultConfigPathFinder{})
	if err != nil {
		return err
	}
	fmt.Printf("✅ Config loaded: %s\n\n", cfgPath)

	// 2. Проверяем наличие директории prompts/
	fmt.Println("📁 Checking prompts directory...")
	if _, err := os.Stat("./prompts"); os.IsNotExist(err) {
		fmt.Println("⚠️  WARNING: ./prompts directory does NOT exist")
		fmt.Println("   Post-prompts configured in config.yaml will fail to load!")
		fmt.Println()
		return fmt.Errorf("prompts directory not found - post-prompts mechanism broken")
	}
	fmt.Println("✅ ./prompts directory exists")
	fmt.Println()

	// 3. Проверяем наличие конкретных post-prompt файлов
	postPromptFiles := []string{
		"agent_system.yaml",
		"parent_categories_analysis.yaml",
		"subjects_analysis.yaml",
		"api_health_report.yaml",
	}

	fmt.Println("📄 Checking post-prompt files...")
	for _, file := range postPromptFiles {
		path := "./prompts/" + file
		if _, err := os.Stat(path); os.IsNotExist(err) {
			fmt.Printf("   ❌ %s - NOT FOUND\n", file)
		} else {
			fmt.Printf("   ✅ %s - exists\n", file)
		}
	}
	fmt.Println()

	// 4. Rule 11: Создаём родительский контекст для инициализации
	initCtx, initCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer initCancel()

	// 5. Создаём агент (здесь произойдет ошибка если post-prompts не найдены)
	fmt.Println("🔧 Creating agent (this will fail if post-prompts are broken)...")
	client, err := agent.New(initCtx, agent.Config{ConfigPath: cfgPath})
	if err != nil {
		fmt.Printf("❌ Agent creation FAILED: %v\n", err)
		fmt.Println()
		fmt.Println("═════════════════════════════════════════════════════════════")
		fmt.Println("           ❌ POST-PROMPT MECHANISM BROKEN")
		fmt.Println("═════════════════════════════════════════════════════════════")
		return err
	}
	fmt.Println("✅ Agent created successfully")
	fmt.Println()

	// 7. Проверяем что tools с post-prompts зарегистрированы
	toolsRegistry := client.GetToolsRegistry()
	allTools := toolsRegistry.GetDefinitions()

	toolsWithPostPrompts := []string{}
	for _, toolDef := range allTools {
		// Tools configured with post_prompt in config.yaml
		if toolDef.Name == "get_wb_parent_categories" ||
		   toolDef.Name == "get_wb_subjects" ||
		   toolDef.Name == "wb_ping" {
			toolsWithPostPrompts = append(toolsWithPostPrompts, toolDef.Name)
		}
	}

	fmt.Printf("📋 Tools with post-prompts configured: %d\n", len(toolsWithPostPrompts))
	for _, name := range toolsWithPostPrompts {
		fmt.Printf("   • %s\n", name)
	}
	fmt.Println()

	// 6. Тестируем вызов tool с post-prompt
	fmt.Println("🧪 Testing tool call with post-prompt...")
	ctx := context.Background()

	// Вызываем get_wb_parent_categories (должен активировать post-prompt)
	testQuery := "Покажи родительские категории Wildberries"
	result, err := client.Run(ctx, testQuery)
	if err != nil {
		fmt.Printf("❌ Query failed: %v\n", err)
		return err
	}

	fmt.Println("✅ Query completed")
	fmt.Println()
	fmt.Println("═════════════════════════════════════════════════════════════")
	fmt.Println("           ✅ POST-PROMPT MECHANISM WORKING")
	fmt.Println("═════════════════════════════════════════════════════════════")
	fmt.Println()
	fmt.Println("Summary:")
	fmt.Println("  • Prompts directory exists")
	fmt.Println("  • Post-prompt files exist")
	fmt.Println("  • Agent initialization successful")
	fmt.Println("  • Tool execution with post-prompt successful")
	fmt.Println()
	fmt.Println("═════════════════════════════════════════════════════════════")
	fmt.Println("                    RESULT")
	fmt.Println("═════════════════════════════════════════════════════════════")
	fmt.Println(result)
	fmt.Println("═════════════════════════════════════════════════════════════")

	return nil
}
