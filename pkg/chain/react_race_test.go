// Package chain предоставляет Chain Pattern для AI агента.
package chain

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/llm"
	"github.com/ilkoid/poncho-ai/pkg/models"
	"github.com/ilkoid/poncho-ai/pkg/state"
	"github.com/ilkoid/poncho-ai/pkg/tools"
)

// mockLLMProvider — mock implementation of llm.Provider for testing.
type mockLLMProvider struct {
	responses chan llm.Message
	mu        sync.Mutex
}

func newMockLLMProvider() *mockLLMProvider {
	return &mockLLMProvider{
		responses: make(chan llm.Message, 100),
	}
}

func (m *mockLLMProvider) Generate(_ context.Context, messages []llm.Message, opts ...any) (llm.Message, error) {
	// Return a simple response (no tool calls)
	return llm.Message{
		Role:    llm.RoleAssistant,
		Content: fmt.Sprintf("Mock response for: %s", messages[len(messages)-1].Content),
	}, nil
}

func (m *mockLLMProvider) Close() error {
	return nil
}

// setupTestCycle создаёт ReActCycle для тестирования.
func setupTestCycle(t *testing.T) *ReActCycle {
	t.Helper()

	// Create model registry with mock provider
	registry := models.NewRegistry()
	modelConfig := config.ModelDef{
		Provider:  "mock",
		APIKey:    "test-key",
		BaseURL:   "https://mock.example.com",
		ModelName: "mock-model",
		Temperature: 0.5,
		MaxTokens:   1000,
	}

	// Create mock provider
	mockProvider := newMockLLMProvider()

	if err := registry.Register("mock-model", modelConfig, mockProvider); err != nil {
		t.Fatalf("Failed to register model: %v", err)
	}

	// Create tools registry
	toolsRegistry := tools.NewRegistry()

	// Create core state with minimal config
	appCfg := &config.AppConfig{
		Models: config.ModelsConfig{
			DefaultReasoning: "mock-model",
			DefaultChat:      "mock-model",
		},
	}
	coreState := state.NewCoreState(appCfg)

	// Create ReActCycle config
	cfg := NewReActCycleConfig()
	cfg.MaxIterations = 3

	// Create ReActCycle
	cycle := NewReActCycle(cfg)
	cycle.SetModelRegistry(registry, "mock-model")
	cycle.SetRegistry(toolsRegistry)
	cycle.SetState(coreState)

	return cycle
}

// TestConcurrentExecution проверяет что несколько Execute()
// могут работать одновременно без data races.
//
// PHASE 1 REFACTOR: Этот тест подтверждает что удаление mutex
// не нарушает thread-safety и позволяет concurrent execution.
func TestConcurrentExecution(t *testing.T) {
	cycle := setupTestCycle(t)
	ctx := context.Background()

	// Запускаем 10 параллельных выполнений
	const numGoroutines = 10
	var wg sync.WaitGroup
	errors := make(chan error, numGoroutines)
	results := make(chan ChainOutput, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			input := ChainInput{
				UserQuery: fmt.Sprintf("Test query %d", idx),
				State:     state.NewCoreState(nil),
				Registry:  tools.NewRegistry(),
			}
			output, err := cycle.Execute(ctx, input)
			if err != nil {
				errors <- err
				return
			}
			results <- output
		}(i)
	}

	wg.Wait()
	close(errors)
	close(results)

	// Проверяем что нет ошибок
	for err := range errors {
		t.Errorf("Concurrent execution failed: %v", err)
	}

	// Проверяем что получили все результаты
	resultCount := 0
	for range results {
		resultCount++
	}

	if resultCount != numGoroutines {
		t.Errorf("Expected %d results, got %d", numGoroutines, resultCount)
	}
}

// TestConcurrentRun проверяет что несколько Run()
// могут работать одновременно без data races.
func TestConcurrentRun(t *testing.T) {
	cycle := setupTestCycle(t)
	ctx := context.Background()

	// Запускаем 10 параллельных Run вызовов
	const numGoroutines = 10
	var wg sync.WaitGroup
	errors := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, err := cycle.Run(ctx, fmt.Sprintf("Test query %d", idx))
			if err != nil {
				errors <- err
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	// Проверяем что нет ошибок
	for err := range errors {
		t.Errorf("Concurrent Run failed: %v", err)
	}
}

// TestConcurrentSetters проверяет что setters могут вызываться
// concurrently во время выполнения Execute.
func TestConcurrentSetters(t *testing.T) {
	cycle := setupTestCycle(t)
	ctx := context.Background()

	var wg sync.WaitGroup
	errors := make(chan error, 20)

	// Запускаем 10 параллельных Execute
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			input := ChainInput{
				UserQuery: fmt.Sprintf("Test query %d", idx),
				State:     state.NewCoreState(nil),
				Registry:  tools.NewRegistry(),
			}
			_, err := cycle.Execute(ctx, input)
			if err != nil {
				errors <- err
			}
		}(i)
	}

	// Запускаем 10 параллельных Setters
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			switch idx % 3 {
			case 0:
				cycle.SetStreamingEnabled(idx%2 == 0)
			case 1:
				cycle.SetEmitter(nil)
			case 2:
				cycle.AttachDebug(nil)
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	// Проверяем что нет ошибок
	for err := range errors {
		t.Errorf("Concurrent operations failed: %v", err)
	}
}

// BenchmarkConcurrentExecution бенчмарк для concurrent execution.
func BenchmarkConcurrentExecution(b *testing.B) {
	cycle := setupTestCycle(&testing.T{})
	ctx := context.Background()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		idx := 0
		for pb.Next() {
			idx++
			input := ChainInput{
				UserQuery: fmt.Sprintf("Benchmark query %d", idx),
				State:     state.NewCoreState(nil),
				Registry:  tools.NewRegistry(),
			}
			_, _ = cycle.Execute(ctx, input)
		}
	})
}

// BenchmarkSequentialExecution бенчмарк для sequential execution (для сравнения).
func BenchmarkSequentialExecution(b *testing.B) {
	cycle := setupTestCycle(&testing.T{})
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		input := ChainInput{
			UserQuery: fmt.Sprintf("Benchmark query %d", i),
			State:     state.NewCoreState(nil),
			Registry:  tools.NewRegistry(),
		}
		_, _ = cycle.Execute(ctx, input)
	}
}

// TestExecutionIsolation проверяет что разные execution
// не влияют друг на друга.
func TestExecutionIsolation(t *testing.T) {
	cycle := setupTestCycle(t)
	ctx := context.Background()

	// Create two executions with different queries
	input1 := ChainInput{
		UserQuery: "Query 1",
		State:     state.NewCoreState(nil),
		Registry:  tools.NewRegistry(),
	}
	input2 := ChainInput{
		UserQuery: "Query 2",
		State:     state.NewCoreState(nil),
		Registry:  tools.NewRegistry(),
	}

	// Execute them concurrently
	var wg sync.WaitGroup
	var result1, result2 ChainOutput

	wg.Add(2)
	go func() {
		defer wg.Done()
		var err error
		result1, err = cycle.Execute(ctx, input1)
		if err != nil {
			t.Errorf("Execution 1 failed: %v", err)
		}
	}()
	go func() {
		defer wg.Done()
		var err error
		result2, err = cycle.Execute(ctx, input2)
		if err != nil {
			t.Errorf("Execution 2 failed: %v", err)
		}
	}()

	wg.Wait()

	// Verify results are different
	if result1.Result == result2.Result {
		t.Error("Expected different results for different queries")
	}

	// Verify each result contains its query
	if !contains(result1.Result, "Query 1") {
		t.Errorf("Result 1 should contain 'Query 1', got: %s", result1.Result)
	}
	if !contains(result2.Result, "Query 2") {
		t.Errorf("Result 2 should contain 'Query 2', got: %s", result2.Result)
	}
}

// contains проверяет что строка содержит подстроку (case-insensitive).
func contains(s, substr string) bool {
	return len(s) >= len(substr) &&
		(len(substr) == 0 || s == substr ||
			len(s) > 0 && (s[:len(substr)] == substr ||
				s[len(s)-len(substr):] == substr ||
			 indexOf(s, substr) >= 0))
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

// TestStreamingToggle проверяет что streaming флаг можно менять
// между выполнениями.
func TestStreamingToggle(t *testing.T) {
	cycle := setupTestCycle(t)
	ctx := context.Background()

	// Execute with streaming enabled
	cycle.SetStreamingEnabled(true)
	input := ChainInput{
		UserQuery: "Test with streaming",
		State:     state.NewCoreState(nil),
		Registry:  tools.NewRegistry(),
	}
	_, err := cycle.Execute(ctx, input)
	if err != nil {
		t.Fatalf("Execute with streaming failed: %v", err)
	}

	// Execute with streaming disabled
	cycle.SetStreamingEnabled(false)
	input2 := ChainInput{
		UserQuery: "Test without streaming",
		State:     state.NewCoreState(nil),
		Registry:  tools.NewRegistry(),
	}
	_, err = cycle.Execute(ctx, input2)
	if err != nil {
		t.Fatalf("Execute without streaming failed: %v", err)
	}
}

// TestConcurrentWithTimeout проверяет concurrent execution с timeout.
func TestConcurrentWithTimeout(t *testing.T) {
	cycle := setupTestCycle(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	const numGoroutines = 5
	var wg sync.WaitGroup
	errors := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			input := ChainInput{
				UserQuery: fmt.Sprintf("Timeout test %d", idx),
				State:     state.NewCoreState(nil),
				Registry:  tools.NewRegistry(),
			}
			_, err := cycle.Execute(ctx, input)
			if err != nil {
				errors <- err
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	// Проверяем что нет ошибок (включая context.DeadlineExceeded)
	for err := range errors {
		t.Errorf("Concurrent execution with timeout failed: %v", err)
	}
}
