package factory

import (
	"fmt"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/llm"
	"github.com/ilkoid/poncho-ai/pkg/llm/openai"
)

// NewLLMProvider создает провайдера на основе конфигурации модели
func NewLLMProvider(modelDef config.ModelDef) (llm.Provider, error) {
	switch modelDef.Provider {
	case "zai", "openai", "deepseek":
		return openai.NewClient(modelDef), nil

	default:
		return nil, fmt.Errorf("unknown provider type: %s", modelDef.Provider)
	}
}
