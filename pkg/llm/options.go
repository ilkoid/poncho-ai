// Package llm provides options pattern for LLM generation parameters.
//
// This package implements functional options for runtime parameter overrides
// while maintaining backward compatibility with existing code.
package llm

// GenerateOptions holds parameters for LLM generation.
// These options can be set at initialization (from config.yaml) and
// overridden at runtime (from prompts or direct calls).
type GenerateOptions struct {
	// Model is the model identifier (e.g., "glm-4.6", "glm-4.6v-flash")
	Model string

	// Temperature controls randomness in responses (0.0 = deterministic, 1.0 = random)
	Temperature float64

	// MaxTokens limits the response length
	MaxTokens int

	// Format specifies response format (e.g., "json_object" for structured output)
	Format string

	// ParallelToolCalls controls whether LLM can call multiple tools at once.
	// nil = use model default from config.yaml
	// NOTE: Currently configured via model definition, not runtime options.
	ParallelToolCalls *bool
}

// GenerateOption is a functional option for configuring GenerateOptions.
type GenerateOption func(*GenerateOptions)

// WithModel sets the model for generation.
// Runtime override: takes precedence over config.yaml default.
func WithModel(model string) GenerateOption {
	return func(o *GenerateOptions) {
		o.Model = model
	}
}

// WithTemperature sets the temperature for generation.
// Runtime override: takes precedence over config.yaml default.
func WithTemperature(temp float64) GenerateOption {
	return func(o *GenerateOptions) {
		o.Temperature = temp
	}
}

// WithMaxTokens sets the maximum tokens for generation.
// Runtime override: takes precedence over config.yaml default.
func WithMaxTokens(tokens int) GenerateOption {
	return func(o *GenerateOptions) {
		o.MaxTokens = tokens
	}
}

// WithFormat sets the response format for generation.
// Use "json_object" for structured JSON output.
// Runtime override: takes precedence over config.yaml default.
func WithFormat(format string) GenerateOption {
	return func(o *GenerateOptions) {
		o.Format = format
	}
}
