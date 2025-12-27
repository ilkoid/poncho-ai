// Simple CLI utility for testing tool post-prompt mechanism
//
// Usage:
//   go run cmd/prompt-test/main.go <tool_name> <query>
//
// Example:
//   go run cmd/prompt-test/main.go get_wb_parent_categories "покажи категории"
//
// This utility:
// 1. Executes the specified tool
// 2. Loads the post-prompt for that tool
// 3. Calls LLM with: post-prompt + tool result + user query
// 4. Returns the formatted response
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	appcomponents "github.com/ilkoid/poncho-ai/pkg/app"
	"github.com/ilkoid/poncho-ai/pkg/factory"
	"github.com/ilkoid/poncho-ai/pkg/llm"
	"github.com/ilkoid/poncho-ai/pkg/prompt"
)

// LocalConfigPathFinder ищет config.yaml рядом с утилитой
type LocalConfigPathFinder struct{}

func (f *LocalConfigPathFinder) FindConfigPath() string {
	// Получаем путь к исполняемому файлу
	execPath, err := os.Executable()
	if err != nil {
		// Fallback: используем текущую директорию
		return "config.yaml"
	}

	// Директория утилиты
	execDir := filepath.Dir(execPath)
	configPath := filepath.Join(execDir, "config.yaml")

	// Проверяем существование
	if _, err := os.Stat(configPath); err == nil {
		return configPath
	}

	// Fallback для разработки: пробуем текущую директорию
	if _, err := os.Stat("config.yaml"); err == nil {
		return "config.yaml"
	}

	// Last resort: возвращаем путь рядом с утилитой (даже если файл не существует)
	return configPath
}

func printUsage() {
	fmt.Fprintf(os.Stderr, "Usage: %s <tool_name> <query>\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "\nExamples:\n")
	fmt.Fprintf(os.Stderr, "  %s get_wb_parent_categories \"покажи все категории\"\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "  %s ping_wb_api \"проверь API\"\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "  %s get_wb_subjects \"покажи подкатегории\"\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "\nAvailable tools:\n")
	fmt.Fprintf(os.Stderr, "  - get_wb_parent_categories\n")
	fmt.Fprintf(os.Stderr, "  - get_wb_subjects\n")
	fmt.Fprintf(os.Stderr, "  - ping_wb_api\n")
}

func main() {
	// 1. Parse args
	if len(os.Args) < 3 {
		printUsage()
		os.Exit(1)
	}

	toolName := os.Args[1]
	userQuery := os.Args[2]

	log.Printf("[Prompt Test] Tool: %s, Query: %s", toolName, userQuery)

	// 2. Load config (ищем рядом с утилитой)
	cfg, cfgPath, err := appcomponents.InitializeConfig(&LocalConfigPathFinder{})
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}
	log.Printf("[Prompt Test] Config loaded from: %s", cfgPath)

	// 2.1. Override prompts_dir на директорию утилиты
	execDir := filepath.Dir(cfgPath)
	cfg.App.PromptsDir = filepath.Join(execDir, "prompts")

	// 3. Initialize components (WB tools only)
	components, err := appcomponents.Initialize(cfg, 1, "", appcomponents.ToolWB)
	if err != nil {
		log.Fatalf("Failed to initialize components: %v", err)
	}

	// 4. Load tool post-prompts config
	postPrompts, err := prompt.LoadToolPostPrompts(cfg)
	if err != nil {
		log.Fatalf("Failed to load post-prompts: %v", err)
	}

	// 5. Get the tool
	registry := components.State.GetToolsRegistry()
	tool, err := registry.Get(toolName)
	if err != nil {
		log.Fatalf("Tool '%s' not found: %v", toolName, err)
	}

	// 6. Execute the tool
	log.Printf("[Prompt Test] Executing tool: %s", toolName)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Build args for tool (empty JSON for no-arg tools, simple for others)
	var argsJSON string
	switch toolName {
	case "get_wb_subjects":
		// Parse parentID from query (simple heuristic)
		// Query format: "...подкатегории для <ID>..."
		// For now, use hardcoded or extract from query
		argsJSON = `{"parentID": 1524}` // Default to "Женщинам"
	default:
		argsJSON = "{}"
	}

	toolResult, err := tool.Execute(ctx, argsJSON)
	if err != nil {
		log.Fatalf("Tool execution failed: %v", err)
	}
	log.Printf("[Prompt Test] Tool result length: %d bytes", len(toolResult))

	// 7. Load post-prompt for this tool
	postPromptText, err := postPrompts.GetToolPostPrompt(toolName, cfg.App.PromptsDir)
	if err != nil {
		log.Fatalf("Failed to get post-prompt: %v", err)
	}
	if postPromptText == "" {
		log.Fatalf("No post-prompt configured for tool: %s", toolName)
	}
	log.Printf("[Prompt Test] Post-prompt loaded (%d bytes)", len(postPromptText))

	// 8. Build LLM messages
	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: postPromptText},
		{Role: llm.RoleUser, Content: fmt.Sprintf("Результат инструмента:\n%s\n\nЗапрос пользователя: %s", toolResult, userQuery)},
	}

	// 9. Call LLM
	log.Printf("[Prompt Test] Calling LLM...")
	modelDef, ok := cfg.Models.Definitions[cfg.Models.DefaultChat]
	if !ok {
		log.Fatalf("Default chat model '%s' not found", cfg.Models.DefaultChat)
	}

	llmProvider, err := factory.NewLLMProvider(modelDef)
	if err != nil {
		log.Fatalf("Failed to create LLM provider: %v", err)
	}

	ctxLLM, cancelLLM := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancelLLM()

	// Вызываем LLM без tools — нам не нужен function calling для post-prompt обработки
	response, err := llmProvider.Generate(ctxLLM, messages)
	if err != nil {
		log.Fatalf("LLM generation failed: %v", err)
	}

	// 10. Output result
	fmt.Println("\n" + strings.Repeat("=", 70))
	fmt.Println("RESULT:")
	fmt.Println(strings.Repeat("=", 70))
	fmt.Println(response.Content)
	fmt.Println(strings.Repeat("=", 70))

	log.Printf("[Prompt Test] Done. Response length: %d bytes", len(response.Content))
}
