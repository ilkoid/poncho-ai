package primitives

import (
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/lipgloss"
	"sync"
)

// StatusBarManager manages a status bar with spinner, debug mode, and custom extra info
type StatusBarManager struct {
	spinner      spinner.Model
	isProcessing bool
	debugMode    bool
	mu           sync.RWMutex

	// Configuration (exact copy of current colors)
	cfg StatusBarConfig

	// Extension point (exact copy of customStatusExtra)
	customExtra func() string
}

// StatusBarConfig holds color configuration for the status bar
type StatusBarConfig struct {
	// Spinner colors
	SpinnerColor lipgloss.Color // 86 (cyan) when processing
	IdleColor    lipgloss.Color // 242 (gray) when ready

	// Background colors
	BackgroundColor lipgloss.Color // 235 (dark gray)
	DebugColor      lipgloss.Color // 196 (red)

	// Text colors
	DebugText lipgloss.Color // 15 (white)
	ExtraText lipgloss.Color // 252 (gray)
}

// DefaultStatusBarConfig returns the default color scheme matching the current implementation
func DefaultStatusBarConfig() StatusBarConfig {
	return StatusBarConfig{
		SpinnerColor:    lipgloss.Color("86"),
		IdleColor:       lipgloss.Color("242"),
		BackgroundColor: lipgloss.Color("235"),
		DebugColor:      lipgloss.Color("196"),
		DebugText:       lipgloss.Color("15"),
		ExtraText:       lipgloss.Color("252"),
	}
}

// NewStatusBarManager creates a new StatusBarManager with the given configuration
func NewStatusBarManager(cfg StatusBarConfig) *StatusBarManager {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(cfg.SpinnerColor)

	return &StatusBarManager{
		spinner:      s,
		isProcessing: false,
		mu:           sync.RWMutex{},
		cfg:          cfg,
	}
}

// Render returns the status bar as a styled string
func (sm *StatusBarManager) Render() string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	// Spinner part
	var spinnerText string
	if sm.isProcessing {
		spinnerText = sm.spinner.View()
	} else {
		spinnerText = "✓ Ready"
	}

	spinnerPart := lipgloss.NewStyle().
		Background(sm.cfg.BackgroundColor).
		Padding(0, 1).
		Foreground(func() lipgloss.Color {
			if sm.isProcessing {
				return sm.cfg.SpinnerColor
			}
			return sm.cfg.IdleColor
		}()).
		Render(spinnerText)

	// DEBUG indicator (red background)
	var extraPart string
	if sm.debugMode {
		extraPart = lipgloss.NewStyle().
			Background(sm.cfg.DebugColor).
			Foreground(sm.cfg.DebugText).
			Bold(true).
			Padding(0, 1).
			Render(" DEBUG")
	}

	// Custom extra info (e.g., "Todo: 3/12")
	if sm.customExtra != nil {
		extraInfo := sm.customExtra()
		if extraInfo != "" {
			extraPart += lipgloss.NewStyle().
				Background(sm.cfg.BackgroundColor).
				Padding(0, 1).
				Foreground(sm.cfg.ExtraText).
				Render(extraInfo)
		}
	}

	return spinnerPart + extraPart
}

// SetProcessing sets the processing state (shows spinner when true)
func (sm *StatusBarManager) SetProcessing(processing bool) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.isProcessing = processing
}

// IsProcessing returns the current processing state
func (sm *StatusBarManager) IsProcessing() bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.isProcessing
}

// SetDebugMode toggles DEBUG indicator
func (sm *StatusBarManager) SetDebugMode(enabled bool) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.debugMode = enabled
}

// IsDebugMode returns the current debug mode state
func (sm *StatusBarManager) IsDebugMode() bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.debugMode
}

// SetCustomExtra sets the callback for custom status extra info
func (sm *StatusBarManager) SetCustomExtra(fn func() string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.customExtra = fn
}

// GetCustomExtra returns the current custom extra callback
func (sm *StatusBarManager) GetCustomExtra() func() string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.customExtra
}
