// Package main provides types for WB Stocks Warehouse downloader.
package main

import (
	"github.com/ilkoid/poncho-ai/pkg/config"
)

// Config represents the YAML configuration.
type Config struct {
	WB     config.WBClientConfig `yaml:"wb"`
	Stocks config.StocksConfig   `yaml:"stocks"`
}
