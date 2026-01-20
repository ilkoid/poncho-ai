// Package app предоставляет PresetConfig для Presets System.
//
// PresetConfig — это пред-конфигурация приложения,
// которая позволяет запускать AI агента в 2 строки:
//   app.RunPreset(ctx, "interactive-tui")
//
// Presets не заменяют config.yaml, а накладываются поверх него,
// позволяя переопределять только нужные параметры.
//
// Rule 6: Reusable library code, no app-specific logic.
// Rule 2: Configuration through YAML (presets are overlays, not replacements).
package app

import (
	"slices"

	"github.com/charmbracelet/lipgloss"
)

// AppType — тип приложения.
type AppType int

const (
	// AppTypeCLI — консольное приложение (stdin/stdout)
	AppTypeCLI AppType = iota

	// AppTypeTUI — терминальный UI (Bubble Tea)
	AppTypeTUI
)

// PresetConfig — конфигурация пресета.
//
// Содержит только те параметры, которые нужно переопределить
// относительно базового config.yaml. Все остальные параметры
// загружаются из config.yaml.
//
// Пример использования:
//
//	preset := &PresetConfig{
//	    Type:     AppTypeTUI,
//	    EnableBundles: []string{"wb-tools", "vision-tools"},
//	    Features:  []string{"streaming", "interruptions"},
//	    Models: ModelSelection{Reasoning: "glm-4.6"},
//	    UI: tui.SimpleUIConfig{Colors: tui.ColorSchemes["dark"]},
//	}
type PresetConfig struct {
	// Name — имя пресета (для логов и ошибок)
	Name string

	// Type — тип приложения (CLI или TUI)
	Type AppType

	// Description — описание пресета (для справки)
	Description string

	// EnableBundles — список bundles для включения.
	// Пустой массив = использовать bundles из config.yaml.
	EnableBundles []string

	// Features — список фич для включения.
	// Возможные значения: "streaming", "debug", "interruptions".
	Features []string

	// Models — выбор моделей (опционально).
	// Пустые значения = использовать модели из config.yaml.
	Models ModelSelection

	// UI — конфигурация TUI (только для AppTypeTUI).
	// Определяется локально для избежания циклического импорта с pkg/tui.
	UI TUIConfig
}

// ===== UI CONFIG STRUCTURES (локальные копии для избежания циклического импорта) =====

// ColorScheme определяет цвета для различных элементов TUI.
//
// Это упрощенная копия из pkg/tui/colors.go для использования в PresetConfig.
// Полная версия с ColorSchemes доступна в pkg/tui.
type ColorScheme struct {
	StatusBackground lipgloss.Color
	StatusForeground lipgloss.Color
	SystemMessage    lipgloss.Color
	UserMessage      lipgloss.Color
	AIMessage        lipgloss.Color
	ErrorMessage     lipgloss.Color
	Thinking         lipgloss.Color
	ThinkingDim      lipgloss.Color
	InputPrompt      lipgloss.Color
	InputBackground  lipgloss.Color
	InputForeground  lipgloss.Color
	Border           lipgloss.Color
}

// TUIConfig конфигурирует TUI (InterruptionModel).
//
// Используется в PresetConfig для настройки TUI параметров.
// Локальная копия для избежания циклического импорта с pkg/tui.
type TUIConfig struct {
	Colors           ColorScheme
	StatusHeight     int
	InputHeight      int
	InputPrompt      string
	ShowTimestamp    bool
	MaxMessages      int
	WrapText         bool
	Title            string
	ModelName        string
	StreamingStatus  string
}

// ModelSelection — выбор моделей для пресета.
//
// Позволяет переопределить модели из config.yaml.
type ModelSelection struct {
	// Chat — модель для chat (без reasoning)
	Chat string

	// Reasoning — модель для reasoning (ReAct agent)
	Reasoning string

	// Vision — модель для vision задач
	Vision string
}

// HasFeature проверяет, включена ли feature в пресете.
func (p *PresetConfig) HasFeature(feature string) bool {
	return slices.Contains(p.Features, feature)
}
