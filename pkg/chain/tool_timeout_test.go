// Package chain предоставляет тесты для Tool Timeout protection.
package chain

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/llm"
	"github.com/ilkoid/poncho-ai/pkg/state"
	"github.com/ilkoid/poncho-ai/pkg/tools"
)

// mockSlowTool — инструмент который "зависает" на указанное время.
type mockSlowTool struct {
	delay time.Duration
}

func (m *mockSlowTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "slow_tool",
		Description: "A tool that hangs for a while",
		Parameters: tools.JSONSchema{
			"type": "object",
			"properties": map[string]any{
				"input": map[string]any{
					"type":        "string",
					"description": "input",
				},
			},
		},
	}
}

func (m *mockSlowTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	// Имитируем долгую операцию
	select {
	case <-time.After(m.delay):
		return fmt.Sprintf("completed after %v", m.delay), nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// TestToolTimeoutProtection проверяет что tool timeout работает.
func TestToolTimeoutProtection(t *testing.T) {
	// Создаём registry
	registry := tools.NewRegistry()

	// Регистрируем медленный tool (5 секунд задержки)
	slowTool := &mockSlowTool{delay: 5 * time.Second}
	if err := registry.Register(slowTool); err != nil {
		t.Fatalf("Failed to register tool: %v", err)
	}

	// Создаём ToolExecutionStep с коротким timeout (1 секунда)
	step := &ToolExecutionStep{
		registry:          registry,
		defaultToolTimeout: 1 * time.Second,
		toolTimeouts:      make(map[string]time.Duration),
	}

	// Создаём ChainContext с реальным CoreState
	chainCtx := NewChainContext(ChainInput{
		UserQuery: "test",
		State:     state.NewCoreState(nil),
		Registry:  registry,
	})

	// Добавляем assistant message с tool call
	chainCtx.AppendMessage(llm.Message{
		Role: llm.RoleAssistant,
		ToolCalls: []llm.ToolCall{
			{ID: "1", Name: "slow_tool", Args: `{"input":"test"}`},
		},
	})

	// Выполняем tool
	tc := llm.ToolCall{ID: "1", Name: "slow_tool", Args: `{"input":"test"}`}
	start := time.Now()
	result, err := step.executeToolCall(context.Background(), tc, chainCtx)
	duration := time.Since(start)

	// Проверяем что timeout сработал
	if err == nil {
		t.Errorf("Expected timeout error, got nil")
	}

	if result.Success {
		t.Errorf("Expected result.Success=false, got true")
	}

	if result.Error == nil {
		t.Errorf("Expected result.Error to be set, got nil")
	}

	// Проверяем что время выполнения близко к timeout (1 секунда + небольшая погрешность)
	if duration < 1*time.Second || duration > 2*time.Second {
		t.Errorf("Expected duration ~1s, got %v", duration)
	}

	t.Logf("Tool timeout protection working: cancelled after %v (expected ~1s)", duration)
	t.Logf("Error: %v", result.Error)
}

// TestToolTimeoutCustom проверяет что custom timeout переопределяет default.
func TestToolTimeoutCustom(t *testing.T) {
	registry := tools.NewRegistry()

	// Регистрируем быстрый tool
	quickTool := &mockSlowTool{delay: 100 * time.Millisecond}
	if err := registry.Register(quickTool); err != nil {
		t.Fatalf("Failed to register tool: %v", err)
	}

	step := &ToolExecutionStep{
		registry:           registry,
		defaultToolTimeout: 5 * time.Second, // Длинный дефолтный timeout
		toolTimeouts:       make(map[string]time.Duration),
		promptLoader:       nil,
		debugRecorder:      nil,
	}

	// Устанавливаем короткий custom timeout для этого tool
	step.SetToolTimeout("slow_tool", 200*time.Millisecond)

	// Проверяем что custom timeout переопределился
	timeout := step.defaultToolTimeout
	custom, exists := step.toolTimeouts["slow_tool"]
	if !exists {
		t.Errorf("Expected custom timeout to be set for slow_tool")
	}
	if custom != 200*time.Millisecond {
		t.Errorf("Expected custom timeout 200ms, got %v", custom)
	}
	t.Logf("Default timeout: %v, Custom timeout for slow_tool: %v", timeout, custom)
}

// TestToolTimeoutSuccess проверяет что tool顺利完成 если не превышает timeout.
func TestToolTimeoutSuccess(t *testing.T) {
	registry := tools.NewRegistry()

	// Регистрируем быстрый tool (100ms)
	quickTool := &mockSlowTool{delay: 100 * time.Millisecond}
	if err := registry.Register(quickTool); err != nil {
		t.Fatalf("Failed to register tool: %v", err)
	}

	step := &ToolExecutionStep{
		registry:           registry,
		defaultToolTimeout: 1 * time.Second,
		toolTimeouts:       make(map[string]time.Duration),
		promptLoader:       nil,
		debugRecorder:      nil,
	}

	chainCtx := NewChainContext(ChainInput{
		UserQuery: "test",
		State:     state.NewCoreState(nil),
		Registry:  registry,
	})

	chainCtx.AppendMessage(llm.Message{
		Role: llm.RoleAssistant,
		ToolCalls: []llm.ToolCall{
			{ID: "1", Name: "slow_tool", Args: `{"input":"test"}`},
		},
	})

	tc := llm.ToolCall{ID: "1", Name: "slow_tool", Args: `{"input":"test"}`}
	result, err := step.executeToolCall(context.Background(), tc, chainCtx)

	// Проверяем что tool завершился успешно
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	if !result.Success {
		t.Errorf("Expected result.Success=true, got false. Result: %s", result.Result)
	}

	t.Logf("Tool completed successfully within timeout: %v", time.Duration(result.Duration)*time.Millisecond)
}

// mockFailingTool — инструмент который возвращает ошибку.
type mockFailingTool struct{}

func (m *mockFailingTool) Definition() tools.ToolDefinition {
	return tools.ToolDefinition{
		Name:        "failing_tool",
		Description: "A tool that always fails",
		Parameters:  tools.JSONSchema{"type": "object"},
	}
}

func (m *mockFailingTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	return "", errors.New("tool execution failed")
}

// TestToolTimeoutWithError проверяет что ошибки tool корректно обрабатываются.
func TestToolTimeoutWithError(t *testing.T) {
	registry := tools.NewRegistry()

	failingTool := &mockFailingTool{}
	if err := registry.Register(failingTool); err != nil {
		t.Fatalf("Failed to register tool: %v", err)
	}

	step := &ToolExecutionStep{
		registry:           registry,
		defaultToolTimeout: 5 * time.Second,
		toolTimeouts:       make(map[string]time.Duration),
		promptLoader:       nil,
		debugRecorder:      nil,
	}

	chainCtx := NewChainContext(ChainInput{
		UserQuery: "test",
		State:     state.NewCoreState(nil),
		Registry:  registry,
	})

	chainCtx.AppendMessage(llm.Message{
		Role: llm.RoleAssistant,
		ToolCalls: []llm.ToolCall{
			{ID: "1", Name: "failing_tool", Args: `{}`},
		},
	})

	tc := llm.ToolCall{ID: "1", Name: "failing_tool", Args: `{}`}
	result, err := step.executeToolCall(context.Background(), tc, chainCtx)

	// Tool должен вернуть ошибку в result.Error (не критическая ошибка)
	// executeToolCall возвращает nil как err, потому что non-critical ошибки tools не прерывают workflow
	if err != nil {
		t.Errorf("Expected nil error from executeToolCall (non-critical), got %v", err)
	}

	if result.Success {
		t.Errorf("Expected result.Success=false for failing tool, got true")
	}

	if result.Error == nil {
		t.Errorf("Expected result.Error to be set for failing tool, got nil")
	}

	t.Logf("Failing tool error correctly handled: %v", result.Error)
}
