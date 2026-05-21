package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// LoadYAMLWithFallback tries primary path, then fallbackPath on file-not-found.
// Returns the primary error if neither path resolves.
func LoadYAMLWithFallback(path, fallbackPath string, cfg any) error {
	err := LoadYAML(path, cfg)
	if err == nil {
		return nil
	}
	if fallbackPath != "" && fallbackPath != path {
		if fbErr := LoadYAML(fallbackPath, cfg); fbErr == nil {
			return nil
		}
	}
	return err
}

// LoadYAML reads a YAML file, expands ${ENV} variables, and unmarshals into cfg.
// cfg must be a pointer to a struct with yaml tags.
func LoadYAML(path string, cfg any) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("config file not found: %s", path)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}

	expanded := os.ExpandEnv(string(raw))

	if err := yaml.Unmarshal([]byte(expanded), cfg); err != nil {
		return fmt.Errorf("parse config: %w", err)
	}

	return nil
}
