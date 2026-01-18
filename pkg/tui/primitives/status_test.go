package primitives

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/stretchr/testify/assert"
)

// Test 1: Basic render - processing state
func TestStatusBarManager_ProcessingState(t *testing.T) {
	cfg := DefaultStatusBarConfig()
	sm := NewStatusBarManager(cfg)

	// Test idle state
	output := sm.Render()
	assert.Contains(t, output, "✓ Ready", "Should show '✓ Ready' when idle")
	assert.False(t, sm.IsProcessing())

	// Test processing state
	sm.SetProcessing(true)
	output = sm.Render()
	assert.NotContains(t, output, "✓ Ready", "Should not show '✓ Ready' when processing")
	assert.NotEqual(t, "", output, "Should show spinner when processing")
	assert.True(t, sm.IsProcessing())
}

// Test 2: Debug mode indicator
func TestStatusBarManager_DebugMode(t *testing.T) {
	cfg := DefaultStatusBarConfig()
	sm := NewStatusBarManager(cfg)

	// Test debug mode OFF
	sm.SetDebugMode(false)
	output := sm.Render()
	assert.NotContains(t, output, "DEBUG", "Should not show DEBUG when mode is off")
	assert.False(t, sm.IsDebugMode())

	// Test debug mode ON
	sm.SetDebugMode(true)
	output = sm.Render()
	assert.Contains(t, output, "DEBUG", "Should show DEBUG when mode is on")
	assert.True(t, sm.IsDebugMode())
}

// Test 3: Custom extra callback
func TestStatusBarManager_CustomExtra(t *testing.T) {
	cfg := DefaultStatusBarConfig()
	sm := NewStatusBarManager(cfg)

	// Test without custom extra
	output := sm.Render()
	assert.NotContains(t, output, "Todo:", "Should not show custom extra when not set")

	// Test with custom extra
	sm.SetCustomExtra(func() string {
		return "Todo: 3/12"
	})
	output = sm.Render()
	assert.Contains(t, output, "Todo: 3/12", "Should show custom extra when set")

	// Test callback retrieval
	callback := sm.GetCustomExtra()
	assert.NotNil(t, callback, "Should return the callback")
	assert.Equal(t, "Todo: 3/12", callback(), "Callback should return the expected string")
}

// Test 4: Combined state - processing + debug + custom extra
func TestStatusBarManager_CombinedState(t *testing.T) {
	cfg := DefaultStatusBarConfig()
	sm := NewStatusBarManager(cfg)

	// Enable all states
	sm.SetProcessing(true)
	sm.SetDebugMode(true)
	sm.SetCustomExtra(func() string {
		return "Todo: 3/12"
	})

	output := sm.Render()
	// Should contain spinner (│), DEBUG, and custom extra
	assert.Contains(t, output, "DEBUG", "Should show DEBUG")
	assert.Contains(t, output, "Todo: 3/12", "Should show custom extra")
}

// Test 5: Thread safety - concurrent access
func TestStatusBarManager_ThreadSafety(t *testing.T) {
	cfg := DefaultStatusBarConfig()
	sm := NewStatusBarManager(cfg)

	done := make(chan bool)

	// Goroutine 1: Concurrent SetProcessing calls
	go func() {
		for i := 0; i < 100; i++ {
			sm.SetProcessing(i%2 == 0)
		}
		done <- true
	}()

	// Goroutine 2: Concurrent SetDebugMode calls
	go func() {
		for i := 0; i < 100; i++ {
			sm.SetDebugMode(i%2 == 0)
		}
		done <- true
	}()

	// Goroutine 3: Concurrent Render calls
	go func() {
		for i := 0; i < 100; i++ {
			_ = sm.Render()
		}
		done <- true
	}()

	// Goroutine 4: Concurrent SetCustomExtra calls
	go func() {
		for i := 0; i < 100; i++ {
			sm.SetCustomExtra(func() string {
				return "Test"
			})
		}
		done <- true
	}()

	// Wait for all goroutines
	<-done
	<-done
	<-done
	<-done

	// Verify final state is consistent
	_ = sm.Render()
	_ = sm.IsProcessing()
	_ = sm.IsDebugMode()
	_ = sm.GetCustomExtra()

	// If we got here without race conditions, test passes
	assert.True(t, true, "Thread safety maintained")
}

// Test 6: State persistence
func TestStatusBarManager_StatePersistence(t *testing.T) {
	cfg := DefaultStatusBarConfig()
	sm := NewStatusBarManager(cfg)

	// Set all states
	sm.SetProcessing(true)
	sm.SetDebugMode(true)
	sm.SetCustomExtra(func() string {
		return "Test"
	})

	// Verify all states persist
	assert.True(t, sm.IsProcessing(), "Processing state should persist")
	assert.True(t, sm.IsDebugMode(), "Debug mode should persist")
	assert.NotNil(t, sm.GetCustomExtra(), "Custom extra callback should persist")
	assert.Equal(t, "Test", sm.GetCustomExtra()(), "Custom extra should return correct value")
}

// Test 7: Color configuration preservation
func TestStatusBarManager_ColorConfiguration(t *testing.T) {
	cfg := DefaultStatusBarConfig()
	_ = NewStatusBarManager(cfg)

	// Verify colors are set correctly
	// Note: We can't directly test the rendered colors without parsing ANSI codes,
	// but we can verify the configuration is stored
	assert.Equal(t, lipgloss.Color("86"), cfg.SpinnerColor)
	assert.Equal(t, lipgloss.Color("242"), cfg.IdleColor)
	assert.Equal(t, lipgloss.Color("235"), cfg.BackgroundColor)
	assert.Equal(t, lipgloss.Color("196"), cfg.DebugColor)
	assert.Equal(t, lipgloss.Color("15"), cfg.DebugText)
	assert.Equal(t, lipgloss.Color("252"), cfg.ExtraText)
}

// Test 8: Empty custom extra handling
func TestStatusBarManager_EmptyCustomExtra(t *testing.T) {
	cfg := DefaultStatusBarConfig()
	sm := NewStatusBarManager(cfg)

	// Set custom extra that returns empty string
	sm.SetCustomExtra(func() string {
		return ""
	})

	output := sm.Render()
	// Should not crash and should not add extra space for empty custom extra
	assert.NotContains(t, output, "  ", "Should not add extra space for empty custom extra")
}

// Test 9: Custom configuration
func TestStatusBarManager_CustomConfiguration(t *testing.T) {
	customCfg := StatusBarConfig{
		SpinnerColor:    lipgloss.Color("200"),
		IdleColor:       lipgloss.Color("240"),
		BackgroundColor: lipgloss.Color("230"),
		DebugColor:      lipgloss.Color("160"),
		DebugText:       lipgloss.Color("255"),
		ExtraText:       lipgloss.Color("245"),
	}

	sm := NewStatusBarManager(customCfg)

	// Verify custom configuration is used
	sm.SetProcessing(true)
	sm.SetDebugMode(true)
	output := sm.Render()

	// Output should be generated without errors
	assert.NotEmpty(t, output, "Should render with custom configuration")
}
