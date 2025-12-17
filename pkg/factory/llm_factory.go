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
		baseURL := cfg.BaseURL
		
		// Fallback defaults если URL не задан в конфиге
		if baseURL == "" {
			if cfg.Provider == "zai" {
				baseURL = "https://open.bigmodel.cn/api/paas/v4"
			} else if cfg.Provider == "openai" {
				baseURL = "https://api.openai.com/v1"
			}
		}

		return openai.New(cfg.APIKey, baseURL, cfg.Timeout), nil
	
	default:
		return nil, fmt.Errorf("unknown provider type: %s", cfg.Provider)
	}
}
