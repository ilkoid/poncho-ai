// config.go — конфигурация утилиты card-early-diagnostics (config.yaml + CLI overrides).
package main

import (
	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/email"
)

// Config — конфигурация утилиты (config.yaml + CLI overrides).
//
// Storage встраивает config.V2StorageConfig (как у collection-readiness и v2-загрузчиков),
// чтобы backend/dsn/pg_database/pg_password_env работали единообразно.
type Config struct {
	// Storage — параметры подключения к БД. Дефолт wb_data_test: отладка на Маке
	// идёт в тест-БД, боевой прогон на VPS2 переопределяет --db wb_data_prod.
	Storage config.V2StorageConfig `yaml:"storage"`

	// Watch — критерии гендира: кого считать «карточкой без заказов».
	Watch WatchConfig `yaml:"watch"`

	// Thresholds — пороги триггеров диагностики (подсветка xlsx + дерево гипотез).
	Thresholds ThresholdsConfig `yaml:"thresholds"`

	// Onec — параметры 1С-источников (каскад FBO-складов).
	Onec OnecConfig `yaml:"onec"`

	// XLSX — путь к выходному файлу. Пусто → card-diagnostics-YYYYMMDD.xlsx в cwd.
	XLSX string `yaml:"xlsx"`

	// EmbedPhotos — встраивать ли миниатюры WB (100×75 JPEG) в колонку «Фото».
	// Вотчлист мал (~десятки строк), поэтому миниатюры дёшевы.
	EmbedPhotos bool `yaml:"embed_photos"`

	// RetentionDays — хранить дневные срезы N дней (0 = не чистить).
	RetentionDays int `yaml:"retention_days"`

	// Email — опциональная отправка готового xlsx по почте через pkg/email.
	Email EmailConfig `yaml:"email"`
}

// WatchConfig — «кого флагуем». Все параметры гендира вынесены в yaml, чтобы
// регламент ОЭК (к 13.11) мог уточнить пороги без правки кода.
type WatchConfig struct {
	// NoOrdersDays — N полных дней без заказов для сигнала slow_start («выведено,
	// но не разогналась»). Письмо гендира: 3.
	NoOrdersDays int `yaml:"no_orders_days"`

	// SlowStartMaxOrders — «не разогналась»: заказов за жизнь ≤ N (включая ноль).
	// Разрез slow_start/stall: что было ДО тишины — карточка не разогналась с
	// начала продаж или разгонялась и остановилась. Дефолт 2: 0 = «не продалось
	// ни разу», 1-2 = «продалось чуть-чуть и умерло» — обе ранняя неудача.
	SlowStartMaxOrders int `yaml:"slow_start_max_orders"`

	// StallDays — N полных дней тишины для сигнала stall («разгонялась и
	// остановилась», заказов за жизнь больше порога). Тоже 3 по письму; отдельный
	// ключ — вдруг регламент разведёт. Stall — категория наблюдения: длинный хвост
	// одежды продаётся не каждый день.
	StallDays int `yaml:"stall_days"`

	// ModelStatusWhitelist — белый список onec_goods.model_status (фильтр входа:
	// мониторим начало продаж УЖЕ одобренных товаров, статусы подготовки — мимо).
	ModelStatusWhitelist []string `yaml:"model_status_whitelist"`

	// AllowedYears — фильтр по году коллекции: символы 2-3 артикула продавца → 20XX
	// (конвенция репо: allowed_years в collection-readiness, pkg/config YearFilterConfig).
	// Гендира волнует «выведено только что и не продаётся» — а это свежая коллекция
	// с понятным текущим годом ([26] = осень-зима 2026). Годы 2- или 4-значные
	// (26 и 2026 эквивалентны, нормализация в applyDefaults). Пусто = без фильтра.
	// При активном фильтре легаси-артикулы с мусорным «годом» отсеиваются SQL-side.
	AllowedYears []int `yaml:"allowed_years"`

	// FunnelWindowDays — окно воронки (opens/carts), дней.
	FunnelWindowDays int `yaml:"funnel_window_days"`

	// IncludePreSales — включать info-строки pre_sales (одобрено, карточка есть,
	// но продажы не начались — остатка никогда не было). В процесс 3/5/7 не входят.
	IncludePreSales bool `yaml:"include_pre_sales"`
}

// ThresholdsConfig — пороги триггеров факторов (подсветка + гипотезы).
type ThresholdsConfig struct {
	// TitleLenCritical — «совсем короткое наименование» (символов строго меньше;
	// кейс гендира: «Ботинки» = 7). Плюс триггер: однословное или == предмету.
	TitleLenCritical int `yaml:"title_len_critical"`

	// TitleLenWarning — «коротковатое» (строго меньше).
	TitleLenWarning int `yaml:"title_len_warning"`

	// PhotoMin — норма фото: меньше — триггер (в парке 21% карточек с <4).
	PhotoMin int `yaml:"photo_min"`

	// SizesRatioMin — норма полноты размерного ряда WB/1С: ниже — триггер.
	SizesRatioMin float64 `yaml:"sizes_ratio_min"`

	// CampaignActiveStatuses — статусы кампаний, считающиеся «живыми»
	// (pkg/wb/promotion_types.go: 9 = Активна, 11 = На паузе).
	CampaignActiveStatuses []int `yaml:"campaign_active_statuses"`
}

// OnecConfig — каскад 1С-складов для размерного ряда.
type OnecConfig struct {
	// FboStorages — приоритетные склады (у кого есть строки — берём оттуда).
	FboStorages []string `yaml:"fbo_storages"`

	// FboFallback — fallback, если на приоритетных складах строк нет.
	// «Склад ОПТ» — зеркало-дубль Внукова, в каскад НЕ включать.
	FboFallback []string `yaml:"fbo_fallback"`
}

// EmailConfig — настройки отправки отчёта по почте (как в collection-readiness).
type EmailConfig struct {
	Enabled    bool             `yaml:"enabled"`
	SMTP       email.SMTPConfig `yaml:"smtp"`
	Recipients email.Recipients `yaml:"recipients"`
	Subject    string           `yaml:"subject"`
	Body       string           `yaml:"body"`
}

// loadConfig загружает конфигурацию из YAML файла (defaults → yaml.Unmarshal поверх).
func loadConfig(path string) (*Config, error) {
	cfg := defaultConfig()
	if err := config.LoadYAML(path, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// defaultConfig возвращает конфигурацию по умолчанию.
func defaultConfig() *Config {
	return &Config{
		Storage: config.V2StorageConfig{
			Backend:       "postgres",
			PgDatabase:    "wb_data_test",
			PgPasswordEnv: "PG_PWD",
		},
		Watch: WatchConfig{
			NoOrdersDays:         3,
			SlowStartMaxOrders:   2,
			StallDays:            3,
			ModelStatusWhitelist: []string{"Утверждена к отгрузке"},
			FunnelWindowDays:     7,
		},
		Thresholds: ThresholdsConfig{
			TitleLenCritical:       20,
			TitleLenWarning:        25,
			PhotoMin:               4,
			SizesRatioMin:          0.5,
			CampaignActiveStatuses: []int{9},
		},
		Onec: OnecConfig{
			FboStorages: []string{"РВБ ФБО"},
			FboFallback: []string{"Внуково"},
		},
		EmbedPhotos:   true,
		RetentionDays: 90,
	}
}

// applyDefaults заполняет нулевые поля значениями по умолчанию (после yaml + до CLI).
func (c *Config) applyDefaults() {
	d := defaultConfig()
	if c.Storage.Backend == "" {
		c.Storage.Backend = d.Storage.Backend
	}
	if c.Storage.PgDatabase == "" {
		c.Storage.PgDatabase = d.Storage.PgDatabase
	}
	if c.Storage.PgPasswordEnv == "" {
		c.Storage.PgPasswordEnv = d.Storage.PgPasswordEnv
	}
	if c.Watch.NoOrdersDays <= 0 {
		c.Watch.NoOrdersDays = d.Watch.NoOrdersDays
	}
	if c.Watch.SlowStartMaxOrders < 0 {
		c.Watch.SlowStartMaxOrders = d.Watch.SlowStartMaxOrders
	}
	if c.Watch.StallDays <= 0 {
		c.Watch.StallDays = d.Watch.StallDays
	}
	if len(c.Watch.ModelStatusWhitelist) == 0 {
		c.Watch.ModelStatusWhitelist = d.Watch.ModelStatusWhitelist
	}
	// Годы: 4-значные → 2-значные (2026 → 26) — конвенция allowed_years по репо.
	for i, y := range c.Watch.AllowedYears {
		if y > 100 {
			c.Watch.AllowedYears[i] = y % 100
		}
	}
	if c.Watch.FunnelWindowDays <= 0 {
		c.Watch.FunnelWindowDays = d.Watch.FunnelWindowDays
	}
	if c.Thresholds.TitleLenCritical <= 0 {
		c.Thresholds.TitleLenCritical = d.Thresholds.TitleLenCritical
	}
	if c.Thresholds.TitleLenWarning <= 0 {
		c.Thresholds.TitleLenWarning = d.Thresholds.TitleLenWarning
	}
	if c.Thresholds.PhotoMin <= 0 {
		c.Thresholds.PhotoMin = d.Thresholds.PhotoMin
	}
	if c.Thresholds.SizesRatioMin <= 0 {
		c.Thresholds.SizesRatioMin = d.Thresholds.SizesRatioMin
	}
	if len(c.Thresholds.CampaignActiveStatuses) == 0 {
		c.Thresholds.CampaignActiveStatuses = d.Thresholds.CampaignActiveStatuses
	}
	if len(c.Onec.FboStorages) == 0 {
		c.Onec.FboStorages = d.Onec.FboStorages
	}
	if len(c.Onec.FboFallback) == 0 {
		c.Onec.FboFallback = d.Onec.FboFallback
	}
}

// CascadeStorages — полный список складов каскада в порядке приоритета
// (FboStorages, затем FboFallback; без дублей).
func (c *Config) CascadeStorages() []string {
	out := make([]string, 0, len(c.Onec.FboStorages)+len(c.Onec.FboFallback))
	seen := map[string]bool{}
	for _, s := range append(append([]string{}, c.Onec.FboStorages...), c.Onec.FboFallback...) {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
