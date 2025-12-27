package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/ilkoid/poncho-ai/internal/app"
	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/llm"
	"github.com/ilkoid/poncho-ai/pkg/s3storage"
	"github.com/ilkoid/poncho-ai/pkg/tools"
)

// MockLLMProvider — мок LLM провайдера для тестирования.
// Реализует интерфейс llm.Provider для детерминированного тестирования.
type MockLLMProvider struct {
	// Responses — последовательность ответов для возврата
	Responses []llm.Message
	// CallCount — количество вызовов Generate
	CallCount int
	// LastMessages — последние сообщения, переданные в Generate
	LastMessages []llm.Message
	// LastTools — последние tools definitions
	LastTools []ToolDef
}

// ToolDef — алиас для tools.ToolDefinition чтобы избежать конфликта имён в тесте.
type ToolDef = tools.ToolDefinition

// Generate реализует llm.Provider интерфейс.
func (m *MockLLMProvider) Generate(ctx context.Context, messages []llm.Message, tools ...any) (llm.Message, error) {
	m.CallCount++
	m.LastMessages = messages
	if len(tools) > 0 {
		if defs, ok := tools[0].([]ToolDef); ok {
			m.LastTools = defs
		}
	}

	if m.CallCount > len(m.Responses) {
		return llm.Message{}, errors.New("unexpected call: no more responses")
	}

	return m.Responses[m.CallCount-1], nil
}

// MockTool — мок инструмента для тестирования.
// Реализует интерфейс tools.Tool с предсказуемым поведением.
type MockTool struct {
	Name        string
	ExecuteFunc func(ctx context.Context, argsJSON string) (string, error)
}

// Definition возвращает определение инструмента.
func (m *MockTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        m.Name,
		Description: "Mock tool for testing",
		Parameters: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
	}
}

// Execute выполняет инструмент.
func (m *MockTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	if m.ExecuteFunc != nil {
		return m.ExecuteFunc(ctx, argsJSON)
	}
	return `{"result": "mock success"}`, nil
}

// setupTestState создаёт тестовое состояние для тестов.
func setupTestState(t *testing.T) *app.GlobalState {
	t.Helper()

	// Создаём минимальную конфигурацию
	cfg := &config.AppConfig{
		S3: config.S3Config{
			Endpoint: "localhost",
			Bucket:   "test",
		},
	}

	// S3 клиент (нужен для NewState, но не будет использоваться)
	s3Client, err := s3storage.New(cfg.S3)
	if err != nil {
		t.Fatalf("failed to create s3 client: %v", err)
	}

	return app.NewState(cfg, s3Client)
}

// TestNewOrchestrator тестирует создание Orchestrator.
func TestNewOrchestrator(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{
			name: "valid config",
			cfg: Config{
				LLM:      &MockLLMProvider{},
				Registry: tools.NewRegistry(),
				State:    setupTestState(t),
			},
			wantErr: false,
		},
		{
			name: "missing LLM",
			cfg: Config{
				LLM:      nil,
				Registry: tools.NewRegistry(),
				State:    setupTestState(t),
			},
			wantErr: true,
		},
		{
			name: "missing Registry",
			cfg: Config{
				LLM:      &MockLLMProvider{},
				Registry: nil,
				State:    setupTestState(t),
			},
			wantErr: true,
		},
		{
			name: "missing State",
			cfg: Config{
				LLM:      &MockLLMProvider{},
				Registry: tools.NewRegistry(),
				State:    nil,
			},
			wantErr: true,
		},
		{
			name: "default values",
			cfg: Config{
				LLM:      &MockLLMProvider{},
				Registry: tools.NewRegistry(),
				State:    setupTestState(t),
				MaxIters: 0,      // должен использовать DefaultMaxIterations
				SystemPrompt: "", // должен использовать дефолтный промпт
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orch, err := New(tt.cfg)

			if tt.wantErr {
				if err == nil {
					t.Error("expected error but got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if orch == nil {
				t.Fatal("expected orchestrator but got nil")
			}

			// Проверка дефолтных значений
			if tt.cfg.MaxIters == 0 && orch.maxIters != DefaultMaxIterations {
				t.Errorf("expected MaxIters=%d, got %d", DefaultMaxIterations, orch.maxIters)
			}
			if tt.cfg.SystemPrompt == "" && orch.systemPrompt == "" {
				t.Error("expected default system prompt but got empty")
			}
		})
	}
}

// TestOrchestrator_Run_NoTools тестирует сценарий без вызова инструментов.
func TestOrchestrator_Run_NoTools(t *testing.T) {
	ctx := context.Background()

	// Создаём мок LLM который сразу возвращает финальный ответ
	mockLLM := &MockLLMProvider{
		Responses: []llm.Message{
			{
				Role:    llm.RoleAssistant,
				Content: "Привет! Я готов помочь.",
			},
		},
	}

	registry := tools.NewRegistry()
	state := setupTestState(t)

	orch, err := New(Config{
		LLM:      mockLLM,
		Registry: registry,
		State:    state,
	})
	if err != nil {
		t.Fatalf("failed to create orchestrator: %v", err)
	}

	// Запускаем
	result, err := orch.Run(ctx, "привет")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Проверяем результат
	expected := "Привет! Я готов помочь."
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}

	// Проверяем что LLM был вызван 1 раз
	if mockLLM.CallCount != 1 {
		t.Errorf("expected 1 LLM call, got %d", mockLLM.CallCount)
	}

	// Проверяем что история содержит 2 сообщения (user + assistant)
	history := state.GetHistory()
	if len(history) != 2 {
		t.Errorf("expected 2 messages in history, got %d", len(history))
	}
}

// TestOrchestrator_Run_WithTool тестирует сценарий с вызовом инструмента.
func TestOrchestrator_Run_WithTool(t *testing.T) {
	ctx := context.Background()

	// Создаём мок инструмента
	mockTool := &MockTool{
		Name: "test_tool",
		ExecuteFunc: func(ctx context.Context, argsJSON string) (string, error) {
			return `{"categories": ["женщинам", "мужчинам"]}`, nil
		},
	}

	// Создаём мок LLM который вызывает инструмент, затем возвращает ответ
	mockLLM := &MockLLMProvider{
		Responses: []llm.Message{
			// Первый вызов — LLM решает вызвать инструмент
			{
				Role: llm.RoleAssistant,
				ToolCalls: []llm.ToolCall{
					{
						ID:   "call_123",
						Name: "test_tool",
						Args: "{}",
					},
				},
			},
			// Второй вызов — LLM формирует финальный ответ
			{
				Role:    llm.RoleAssistant,
				Content: "Найдены категории: женщинам, мужчинам",
			},
		},
	}

	// Регистрируем инструмент
	registry := tools.NewRegistry()
	registry.Register(mockTool)

	state := setupTestState(t)

	orch, err := New(Config{
		LLM:      mockLLM,
		Registry: registry,
		State:    state,
	})
	if err != nil {
		t.Fatalf("failed to create orchestrator: %v", err)
	}

	// Запускаем
	result, err := orch.Run(ctx, "покажи категории")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Проверяем результат
	expected := "Найдены категории: женщинам, мужчинам"
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}

	// Проверяем что LLM был вызван 2 раза
	if mockLLM.CallCount != 2 {
		t.Errorf("expected 2 LLM calls, got %d", mockLLM.CallCount)
	}

	// Проверяем историю: user → assistant (tool call) → tool → assistant
	history := state.GetHistory()
	if len(history) != 4 {
		t.Errorf("expected 4 messages in history, got %d", len(history))
	}

	// Проверяем что tool message содержит результат
	toolMsg := history[2]
	if toolMsg.Role != llm.RoleTool {
		t.Errorf("expected tool role, got %s", toolMsg.Role)
	}
	if toolMsg.ToolCallID != "call_123" {
		t.Errorf("expected ToolCallID call_123, got %s", toolMsg.ToolCallID)
	}
}

// TestOrchestrator_Run_ToolError тестирует обработку ошибок инструмента.
func TestOrchestrator_Run_ToolError(t *testing.T) {
	ctx := context.Background()

	// Создаём мок инструмента который возвращает ошибку
	mockTool := &MockTool{
		Name: "failing_tool",
		ExecuteFunc: func(ctx context.Context, argsJSON string) (string, error) {
			return "", errors.New("tool execution failed")
		},
	}

	// LLM получает ошибку и формирует ответ
	mockLLM := &MockLLMProvider{
		Responses: []llm.Message{
			{
				Role: llm.RoleAssistant,
				ToolCalls: []llm.ToolCall{
					{
						ID:   "call_456",
						Name: "failing_tool",
						Args: "{}",
					},
				},
			},
			{
				Role:    llm.RoleAssistant,
				Content: "К сожалению, не удалось получить данные. Ошибка: tool execution failed",
			},
		},
	}

	registry := tools.NewRegistry()
	registry.Register(mockTool)

	state := setupTestState(t)

	orch, err := New(Config{
		LLM:      mockLLM,
		Registry: registry,
		State:    state,
	})
	if err != nil {
		t.Fatalf("failed to create orchestrator: %v", err)
	}

	// Запускаем — ошибка инструмента не должна прерывать выполнение
	result, err := orch.Run(ctx, "вызови failing_tool")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// LLM должен был сообщить об ошибке
	if result == "" {
		t.Error("expected non-empty result")
	}

	// Проверяем что в истории есть сообщение об ошибке
	history := state.GetHistory()
	toolMsg := history[2]
	if !contains(toolMsg.Content, "Tool execution error") {
		t.Errorf("expected error message in tool result, got %q", toolMsg.Content)
	}
}

// TestOrchestrator_Run_MaxIterations тестирует защиту от бесконечного цикла.
func TestOrchestrator_Run_MaxIterations(t *testing.T) {
	ctx := context.Background()

	// LLM постоянно возвращает tool calls (симуляция цикла)
	endlessToolCall := llm.Message{
		Role: llm.RoleAssistant,
		ToolCalls: []llm.ToolCall{
			{
				ID:   "call_loop",
				Name: "loop_tool",
				Args: "{}",
			},
		},
	}

	mockLLM := &MockLLMProvider{
		// Генерируем достаточно ответов для превышения лимита
		Responses: make([]llm.Message, 20),
	}
	for i := range mockLLM.Responses {
		mockLLM.Responses[i] = endlessToolCall
	}

	mockTool := &MockTool{Name: "loop_tool"}

	registry := tools.NewRegistry()
	registry.Register(mockTool)

	state := setupTestState(t)

	orch, err := New(Config{
		LLM:      mockLLM,
		Registry: registry,
		State:    state,
		MaxIters: 3, // Маленький лимит для быстрого теста
	})
	if err != nil {
		t.Fatalf("failed to create orchestrator: %v", err)
	}

	// Должен вернуть ошибку превышения итераций
	_, err = orch.Run(ctx, "запусти цикл")
	if err == nil {
		t.Fatal("expected max iterations error but got nil")
	}

	if !contains(err.Error(), "max iterations") {
		t.Errorf("expected max iterations error, got: %v", err)
	}
}

// TestOrchestrator_Run_ContextCanceled тестирует отмену контекста.
func TestOrchestrator_Run_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	// Сразу отменяем контекст
	cancel()

	mockLLM := &MockLLMProvider{
		Responses: []llm.Message{
			{Role: llm.RoleAssistant, Content: "never reached"},
		},
	}

	registry := tools.NewRegistry()
	state := setupTestState(t)

	orch, err := New(Config{
		LLM:      mockLLM,
		Registry: registry,
		State:    state,
	})
	if err != nil {
		t.Fatalf("failed to create orchestrator: %v", err)
	}

	_, err = orch.Run(ctx, "test")
	if err == nil {
		t.Fatal("expected context canceled error but got nil")
	}
}

// TestOrchestrator_ClearHistory тестирует очистку истории.
func TestOrchestrator_ClearHistory(t *testing.T) {
	state := setupTestState(t)

	// Добавляем сообщения в историю
	state.AppendMessage(llm.Message{Role: llm.RoleUser, Content: "test1"})
	state.AppendMessage(llm.Message{Role: llm.RoleUser, Content: "test2"})

	if len(state.GetHistory()) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(state.GetHistory()))
	}

	orch, err := New(Config{
		LLM:      &MockLLMProvider{},
		Registry: tools.NewRegistry(),
		State:    state,
	})
	if err != nil {
		t.Fatalf("failed to create orchestrator: %v", err)
	}

	orch.ClearHistory()

	if len(state.GetHistory()) != 0 {
		t.Errorf("expected empty history after clear, got %d messages", len(state.GetHistory()))
	}
}

// TestOrchestrator_SetSystemPrompt тестирует смену системного промпта.
func TestOrchestrator_SetSystemPrompt(t *testing.T) {
	state := setupTestState(t)

	orch, err := New(Config{
		LLM:      &MockLLMProvider{},
		Registry: tools.NewRegistry(),
		State:    state,
		SystemPrompt: "original",
	})
	if err != nil {
		t.Fatalf("failed to create orchestrator: %v", err)
	}

	newPrompt := "new system prompt"
	orch.SetSystemPrompt(newPrompt)

	if orch.systemPrompt != newPrompt {
		t.Errorf("expected system prompt %q, got %q", newPrompt, orch.systemPrompt)
	}
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
