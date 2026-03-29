// Package main provides configuration types for download-wb-stock-history.
package main

import (
	"github.com/ilkoid/poncho-ai/pkg/config"
)

// Config represents the YAML configuration.
// REFACTORED 2026-03-29: Uses pkg/config types to avoid duplication.
type Config struct {
	WB          config.WBClientConfig    `yaml:"wb"`
	StockHistory config.StockHistoryConfig `yaml:"stock_history"`
}
