// Package chain предоставляет Chain Pattern для AI агента.
package chain

import (
	"context"
	"fmt"
	"testing"
)

// TestStepCreation verifies StepResult creation with various actions and signals.
func TestStepCreation(t *testing.T) {
	tests := []struct {
		name     string
		result   StepResult
		wantAction NextAction
		wantSignal ExecutionSignal
	}{
		{
			name: "Continue with no signal",
			result: StepResult{
				Action: ActionContinue,
				Signal: SignalNone,
			},
			wantAction: ActionContinue,
			wantSignal: SignalNone,
		},
		{
			name: "Break with final answer signal",
			result: StepResult{
				Action: ActionBreak,
				Signal: SignalFinalAnswer,
			},
			wantAction: ActionBreak,
			wantSignal: SignalFinalAnswer,
		},
		{
			name: "Break with need user input signal",
			result: StepResult{
				Action: ActionBreak,
				Signal: SignalNeedUserInput,
			},
			wantAction: ActionBreak,
			wantSignal: SignalNeedUserInput,
		},
		{
			name: "Error with error signal",
			result: StepResult{
				Action: ActionError,
				Signal: SignalError,
				Error:  nil,
			},
			wantAction: ActionError,
			wantSignal: SignalError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.result.Action != tt.wantAction {
				t.Errorf("Action = %v, want %v", tt.result.Action, tt.wantAction)
			}
			if tt.result.Signal != tt.wantSignal {
				t.Errorf("Signal = %v, want %v", tt.result.Signal, tt.wantSignal)
			}
		})
	}
}

// TestStepResultWithError verifies WithError helper method.
func TestStepResultWithError(t *testing.T) {
	baseResult := StepResult{
		Action: ActionContinue,
		Signal: SignalNone,
	}

	err := fmt.Errorf("test error")
	result := baseResult.WithError(err)

	if result.Action != ActionError {
		t.Errorf("Action = %v, want %v", result.Action, ActionError)
	}
	if result.Signal != SignalError {
		t.Errorf("Signal = %v, want %v", result.Signal, SignalError)
	}
	if result.Error != err {
		t.Errorf("Error = %v, want %v", result.Error, err)
	}
}

// TestExecutionSignalString verifies String() method for ExecutionSignal.
func TestExecutionSignalString(t *testing.T) {
	tests := []struct {
		signal   ExecutionSignal
		expected string
	}{
		{SignalNone, "None"},
		{SignalFinalAnswer, "FinalAnswer"},
		{SignalNeedUserInput, "NeedUserInput"},
		{SignalError, "Error"},
		{ExecutionSignal(999), "Unknown(999)"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.signal.String(); got != tt.expected {
				t.Errorf("String() = %v, want %v", got, tt.expected)
			}
		})
	}
}

// TestNextActionString verifies String() method for NextAction.
func TestNextActionString(t *testing.T) {
	tests := []struct {
		action   NextAction
		expected string
	}{
		{ActionContinue, "Continue"},
		{ActionBreak, "Break"},
		{ActionError, "Error"},
		{NextAction(999), "Unknown(999)"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.action.String(); got != tt.expected {
				t.Errorf("String() = %v, want %v", got, tt.expected)
			}
		})
	}
}

// TestStepResultString verifies String() method for StepResult.
func TestStepResultString(t *testing.T) {
	result := StepResult{
		Action: ActionContinue,
		Signal: SignalNone,
		Error:  nil,
	}

	got := result.String()
	expected := "StepResult{Action: Continue, Signal: None, Error: <nil>}"

	if got != expected {
		t.Errorf("String() = %v, want %v", got, expected)
	}
}

// mockStepForSignals is a mock Step implementation for testing signals.
type mockStepForSignals struct {
	name   string
	result StepResult
}

func (m *mockStepForSignals) Name() string {
	return m.name
}

func (m *mockStepForSignals) Execute(ctx context.Context, chainCtx *ChainContext) StepResult {
	return m.result
}

// TestStepInterfaceWithStepResult verifies that Step interface works with StepResult.
func TestStepInterfaceWithStepResult(t *testing.T) {
	ctx := context.Background()
	chainCtx := NewChainContext(ChainInput{
		UserQuery: "test",
		State:     nil,
		Registry:  nil,
	})

	tests := []struct {
		name     string
		step     Step
		wantAction NextAction
		wantSignal ExecutionSignal
	}{
		{
			name: "Continue step",
			step: &mockStepForSignals{
				name: "continue_step",
				result: StepResult{
					Action: ActionContinue,
					Signal: SignalNone,
				},
			},
			wantAction: ActionContinue,
			wantSignal: SignalNone,
		},
		{
			name: "Final answer step",
			step: &mockStepForSignals{
				name: "final_step",
				result: StepResult{
					Action: ActionBreak,
					Signal: SignalFinalAnswer,
				},
			},
			wantAction: ActionBreak,
			wantSignal: SignalFinalAnswer,
		},
		{
			name: "Need user input step",
			step: &mockStepForSignals{
				name: "input_step",
				result: StepResult{
					Action: ActionBreak,
					Signal: SignalNeedUserInput,
				},
			},
			wantAction: ActionBreak,
			wantSignal: SignalNeedUserInput,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.step.Execute(ctx, chainCtx)

			if result.Action != tt.wantAction {
				t.Errorf("Action = %v, want %v", result.Action, tt.wantAction)
			}
			if result.Signal != tt.wantSignal {
				t.Errorf("Signal = %v, want %v", result.Signal, tt.wantSignal)
			}
		})
	}
}

// TestChainOutputWithSignal verifies ChainOutput includes Signal field.
func TestChainOutputWithSignal(t *testing.T) {
	tests := []struct {
		name   string
		output ChainOutput
		signal ExecutionSignal
	}{
		{
			name: "Normal completion",
			output: ChainOutput{
				Result:     "Hello, world!",
				Iterations: 1,
				Signal:     SignalFinalAnswer,
			},
			signal: SignalFinalAnswer,
		},
		{
			name: "Need user input",
			output: ChainOutput{
				Result:     "Please provide more information",
				Iterations: 2,
				Signal:     SignalNeedUserInput,
			},
			signal: SignalNeedUserInput,
		},
		{
			name: "No signal",
			output: ChainOutput{
				Result:     "Some result",
				Iterations: 3,
				Signal:     SignalNone,
			},
			signal: SignalNone,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.output.Signal != tt.signal {
				t.Errorf("Signal = %v, want %v", tt.output.Signal, tt.signal)
			}
		})
	}
}

// TestSignalConstants verifies signal constants are unique.
func TestSignalConstants(t *testing.T) {
	signals := []ExecutionSignal{
		SignalNone,
		SignalFinalAnswer,
		SignalNeedUserInput,
		SignalError,
	}

	// Verify all constants have unique values
	seen := make(map[ExecutionSignal]bool)
	for _, sig := range signals {
		if seen[sig] {
			t.Errorf("Duplicate signal value detected: %v", sig)
		}
		seen[sig] = true
	}

	// Verify we have exactly 4 unique signals
	if len(seen) != 4 {
		t.Errorf("Expected 4 unique signals, got %d", len(seen))
	}
}

// TestActionConstants verifies action constants are unique.
func TestActionConstants(t *testing.T) {
	actions := []NextAction{
		ActionContinue,
		ActionBreak,
		ActionError,
	}

	// Verify all constants have unique values
	seen := make(map[NextAction]bool)
	for _, action := range actions {
		if seen[action] {
			t.Errorf("Duplicate action value detected: %v", action)
		}
		seen[action] = true
	}

	// Verify we have exactly 3 unique actions
	if len(seen) != 3 {
		t.Errorf("Expected 3 unique actions, got %d", len(seen))
	}
}

// TestStepResultZeroValue verifies zero value of StepResult.
func TestStepResultZeroValue(t *testing.T) {
	var result StepResult

	if result.Action != ActionContinue {
		t.Errorf("Zero value Action = %v, want %v", result.Action, ActionContinue)
	}
	if result.Signal != SignalNone {
		t.Errorf("Zero value Signal = %v, want %v", result.Signal, SignalNone)
	}
	if result.Error != nil {
		t.Errorf("Zero value Error = %v, want nil", result.Error)
	}
}

// TestSignalPropagation verifies signals propagate through execution.
func TestSignalPropagation(t *testing.T) {
	// This test verifies that when a Step returns a signal,
	// it properly propagates through the execution chain.

	ctx := context.Background()
	chainCtx := NewChainContext(ChainInput{
		UserQuery: "test",
		State:     nil,
		Registry:  nil,
	})

	// Test final answer signal propagation
	finalStep := &mockStepForSignals{
		name: "final",
		result: StepResult{
			Action: ActionBreak,
			Signal: SignalFinalAnswer,
		},
	}

	result := finalStep.Execute(ctx, chainCtx)
	if result.Signal != SignalFinalAnswer {
		t.Errorf("Expected SignalFinalAnswer, got %v", result.Signal)
	}

	// Test need user input signal propagation
	inputStep := &mockStepForSignals{
		name: "input",
		result: StepResult{
			Action: ActionBreak,
			Signal: SignalNeedUserInput,
		},
	}

	result = inputStep.Execute(ctx, chainCtx)
	if result.Signal != SignalNeedUserInput {
		t.Errorf("Expected SignalNeedUserInput, got %v", result.Signal)
	}
}
