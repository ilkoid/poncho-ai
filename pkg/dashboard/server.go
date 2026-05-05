package dashboard

import (
	"database/sql"
	"fmt"
	"log"
	"net/http"
)

// ServerConfig — конфигурация HTTP-сервера дашборда.
type ServerConfig struct {
	Host            string `yaml:"host"`
	Port            int    `yaml:"port"`
	DBPath          string `yaml:"db_path"`
	DBAnalyticsPath string `yaml:"db_analytics_path"` // Дополнительная БД (ATTACH)
	Table           string `yaml:"table"`             // Таблица для query (напр. "ma_sku_daily")
	DateColumn      string `yaml:"date_column"`       // Колонка даты (напр. "snapshot_date")
}

// TabbedHandler строит полную страницу дашборда с табами.
//
// Реализуется в cmd/data-dashboards/ — отвечает за все табы и секции.
type TabbedHandler func(db *sql.DB, filter FilterParams) (*DashboardPage, error)

// Server — HTTP-сервер дашборда.
//
// SRP: отвечает только за HTTP-слой (parse request → handler → render → response).
type Server struct {
	cfg     ServerConfig
	db      *sql.DB
	handler TabbedHandler
}

// NewServer создаёт сервер дашборда.
func NewServer(cfg ServerConfig, db *sql.DB, handler TabbedHandler) *Server {
	return &Server{
		cfg:     cfg,
		db:      db,
		handler: handler,
	}
}

// ListenAndServe запускает HTTP-сервер.
func (s *Server) ListenAndServe() error {
	addr := fmt.Sprintf("%s:%d", s.cfg.Host, s.cfg.Port)

	http.HandleFunc("/", s.handleDashboard)

	log.Printf("[dashboard] server starting at http://%s", addr)
	return http.ListenAndServe(addr, nil)
}

// handleDashboard — HTTP handler для главной страницы.
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	filter := s.parseFilter(r)

	// Если дата не указана — берём MAX из таблицы
	if filter.Date == "" {
		date, err := QueryMaxDate(s.db, s.cfg.Table, s.cfg.DateColumn)
		if err != nil {
			http.Error(w, fmt.Sprintf("query max date: %v", err), http.StatusInternalServerError)
			return
		}
		filter.Date = date
	}

	// Строим страницу через handler
	page, err := s.handler(s.db, filter)
	if err != nil {
		http.Error(w, fmt.Sprintf("build dashboard: %v", err), http.StatusInternalServerError)
		return
	}

	// Заполняем общие поля
	page.Title = "SKU Analytics"
	page.Description = fmt.Sprintf("Срез: %s", filter.Date)
	page.Filter = filter

	// Собираем списки для фильтров
	if len(page.Brands) == 0 {
		page.Brands, _ = QueryDistinctValues(s.db, s.cfg.Table, "brand")
	}
	if len(page.Categories) == 0 {
		page.Categories, _ = QueryDistinctValues(s.db, s.cfg.Table, "category_level1")
	}
	if len(page.Regions) == 0 {
		page.Regions, _ = QueryDistinctValues(s.db, s.cfg.Table, "region_name")
	}

	html, err := RenderPage(page)
	if err != nil {
		http.Error(w, fmt.Sprintf("render page: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(html)
}

// parseFilter извлекает FilterParams из URL query.
func (s *Server) parseFilter(r *http.Request) FilterParams {
	q := r.URL.Query()
	return FilterParams{
		Date:     q.Get("date"),
		Brand:    q.Get("brand"),
		Category: q.Get("category"),
		Region:   q.Get("region"),
		RiskOnly: q.Get("risk_only") == "1",
		Tab:      q.Get("tab"),
	}
}
