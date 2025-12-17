package app

import (
"github.com/ilkoid/poncho-ai/pkg/config"
"github.com/ilkoid/poncho-ai/pkg/classifier"
"github.com/ilkoid/poncho-ai/pkg/s3storage"

)

// GlobalState хранит данные сессии
type GlobalState struct {
    Config *config.AppConfig
    S3     *s3storage.Client
    
    // Данные текущей сессии
    CurrentArticleID string
    CurrentModel     string
    IsProcessing     bool

    // Files хранит классифицированные файлы артикула
    // Ключ: тег (например, "sketch", "plm_data")
    // Значение: список файлов
    Files map[string][]classifier.ClassifiedFile // <--- Добавляем это поле
}

// NewState создает начальное состояние
func NewState(cfg *config.AppConfig, s3Client *s3storage.Client) *GlobalState {
    return &GlobalState{
        Config:           cfg,
        S3:               s3Client,
        CurrentArticleID: "NONE",
        CurrentModel:     cfg.Models.DefaultVision,
        IsProcessing:     false,
        
        // Инициализируем пустую карту, чтобы не было panic при чтении
        Files:            make(map[string][]classifier.ClassifiedFile), 
    }
}