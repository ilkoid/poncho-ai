// Package config предоставляет конфигурационные типы для cmd/ утилит.
//
// Файл содержит utility-specific конфигурации, которые переиспользуются
// между различными cmd/ утилитами для избежания дублирования кода.
//
// Соблюдение правил из dev_manifest.md:
//   - Rule 0: Code Reuse — используем существующие решения
//   - Rule 2: Configuration — YAML с ENV поддержкой
package config

// PromotionConfig — конфигурация для download-wb-promotion утилиты.
//
// Используется для загрузки данных о продвижении товаров с WB API.
type PromotionConfig struct {
	DbPath     string               `yaml:"db_path"`     // Путь к SQLite базе данных
	Begin      string               `yaml:"begin"`       // Начальная дата (YYYY-MM-DD)
	End        string               `yaml:"end"`         // Конечная дата (YYYY-MM-DD)
	Days       int                  `yaml:"days"`        // Дней от сегодня (альтернатива begin/end)
	Statuses   []int                `yaml:"statuses"`    // Фильтр по статусам (например, 9, 11)
	Resume     bool                 `yaml:"resume"`      // Продолжить с последней даты
	RateLimits PromotionRateLimits  `yaml:"rate_limits"` // Rate limits per endpoint (req/min)
	AdaptiveRecoverAfter int     `yaml:"adaptive_recover_after"` // OKs to restore to api floor after 429 (default: 5)
	AdaptiveProbeAfter   int     `yaml:"adaptive_probe_after"`   // OKs at api floor before probing desired (default: 10)
	MaxBackoffSeconds    int     `yaml:"max_backoff_seconds"`    // Cap for exponential backoff (default: 60)
	SkipDetails bool                 `yaml:"skip_details"` // Skip campaign details download (name, payment_type)
	SkipCampaigns bool                `yaml:"skip_campaigns"` // Skip campaign list download (reuse IDs from DB)
	SkipStats     bool                `yaml:"skip_stats"`     // Skip stats download
}

// PromotionRateLimits — rate limits для promotion API endpoints.
//
// Два уровня rate для каждого endpoint:
//   - desired:      желаемый rate (можно превышать swagger — adaptive limiter обработает 429)
//   - desired_burst: burst для desired rate
//   - api:           swagger-documented rate (recovery floor после 429)
//   - api_burst:     burst для api rate
//
// Если desired не указан — используется api (без превышения swagger).
type PromotionRateLimits struct {
	PromotionCount      int `yaml:"promotion_count"`       // desired rate (default: 300)
	PromotionCountBurst int `yaml:"promotion_count_burst"` // desired burst (default: 5)
	PromotionCountApi    int `yaml:"promotion_count_api"`  // swagger rate (default: 300)
	PromotionCountApiBurst int `yaml:"promotion_count_api_burst"` // swagger burst (default: 5)

	AdvertDetails       int `yaml:"advert_details"`        // desired rate (default: 300)
	AdvertDetailsBurst  int `yaml:"advert_details_burst"` // desired burst (default: 5)
	AdvertDetailsApi    int `yaml:"advert_details_api"`   // swagger rate (default: 300)
	AdvertDetailsApiBurst int `yaml:"advert_details_api_burst"` // swagger burst (default: 5)

	Fullstats           int `yaml:"fullstats"`            // desired rate (default: 3)
	FullstatsBurst      int `yaml:"fullstats_burst"`      // desired burst (default: 1)
	FullstatsApi        int `yaml:"fullstats_api"`        // swagger rate (default: 3)
	FullstatsApiBurst   int `yaml:"fullstats_api_burst"`  // swagger burst (default: 1)
}

// GetDefaults возвращает дефолтные значения для незаполненных полей.
// Если desired не указан — используется api значение (без превышения swagger).
func (c *PromotionConfig) GetDefaults() PromotionConfig {
	result := *c
	if result.DbPath == "" {
		result.DbPath = "promotion.db"
	}
	if result.Days == 0 {
		result.Days = 7
	}

	// Promotion count
	if result.RateLimits.PromotionCountApi == 0 {
		result.RateLimits.PromotionCountApi = 300 // 5 req/sec (swagger)
	}
	if result.RateLimits.PromotionCount == 0 {
		result.RateLimits.PromotionCount = result.RateLimits.PromotionCountApi // default = api
	}
	if result.RateLimits.PromotionCountApiBurst == 0 {
		result.RateLimits.PromotionCountApiBurst = 5
	}
	if result.RateLimits.PromotionCountBurst == 0 {
		result.RateLimits.PromotionCountBurst = result.RateLimits.PromotionCountApiBurst
	}

	// Advert details
	if result.RateLimits.AdvertDetailsApi == 0 {
		result.RateLimits.AdvertDetailsApi = 300 // 5 req/sec (swagger)
	}
	if result.RateLimits.AdvertDetails == 0 {
		result.RateLimits.AdvertDetails = result.RateLimits.AdvertDetailsApi
	}
	if result.RateLimits.AdvertDetailsApiBurst == 0 {
		result.RateLimits.AdvertDetailsApiBurst = 5
	}
	if result.RateLimits.AdvertDetailsBurst == 0 {
		result.RateLimits.AdvertDetailsBurst = result.RateLimits.AdvertDetailsApiBurst
	}

	// Fullstats
	if result.RateLimits.FullstatsApi == 0 {
		result.RateLimits.FullstatsApi = 3 // 3 req/min (swagger)
	}
	if result.RateLimits.Fullstats == 0 {
		result.RateLimits.Fullstats = result.RateLimits.FullstatsApi
	}
	if result.RateLimits.FullstatsApiBurst == 0 {
		result.RateLimits.FullstatsApiBurst = 1
	}
	if result.RateLimits.FullstatsBurst == 0 {
		result.RateLimits.FullstatsBurst = result.RateLimits.FullstatsApiBurst
	}

	// Adaptive tuning defaults
	if result.AdaptiveRecoverAfter == 0 {
		result.AdaptiveRecoverAfter = 5
	}
	if result.AdaptiveProbeAfter == 0 {
		result.AdaptiveProbeAfter = 10
	}
	if result.MaxBackoffSeconds == 0 {
		result.MaxBackoffSeconds = 60
	}

	return result
}

// DownloadConfig — конфигурация для download-wb-sales утилиты.
//
// Используется для загрузки данных о продажах с WB API.
type DownloadConfig struct {
	From        string `yaml:"from"`         // Начальная дата (YYYY-MM-DD)
	To          string `yaml:"to"`           // Конечная дата (YYYY-MM-DD)
	DbPath      string `yaml:"db_path"`      // Путь к SQLite базе данных
	FBWOnly     bool   `yaml:"fbw_only"`     // Только FBW продажи
	Resume      bool   `yaml:"resume"`       // Продолжить с последней даты
	IntervalDays int   `yaml:"interval_days"` // Дней на один API запрос (default: 30)
}

// GetDefaults возвращает дефолтные значения для незаполненных полей.
func (c *DownloadConfig) GetDefaults() DownloadConfig {
	result := *c
	if result.DbPath == "" {
		result.DbPath = "sales.db"
	}
	if result.IntervalDays == 0 {
		result.IntervalDays = 30
	}
	return result
}

// FunnelConfig — конфигурация для funnel данных (WB Analytics API v3).
//
// Используется для загрузки воронки продаж с расширенными метриками.
// Двухуровневый rate limiting: desired (агрессивный) + api (swagger floor для восстановления).
// См. dev_limits.md для деталей.
type FunnelConfig struct {
	Days       int    `yaml:"days"`        // Дней истории (1-365) — альтернатива from/to
	BatchSize  int    `yaml:"batch_size"`  // Продуктов на запрос (max 20)
	RateLimit  int    `yaml:"rate_limit"`  // Запросов в минуту (default: 3, WB Analytics API limit)
	BurstLimit int    `yaml:"burst"`       // Burst для rate limiter (default: 3)
	From       string `yaml:"from"`        // Начальная дата YYYY-MM-DD (опционально, приоритет над days)
	To         string `yaml:"to"`          // Конечная дата YYYY-MM-DD (опционально, приоритет над days)
	MaxBatches int    `yaml:"max_batches"` // Макс. батчей для загрузки (0 = все, полезно для тестов)

	// Adaptive rate limiting (two-level: desired + api floor)
	FunnelRateLimit         int `yaml:"funnel_rate_limit"`           // desired rate (default: api value)
	FunnelRateLimitBurst    int `yaml:"funnel_rate_limit_burst"`     // desired burst
	FunnelRateLimitApi      int `yaml:"funnel_rate_limit_api"`       // swagger rate (default: 3)
	FunnelRateLimitApiBurst int `yaml:"funnel_rate_limit_api_burst"` // swagger burst (default: 3)

	// Adaptive tuning (see dev_limits.md)
	AdaptiveRecoverAfter int `yaml:"adaptive_recover_after"` // OKs to restore to api floor after 429 (default: 5)
	AdaptiveProbeAfter   int `yaml:"adaptive_probe_after"`   // OKs at api floor before probing desired (default: 10)
	MaxBackoffSeconds    int `yaml:"max_backoff_seconds"`    // Cap for exponential backoff (default: 60)
}

// GetDefaults возвращает дефолтные значения для незаполненных полей.
// Каскадные дефолты: api → desired, api_burst → desired_burst.
func (c *FunnelConfig) GetDefaults() FunnelConfig {
	result := *c
	if result.Days == 0 {
		result.Days = 7
	}
	if result.BatchSize == 0 {
		result.BatchSize = 20
	}
	if result.RateLimit == 0 {
		result.RateLimit = 3 // WB Analytics API default: 3 req/min
	}
	if result.BurstLimit == 0 {
		result.BurstLimit = 3
	}

	// Funnel rate limits (two-level adaptive)
	if result.FunnelRateLimitApi == 0 {
		result.FunnelRateLimitApi = 3 // swagger: 3 req/min
	}
	if result.FunnelRateLimit == 0 {
		result.FunnelRateLimit = result.FunnelRateLimitApi // default = api (safe)
	}
	if result.FunnelRateLimitApiBurst == 0 {
		result.FunnelRateLimitApiBurst = 3
	}
	if result.FunnelRateLimitBurst == 0 {
		result.FunnelRateLimitBurst = result.FunnelRateLimitApiBurst
	}

	// Adaptive tuning defaults
	if result.AdaptiveRecoverAfter == 0 {
		result.AdaptiveRecoverAfter = 5
	}
	if result.AdaptiveProbeAfter == 0 {
		result.AdaptiveProbeAfter = 10
	}
	if result.MaxBackoffSeconds == 0 {
		result.MaxBackoffSeconds = 60
	}

	return result
}

// WBClientConfig — расширенная конфигурация WB клиента для утилит.
//
// Включает дополнительные поля, специфичные для cmd/ утилит.
// Встраивает стандартную WBConfig для базовых настроек.
type WBClientConfig struct {
	APIKey          string `yaml:"api_key"`           // API ключ (WB_STAT)
	AnalyticsAPIKey string `yaml:"analytics_api_key"` // Analytics API ключ (опционально)
	BaseURL         string `yaml:"base_url"`          // Базовый URL Content API
	RateLimit       int    `yaml:"rate_limit"`        // Запросов в минуту
	BurstLimit      int    `yaml:"burst"`             // Burst для rate limiter
	Timeout         string `yaml:"timeout"`           // Timeout HTTP запросов
	Endpoint        string `yaml:"endpoint"`          // Альтернативный endpoint (опционально)
}

// ToWBConfig конвертирует WBClientConfig в стандартную WBConfig.
func (c *WBClientConfig) ToWBConfig() WBConfig {
	return WBConfig{
		APIKey:     c.APIKey,
		BaseURL:    c.BaseURL,
		RateLimit:  c.RateLimit,
		BurstLimit: c.BurstLimit,
		Timeout:    c.Timeout,
	}
}

// GetDefaults возвращает дефолтные значения для незаполненных полей.
func (c *WBClientConfig) GetDefaults() WBClientConfig {
	result := *c
	if result.BaseURL == "" {
		result.BaseURL = "https://content-api.wildberries.ru"
	}
	if result.RateLimit == 0 {
		result.RateLimit = 100
	}
	if result.BurstLimit == 0 {
		result.BurstLimit = 5
	}
	if result.Timeout == "" {
		result.Timeout = "30s"
	}
	return result
}

// FeedbacksConfig — конфигурация для download-wb-feedbacks утилиты.
//
// Используется для загрузки отзывов и вопросов с WB Feedbacks API.
type FeedbacksConfig struct {
	DbPath    string `yaml:"db_path"`   // Путь к SQLite базе данных
	Begin     string `yaml:"begin"`     // Начальная дата (YYYY-MM-DD)
	End       string `yaml:"end"`       // Конечная дата (YYYY-MM-DD)
	Days      int    `yaml:"days"`      // Дней от сегодня (альтернатива begin/end)
	Feedbacks bool   `yaml:"feedbacks"` // Загружать отзывы (default: true)
	Questions bool   `yaml:"questions"` // Загружать вопросы (default: true)
}

// GetDefaults возвращает дефолтные значения для незаполненных полей.
func (c *FeedbacksConfig) GetDefaults() FeedbacksConfig {
	result := *c
	if result.DbPath == "" {
		result.DbPath = "feedbacks.db"
	}
	if result.Days == 0 {
		result.Days = 7
	}
	if !result.Feedbacks && !result.Questions {
		result.Feedbacks = true
		result.Questions = true
	}
	return result
}

// FunnelAggregatedConfig — конфигурация для aggregated funnel данных.
//
// Используется для загрузки агрегированной воронки продаж за период
// из WB Analytics API v3 (/sales-funnel/products).
type FunnelAggregatedConfig struct {
	// Периоды (required)
	SelectedStart string `yaml:"selected_start"` // YYYY-MM-DD
	SelectedEnd   string `yaml:"selected_end"`   // YYYY-MM-DD
	PastStart     string `yaml:"past_start"`     // YYYY-MM-DD (optional)
	PastEnd       string `yaml:"past_end"`       // YYYY-MM-DD (optional)

	// Фильтры (optional - пусто = все товары)
	NmIDs         []int    `yaml:"nm_ids"`          // Список nmID
	BrandNames    []string `yaml:"brand_names"`     // Фильтр по брендам
	SubjectIDs    []int    `yaml:"subject_ids"`     // Фильтр по категориям
	TagIDs        []int    `yaml:"tag_ids"`         // Фильтр по тегам
	SkipDeletedNm bool     `yaml:"skip_deleted_nm"` // Скрыть удалённые

	// Сортировка (optional)
	OrderByField string `yaml:"order_by_field"` // openCard, orders, buyouts, etc.
	OrderByMode  string `yaml:"order_by_mode"`  // asc, desc

	// Пагинация
	PageSize int `yaml:"page_size"` // Товаров за запрос (0 = auto, max 1000)

	// Rate limiting
	RateLimit  int `yaml:"rate_limit"`  // Запросов в минуту (default: 3)
	BurstLimit int `yaml:"burst"`       // Burst (default: 3)

	// Хранилище
	DBPath string `yaml:"db_path"` // Путь к SQLite базе
}

// GetDefaults возвращает дефолтные значения.
func (c *FunnelAggregatedConfig) GetDefaults() FunnelAggregatedConfig {
	result := *c
	if result.RateLimit == 0 {
		result.RateLimit = 3 // WB Analytics API default
	}
	if result.BurstLimit == 0 {
		result.BurstLimit = 3
	}
	if result.PageSize == 0 {
		result.PageSize = 100 // Optimal balance
	}
	if result.DBPath == "" {
		result.DBPath = "sales.db"
	}
	return result
}
