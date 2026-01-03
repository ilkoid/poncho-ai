// Загрузка и Рендер - чтение файла и text/template.

package prompt

import (
	"bytes"
	"fmt"
	"os"
	"text/template"

	"gopkg.in/yaml.v3"
)

// Load загружает и парсит YAML файл промпта
func Load(path string) (*PromptFile, error) {
	// 1. Проверяем наличие
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, fmt.Errorf("prompt file not found: %s", path)
	}

	// 2. Читаем байты
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read error: %w", err)
	}

	// 3. Парсим YAML
	var pf PromptFile
	if err := yaml.Unmarshal(data, &pf); err != nil {
		return nil, fmt.Errorf("yaml parse error: %w", err)
	}

	return &pf, nil
}

// RenderMessages принимает данные (struct или map) и возвращает готовые сообщения
// где все {{.Field}} заменены на значения.
func (pf *PromptFile) RenderMessages(data interface{}) ([]Message, error) {
	rendered := make([]Message, len(pf.Messages))

	for i, msg := range pf.Messages {
		// Создаем шаблон
		tmpl, err := template.New("msg").Parse(msg.Content)
		if err != nil {
			return nil, fmt.Errorf("template parse error in message #%d (%s): %w", i, msg.Role, err)
		}

		// Рендерим в буфер
		var buf bytes.Buffer
		if err := tmpl.Execute(&buf, data); err != nil {
			return nil, fmt.Errorf("template execute error in message #%d: %w", i, err)
		}

		// Сохраняем результат
		rendered[i] = Message{
			Role:    msg.Role,
			Content: buf.String(),
		}
	}

	return rendered, nil
}

// LoadVisionSystemPrompt загружает системный промпт для Vision LLM.
//
// TODO: Реализовать загрузку из файла prompts/vision_system_prompt.yaml
// Сейчас возвращает дефолтный промпт.
func LoadVisionSystemPrompt(cfg interface{}) (string, error) {
	// STUB: Возвращаем дефолтный промпт для vision-анализа
	defaultPrompt := `Ты эксперт по анализу fashion-эскизов.

Твоя задача - проанализировать изображение и описать:
1. Тип изделия (куртка, брюки, платье и т.д.)
2. Силуэт (приталенный, прямой, свободный)
3. Детали (карманы, воротник, манжеты)
4. Цвет и материалы
5. Стиль (casual, business, sport)

Отвечай кратко и по делу, на русском языке.`

	return defaultPrompt, nil
}

