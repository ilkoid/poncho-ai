// Package std содержит общие вспомогательные функции для WB tools.
package std

import (
	"github.com/ilkoid/poncho-ai/pkg/config"
)

// applyWbDefaults применяет дефолтные значения из wb секции config.
//
// Параметры:
//   - cfg: конфигурация конкретного tool из YAML
//   - wbDefaults: дефолтные значения из wb секции config.yaml
//
// Возвращает endpoint, rateLimit, burst для использования в tool.
// Логика приоритета: значения из tool config перекрывают wb defaults.
func applyWbDefaults(cfg config.ToolConfig, wbDefaults config.WBConfig) (endpoint string, rateLimit int, burst int) {
	wbDefaults = wbDefaults.GetDefaults()

	endpoint = cfg.Endpoint
	if endpoint == "" {
		endpoint = wbDefaults.BaseURL
	}

	rateLimit = cfg.RateLimit
	if rateLimit == 0 {
		rateLimit = wbDefaults.RateLimit
	}

	burst = cfg.Burst
	if burst == 0 {
		burst = wbDefaults.BurstLimit
	}

	return
}
