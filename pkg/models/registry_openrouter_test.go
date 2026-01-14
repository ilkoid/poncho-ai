package models_test

import (
	"testing"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/models"
)

// TestCreateProvider_OpenRouter проверяет создание OpenRouter провайдера.
func TestCreateProvider_OpenRouter(t *testing.T) {
	tests := []struct {
		name        string
		modelDef    config.ModelDef
		expectError bool
	}{
		{
			name: "Claude 3.5 Sonnet",
			modelDef: config.ModelDef{
				Provider:  "openrouter",
				ModelName: "anthropic/claude-3.5-sonnet",
				APIKey:    "test-key",
				BaseURL:   "https://openrouter.ai/api/v1",
			},
			expectError: false,
		},
		{
			name: "Claude 3.5 Haiku",
			modelDef: config.ModelDef{
				Provider:  "openrouter",
				ModelName: "anthropic/claude-3.5-haiku",
				APIKey:    "test-key",
				BaseURL:   "https://openrouter.ai/api/v1",
			},
			expectError: false,
		},
		{
			name: "Llama 3.1 70B",
			modelDef: config.ModelDef{
				Provider:  "openrouter",
				ModelName: "meta-llama/llama-3.1-70b-instruct",
				APIKey:    "test-key",
				BaseURL:   "https://openrouter.ai/api/v1",
			},
			expectError: false,
		},
		{
			name: "Gemini Pro 1.5",
			modelDef: config.ModelDef{
				Provider:  "openrouter",
				ModelName: "google/gemini-pro-1.5",
				APIKey:    "test-key",
				BaseURL:   "https://openrouter.ai/api/v1",
			},
			expectError: false,
		},
		{
			name: "GPT-4o",
			modelDef: config.ModelDef{
				Provider:  "openrouter",
				ModelName: "openai/gpt-4o",
				APIKey:    "test-key",
				BaseURL:   "https://openrouter.ai/api/v1",
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, err := models.CreateProvider(tt.modelDef)

			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if provider == nil {
				t.Errorf("expected provider but got nil")
				return
			}
		})
	}
}

// TestCreateProvider_UnknownProvider проверяет обработку неизвестного провайдера.
func TestCreateProvider_UnknownProvider(t *testing.T) {
	modelDef := config.ModelDef{
		Provider:  "unknown-provider",
		ModelName: "some-model",
		APIKey:    "test-key",
		BaseURL:   "https://api.example.com/v1",
	}

	_, err := models.CreateProvider(modelDef)
	if err == nil {
		t.Errorf("expected error for unknown provider")
	}
}

// TestCreateProvider_AllProviders проверяет все поддерживаемые провайдеры.
func TestCreateProvider_AllProviders(t *testing.T) {
	providers := []string{
		"zai",
		"openai",
		"deepseek",
		"openrouter", // NEW: OpenRouter support
	}

	for _, provider := range providers {
		t.Run(provider, func(t *testing.T) {
			modelDef := config.ModelDef{
				Provider:  provider,
				ModelName: "test-model",
				APIKey:    "test-key",
				BaseURL:   "https://api.example.com/v1",
			}

			provider, err := models.CreateProvider(modelDef)

			if err != nil {
				t.Errorf("unexpected error for provider %s: %v", provider, err)
			}

			if provider == nil {
				t.Errorf("expected provider for %s but got nil", provider)
			}
		})
	}
}
