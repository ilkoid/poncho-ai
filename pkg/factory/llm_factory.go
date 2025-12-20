package factory

import (
	"fmt"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/llm"
	"github.com/ilkoid/poncho-ai/pkg/llm/openai" // Импорт конкретной реализации
)

// NewLLMProvider создает провайдера на основе конфига
func NewLLMProvider(cfg config.ModelDef) (llm.Provider, error) {
	switch cfg.Provider {
	case "zai", "openai", "deepseek":
		// Create a temporary AppConfig with just the model definition
		// This is a workaround since NewClient expects a full AppConfig
		tempCfg := &config.AppConfig{
			Models: config.ModelsConfig{
				DefaultChat: "temp", // This won't be used
				Definitions: map[string]config.ModelDef{
					"temp": cfg,
				},
			},
		}

		return openai.NewClient(tempCfg), nil

	default:
		return nil, fmt.Errorf("unknown provider type: %s", cfg.Provider)
	}
}
