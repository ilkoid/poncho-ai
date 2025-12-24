//go:build short

package factory

import (
	"fmt"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/llm"
	"github.com/ilkoid/poncho-ai/pkg/llm/openai" // Импорт конкретной реализации
)

// NewLLMProvider создает провайдера на основе конфига
func NewLLMProvider(cfg config.ModelDef) (llm.Provider, error) {
	// TODO: Switch on provider type (zai, openai, deepseek)
	// TODO: Create temporary AppConfig with just the model definition
	// TODO: Return appropriate client implementation
	// TODO: Handle unknown provider types with error
	return nil, nil
}