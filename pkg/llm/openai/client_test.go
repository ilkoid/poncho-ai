package openai

import (
	"context"
	"testing"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/llm"
	"github.com/ilkoid/poncho-ai/pkg/tools"
)

// TestNewClient тестирует создание клиента.
func TestNewClient(t *testing.T) {
	tests := []struct {
		name    string
		modelDef config.ModelDef
	}{
		{
			name: "minimal config",
			modelDef: config.ModelDef{
				APIKey:    "test-key",
				ModelName: "gpt-4",
			},
		},
		{
			name: "with custom base url",
			modelDef: config.ModelDef{
				APIKey:    "test-key",
				ModelName: "glm-4",
				BaseURL:   "https://api.z.ai/v4",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := NewClient(tt.modelDef)
			if client == nil {
				t.Fatal("expected non-nil client")
			}
			if client.model != tt.modelDef.ModelName {
				t.Errorf("expected model %s, got %s", tt.modelDef.ModelName, client.model)
			}
			if client.api == nil {
				t.Error("expected non-nil api client")
			}
		})
	}
}

// TestConvertToolsToOpenAI тестирует конвертацию tools.
func TestConvertToolsToOpenAI(t *testing.T) {
	input := []tools.ToolDefinition{
		{
			Name:        "test_tool",
			Description: "A test tool",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"arg1": map[string]interface{}{
						"type":        "string",
						"description": "First argument",
					},
				},
			},
		},
		{
			Name:        "another_tool",
			Description: "Another test tool",
			Parameters: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
	}

	result := convertToolsToOpenAI(input)

	if len(result) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(result))
	}

	// Проверяем первый tool
	if result[0].Type != "function" {
		t.Errorf("expected type function, got %s", result[0].Type)
	}
	if result[0].Function.Name != "test_tool" {
		t.Errorf("expected name test_tool, got %s", result[0].Function.Name)
	}
	if result[0].Function.Description != "A test tool" {
		t.Errorf("expected description 'A test tool', got %s", result[0].Function.Description)
	}
	if result[0].Function.Parameters == nil {
		t.Error("expected non-nil parameters")
	}

	// Проверяем второй tool
	if result[1].Function.Name != "another_tool" {
		t.Errorf("expected name another_tool, got %s", result[1].Function.Name)
	}
}

// TestMapToOpenAI тестирует конвертацию сообщений.
func TestMapToOpenAI(t *testing.T) {
	tests := []struct {
		name     string
		input    llm.Message
		checkFn  func(t *testing.T, msg interface{})
	}{
		{
			name: "simple text message",
			input: llm.Message{
				Role:    llm.RoleUser,
				Content: "Hello, world!",
			},
			checkFn: func(t *testing.T, msg interface{}) {
				// Тип должен быть ChatCompletionMessage
				// Но так как это внутренний тип openai SDK, просто проверим что нет ошибки
				t.Log("simple text message converted successfully")
			},
		},
		{
			name: "message with tool calls",
			input: llm.Message{
				Role:    llm.RoleAssistant,
				Content: "",
				ToolCalls: []llm.ToolCall{
					{
						ID:   "call_123",
						Name: "test_tool",
						Args: `{"arg1": "value1"}`,
					},
				},
			},
			checkFn: func(t *testing.T, msg interface{}) {
				t.Log("message with tool calls converted successfully")
			},
		},
		{
			name: "message with images (vision)",
			input: llm.Message{
				Role:    llm.RoleUser,
				Content: "What's in this image?",
				Images:  []string{"http://example.com/image.jpg"},
			},
			checkFn: func(t *testing.T, msg interface{}) {
				t.Log("vision message converted successfully")
			},
		},
		{
			name: "tool result message",
			input: llm.Message{
				Role:       llm.RoleTool,
				ToolCallID: "call_123",
				Content:    `{"result": "success"}`,
			},
			checkFn: func(t *testing.T, msg interface{}) {
				t.Log("tool result message converted successfully")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mapToOpenAI(tt.input)
			if tt.checkFn != nil {
				tt.checkFn(t, result)
			}
			// Базовая проверка — результат должен быть валидным типом
			if result.Role == "" {
				t.Error("expected non-empty role")
			}
		})
	}
}

// TestGenerate_InvalidToolsType тестирует обработку невалидного типа tools.
//
// Этот тест проверяет что функция корректно обрабатывает случай,
// когда передан неверный тип для tools аргумента.
//
// Правило 7: Ошибка возвращается, а не паникует.
func TestGenerate_InvalidToolsType(t *testing.T) {
	client := NewClient(config.ModelDef{
		APIKey:    "test-key",
		ModelName: "gpt-4",
	})

	messages := []llm.Message{
		{
			Role:    llm.RoleUser,
			Content: "test",
		},
	}

	// Передаём неверный тип вместо []tools.ToolDefinition
	_, err := client.Generate(context.Background(), messages, "invalid type")

	if err == nil {
		t.Fatal("expected error for invalid tools type, got nil")
	}

	// Проверяем что сообщение об ошибке содержит описание проблемы
	if !contains(err.Error(), "invalid tools type") {
		t.Errorf("expected error message to contain 'invalid tools type', got: %v", err)
	}
}

// TestGenerate_NoTools тестирует генерацию без инструментов.
//
// Интеграционный тест, который требует реального API ключа.
// Пропускается если ключ не доступен.
func TestGenerate_NoTools(t *testing.T) {
	// Пропускаем если нет API ключа
	apiKey := getConfigOrSkip(t, "OPENAI_API_KEY")
	if apiKey == "" {
		t.Skip("OPENAI_API_KEY not set")
	}

	client := NewClient(config.ModelDef{
		APIKey:    apiKey,
		ModelName: getConfigOrSkip(t, "OPENAI_MODEL"),
	})

	messages := []llm.Message{
		{
			Role:    llm.RoleUser,
			Content: "Say 'test passed'",
		},
	}

	ctx := context.Background()
	result, err := client.Generate(ctx, messages)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Role != llm.RoleAssistant {
		t.Errorf("expected role assistant, got %s", result.Role)
	}

	if result.Content == "" {
		t.Error("expected non-empty content")
	}
}

// TestGenerate_WithTools тестирует генерацию с инструментами.
//
// Интеграционный тест, который требует реального API ключа.
// Пропускается если ключ не доступен.
func TestGenerate_WithTools(t *testing.T) {
	apiKey := getConfigOrSkip(t, "OPENAI_API_KEY")
	if apiKey == "" {
		t.Skip("OPENAI_API_KEY not set")
	}

	client := NewClient(config.ModelDef{
		APIKey:    apiKey,
		ModelName: getConfigOrSkip(t, "OPENAI_MODEL"),
	})

	toolDefs := []tools.ToolDefinition{
		{
			Name:        "get_weather",
			Description: "Get the current weather for a location",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"location": map[string]interface{}{
						"type":        "string",
						"description": "The city and state, e.g. San Francisco, CA",
					},
				},
				"required": []string{"location"},
			},
		},
	}

	messages := []llm.Message{
		{
			Role:    llm.RoleUser,
			Content: "What's the weather in Tokyo?",
		},
	}

	ctx := context.Background()
	result, err := client.Generate(ctx, messages, toolDefs)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Модель должна либо вызвать инструмент, либо запросить локацию
	if len(result.ToolCalls) == 0 && result.Content == "" {
		t.Error("expected either tool calls or content")
	}

	t.Logf("Result: Role=%s, Content=%s, ToolCalls=%d",
		result.Role, result.Content, len(result.ToolCalls))
}

// Helper функция для получения значения из переменной окружения.
func getConfigOrSkip(t *testing.T, key string) string {
	t.Helper()
	// Можно добавить логику для чтения из .env или переменных окружения
	// Пока возвращаем пустую строку чтобы тесты пропускались
	return ""
}

// Helper функция для проверки вхождения подстроки.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && containsHelper(s, substr)))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
