// Package chain предоставляет Chain Pattern для AI агента.
package chain

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/events"
	"github.com/ilkoid/poncho-ai/pkg/llm"
	"github.com/ilkoid/poncho-ai/pkg/models"
	"github.com/ilkoid/poncho-ai/pkg/state"
	"github.com/ilkoid/poncho-ai/pkg/tools"
)

// TestStepExecutorInterface verifies that ReActExecutor implements StepExecutor.
func TestStepExecutorInterface(t *testing.T) {
	var _ StepExecutor = (*ReActExecutor)(nil)
}

// TestNewReActExecutor verifies executor creation.
func TestNewReActExecutor(t *testing.T) {
	executor := NewReActExecutor()

	if executor == nil {
		t.Fatal("NewReActExecutor returned nil")
	}

	if executor.observers == nil {
		t.Error("observers slice not initialized")
	}
}

// mockObserver is a mock ExecutionObserver for testing.
type mockObserver struct {
	startCalls          int
	iterationStartCalls int
	iterationEndCalls   int
	finishCalls         int
	lastResult          ChainOutput
	lastError           error
}

func (m *mockObserver) OnStart(ctx context.Context, exec *ReActExecution) {
	m.startCalls++
}

func (m *mockObserver) OnIterationStart(iteration int) {
	m.iterationStartCalls++
}

func (m *mockObserver) OnIterationEnd(iteration int) {
	m.iterationEndCalls++
}

func (m *mockObserver) OnFinish(result ChainOutput, err error) {
	m.finishCalls++
	m.lastResult = result
	m.lastError = err
}

// TestReActExecutorObserverNotifications verifies observer notifications.
func TestReActExecutorObserverNotifications(t *testing.T) {
	ctx := context.Background()

	// Create real components
	config := NewReActCycleConfig()
	config.MaxIterations = 1

	modelRegistry := models.NewRegistry()
	toolsRegistry := tools.NewRegistry()

	// Create template steps
	llmStepTemplate := &LLMInvocationStep{
		modelRegistry: modelRegistry,
		defaultModel:  "test-model",
		registry:      toolsRegistry,
		systemPrompt:  "test prompt",
	}

	toolStepTemplate := &ToolExecutionStep{
		registry:     toolsRegistry,
		promptLoader: nil,
	}

	// Create execution
	input := ChainInput{
		UserQuery: "test query",
		State:     state.NewCoreState(nil),
		Registry:  toolsRegistry,
	}

	execution := NewReActExecution(
		ctx,
		input,
		llmStepTemplate,
		toolStepTemplate,
		nil, // emitter
		nil, // debugRecorder
		false,
		&config,
	)

	// Create executor with observer
	executor := NewReActExecutor()
	observer := &mockObserver{}
	executor.AddObserver(observer)

	// Execute (will fail because no actual LLM, but we test observer notifications)
	_, err := executor.Execute(ctx, execution)

	// We expect an error since we don't have a real LLM configured
	if err == nil {
		t.Log("Note: Execute succeeded (unexpected in test setup)")
	}

	// Verify observer was notified at least once
	if observer.startCalls != 1 {
		t.Errorf("Expected 1 OnStart call, got %d", observer.startCalls)
	}

	if observer.finishCalls != 1 {
		t.Errorf("Expected 1 OnFinish call, got %d", observer.finishCalls)
	}
}

// TestReActExecutorMultipleObservers verifies multiple observers work correctly.
func TestReActExecutorMultipleObservers(t *testing.T) {
	ctx := context.Background()

	config := NewReActCycleConfig()
	config.MaxIterations = 1

	modelRegistry := models.NewRegistry()
	toolsRegistry := tools.NewRegistry()

	llmStepTemplate := &LLMInvocationStep{
		modelRegistry: modelRegistry,
		defaultModel:  "test-model",
		registry:      toolsRegistry,
		systemPrompt:  "test prompt",
	}

	toolStepTemplate := &ToolExecutionStep{
		registry:     toolsRegistry,
		promptLoader: nil,
	}

	input := ChainInput{
		UserQuery: "test query",
		State:     state.NewCoreState(nil),
		Registry:  toolsRegistry,
	}

	execution := NewReActExecution(
		ctx,
		input,
		llmStepTemplate,
		toolStepTemplate,
		nil,
		nil,
		false,
		&config,
	)

	// Create executor with multiple observers
	executor := NewReActExecutor()
	observer1 := &mockObserver{}
	observer2 := &mockObserver{}
	executor.AddObserver(observer1)
	executor.AddObserver(observer2)

	// Execute
	_, _ = executor.Execute(ctx, execution)

	// Verify both observers received notifications
	if observer1.startCalls != 1 {
		t.Errorf("Observer1: Expected 1 OnStart call, got %d", observer1.startCalls)
	}

	if observer2.startCalls != 1 {
		t.Errorf("Observer2: Expected 1 OnStart call, got %d", observer2.startCalls)
	}
}

// TestReActExecutionAsDataContainer verifies that ReActExecution is a pure data container.
func TestReActExecutionAsDataContainer(t *testing.T) {
	ctx := context.Background()
	config := NewReActCycleConfig()
	config.MaxIterations = 5

	// Create a mock model registry
	modelRegistry := models.NewRegistry()
	toolsRegistry := tools.NewRegistry()
	coreState := state.NewCoreState(nil)

	// Create template steps
	llmStepTemplate := &LLMInvocationStep{
		modelRegistry: modelRegistry,
		defaultModel:  "test-model",
		registry:      toolsRegistry,
		systemPrompt:  "test prompt",
	}

	toolStepTemplate := &ToolExecutionStep{
		registry:     toolsRegistry,
		promptLoader: nil,
	}

	emitter := events.NewChanEmitter(10)
	debugRecorder := &ChainDebugRecorder{}

	// Create execution
	input := ChainInput{
		UserQuery: "test query",
		State:     coreState,
		Registry:  toolsRegistry,
	}

	execution := NewReActExecution(
		ctx,
		input,
		llmStepTemplate,
		toolStepTemplate,
		emitter,
		debugRecorder,
		true,
		&config,
	)

	// Verify all fields are set correctly
	if execution.ctx != ctx {
		t.Error("ctx not set correctly")
	}

	if execution.chainCtx == nil {
		t.Error("chainCtx is nil")
	}

	if execution.llmStep == nil {
		t.Error("llmStep is nil")
	}

	if execution.toolStep == nil {
		t.Error("toolStep is nil")
	}

	if execution.emitter != emitter {
		t.Error("emitter not set correctly")
	}

	if execution.debugRecorder != debugRecorder {
		t.Error("debugRecorder not set correctly")
	}

	if !execution.streamingEnabled {
		t.Error("streamingEnabled not set correctly")
	}

	if execution.config != &config {
		t.Error("config not set correctly")
	}

	// Verify steps are cloned (not the same instance)
	if execution.llmStep == llmStepTemplate {
		t.Error("llmStep not cloned, same instance as template")
	}

	if execution.toolStep == toolStepTemplate {
		t.Error("toolStep not cloned, same instance as template")
	}

	// Verify emitter and debugRecorder are cloned to steps
	if execution.llmStep.emitter != emitter {
		t.Error("llmStep emitter not set correctly")
	}

	if execution.llmStep.debugRecorder != debugRecorder {
		t.Error("llmStep debugRecorder not set correctly")
	}

	if execution.toolStep.debugRecorder != debugRecorder {
		t.Error("toolStep debugRecorder not set correctly")
	}
}

// TestReActExecutionHelpers verifies helper methods on ReActExecution.
func TestReActExecutionHelpers(t *testing.T) {
	ctx := context.Background()
	config := NewReActCycleConfig()
	config.MaxIterations = 1

	modelRegistry := models.NewRegistry()
	toolsRegistry := tools.NewRegistry()

	llmStepTemplate := &LLMInvocationStep{
		modelRegistry: modelRegistry,
		defaultModel:  "test-model",
		registry:      toolsRegistry,
		systemPrompt:  "test prompt",
	}

	toolStepTemplate := &ToolExecutionStep{
		registry:     toolsRegistry,
		promptLoader: nil,
	}

	emitter := events.NewChanEmitter(10)
	debugRecorder := &ChainDebugRecorder{}

	input := ChainInput{
		UserQuery: "test query",
		State:     state.NewCoreState(nil),
		Registry:  toolsRegistry,
	}

	execution := NewReActExecution(
		ctx,
		input,
		llmStepTemplate,
		toolStepTemplate,
		emitter,
		debugRecorder,
		true,
		&config,
	)

	// Test emitEvent - should not panic
	execution.emitEvent(events.Event{
		Type:      events.EventThinking,
		Data:      events.ThinkingData{Query: "test"},
		Timestamp: time.Now(),
	})

	// Test endDebugIteration - should not panic
	execution.endDebugIteration()

	// Test finalizeWithError - should return empty output and error
	testErr := fmt.Errorf("test error")
	output, err := execution.finalizeWithError(testErr)

	if err != testErr {
		t.Errorf("Expected error %v, got %v", testErr, err)
	}

	if output.Result != "" {
		t.Error("Expected empty result on error")
	}
}

// TestStepExecutorInterfaceContract verifies the StepExecutor interface contract.
func TestStepExecutorInterfaceContract(t *testing.T) {
	// This test documents the expected behavior of StepExecutor implementations
	// Any type implementing StepExecutor must:
	//
	// 1. Accept a ReActExecution (data container)
	// 2. Execute the iteration loop
	// 3. Return ChainOutput or error
	// 4. Notify observers of lifecycle events
	//
	// ReActExecutor is the reference implementation.

	t.Skip("Documentation test - documents the StepExecutor interface contract")
}

// TestReActExecutorWithNilObservers verifies executor works with no observers.
func TestReActExecutorWithNilObservers(t *testing.T) {
	ctx := context.Background()

	config := NewReActCycleConfig()
	config.MaxIterations = 1

	modelRegistry := models.NewRegistry()
	toolsRegistry := tools.NewRegistry()

	llmStepTemplate := &LLMInvocationStep{
		modelRegistry: modelRegistry,
		defaultModel:  "test-model",
		registry:      toolsRegistry,
		systemPrompt:  "test prompt",
	}

	toolStepTemplate := &ToolExecutionStep{
		registry:     toolsRegistry,
		promptLoader: nil,
	}

	input := ChainInput{
		UserQuery: "test query",
		State:     state.NewCoreState(nil),
		Registry:  toolsRegistry,
	}

	execution := NewReActExecution(
		ctx,
		input,
		llmStepTemplate,
		toolStepTemplate,
		nil,
		nil,
		false,
		&config,
	)

	// Create executor without observers
	executor := NewReActExecutor()

	// Execute - should not panic even without observers
	_, _ = executor.Execute(ctx, execution)
}

// TestExecutionObserverInterface verifies ExecutionObserver interface.
func TestExecutionObserverInterface(t *testing.T) {
	// Verify mockObserver implements ExecutionObserver
	var _ ExecutionObserver = (*mockObserver)(nil)

	// Verify ChainDebugRecorder implements ExecutionObserver (PHASE 4)
	var _ ExecutionObserver = (*ChainDebugRecorder)(nil)

	// Verify EmitterObserver implements ExecutionObserver (PHASE 4)
	var _ ExecutionObserver = (*EmitterObserver)(nil)
}

// PHASE 4 REFACTOR: Tests for new observers

// TestEmitterObserver verifies EmitterObserver sends correct events.
func TestEmitterObserver(t *testing.T) {
	ctx := context.Background()
	config := NewReActCycleConfig()
	config.MaxIterations = 1

	// Create a mock emitter to capture events
	eventChan := make(chan events.Event, 10)
	mockEmitter := &mockEmitter{eventsCh: eventChan}

	// Create EmitterObserver
	observer := NewEmitterObserver(mockEmitter)

	// Create a mock execution
	modelRegistry := models.NewRegistry()
	toolsRegistry := tools.NewRegistry()

	llmStepTemplate := &LLMInvocationStep{
		modelRegistry: modelRegistry,
		defaultModel:  "test-model",
		registry:      toolsRegistry,
		systemPrompt:  "test prompt",
	}

	toolStepTemplate := &ToolExecutionStep{
		registry:     toolsRegistry,
		promptLoader: nil,
	}

	input := ChainInput{
		UserQuery: "test query",
		State:     state.NewCoreState(nil),
		Registry:  toolsRegistry,
	}

	execution := NewReActExecution(
		ctx,
		input,
		llmStepTemplate,
		toolStepTemplate,
		nil,
		nil,
		false,
		&config,
	)

	// Test OnStart (should not emit anything)
	observer.OnStart(ctx, execution)

	// Test OnFinish with success
	output := ChainOutput{
		Result:     "test result",
		Iterations: 1,
		Duration:   100 * time.Millisecond,
		Signal:     SignalFinalAnswer,
	}

	observer.OnFinish(output, nil)

	// Verify EventDone was sent
	select {
	case event := <-eventChan:
		if event.Type != events.EventDone {
			t.Errorf("Expected EventDone, got %v", event.Type)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("Expected EventDone event but received none")
	}

	// Test OnFinish with error
	testErr := fmt.Errorf("test error")
	observer.OnFinish(ChainOutput{}, testErr)

	// Verify EventError was sent
	select {
	case event := <-eventChan:
		if event.Type != events.EventError {
			t.Errorf("Expected EventError, got %v", event.Type)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("Expected EventError event but received none")
	}
}

// TestEmitterIterationObserver verifies EmitterIterationObserver sends correct events.
func TestEmitterIterationObserver(t *testing.T) {
	ctx := context.Background()

	// Create a mock emitter to capture events
	eventChan := make(chan events.Event, 10)
	mockEmitter := &mockEmitter{eventsSh: eventChan}

	// Create EmitterIterationObserver
	observer := NewEmitterIterationObserver(mockEmitter)

	// Test EmitThinking
	observer.EmitThinking(ctx, "test query")

	select {
	case event := <-eventChan:
		if event.Type != events.EventThinking {
			t.Errorf("Expected EventThinking, got %v", event.Type)
		}
		if data, ok := event.Data.(events.ThinkingData); ok {
			if data.Query != "test query" {
				t.Errorf("Expected query 'test query', got %v", data.Query)
			}
		} else {
			t.Error("Expected ThinkingData, got different type")
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("Expected EventThinking event but received none")
	}

	// Test EmitToolCall
	toolCall := llm.ToolCall{
		ID:   "call-123",
		Name: "test_tool",
		Args: `{"arg1": "value1"}`,
	}
	observer.EmitToolCall(ctx, toolCall)

	select {
	case event := <-eventChan:
		if event.Type != events.EventToolCall {
			t.Errorf("Expected EventToolCall, got %v", event.Type)
		}
		if data, ok := event.Data.(events.ToolCallData); ok {
			if data.ToolName != "test_tool" {
				t.Errorf("Expected tool name 'test_tool', got %v", data.ToolName)
			}
		} else {
			t.Error("Expected ToolCallData, got different type")
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("Expected EventToolCall event but received none")
	}

	// Test EmitToolResult
	observer.EmitToolResult(ctx, "test_tool", `{"result": "success"}`, 50*time.Millisecond)

	select {
	case event := <-eventChan:
		if event.Type != events.EventToolResult {
			t.Errorf("Expected EventToolResult, got %v", event.Type)
		}
		if data, ok := event.Data.(events.ToolResultData); ok {
			if data.ToolName != "test_tool" {
				t.Errorf("Expected tool name 'test_tool', got %v", data.ToolName)
			}
			if data.Duration != 50*time.Millisecond {
				t.Errorf("Expected duration 50ms, got %v", data.Duration)
			}
		} else {
			t.Error("Expected ToolResultData, got different type")
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("Expected EventToolResult event but received none")
	}

	// Test EmitMessage
	observer.EmitMessage(ctx, "test message")

	select {
	case event := <-eventChan:
		if event.Type != events.EventMessage {
			t.Errorf("Expected EventMessage, got %v", event.Type)
		}
		if data, ok := event.Data.(events.MessageData); ok {
			if data.Content != "test message" {
				t.Errorf("Expected content 'test message', got %v", data.Content)
			}
		} else {
			t.Error("Expected MessageData, got different type")
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("Expected EventMessage event but received none")
	}
}

// TestEmitterIterationObserverWithNilEmitter verifies observer works with nil emitter.
func TestEmitterIterationObserverWithNilEmitter(t *testing.T) {
	ctx := context.Background()

	// Create observer with nil emitter
	observer := NewEmitterIterationObserver(nil)

	// Should not panic
	observer.EmitThinking(ctx, "test query")
	observer.EmitToolCall(ctx, llm.ToolCall{})
	observer.EmitToolResult(ctx, "tool", "result", 0)
	observer.EmitMessage(ctx, "message")
}

// mockEmitter is a mock Emitter for testing.
type mockEmitter struct {
	eventsSh chan events.Event
	eventsCh chan events.Event // alias for compatibility
}

func (m *mockEmitter) Emit(ctx context.Context, event events.Event) {
	if m.eventsSh != nil {
		select {
		case m.eventsSh <- event:
		case <-ctx.Done():
		}
	}
	if m.eventsCh != nil {
		select {
		case m.eventsCh <- event:
		case <-ctx.Done():
		}
	}
}
