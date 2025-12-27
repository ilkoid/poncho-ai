// Package prompt предоставляет функции для загрузки и рендеринга промптов.
package prompt

import (
	"fmt"
	"os"

	"github.com/ilkoid/poncho-ai/pkg/config"
)

// LoadAgentSystemPrompt загружает системный промпт для AI-агента.
//
// Пытается загрузить промпт из файла {PromptsDir}/agent_system.yaml.
// Если файл не существует или ошибка загрузки — возвращает дефолтный промпт.
//
// Дефолтный промпт базовый и может быть переопределён через YAML файл
// для кастомизации поведения агента под конкретные задачи.
func LoadAgentSystemPrompt(cfg *config.AppConfig) (string, error) {
	// 1. Формируем путь к файлу промпта
	promptPath := fmt.Sprintf("%s/agent_system.yaml", cfg.App.PromptsDir)

	// 2. Проверяем существование файла
	if _, err := os.Stat(promptPath); os.IsNotExist(err) {
		// Файл не существует — возвращаем дефолтный промпт
		return getDefaultAgentPrompt(), nil
	}

	// 3. Загружаем файл
	pf, err := Load(promptPath)
	if err != nil {
		return "", fmt.Errorf("failed to load agent prompt from %s: %w", promptPath, err)
	}

	// 4. Проверяем наличие сообщений
	if len(pf.Messages) == 0 {
		return getDefaultAgentPrompt(), nil
	}

	// 5. Возвращаем контент первого сообщения (системного)
	// Предполагаем что первое сообщение — системный промпт агента
	if pf.Messages[0].Content != "" {
		return pf.Messages[0].Content, nil
	}

	return getDefaultAgentPrompt(), nil
}

// getDefaultAgentPrompt возвращает дефолтный системный промпт агента.
//
// Используется как fallback когда:
// - Файл agent_system.yaml не существует
// - Файл пустой или некорректный
func getDefaultAgentPrompt() string {
	return `Ты AI-ассистент для работы с Wildberries и анализа данных.

## Твои возможности

У тебя есть доступ к инструментам (tools) для получения актуальных данных:
- Работа с категориями Wildberries
- Работа с S3 хранилищем
- Управление задачами

## Правила работы

1. Используй tools когда нужно получить актуальные данные — не выдумывай
2. Анализируй запрос пользователя перед вызовом инструмента
3. Формируй понятные структурированные ответы
4. Если инструмент вернул ошибку — сообщи о ней пользователю
5. Если данных недостаточно — спроси уточняющий вопрос

## Примеры

Запрос: "покажи родительские категории товаров"
Действие: Вызвать get_wb_parent_categories и оформить ответ

Запрос: "какие товары в категории Женщинам?"
Действие:
  1. Вызвать get_wb_parent_categories → найти ID категории
  2. Вызвать get_wb_subjects с этим ID
  3. Оформить ответ пользователю
`
}
