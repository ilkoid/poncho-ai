package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/factory"
	"github.com/ilkoid/poncho-ai/pkg/llm"
)

func main() {
	// 1. Получаем текст запроса из аргументов
	if len(os.Args) < 2 {
		fmt.Println("Usage: simple-llm-util <your prompt text>")
		os.Exit(1)
	}
	userPrompt := strings.Join(os.Args[1:], " ")

	// 2. Грузим конфиг (чтобы получить API Key)
	cfg, err := config.Load("config.yaml")
	if err != nil {
		log.Fatalf("Config load error: %v", err)
	}

	// 3. Находим настройки дефолтной модели
	modelName := cfg.Models.DefaultVision
	modelDef, ok := cfg.Models.Definitions[modelName]
	if !ok {
		log.Fatalf("Default model '%s' not found in config definitions", modelName)
	}

	fmt.Printf("🤖 Using Model: %s (Provider: %s)\n", modelName, modelDef.Provider)
	fmt.Printf("🔑 Base URL: %s\n", modelDef.BaseURL) // Проверка URL
	fmt.Printf("💬 Sending: %q\n...\n", userPrompt)

	// 4. Создаем провайдера через Фабрику
	provider, err := factory.NewLLMProvider(modelDef)
	if err != nil {
		log.Fatalf("Provider init error: %v", err)
	}

	// 5. Формируем запрос
	req := llm.ChatRequest{
		Model:       modelDef.ModelName, // "glm-4.6v-flash"
		Temperature: 0.7,
		MaxTokens:   4000,
		Messages: []llm.Message{
			{
				Role: llm.RoleSystem,
				Content: []llm.ContentPart{
					{Type: llm.TypeText, Text: "Ты полезный CLI ассистент. Отвечай кратко."},
				},
			},
			{
				Role: llm.RoleUser,
				Content: []llm.ContentPart{
					{Type: llm.TypeText, Text: userPrompt},
				},
			},
		},
	}

	// 6. Отправляем (с таймаутом)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	start := time.Now()
	response, err := provider.Chat(ctx, req)
	duration := time.Since(start)

	if err != nil {
		log.Fatalf("\n❌ LLM Error: %v", err)
	}

	// 7. Выводим результат
	fmt.Printf("\n✅ Response (took %v):\n%s\n", duration, response)
}

