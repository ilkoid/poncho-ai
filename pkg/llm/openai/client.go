/*
Адаптер OpenAI-Compatible (pkg/llm/adapters/openai/client.go)
Большинство современных API (включая GLM-4.6 и DeepSeek) совместимы с форматом OpenAI. Адаптер покрывает 99% нужд.
Важно: используем стандартную библиотеку net/http и encoding/json, чтобы не тащить тяжелые SDK.
*/

package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/llm"
)

// Client реализует интерфейс llm.Provider
type Client struct {
	apiKey  string
	baseURL string
	http    *http.Client
}

// New создает нового клиента
func New(apiKey, baseURL string, timeout time.Duration) *Client {
	if timeout == 0 {
		timeout = 60 * time.Second
	}
	return &Client{
		apiKey:  apiKey,
		baseURL: baseURL,
		http: &http.Client{
			Timeout: timeout,
		},
	}
}

// Структуры для JSON API (внутренние)
type apiRequest struct {
	Model       string       `json:"model"`
	Messages    []apiMessage `json:"messages"`
	Temperature float64      `json:"temperature,omitempty"`
	MaxTokens   int          `json:"max_tokens,omitempty"`
	Stream      bool         `json:"stream"`
	// Поддержка JSON режима
	ResponseFormat *apiRespFormat `json:"response_format,omitempty"`
}

type apiRespFormat struct {
	Type string `json:"type"` // "json_object"
}

type apiMessage struct {
	Role    string       `json:"role"`
	Content interface{}  `json:"content"` // string или []apiContent
}

type apiContent struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *apiImage `json:"image_url,omitempty"`
}

type apiImage struct {
	URL string `json:"url"`
}

type apiResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Code    string `json:"code"`
	} `json:"error,omitempty"`
}

// Chat — реализация интерфейса
func (c *Client) Chat(ctx context.Context, req llm.ChatRequest) (string, error) {
	// 1. Конвертация нашего формата в формат API
	apiMsgs := make([]apiMessage, len(req.Messages))
	for i, msg := range req.Messages {
		// Если контент чисто текстовый и простой -> отправляем строкой (совместимость)
		if len(msg.Content) == 1 && msg.Content[0].Type == llm.TypeText {
			apiMsgs[i] = apiMessage{
				Role:    msg.Role,
				Content: msg.Content[0].Text,
			}
			continue
		}

		// Если есть картинки или сложный контент
		contentList := make([]apiContent, len(msg.Content))
		for j, part := range msg.Content {
			if part.Type == llm.TypeText {
				contentList[j] = apiContent{Type: "text", Text: part.Text}
			} else if part.Type == llm.TypeImage {
				contentList[j] = apiContent{
					Type:     "image_url",
					ImageURL: &apiImage{URL: part.ImageURL},
				}
			}
		}
		apiMsgs[i] = apiMessage{
			Role:    msg.Role,
			Content: contentList,
		}
	}

	apiReq := apiRequest{
		Model:       req.Model,
		Messages:    apiMsgs,
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
		Stream:      false,
	}

	if req.Format == "json_object" {
		apiReq.ResponseFormat = &apiRespFormat{Type: "json_object"}
	}

	// 2. Сериализация
	bodyBytes, err := json.Marshal(apiReq)
	if err != nil {
		return "", fmt.Errorf("marshal error: %w", err)
	}

	// 3. Запрос
	url := fmt.Sprintf("%s/chat/completions", c.baseURL)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("request creation error: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("api call error: %w", err)
	}
	defer resp.Body.Close()

	// 4. Чтение ответа
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("api error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var result apiResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("unmarshal response error: %w. body: %s", err, string(respBody))
	}

	if result.Error != nil {
		return "", fmt.Errorf("api returned error: %s (code: %s)", result.Error.Message, result.Error.Code)
	}

	if len(result.Choices) == 0 {
		return "", fmt.Errorf("empty choices in response")
	}

	return result.Choices[0].Message.Content, nil
}

