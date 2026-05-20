package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/utils"
)

// GenerateJSON вызывает LLM с json_object mode и парсит ответ в target.
//
// Автоматически:
//   - добавляет WithFormat("json_object") к opts
//   - чистит markdown-обёртки (CleanJsonBlock)
//   - извлекает JSON-объект (ExtractJSON с подсчётом глубины скобок)
//   - делает unmarshal в target
//   - при ошибке парсинга — retry до 3 попыток с feedback-сообщением
//   - при сетевой ошибке — retry с backoff (до 3 раз)
func GenerateJSON(ctx context.Context, provider Provider, messages []Message, target any, opts ...any) error {
	// Гарантируем json_object mode
	opts = append(opts, WithFormat("json_object"))

	const maxAttempts = 3
	var lastParseErr error

	for attempt := 0; attempt < maxAttempts; attempt++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		msgs := messages
		if attempt > 0 && lastParseErr != nil {
			msgs = append(msgs, Message{
				Role:    RoleUser,
				Content: fmt.Sprintf("ОШИБКА: ответ не прошёл парсинг JSON (%v). Ответь СТРОГО JSON-объектом без markdown обёртки, без пояснений, без рассуждений. Начни с { и закончи }.", lastParseErr),
			})
		}

		resp, err := generateWithBackoff(ctx, provider, msgs, opts...)
		if err != nil {
			return fmt.Errorf("LLM call: %w", err)
		}

		cleaned := utils.CleanJsonBlock(resp.Content)
		extracted := utils.ExtractJSON(cleaned)
		if extracted == "" {
			lastParseErr = fmt.Errorf("пустой ответ после извлечения JSON")
			log.Printf("    WARN: parse attempt %d/%d: empty JSON extraction", attempt+1, maxAttempts)
			continue
		}

		if err := json.Unmarshal([]byte(extracted), target); err != nil {
			lastParseErr = err
			log.Printf("    WARN: parse attempt %d/%d failed (%v)", attempt+1, maxAttempts, err)
			continue
		}

		return nil
	}

	return fmt.Errorf("JSON parse failed after %d attempts: %w", maxAttempts, lastParseErr)
}

// generateWithBackoff вызывает Provider.Generate с retry и exponential backoff.
func generateWithBackoff(ctx context.Context, provider Provider, messages []Message, opts ...any) (Message, error) {
	const maxRetries = 3
	var lastErr error

	for i := 0; i < maxRetries; i++ {
		select {
		case <-ctx.Done():
			return Message{}, ctx.Err()
		default:
		}

		resp, err := provider.Generate(ctx, messages, opts...)
		if err != nil {
			lastErr = err
			backoff := time.Duration(i+1) * 2 * time.Second
			log.Printf("    Retry %d/%d after %v: %v", i+1, maxRetries, backoff, err)
			select {
			case <-ctx.Done():
				return Message{}, ctx.Err()
			case <-time.After(backoff):
			}
			continue
		}

		return resp, nil
	}

	return Message{}, fmt.Errorf("max retries exceeded: %w", lastErr)
}
