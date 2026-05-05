package main

import (
	"database/sql"

	"github.com/ilkoid/poncho-ai/pkg/dashboard"
)

// dashboardTabs определяет вкладки дашборда.
var dashboardTabs = []dashboard.Tab{
	{ID: "risks", Title: "Риски SKU", Icon: "⚠️", Default: true},
	{ID: "sales", Title: "Продажи", Icon: "💰"},
	{ID: "warehouse", Title: "Склады", Icon: "📦"},
}

// BuildDashboard — главный обработчик: строит все табы дашборда.
func BuildDashboard(db *sql.DB, filter dashboard.FilterParams) (*dashboard.DashboardPage, error) {
	activeTab := filter.Tab
	if activeTab == "" {
		for _, t := range dashboardTabs {
			if t.Default {
				activeTab = t.ID
				break
			}
		}
	}

	tabSections := make(map[string][]dashboard.Section)

	// Build all tabs (each is independent)
	tabSections["risks"] = BuildRiskSections(db, filter)
	tabSections["sales"] = BuildSalesSections(db, filter)
	tabSections["warehouse"] = BuildWarehouseSections(db, filter)

	return &dashboard.DashboardPage{
		Tabs:        dashboardTabs,
		ActiveTab:   activeTab,
		TabSections: tabSections,
	}, nil
}
