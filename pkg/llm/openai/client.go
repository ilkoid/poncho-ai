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
	// Get the default chat model configuration
	modelDef, _ := cfg.GetVisionModel(cfg.Models.DefaultChat)
	return &Client{
		api:   openai.NewClient(modelDef.APIKey), // Use API key from model definition
		model: modelDef.ModelName,                // Use model name from definition
	}
}

// Generate выполняет запрос к API.
func (c *Client) Generate(ctx context.Context, messages []llm.Message, tools ...any) (llm.Message, error) {
	// 1. Конвертируем наши сообщения в формат OpenAI
	openaiMsgs := make([]openai.ChatCompletionMessage, len(messages))
	for i, m := range messages {
		openaiMsgs[i] = mapToOpenAI(m)
	}

	// 2. Создаем запрос
	req := openai.ChatCompletionRequest{
		Model:    c.model,
		Messages: openaiMsgs,
	}

	// (Тут можно добавить логику для tools, если нужно, пока пропускаем для краткости)

	// 3. Вызываем API
	resp, err := c.api.CreateChatCompletion(ctx, req)
	if err != nil {
		return llm.Message{}, err
	}

	// 4. Маппим ответ обратно в наш формат
	choice := resp.Choices[0].Message

	result := llm.Message{
		Role:    llm.Role(choice.Role),
		Content: choice.Content,
	}

	// Обработка Tool Calls (если модель решила вызвать функцию)
	if len(choice.ToolCalls) > 0 {
		result.ToolCalls = make([]llm.ToolCall, len(choice.ToolCalls))
		for i, tc := range choice.ToolCalls {
			result.ToolCalls[i] = llm.ToolCall{
				ID:   tc.ID,
				Name: tc.Function.Name,
				Args: tc.Function.Arguments,
			}
		}
	}

	return result, nil
}

// mapToOpenAI конвертирует наше внутреннее сообщение в формат SDK.
// Здесь происходит магия Vision: если есть картинки, создаем MultiContent.
func mapToOpenAI(m llm.Message) openai.ChatCompletionMessage {
	msg := openai.ChatCompletionMessage{
		Role: string(m.Role),
	}

	// Если картинок нет, отправляем просто текст
	if len(m.Images) == 0 {
		msg.Content = m.Content
		return msg
	}

	// Если есть картинки (Vision запрос)
	parts := []openai.ChatMessagePart{
		{
			Type: openai.ChatMessagePartTypeText,
			Text: m.Content,
		},
	}

	for _, imgURL := range m.Images {
		parts = append(parts, openai.ChatMessagePart{
			Type: openai.ChatMessagePartTypeImageURL,
			ImageURL: &openai.ChatMessageImageURL{
				URL:    imgURL, // Ожидается base64 data-uri или http ссылка
				Detail: openai.ImageURLDetailAuto,
			},
		})
	}

	msg.MultiContent = parts
	return msg
}
