// config.go — конфигурация утилиты collection-readiness (config.yaml + CLI overrides).
package main

import (
	"github.com/ilkoid/poncho-ai/pkg/config"
	"github.com/ilkoid/poncho-ai/pkg/email"
)

// Config — конфигурация утилиты (config.yaml + CLI overrides).
//
// Storage встраивает config.V2StorageConfig (как у v2-загрузчиков), чтобы
// backend/dsn/pg_database/pg_password_env работали единообразно.
type Config struct {
	// Collections — список коллекций 1С для отчёта (фильтр onec_goods.collection).
	// Утилита универсальна: любая коллекция, не только «Школа 2026».
	Collections []string `yaml:"collections"`

	// Seasons — список сезонов 1С. Матчит ОБА поля onec_goods: season (функциональный сезон
	// ткани) OR collection_season (коммерческая коллекция). collection_season на 77% пуст,
	// поэтому матчим оба — иначе теряем товары коллекций «School boys/girls YYYY».
	// Для 'Школа': season=2880, collection_season=1439, union=2917.
	// Комбинируется с Collections через AND.
	Seasons []string `yaml:"seasons"`

	// Storage — параметры подключения к БД (backend: postgres, pg_database, и т.д.).
	Storage config.V2StorageConfig `yaml:"storage"`

	// Limit — ограничение числа строк (0 = все).
	Limit int `yaml:"limit"`

	// XLSX — путь к выходному файлу. Пусто → report-<slug>-YYYYMMDD.xlsx в cwd.
	XLSX string `yaml:"xlsx"`

	// EmbedPhotos — встраивать ли миниатюры WB (100×75 JPEG) в колонку A xlsx.
	// Источник — card_photos.tm; сборка качает ~2.6k фото с CDN WB (+30-60с, +6-12MB).
	// false → только колонка-ссылка (быстро, нужен интернет для просмотра).
	EmbedPhotos bool `yaml:"embed_photos"`

	// ExcludeLengths — исключить артикулы заданных длин (точное совпадение, как в даунлоадерах/
	// фиксёрах: pkg/config/utility.go FunnelFilterConfig.ExcludeLengths, slices.Contains).
	// По умолчанию [6, 7]: 6-значные = легаси-нумерация (дают мусорный «год» 2081/2083/2094 по
	// символам 2-3), 7-значные = старая нумерация. Пустой список [] = не фильтровать.
	ExcludeLengths []int `yaml:"exclude_lengths"`

	// Email — опциональная отправка готового xlsx по почте через pkg/email.
	// Срабатывает, когда Email.Enabled=true ИЛИ передан флаг --mail. См. EmailConfig.
	Email EmailConfig `yaml:"email"`
}

// EmailConfig — настройки отправки отчёта по почте.
//
// Подключение SMTP (host/port/from/username/auth/tls_mode/...) вшито literal значениями
// (как в test-smtp.sh), пароль — единственный секрет, через ${SMTP_PASSWORD} (развёртывается
// pkg/config.LoadYAML, os.ExpandEnv). Получатели, тема и вводный текст — здесь же.
// Поля SMTP/Recipients — это типы из pkg/email; в момент отправки собираем из них email.Config.
type EmailConfig struct {
	// Enabled — отправлять письмо после генерации xlsx. Можно включить и флагом --mail.
	Enabled bool `yaml:"enabled"`

	// SMTP — параметры подключения (literal; password через ${SMTP_PASSWORD}).
	SMTP email.SMTPConfig `yaml:"smtp"`

	// Recipients — адреса получателей по умолчанию (To — обязательно; Cc/Bcc опциональны).
	Recipients email.Recipients `yaml:"recipients"`

	// Subject — тема письма. Пусто → «Отчёт готовности карточек — <фильтр>».
	Subject string `yaml:"subject"`

	// Body — вводный текст письма (plain text). Пусто → короткий стандартный текст.
	Body string `yaml:"body"`
}

// loadConfig загружает конфигурацию из YAML файла.
//
// Предынициализируем defaultConfig(): yaml.Unmarshal сохраняет уже заданные поля,
// если YAML их не упоминает — поэтому bool-флаги (EmbedPhotos) получают корректный
// default, а явное `embed_photos: false` в YAML всё равно переопределяет.
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
			Backend:      "postgres",
			PgDatabase:   "wb_data_prod",
			PgPasswordEnv: "PG_PWD",
		},
		Limit:       0,
		EmbedPhotos: true,
		ExcludeLengths: []int{6, 7},
	}
}

// applyDefaults заполняет нулевые поля значениями по умолчанию.
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
}
