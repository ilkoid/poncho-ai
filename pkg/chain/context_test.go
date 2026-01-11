package chain

import (
	"strings"
	"testing"

	"github.com/ilkoid/poncho-ai/pkg/llm"
)

func TestFilterMessagesForChatModel(t *testing.T) {
	chainCtx := &ChainContext{}

	t.Run("removes vision context from system messages", func(t *testing.T) {
		messages := []llm.Message{
			{Role: llm.RoleSystem, Content: "System prompt\n\nКОНТЕКСТ АРТИКУЛА (Результаты анализа файлов):\n- Файл [sketch] dress.jpg: Красное платье\n"},
		}

		filtered := chainCtx.filterMessagesForChatModel(messages)

		if strings.Contains(filtered[0].Content, "КОНТЕКСТ АРТИКУЛА") {
			t.Errorf("Expected vision context to be removed, but got: %s", filtered[0].Content)
		}
	})

	t.Run("removes images from all messages", func(t *testing.T) {
		messages := []llm.Message{
			{Role: llm.RoleUser, Content: "What do you see?", Images: []string{"data:image/jpeg;base64,..."}},
		}

		filtered := chainCtx.filterMessagesForChatModel(messages)

		if len(filtered[0].Images) > 0 {
			t.Errorf("Expected images to be removed, but got %d images", len(filtered[0].Images))
		}
	})

	t.Run("keeps non-system content intact", func(t *testing.T) {
		originalContent := "This is regular content"
		messages := []llm.Message{
			{Role: llm.RoleUser, Content: originalContent},
		}

		filtered := chainCtx.filterMessagesForChatModel(messages)

		if filtered[0].Content != originalContent {
			t.Errorf("Expected content to remain intact, got: %s", filtered[0].Content)
		}
	})
}

func TestIsModelVision(t *testing.T) {
	chainCtx := &ChainContext{}

	tests := []struct {
		name     string
		model    string
		expected bool
	}{
		{"chat model", "glm-4.6", false},
		{"vision model with v- prefix", "glm-4.6v-flash", true},
		{"vision model with vision in name", "vision-model", true},
		{"vision model with v- prefix only", "v-1.0", true},
		{"empty model", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := chainCtx.isModelVision(tt.model)
			if result != tt.expected {
				t.Errorf("isModelVision(%q) = %v, want %v", tt.model, result, tt.expected)
			}
		})
	}
}

func TestRemoveVisionContextFromSystem(t *testing.T) {
	chainCtx := &ChainContext{}

	t.Run("removes vision context block", func(t *testing.T) {
		content := "System prompt\n\nКОНТЕКСТ АРТИКУЛА (Результаты анализа файлов):\n- Файл [sketch] dress.jpg: Красное платье\n"
		expected := "System prompt"

		result := chainCtx.removeVisionContextFromSystem(content)

		if result != expected {
			t.Errorf("Expected %q, got %q", expected, result)
		}
	})

	t.Run("returns content unchanged if no vision context", func(t *testing.T) {
		content := "Just a system prompt"
		result := chainCtx.removeVisionContextFromSystem(content)

		if result != content {
			t.Errorf("Expected %q, got %q", content, result)
		}
	})

	t.Run("handles content starting with vision context", func(t *testing.T) {
		content := "КОНТЕКСТ АРТИКУЛА\nsome content"
		expected := ""

		result := chainCtx.removeVisionContextFromSystem(content)

		if result != expected {
			t.Errorf("Expected %q, got %q", expected, result)
		}
	})
}
