// Package chain предоставляет Chain Pattern для AI агента.
package chain

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/events"
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

	// Verify ChainDebugRecorder could implement ExecutionObserver (Phase 4)
	// var _ ExecutionObserver = (*ChainDebugRecorder)(nil)
}
