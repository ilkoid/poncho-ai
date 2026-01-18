package primitives

import (
	"github.com/charmbracelet/bubbles/viewport"
	"github.com/muesli/reflow/wrap"
	tea "github.com/charmbracelet/bubbletea"
	"strings"
	"sync"
)

// ViewportManager manages a viewport with thread-safe operations and bug fixes
// Fixes 4 critical bugs from the original implementation:
// 1. Timing Attack: wasAtBottom computed AFTER height change
// 2. Height = 0: Death of scroll when vpHeight < 0
// 3. Off-by-One: Incorrect clamp logic
// 4. Race Condition: No mutex in resize handler
type ViewportManager struct {
	viewport viewport.Model
	logLines []string // Original lines without word-wrap (KEY to reflow!)
	mu       sync.RWMutex
}

// ViewportConfig holds configuration for ViewportManager
type ViewportConfig struct {
	MinWidth  int
	MinHeight int
}

// NewViewportManager creates a new ViewportManager with default settings
func NewViewportManager(cfg ViewportConfig) *ViewportManager {
	return &ViewportManager{
		viewport: viewport.New(0, 0),
		logLines: []string{},
		mu:       sync.RWMutex{},
	}
}

// HandleResize processes window resize events
// ✅ FIXED: All 4 bugs resolved
func (vm *ViewportManager) HandleResize(msg tea.WindowSizeMsg, headerHeight, footerHeight int) {
	vm.mu.Lock()
	defer vm.mu.Unlock()

	// 1. Compute dimensions
	vpHeight := msg.Height - headerHeight - footerHeight
	if vpHeight < 1 { // ✅ FIX #2: Minimum 1, not 0
		vpHeight = 1
	}
	vpWidth := msg.Width
	if vpWidth < 20 {
		vpWidth = 20
	}

	// ✅ FIX #1: Compute wasAtBottom BEFORE changing Height
	totalLinesBefore := vm.viewport.TotalLineCount()
	wasAtBottom := vm.viewport.YOffset+vm.viewport.Height >= totalLinesBefore

	// Update dimensions AFTER wasAtBottom
	vm.viewport.Height = vpHeight
	vm.viewport.Width = vpWidth

	// Reflow content with new word-wrap
	var wrappedLines []string
	for _, line := range vm.logLines {
		wrapped := wrap.String(line, vpWidth)
		wrappedLines = append(wrappedLines, strings.Split(wrapped, "\n")...)
	}
	fullContent := strings.Join(wrappedLines, "\n")
	vm.viewport.SetContent(fullContent)

	// Restore scroll position
	if wasAtBottom {
		vm.viewport.GotoBottom()
	} else {
		// ✅ FIX #3: Proper clamp logic with explicit maxOffset
		newTotalLines := vm.viewport.TotalLineCount()
		maxOffset := newTotalLines - vm.viewport.Height
		if maxOffset < 0 {
			maxOffset = 0
		}
		if vm.viewport.YOffset > maxOffset {
			vm.viewport.YOffset = maxOffset
		}
	}
}

// Append adds content with smart scroll
// ✅ FIXED: Thread-safe, preserves position
func (vm *ViewportManager) Append(content string, preservePosition bool) {
	vm.mu.Lock()
	defer vm.mu.Unlock()

	// Store original line
	vm.logLines = append(vm.logLines, content)

	// Reflow all content
	var wrappedLines []string
	for _, line := range vm.logLines {
		wrapped := wrap.String(line, vm.viewport.Width)
		wrappedLines = append(wrappedLines, strings.Split(wrapped, "\n")...)
	}
	fullContent := strings.Join(wrappedLines, "\n")

	// Smart scroll
	if preservePosition {
		wasAtBottom := vm.viewport.YOffset+vm.viewport.Height >= vm.viewport.TotalLineCount()
		vm.viewport.SetContent(fullContent)
		if wasAtBottom {
			vm.viewport.GotoBottom()
		}
	} else {
		vm.viewport.SetContent(fullContent)
	}
}

// GetViewport returns the underlying viewport.Model
func (vm *ViewportManager) GetViewport() viewport.Model {
	vm.mu.RLock()
	defer vm.mu.RUnlock()
	return vm.viewport
}

// SetInitialContent sets content on first run
func (vm *ViewportManager) SetInitialContent(content string) {
	vm.mu.Lock()
	defer vm.mu.Unlock()

	vm.logLines = []string{content}
	vm.viewport.SetContent(content)
	vm.viewport.YOffset = 0
}

// Content returns the current content as a slice of lines
func (vm *ViewportManager) Content() []string {
	vm.mu.RLock()
	defer vm.mu.RUnlock()
	return vm.logLines
}

// ScrollUp scrolls the viewport up by n lines
func (vm *ViewportManager) ScrollUp(n int) {
	vm.mu.Lock()
	defer vm.mu.Unlock()
	vm.viewport.ScrollUp(n)
}

// ScrollDown scrolls the viewport down by n lines
func (vm *ViewportManager) ScrollDown(n int) {
	vm.mu.Lock()
	defer vm.mu.Unlock()
	vm.viewport.ScrollDown(n)
}

// GotoTop scrolls to the top of the viewport
func (vm *ViewportManager) GotoTop() {
	vm.mu.Lock()
	defer vm.mu.Unlock()
	vm.viewport.GotoTop()
}

// GotoBottom scrolls to the bottom of the viewport
func (vm *ViewportManager) GotoBottom() {
	vm.mu.Lock()
	defer vm.mu.Unlock()
	vm.viewport.GotoBottom()
}

// SetDimensions sets the viewport dimensions directly
func (vm *ViewportManager) SetDimensions(width, height int) {
	vm.mu.Lock()
	defer vm.mu.Unlock()
	vm.viewport.Width = width
	vm.viewport.Height = height
}

// GetDimensions returns the current viewport dimensions
func (vm *ViewportManager) GetDimensions() (width, height int) {
	vm.mu.RLock()
	defer vm.mu.RUnlock()
	return vm.viewport.Width, vm.viewport.Height
}
