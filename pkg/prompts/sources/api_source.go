package sources

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// APISource — загрузка промптов из HTTP API.
//
// Пример реализации для демонстрации OCP расширяемости.
// Поддерживает Bearer token авторизацию.
type APISource struct {
	endpoint string
	token    string
	client   *http.Client
}

// NewAPISource создаёт источник промптов из HTTP API.
//
// Параметры:
//   - endpoint: базовый URL API (например, "https://api.example.com")
//   - token: опциональный Bearer token для авторизации
//
// API контракт (пример):
//   GET /prompts/{promptID}
//   Authorization: Bearer {token}
//
//   Response 200:
//   {
//     "system": "You are...",
//     "template": "...",
//     "variables": {"key": "value"},
//     "metadata": {"version": "1.0"}
//   }
func NewAPISource(endpoint string, token string) *APISource {
	return &APISource{
		endpoint: endpoint,
		token:    token,
		client:   &http.Client{}, // В продакшене: настроить timeout, retry
	}
}

// Load загружает промпт из HTTP API.
//
// Возвращает *PromptData для избежания циклического импорта.
func (s *APISource) Load(promptID string) (*PromptData, error) {
	// Build request URL
	url := fmt.Sprintf("%s/prompts/%s", s.endpoint, promptID)

	// Create request
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Add authorization header if token provided
	if s.token != "" {
		req.Header.Set("Authorization", "Bearer "+s.token)
	}
	req.Header.Set("Accept", "application/json")

	// Execute request
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("API request failed: %w", err)
	}
	defer resp.Body.Close()

	// Handle errors
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("prompt '%s' not found in API", promptID)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API returned error %d: %s", resp.StatusCode, string(body))
	}

	// Parse response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var file PromptData
	if err := json.Unmarshal(body, &file); err != nil {
		return nil, fmt.Errorf("failed to parse API response: %w", err)
	}

	return &file, nil
}

// SetClient устанавливает кастомный HTTP клиент (для тестирования).
func (s *APISource) SetClient(client *http.Client) {
	s.client = client
}
