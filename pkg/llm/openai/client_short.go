//go:build short

package openai

import (
	"context"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/llm"
	openai "github.com/sashabaranov/go-openai"
)

// Client реализует интерфейс llm.Provider для OpenAI.
type Client struct {
	api   *openai.Client
	model string
}

func NewClient(cfg *config.AppConfig) *Client {
	// TODO: Get the default chat model configuration
	// TODO: Create OpenAI client with API key from model definition
	// TODO: Return initialized client
	return nil
}

// Generate выполняет запрос к API.
func (c *Client) Generate(ctx context.Context, messages []llm.Message, tools ...any) (llm.Message, error) {
	// TODO: Convert internal messages to OpenAI format
	// TODO: Create chat completion request
	// TODO: Call OpenAI API
	// TODO: Map response back to internal format
	// TODO: Handle tool calls if model decided to call functions
	return llm.Message{}, nil
}

// mapToOpenAI конвертирует наше внутреннее сообщение в формат SDK.
// Здесь происходит магия Vision: если есть картинки, создаем MultiContent.
func mapToOpenAI(m llm.Message) openai.ChatCompletionMessage {
	// TODO: Create base message with role
	// TODO: If no images, return simple text message
	// TODO: If images exist, create multi-content message with text and image parts
	// TODO: Return formatted message
	return openai.ChatCompletionMessage{}
}