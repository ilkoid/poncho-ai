// Chain-cli — CLI утилита для тестирования Chain Pattern.
//
// Rule 0: Используем существующую инфраструктуру из pkg/app/components.go
// вместо дублирования кода инициализации.
package main

import (
	"fmt"

	appcomponents "github.com/ilkoid/poncho-ai/pkg/app"
	"github.com/ilkoid/poncho-ai/pkg/config"
)

// findConfigPath находит config.yaml используя существующую инфраструктуру.
//
// Rule 0: Переиспользуем код вместо дублирования.
// Rule 11: config.yaml должен находиться рядом с бинарником.
func findConfigPath() string {
	finder := &appcomponents.DefaultConfigPathFinder{}
	return finder.FindConfigPath()
}

// createComponents создаёт все необходимые компоненты для работы Chain.
//
// Rule 0: Используем appcomponents.Initialize() вместо дублирования 133 строк кода.
func createComponents(cfg *config.AppConfig, modelNameOverride string) (*appcomponents.Components, error) {
	// Rule 0: Переиспользуем существующую инфраструктуру
	// Initialize регистрирует все доступные инструменты согласно config.yaml
	comps, err := appcomponents.Initialize(cfg, 10, "")
	if err != nil {
		return nil, fmt.Errorf("failed to initialize components: %w", err)
	}

	// Примечание: modelNameOverride будет обрабатываться в main.go при настройке Chain
	// LLM provider уже создан с дефолтной моделью из конфигурации

	return comps, nil
}

// defaultSystemPrompt возвращает дефолтный системный промпт.
func defaultSystemPrompt() string {
	return `Ты AI-ассистент для тестирования Chain Pattern.

## Твои возможности

У тебя есть доступ к инструментам (tools) для получения данных:
- Работа с категориями Wildberries
- Работа с S3 хранилищем
- Управление планом действий

## Правила работы

1. Используй tools когда нужно получить актуальные данные
2. Анализируй запрос пользователя перед вызовом инструмента
3. Формируй понятные структурированные ответы
4. Если инструмент вернул ошибку — сообщи о ней пользователю
`
}
