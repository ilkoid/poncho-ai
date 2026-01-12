// Package llm предоставляет функциональные опции для настройки стриминга.
package llm

// StreamOptions — параметры стриминга.
//
// Используется вместе с функциональными опциями StreamOption для
// настройки поведения GenerateStream.
type StreamOptions struct {
	// Enabled — включен ли стриминг (default: true, opt-out).
	//
	// true: GenerateStream будет использовать стриминг
	// false: GenerateStream fallback на обычный Generate
	Enabled bool

	// ThinkingOnly — отправлять только reasoning_content (default: true).
	//
	// true: события отправляются только для reasoning_content
	// false: события отправляются для всех чанков
	ThinkingOnly bool
}

// StreamOption — функциональная опция для настройки стриминга.
//
// Реализует pattern "Functional Options" для гибкой конфигурации.
type StreamOption func(*StreamOptions)

// WithStream включает или выключает стриминг.
//
// # Parameters
//
//   - enabled: true для включения, false для выключения
//
// # Usage
//
//	// Включить стриминг (default behaviour)
//	provider.GenerateStream(ctx, msgs, cb, WithStream(true))
//
//	// Выключить стриминг (fallback на sync)
//	provider.GenerateStream(ctx, msgs, cb, WithStream(false))
func WithStream(enabled bool) StreamOption {
	return func(o *StreamOptions) {
		o.Enabled = enabled
	}
}

// WithThinkingOnly настраивает отправку только reasoning_content.
//
// # Parameters
//
//   - thinkingOnly: true для отправки только reasoning_content
//
// # Usage
//
//	// Отправлять только reasoning_content (default)
//	provider.GenerateStream(ctx, msgs, cb, WithThinkingOnly(true))
//
//	// Отправлять все чанки (content + thinking)
//	provider.GenerateStream(ctx, msgs, cb, WithThinkingOnly(false))
func WithThinkingOnly(thinkingOnly bool) StreamOption {
	return func(o *StreamOptions) {
		o.ThinkingOnly = thinkingOnly
	}
}

// DefaultStreamOptions возвращает дефолтные значения для StreamOptions.
//
// Используется внутри GenerateStream для инициализации.
func DefaultStreamOptions() StreamOptions {
	return StreamOptions{
		Enabled:      true,  // Default: streaming enabled (opt-out)
		ThinkingOnly: true,  // Default: only reasoning_content events
	}
}
