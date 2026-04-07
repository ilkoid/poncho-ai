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
	Days        int    `yaml:"days"`         // Дней от сегодня (альтернатива from/to, default: 7)
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
	DbPath     string             `yaml:"db_path"`     // Путь к SQLite базе данных
	Begin      string             `yaml:"begin"`       // Начальная дата (YYYY-MM-DD)
	End        string             `yaml:"end"`         // Конечная дата (YYYY-MM-DD)
	Days       int                `yaml:"days"`        // Дней от сегодня (альтернатива begin/end)
	Feedbacks  bool               `yaml:"feedbacks"`   // Загружать отзывы (default: true)
	Questions  bool               `yaml:"questions"`   // Загружать вопросы (default: true)
	RateLimits FeedbacksRateLimits `yaml:"rate_limits"` // Rate limits per endpoint (req/min)
	AdaptiveProbeAfter int        `yaml:"adaptive_probe_after"`   // OKs at api floor before probing desired (default: 10)
	MaxBackoffSeconds  int        `yaml:"max_backoff_seconds"`    // Cap for cooldown (default: 60)
}

// FeedbacksRateLimits — rate limits для feedbacks API endpoints.
//
// Feedbacks API: 3 req/sec (180 req/min), burst 6.
//
// Два уровня rate для каждого endpoint:
//   - desired:      желаемый rate (можно превышать swagger — adaptive limiter обработает 429)
//   - desired_burst: burst для desired rate
//   - api:           swagger-documented rate (recovery floor после 429)
//   - api_burst:     burst для api rate
//
// Если desired не указан — используется api (без превышения swagger).
type FeedbacksRateLimits struct {
	DownloadFeedbacks      int `yaml:"download_feedbacks"`       // desired rate (default: 180)
	DownloadFeedbacksBurst int `yaml:"download_feedbacks_burst"` // desired burst (default: 6)
	DownloadFeedbacksApi    int `yaml:"download_feedbacks_api"`  // swagger rate (default: 180)
	DownloadFeedbacksApiBurst int `yaml:"download_feedbacks_api_burst"` // swagger burst (default: 6)

	DownloadQuestions      int `yaml:"download_questions"`       // desired rate (default: 180)
	DownloadQuestionsBurst int `yaml:"download_questions_burst"` // desired burst (default: 6)
	DownloadQuestionsApi    int `yaml:"download_questions_api"`  // swagger rate (default: 180)
	DownloadQuestionsApiBurst int `yaml:"download_questions_api_burst"` // swagger burst (default: 6)
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

	// Rate limits defaults
	if result.RateLimits.DownloadFeedbacksApi == 0 {
		result.RateLimits.DownloadFeedbacksApi = 180 // 3 req/sec
	}
	if result.RateLimits.DownloadFeedbacks == 0 {
		result.RateLimits.DownloadFeedbacks = result.RateLimits.DownloadFeedbacksApi
	}
	if result.RateLimits.DownloadFeedbacksApiBurst == 0 {
		result.RateLimits.DownloadFeedbacksApiBurst = 6
	}
	if result.RateLimits.DownloadFeedbacksBurst == 0 {
		result.RateLimits.DownloadFeedbacksBurst = result.RateLimits.DownloadFeedbacksApiBurst
	}

	if result.RateLimits.DownloadQuestionsApi == 0 {
		result.RateLimits.DownloadQuestionsApi = 180 // 3 req/sec
	}
	if result.RateLimits.DownloadQuestions == 0 {
		result.RateLimits.DownloadQuestions = result.RateLimits.DownloadQuestionsApi
	}
	if result.RateLimits.DownloadQuestionsApiBurst == 0 {
		result.RateLimits.DownloadQuestionsApiBurst = 6
	}
	if result.RateLimits.DownloadQuestionsBurst == 0 {
		result.RateLimits.DownloadQuestionsBurst = result.RateLimits.DownloadQuestionsApiBurst
	}

	// Adaptive defaults
	if result.AdaptiveProbeAfter == 0 {
		result.AdaptiveProbeAfter = 10
	}
	if result.MaxBackoffSeconds == 0 {
		result.MaxBackoffSeconds = 60
	}

	return result
}

// FunnelAggregatedConfig — конфигурация для aggregated funnel данных.
//
// Используется для загрузки агрегированной воронки продаж за период
// из WB Analytics API v3 (/sales-funnel/products).
type FunnelAggregatedConfig struct {
	// Периоды (required)
	Days          int    `yaml:"days"`           // Дней от сегодня (альтернатива selected_start/end)
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

	// Rate limiting (legacy fields for backwards compatibility)
	RateLimit  int `yaml:"rate_limit"`  // Запросов в минуту (deprecated: use rate_limits instead)
	BurstLimit int `yaml:"burst"`       // Burst (deprecated: use rate_limits instead)

	// Adaptive rate limiting
	RateLimits         FunnelAggregatedRateLimits `yaml:"rate_limits"` // Rate limits per endpoint (req/min)
	AdaptiveProbeAfter int                        `yaml:"adaptive_probe_after"` // OKs at api floor before probing desired (default: 10)
	MaxBackoffSeconds  int                        `yaml:"max_backoff_seconds"`  // Cap for cooldown (default: 60)

	// Хранилище
	DBPath string `yaml:"db_path"` // Путь к SQLite базе
}

// FunnelAggregatedRateLimits — rate limits для aggregated funnel API endpoint.
//
// Analytics API v3: 3 req/min, burst 3 (very slow).
//
// Два уровня rate:
//   - desired:      желаемый rate (можно превышать swagger — adaptive limiter обработает 429)
//   - desired_burst: burst для desired rate
//   - api:           swagger-documented rate (recovery floor после 429)
//   - api_burst:     burst для api rate
//
// Если desired не указан — используется api (без превышения swagger).
type FunnelAggregatedRateLimits struct {
	FunnelAggregated      int `yaml:"funnel_aggregated"`       // desired rate (default: 3)
	FunnelAggregatedBurst int `yaml:"funnel_aggregated_burst"` // desired burst (default: 3)
	FunnelAggregatedApi    int `yaml:"funnel_aggregated_api"`  // swagger rate (default: 3)
	FunnelAggregatedApiBurst int `yaml:"funnel_aggregated_api_burst"` // swagger burst (default: 3)
}

// GetDefaults возвращает дефолтные значения.
func (c *FunnelAggregatedConfig) GetDefaults() FunnelAggregatedConfig {
	result := *c

	// Legacy rate_limits (for backwards compatibility)
	if result.RateLimit == 0 {
		result.RateLimit = 3 // WB Analytics API default
	}
	if result.BurstLimit == 0 {
		result.BurstLimit = 3
	}

	// New adaptive rate limits
	if result.RateLimits.FunnelAggregatedApi == 0 {
		result.RateLimits.FunnelAggregatedApi = result.RateLimit // Use legacy value as default
	}
	if result.RateLimits.FunnelAggregated == 0 {
		result.RateLimits.FunnelAggregated = result.RateLimits.FunnelAggregatedApi
	}
	if result.RateLimits.FunnelAggregatedApiBurst == 0 {
		result.RateLimits.FunnelAggregatedApiBurst = result.BurstLimit
	}
	if result.RateLimits.FunnelAggregatedBurst == 0 {
		result.RateLimits.FunnelAggregatedBurst = result.RateLimits.FunnelAggregatedApiBurst
	}

	// Adaptive defaults
	if result.AdaptiveProbeAfter == 0 {
		result.AdaptiveProbeAfter = 10
	}
	if result.MaxBackoffSeconds == 0 {
		result.MaxBackoffSeconds = 60
	}

	// Pagination defaults
	if result.PageSize == 0 {
		result.PageSize = 100 // Optimal balance
	}

	// Database defaults
	if result.DBPath == "" {
		result.DBPath = "sales.db"
	}

	return result
}

// StocksConfig — конфигурация для download-wb-stocks утилиты.
//
// Используется для загрузки ежедневных снимков остатков с WB Analytics API.
// Двухуровневый rate limiting: desired (агрессивный) + api (swagger floor для восстановления).
// См. dev_limits.md для деталей.
type StocksConfig struct {
	DbPath             string             `yaml:"db_path"`              // Путь к SQLite базе данных (default: sales.db)
	FirstDate          string             `yaml:"first_date"`           // Начало отсчёта для gap detection (YYYY-MM-DD)
	APIKeyEnv          string             `yaml:"api_key_env"`          // Имя переменной окружения для API ключа (default: WB_API_ANALYTICS_AND_PROMO_KEY)
	RateLimits         StocksRateLimits   `yaml:"rate_limits"`          // Rate limits для warehouse endpoint
	AdaptiveProbeAfter int                `yaml:"adaptive_probe_after"` // OKs at api floor before probing desired (default: 10)
	MaxBackoffSeconds  int                `yaml:"max_backoff_seconds"`  // Cap for exponential backoff (default: 60)
}

// StocksRateLimits — rate limits для stocks warehouse API endpoint.
//
// Analytics API: 3 req/min, burst 1.
//
// Два уровня rate:
//   - desired:      желаемый rate (можно превышать swagger — adaptive limiter обработает 429)
//   - desired_burst: burst для desired rate
//   - api:           swagger-documented rate (recovery floor после 429)
//   - api_burst:     burst для api rate
//
// Если desired не указан — используется api (без превышения swagger).
type StocksRateLimits struct {
	Warehouse         int `yaml:"warehouse"`           // desired rate (default: 3)
	WarehouseBurst    int `yaml:"warehouse_burst"`     // desired burst (default: 1)
	WarehouseApi      int `yaml:"warehouse_api"`       // swagger rate (default: 3)
	WarehouseApiBurst int `yaml:"warehouse_api_burst"` // swagger burst (default: 1)
}

// GetDefaults возвращает дефолтные значения для незаполненных полей.
// Каскадные дефолты: api -> desired, api_burst -> desired_burst.
func (c *StocksConfig) GetDefaults() StocksConfig {
	result := *c
	if result.DbPath == "" {
		result.DbPath = "sales.db"
	}
	if result.APIKeyEnv == "" {
		result.APIKeyEnv = "WB_API_ANALYTICS_AND_PROMO_KEY" // default env var
	}

	// Warehouse rate limits (two-level adaptive)
	if result.RateLimits.WarehouseApi == 0 {
		result.RateLimits.WarehouseApi = 3 // swagger: 3 req/min
	}
	if result.RateLimits.Warehouse == 0 {
		result.RateLimits.Warehouse = result.RateLimits.WarehouseApi // default = api (safe)
	}
	if result.RateLimits.WarehouseApiBurst == 0 {
		result.RateLimits.WarehouseApiBurst = 1
	}
	if result.RateLimits.WarehouseBurst == 0 {
		result.RateLimits.WarehouseBurst = result.RateLimits.WarehouseApiBurst
	}

	// Adaptive tuning defaults
	if result.AdaptiveProbeAfter == 0 {
		result.AdaptiveProbeAfter = 10
	}
	if result.MaxBackoffSeconds == 0 {
		result.MaxBackoffSeconds = 60
	}

	return result
}

// StockHistoryConfig — конфигурация для download-wb-stock-history утилиты.
//
// Используется для загрузки исторических данных об остатках через CSV отчёты.
type StockHistoryConfig struct {
	DbPath      string                `yaml:"db_path"`
	Begin       string                `yaml:"begin"`
	End         string                `yaml:"end"`
	Days        int                   `yaml:"days"`
	ReportType  string                `yaml:"report_type"`
	StockType   string                `yaml:"stock_type"`
	Resume      bool                  `yaml:"resume"`
	RateLimits  StockHistoryRateLimits `yaml:"rate_limits"`
	AdaptiveProbeAfter int            `yaml:"adaptive_probe_after"`
	MaxBackoffSeconds  int            `yaml:"max_backoff_seconds"`
	PollIntervalSec    int            `yaml:"poll_interval_sec"`
	PollTimeoutMin     int            `yaml:"poll_timeout_min"`
}

type StockHistoryRateLimits struct {
	Create           int `yaml:"create"`
	CreateBurst      int `yaml:"create_burst"`
	CreateApi        int `yaml:"create_api"`
	CreateApiBurst   int `yaml:"create_api_burst"`
	StatusCheck      int `yaml:"status_check"`
	StatusCheckBurst int `yaml:"status_check_burst"`
	StatusCheckApi   int `yaml:"status_check_api"`
	StatusCheckApiBurst int `yaml:"status_check_api_burst"`
	Download         int `yaml:"download"`
	DownloadBurst    int `yaml:"download_burst"`
	DownloadApi      int `yaml:"download_api"`
	DownloadApiBurst int `yaml:"download_api_burst"`
}

func (c *StockHistoryConfig) GetDefaults() StockHistoryConfig {
	result := *c
	if result.DbPath == "" { result.DbPath = "stock_history.db" }
	if result.Days == 0 { result.Days = 30 }
	if result.ReportType == "" { result.ReportType = "daily" }
	if result.StockType == "" { result.StockType = "" }
	if result.RateLimits.CreateApi == 0 { result.RateLimits.CreateApi = 3 }
	if result.RateLimits.Create == 0 { result.RateLimits.Create = result.RateLimits.CreateApi }
	if result.RateLimits.CreateApiBurst == 0 { result.RateLimits.CreateApiBurst = 3 }
	if result.RateLimits.CreateBurst == 0 { result.RateLimits.CreateBurst = result.RateLimits.CreateApiBurst }
	if result.RateLimits.StatusCheckApi == 0 { result.RateLimits.StatusCheckApi = 3 }
	if result.RateLimits.StatusCheck == 0 { result.RateLimits.StatusCheck = result.RateLimits.StatusCheckApi }
	if result.RateLimits.StatusCheckApiBurst == 0 { result.RateLimits.StatusCheckApiBurst = 3 }
	if result.RateLimits.StatusCheckBurst == 0 { result.RateLimits.StatusCheckBurst = result.RateLimits.StatusCheckApiBurst }
	if result.RateLimits.DownloadApi == 0 { result.RateLimits.DownloadApi = 3 }
	if result.RateLimits.Download == 0 { result.RateLimits.Download = result.RateLimits.DownloadApi }
	if result.RateLimits.DownloadApiBurst == 0 { result.RateLimits.DownloadApiBurst = 3 }
	if result.RateLimits.DownloadBurst == 0 { result.RateLimits.DownloadBurst = result.RateLimits.DownloadApiBurst }
	if result.AdaptiveProbeAfter == 0 { result.AdaptiveProbeAfter = 10 }
	if result.MaxBackoffSeconds == 0 { result.MaxBackoffSeconds = 60 }
	if result.PollIntervalSec == 0 { result.PollIntervalSec = 30 }
	if result.PollTimeoutMin == 0 { result.PollTimeoutMin = 30 }
	return result
}

// RegionSalesConfig — конфигурация для download-wb-region-sales утилиты.
//
// Используется для загрузки данных о продажах по регионам с WB Seller Analytics API.
// Двухуровневый rate limiting: desired + api (swagger floor для восстановления).
type RegionSalesConfig struct {
	DbPath             string                  `yaml:"db_path"`
	Days               int                     `yaml:"days"`
	Begin              string                  `yaml:"begin"`
	End                string                  `yaml:"end"`
	APIKeyEnv          string                  `yaml:"api_key_env"`
	RateLimits         RegionSalesRateLimits   `yaml:"rate_limits"`
	AdaptiveProbeAfter int                     `yaml:"adaptive_probe_after"`
	MaxBackoffSeconds  int                     `yaml:"max_backoff_seconds"`
}

// RegionSalesRateLimits — rate limits для region sale API endpoint.
//
// Seller Analytics API: 1 req/10sec (6 req/min), burst 5 (swagger).
type RegionSalesRateLimits struct {
	RegionSale         int `yaml:"region_sale"`
	RegionSaleBurst    int `yaml:"region_sale_burst"`
	RegionSaleApi      int `yaml:"region_sale_api"`
	RegionSaleApiBurst int `yaml:"region_sale_api_burst"`
}

// GetDefaults возвращает дефолтные значения для незаполненных полей.
// Каскадные дефолты: api -> desired, api_burst -> desired_burst.
// NOTE: Days is NOT defaulted here — default in main.go only when Begin/End empty.
func (c *RegionSalesConfig) GetDefaults() RegionSalesConfig {
	result := *c
	if result.DbPath == "" {
		result.DbPath = "sales.db"
	}
	if result.APIKeyEnv == "" {
		result.APIKeyEnv = "WB_API_ANALYTICS_AND_PROMO_KEY"
	}

	// Region sale rate limits (two-level adaptive)
	// Swagger: 1 req/10sec = 6 req/min, burst 5
	if result.RateLimits.RegionSaleApi == 0 {
		result.RateLimits.RegionSaleApi = 6
	}
	if result.RateLimits.RegionSale == 0 {
		result.RateLimits.RegionSale = result.RateLimits.RegionSaleApi
	}
	if result.RateLimits.RegionSaleApiBurst == 0 {
		result.RateLimits.RegionSaleApiBurst = 5
	}
	if result.RateLimits.RegionSaleBurst == 0 {
		result.RateLimits.RegionSaleBurst = result.RateLimits.RegionSaleApiBurst
	}

	// Adaptive tuning defaults
	if result.AdaptiveProbeAfter == 0 {
		result.AdaptiveProbeAfter = 10
	}
	if result.MaxBackoffSeconds == 0 {
		result.MaxBackoffSeconds = 60
	}

	return result
}

// ============================================================================
// Content Cards (product catalog) — WB Content API
// ============================================================================

// CardsConfig — конфигурация для download-wb-cards утилиты.
//
// Используется для загрузки карточек товаров с WB Content API (/content/v2/get/cards/list).
// Курсорная пагинация (не date-based). Нет поля Days.
// Двухуровневый rate limiting: desired + api (swagger floor для восстановления).
type CardsConfig struct {
	DbPath             string          `yaml:"db_path"`
	APIKeyEnv          string          `yaml:"api_key_env"`
	RateLimits         CardsRateLimits `yaml:"rate_limits"`
	AdaptiveProbeAfter int             `yaml:"adaptive_probe_after"`
	MaxBackoffSeconds  int             `yaml:"max_backoff_seconds"`
}

// CardsRateLimits — rate limits для cards list API endpoint.
//
// Content API: 100 req/min, burst 5 (swagger).
//
// Два уровня rate:
//   - desired:      желаемый rate (можно превышать swagger — adaptive limiter обработает 429)
//   - desired_burst: burst для desired rate
//   - api:           swagger-documented rate (recovery floor после 429)
//   - api_burst:     burst для api rate
//
// Если desired не указан — используется api (без превышения swagger).
type CardsRateLimits struct {
	CardsList         int `yaml:"cards_list"`
	CardsListBurst    int `yaml:"cards_list_burst"`
	CardsListApi      int `yaml:"cards_list_api"`
	CardsListApiBurst int `yaml:"cards_list_api_burst"`
}

// GetDefaults возвращает дефолтные значения для незаполненных полей.
// Каскадные дефолты: api -> desired, api_burst -> desired_burst.
// NOTE: Нет Days поля — cards используют курсорную пагинацию, не date ranges.
func (c *CardsConfig) GetDefaults() CardsConfig {
	result := *c
	if result.DbPath == "" {
		result.DbPath = "cards.db"
	}
	if result.APIKeyEnv == "" {
		result.APIKeyEnv = "WB_API_KEY"
	}

	// Cards list rate limits (two-level adaptive)
	// Swagger: 100 req/min, burst 5
	if result.RateLimits.CardsListApi == 0 {
		result.RateLimits.CardsListApi = 100
	}
	if result.RateLimits.CardsList == 0 {
		result.RateLimits.CardsList = result.RateLimits.CardsListApi
	}
	if result.RateLimits.CardsListApiBurst == 0 {
		result.RateLimits.CardsListApiBurst = 5
	}
	if result.RateLimits.CardsListBurst == 0 {
		result.RateLimits.CardsListBurst = result.RateLimits.CardsListApiBurst
	}

	// Adaptive tuning defaults
	if result.AdaptiveProbeAfter == 0 {
		result.AdaptiveProbeAfter = 10
	}
	if result.MaxBackoffSeconds == 0 {
		result.MaxBackoffSeconds = 60
	}

	return result
}

// ============================================================================
// Product Prices (Discounts-Prices API)
// ============================================================================

// PricesConfig — конфигурация для download-wb-prices утилиты.
//
// Используется для загрузки текущих цен товаров с WB Discounts-Prices API.
// Offset-пагинация (не date-based). Нет поля Days — snapshot делается на момент запуска.
// Двухуровневый rate limiting: desired + api (swagger floor для восстановления).
type PricesConfig struct {
	DbPath             string           `yaml:"db_path"`
	APIKeyEnv          string           `yaml:"api_key_env"`
	RateLimits         PricesRateLimits `yaml:"rate_limits"`
	AdaptiveProbeAfter int              `yaml:"adaptive_probe_after"`
	MaxBackoffSeconds  int              `yaml:"max_backoff_seconds"`
}

// PricesRateLimits — rate limits для prices list API endpoint.
//
// Discounts-Prices API: 10 req/6sec (~100 req/min), burst 5 (swagger).
//
// Два уровня rate:
//   - desired:      желаемый rate (можно превышать swagger — adaptive limiter обработает 429)
//   - desired_burst: burst для desired rate
//   - api:           swagger-documented rate (recovery floor после 429)
//   - api_burst:     burst для api rate
//
// Если desired не указан — используется api (без превышения swagger).
type PricesRateLimits struct {
	PricesList         int `yaml:"prices_list"`
	PricesListBurst    int `yaml:"prices_list_burst"`
	PricesListApi      int `yaml:"prices_list_api"`
	PricesListApiBurst int `yaml:"prices_list_api_burst"`
}

// GetDefaults возвращает дефолтные значения для незаполненных полей.
// Каскадные дефолты: api -> desired, api_burst -> desired_burst.
// NOTE: Нет Days поля — prices snapshot делается на момент запуска.
func (c *PricesConfig) GetDefaults() PricesConfig {
	result := *c
	if result.DbPath == "" {
		result.DbPath = "sales.db"
	}
	if result.APIKeyEnv == "" {
		result.APIKeyEnv = "WB_API_KEY"
	}

	// Prices list rate limits (two-level adaptive)
	// Swagger: 10 req/6sec ≈ 100 req/min, burst 5
	if result.RateLimits.PricesListApi == 0 {
		result.RateLimits.PricesListApi = 100
	}
	if result.RateLimits.PricesList == 0 {
		result.RateLimits.PricesList = result.RateLimits.PricesListApi
	}
	if result.RateLimits.PricesListApiBurst == 0 {
		result.RateLimits.PricesListApiBurst = 5
	}
	if result.RateLimits.PricesListBurst == 0 {
		result.RateLimits.PricesListBurst = result.RateLimits.PricesListApiBurst
	}

	// Adaptive tuning defaults
	if result.AdaptiveProbeAfter == 0 {
		result.AdaptiveProbeAfter = 10
	}
	if result.MaxBackoffSeconds == 0 {
		result.MaxBackoffSeconds = 60
	}

	return result
}

// ============================================================================
// Freshness Checker (data quality verification)
// ============================================================================

// FreshnessConfig — конфигурация для check-db-freshness утилиты.
//
// Используется для проверки свежести данных в SQLite базе.
// Проверяет последнюю дату в каждой таблице и сравнивает с порогами.
type FreshnessConfig struct {
	DbPath      string   `yaml:"db_path"`       // Путь к SQLite базе данных
	Tables      []string `yaml:"tables"`        // Список таблиц для проверки (пусто = все)
	WarnAgeDays int      `yaml:"warn_age_days"` // Порог предупреждения в днях (default: 7)
	CritAgeDays int      `yaml:"crit_age_days"` // Порог критичности в днях (default: 14)
	Verbose     bool     `yaml:"verbose"`       // Подробный вывод
}

// GetDefaults возвращает дефолтные значения для незаполненных полей.
func (c *FreshnessConfig) GetDefaults() FreshnessConfig {
	result := *c
	if result.DbPath == "" {
		result.DbPath = "db/wb-sales.db"
	}
	if result.WarnAgeDays == 0 {
		result.WarnAgeDays = 7
	}
	if result.CritAgeDays == 0 {
		result.CritAgeDays = 14
	}
	return result
}
