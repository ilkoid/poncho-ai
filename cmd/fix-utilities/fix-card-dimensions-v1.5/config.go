package main

import (
	"fmt"
	"strings"

	"github.com/ilkoid/poncho-ai/pkg/cardupdate"
	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/filter"
)

type CompareConfig struct {
	ToleranceCm3 float64 `yaml:"tolerance_cm3"`
	ToleranceKg  float64 `yaml:"tolerance_kg"`
}

// VolumeConfig — отбор по направлению расхождения объёмов (сырой замер 1С vs
// габариты карточки WB) и страховочный запас к записываемым осям.
// Секция необязательна: без неё поведение прежнее (any / 0).
type VolumeConfig struct {
	Direction   string  `yaml:"direction"`     // under | over | any (default)
	MarginPct   float64 `yaml:"margin_pct"`    // отбор: 1С больше WB минимум на этот процент
	MinDeltaCm3 float64 `yaml:"min_delta_cm3"` // отбор: абсолютная дельта объёма, см³ (AND с margin_pct)
	PaddingCm   float64 `yaml:"padding_cm"`    // запись: + см к L/W/H перед ceil; на отбор не влияет
}

// normalized приводит direction к каноническому виду и валидирует значения.
func (v VolumeConfig) normalized() (VolumeConfig, error) {
	v.Direction = strings.ToLower(strings.TrimSpace(v.Direction))
	switch v.Direction {
	case "":
		v.Direction = "any"
	case "under", "over", "any":
	default:
		return v, fmt.Errorf("volume.direction: ожидается under|over|any, получено %q", v.Direction)
	}
	if v.MarginPct < 0 {
		return v, fmt.Errorf("volume.margin_pct: не может быть отрицательным (%g)", v.MarginPct)
	}
	if v.MinDeltaCm3 < 0 {
		return v, fmt.Errorf("volume.min_delta_cm3: не может быть отрицательным (%g)", v.MinDeltaCm3)
	}
	if v.PaddingCm < 0 {
		return v, fmt.Errorf("volume.padding_cm: не может быть отрицательным (%g)", v.PaddingCm)
	}
	return v, nil
}

type Config struct {
	DBPath   string                    `yaml:"db_path"`
	Filters  filter.Filter             `yaml:"filters"`
	WBUpdate cardupdate.WBUpdateConfig `yaml:"wb_update"`
	Compare  CompareConfig             `yaml:"compare"`
	Volume   VolumeConfig              `yaml:"volume"`
}

func loadConfig(path string) (*Config, error) {
	var cfg Config
	if err := config.LoadYAML(path, &cfg); err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	if cfg.DBPath == "" {
		cfg.DBPath = "/var/db/wb-sales.db"
	}
	vol, err := cfg.Volume.normalized()
	if err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	cfg.Volume = vol
	cfg.WBUpdate = cfg.WBUpdate.Defaults()
	return &cfg, nil
}
