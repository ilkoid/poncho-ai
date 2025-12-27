// Package ui тесты для рендеринга UI компонентов
package ui

import (
	"testing"

	"github.com/ilkoid/poncho-ai/pkg/todo"
)

// TestRenderTodoPanel verifies that the todo panel renders correctly
func TestRenderTodoPanel(t *testing.T) {
	tests := []struct {
		name           string
		setupTasks     func(*todo.Manager)
		expectedSubstr string
	}{
		{
			name: "empty todo list",
			setupTasks: func(m *todo.Manager) {
				// No tasks
			},
			expectedSubstr: "Нет активных задач",
		},
		{
			name: "single pending task",
			setupTasks: func(m *todo.Manager) {
				m.Add("Test task 1")
			},
			expectedSubstr: "○ 1. Test task 1",
		},
		{
			name: "multiple tasks with different statuses",
			setupTasks: func(m *todo.Manager) {
				m.Add("Pending task")
				id := m.Add("Task to complete")
				m.Complete(id)
				failID := m.Add("Task to fail")
				m.Fail(failID, "test error")
			},
			expectedSubstr: "✓ 2. Task to complete",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a fresh todo manager for each test
			mgr := todo.NewManager()
			tt.setupTasks(mgr)

			// Render the panel
			result := RenderTodoPanel(mgr, 40)

			// Check that expected substring is present
			if !contains(result, tt.expectedSubstr) {
				t.Errorf("RenderTodoPanel() output does not contain expected substring:\nExpected: %s\nGot:\n%s",
					tt.expectedSubstr, result)
			}

			// Print output for visual verification
			t.Logf("Rendered output:\n%s", result)
		})
	}
}

// TestRenderTodoPanelWithMockData tests with realistic mock data
func TestRenderTodoPanelWithMockData(t *testing.T) {
	mgr := todo.NewManager()

	// Add mock tasks similar to what demo command would add
	mgr.Add("Проверить API Wildberries")
	mgr.Add("Загрузить эскизы из S3")
	id3 := mgr.Add("Сгенерировать описание товара")
	mgr.Complete(id3)
	id4 := mgr.Add("Провалить эту задачу для теста")
	mgr.Fail(id4, "Тестовая ошибка")

	// Render with typical width
	result := RenderTodoPanel(mgr, 40)

	// Verify all expected elements are present
	expectedStrings := []string{
		"📋 ПЛАН ДЕЙСТВИЙ",
		"Проверить API Wildberries",
		"Загрузить эскизы из S3",
		"✓",
		"✗",
		"Ошибка: Тестовая ошибка",
		"Выполнено:",
		"В работе:",
		"Провалено:",
	}

	for _, expected := range expectedStrings {
		if !contains(result, expected) {
			t.Errorf("Expected output to contain '%s', but it didn't.\nOutput:\n%s", expected, result)
		}
	}

	// Print for visual verification
	t.Logf("Full rendered output:\n%s", result)
}

// contains is a helper to check if a string contains a substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && findSubstring(s, substr)
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
