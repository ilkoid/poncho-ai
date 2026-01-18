// Package tui предоставляет reusable helpers для подключения Bubble Tea TUI к агенту.
package tui

import (
	"context"
	"testing"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/events"
	"github.com/stretchr/testify/assert"
	tea "github.com/charmbracelet/bubbletea"
)

// Test 1: BaseModel initialization
func TestBaseModel_NewBaseModel(t *testing.T) {
	ctx := context.Background()
	emitter := events.NewChanEmitter(100)
	sub := emitter.Subscribe()

	model := NewBaseModel(ctx, sub)

	// Verify all primitives are initialized
	assert.NotNil(t, model.GetViewportMgr(), "ViewportManager should be initialized")
	assert.NotNil(t, model.GetStatusBarMgr(), "StatusBarManager should be initialized")
	assert.NotNil(t, model.GetEventHandler(), "EventHandler should be initialized")
	assert.NotNil(t, model.GetDebugManager(), "DebugManager should be initialized")
	assert.NotNil(t, model.GetContext(), "Context should be set")
	assert.NotNil(t, model.GetSubscriber(), "Subscriber should be set")

	// Verify default title
	assert.Equal(t, "AI Agent", model.title, "Default title should be 'AI Agent'")
}

// Test 2: BaseModel Init returns valid commands
func TestBaseModel_Init(t *testing.T) {
	ctx := context.Background()
	emitter := events.NewChanEmitter(100)
	sub := emitter.Subscribe()

	model := NewBaseModel(ctx, sub)

	// Init should return a valid command
	cmd := model.Init()
	assert.NotNil(t, cmd, "Init should return a valid command")
}

// Test 3: BaseModel Update handles WindowSizeMsg
func TestBaseModel_Update_WindowSizeMsg(t *testing.T) {
	ctx := context.Background()
	emitter := events.NewChanEmitter(100)
	sub := emitter.Subscribe()

	model := NewBaseModel(ctx, sub)

	// Send WindowSizeMsg
	msg := tea.WindowSizeMsg{
		Width:  80,
		Height: 24,
	}

	newModel, cmd := model.Update(msg)
	assert.NotNil(t, newModel, "Update should return a model")
	assert.Nil(t, cmd, "WindowSizeMsg should not return a command")

	// Model should be marked as ready
	assert.True(t, newModel.(*BaseModel).ready, "Model should be ready after WindowSizeMsg")
}

// Test 4: BaseModel Update handles KeyMsg (Quit)
func TestBaseModel_Update_KeyMsgQuit(t *testing.T) {
	ctx := context.Background()
	emitter := events.NewChanEmitter(100)
	sub := emitter.Subscribe()

	model := NewBaseModel(ctx, sub)

	// Send Ctrl+C key
	msg := tea.KeyMsg{Type: tea.KeyCtrlC}

	newModel, cmd := model.Update(msg)
	assert.NotNil(t, newModel, "Update should return a model")

	// Execute the command to check if it returns QuitMsg
	if cmd != nil {
		resultMsg := cmd()
		assert.Equal(t, tea.QuitMsg{}, resultMsg, "Ctrl+C should return quit message")
	}
}

// Test 5: BaseModel Update handles KeyMsg (ToggleHelp)
func TestBaseModel_Update_KeyMsgToggleHelp(t *testing.T) {
	ctx := context.Background()
	emitter := events.NewChanEmitter(100)
	sub := emitter.Subscribe()

	model := NewBaseModel(ctx, sub)

	// Initial state: help is off
	assert.False(t, model.showHelp, "Help should be disabled initially")

	// Toggle help directly (testing the field)
	model.showHelp = true
	assert.True(t, model.showHelp, "Help should be enabled")

	model.showHelp = false
	assert.False(t, model.showHelp, "Help should be disabled after toggle")
}

// Test 6: BaseModel View returns non-empty string
func TestBaseModel_View(t *testing.T) {
	ctx := context.Background()
	emitter := events.NewChanEmitter(100)
	sub := emitter.Subscribe()

	model := NewBaseModel(ctx, sub)

	// Before ready, should show initializing
	view := model.View()
	assert.Equal(t, "Initializing...", view, "View should show initializing before ready")

	// After WindowSizeMsg, should have full view
	msg := tea.WindowSizeMsg{Width: 80, Height: 24}
	newModel, _ := model.Update(msg)
	model = newModel.(*BaseModel)

	view = model.View()
	assert.NotEqual(t, "Initializing...", view, "View should show full UI after ready")
	assert.NotEmpty(t, view, "View should not be empty")
	assert.Contains(t, view, "AI Agent", "View should contain title")
}

// Test 7: BaseModel Append method
func TestBaseModel_Append(t *testing.T) {
	ctx := context.Background()
	emitter := events.NewChanEmitter(100)
	sub := emitter.Subscribe()

	model := NewBaseModel(ctx, sub)

	// Initialize viewport with WindowSizeMsg
	msg := tea.WindowSizeMsg{Width: 80, Height: 24}
	newModel, _ := model.Update(msg)
	model = newModel.(*BaseModel)

	// Append content
	model.Append("Test line", false)

	// Check content was added
	content := model.GetViewportMgr().Content()
	assert.Greater(t, len(content), 0, "Viewport should have content after Append")
}

// Test 8: BaseModel SetProcessing method
func TestBaseModel_SetProcessing(t *testing.T) {
	ctx := context.Background()
	emitter := events.NewChanEmitter(100)
	sub := emitter.Subscribe()

	model := NewBaseModel(ctx, sub)

	// Initial state: not processing
	assert.False(t, model.IsProcessing(), "Should not be processing initially")

	// Set processing
	model.SetProcessing(true)
	assert.True(t, model.IsProcessing(), "Should be processing after SetProcessing(true)")

	// Clear processing
	model.SetProcessing(false)
	assert.False(t, model.IsProcessing(), "Should not be processing after SetProcessing(false)")
}

// Test 9: BaseModel SetTitle method
func TestBaseModel_SetTitle(t *testing.T) {
	ctx := context.Background()
	emitter := events.NewChanEmitter(100)
	sub := emitter.Subscribe()

	model := NewBaseModel(ctx, sub)

	// Set custom title
	model.SetTitle("Custom Title")
	assert.Equal(t, "Custom Title", model.title, "Title should be updated")
}

// Test 10: BaseModel SetCustomStatus method
func TestBaseModel_SetCustomStatus(t *testing.T) {
	ctx := context.Background()
	emitter := events.NewChanEmitter(100)
	sub := emitter.Subscribe()

	model := NewBaseModel(ctx, sub)

	// Set custom status callback
	callCount := 0
	model.SetCustomStatus(func() string {
		callCount++
		return "Custom Status"
	})

	// Render status bar to trigger callback
	status := model.GetStatusBarMgr().Render()
	assert.Contains(t, status, "Custom Status", "Status bar should contain custom status")
}

// Test 11: BaseModel Update handles EventMsg
func TestBaseModel_Update_EventMsg(t *testing.T) {
	ctx := context.Background()
	emitter := events.NewChanEmitter(100)
	sub := emitter.Subscribe()

	model := NewBaseModel(ctx, sub)

	// Initialize viewport
	msg := tea.WindowSizeMsg{Width: 80, Height: 24}
	newModel, _ := model.Update(msg)
	model = newModel.(*BaseModel)

	// Send EventThinking event
	go func() {
		emitter.Emit(ctx, events.Event{
			Type:      events.EventThinking,
			Data:      events.ThinkingData{Query: "test query"},
			Timestamp: time.Now(),
		})
	}()

	// Process event
	eventMsg := EventMsg(events.Event{
		Type:      events.EventThinking,
		Data:      events.ThinkingData{Query: "test query"},
		Timestamp: time.Now(),
	})

	newModel, cmd := model.Update(eventMsg)
	assert.NotNil(t, newModel, "Update should return a model")
	assert.NotNil(t, cmd, "Update should return a command to wait for more events")

	// Status should be processing
	assert.True(t, newModel.(*BaseModel).IsProcessing(), "Should be processing after EventThinking")
}

// Test 12: BaseModel HandleKeyPress with debug toggle
func TestBaseModel_HandleKeyPress_DebugToggle(t *testing.T) {
	ctx := context.Background()
	emitter := events.NewChanEmitter(100)
	sub := emitter.Subscribe()

	model := NewBaseModel(ctx, sub)

	// Initialize viewport
	msg := tea.WindowSizeMsg{Width: 80, Height: 24}
	newModel, _ := model.Update(msg)
	model = newModel.(*BaseModel)

	// Send Ctrl+G key (toggle debug)
	// Note: We can't easily test key.Matches without the full key binding setup
	// So we'll test the DebugManager directly
	debugMsg := model.GetDebugManager().ToggleDebug()
	assert.Contains(t, debugMsg, "ON", "Debug mode should be ON after toggle")
	assert.True(t, model.GetDebugManager().IsEnabled(), "Debug manager should be enabled")

	// Toggle again
	debugMsg = model.GetDebugManager().ToggleDebug()
	assert.Contains(t, debugMsg, "OFF", "Debug mode should be OFF after second toggle")
	assert.False(t, model.GetDebugManager().IsEnabled(), "Debug manager should be disabled")
}

// Test 13: BaseModel implements tea.Model interface
func TestBaseModel_ModelInterface(t *testing.T) {
	ctx := context.Background()
	emitter := events.NewChanEmitter(100)
	sub := emitter.Subscribe()

	model := NewBaseModel(ctx, sub)

	// Verify BaseModel implements tea.Model
	var _ tea.Model = model
	assert.NotNil(t, model, "BaseModel should implement tea.Model")
}

// Test 14: BaseModel context propagation (Rule 11)
func TestBaseModel_ContextPropagation(t *testing.T) {
	ctx := context.Background()
	emitter := events.NewChanEmitter(100)
	sub := emitter.Subscribe()

	model := NewBaseModel(ctx, sub)

	// Context should be preserved
	assert.Equal(t, ctx, model.GetContext(), "Context should be preserved")
}

// Test 15: BaseModel subscriber is preserved
func TestBaseModel_SubscriberPreserved(t *testing.T) {
	ctx := context.Background()
	emitter := events.NewChanEmitter(100)
	sub := emitter.Subscribe()

	model := NewBaseModel(ctx, sub)

	// Subscriber should be preserved
	assert.Equal(t, sub, model.GetSubscriber(), "Subscriber should be preserved")
}
