// cube.go — сборка payload дашборда из строк куба (aggregate-и по колонкам).
package main

import "sort"

// assembleCube превращает строки куба в колоночный payload: Meta из данных,
// Sups — колонки, строка за строкой (порядок ORDER BY created_at сохраняется).
// Период Meta.From/To — по ФАКТИЧЕСКИМ дням куба: при --days N coverage несёт
// весь диапазон таблицы, и его нельзя сеять в Meta (иначе шапка покажет окно,
// которого в данных нет).
func assembleCube(rows []supplyRow, dbName string) *SuppliesData {
	cube := &SuppliesData{Meta: Meta{
		GeneratedAt: nowMoscow().Format("02.01.2006 15:04"),
		Db:          dbName,
	}}

	n := len(rows)
	cube.Sups.ID = make([]string, 0, n)
	cube.Sups.Day = make([]string, 0, n)
	cube.Sups.Name = make([]string, 0, n)
	cube.Sups.Created = make([]string, 0, n)
	cube.Sups.First = make([]string, 0, n)
	cube.Sups.Closed = make([]string, 0, n)
	cube.Sups.Scanned = make([]string, 0, n)
	cube.Sups.Rejected = make([]int, 0, n)
	cube.Sups.LagH = make([]float64, 0, n)
	cube.Sups.NTasks = make([]int, 0, n)
	cube.Sups.CancelPre = make([]int, 0, n)
	cube.Sups.CancelPost = make([]int, 0, n)
	cube.Sups.Hist = make([][8]int, 0, n)

	for _, r := range rows {
		cube.Sups.ID = append(cube.Sups.ID, r.ID)
		cube.Sups.Day = append(cube.Sups.Day, r.Day)
		cube.Sups.Name = append(cube.Sups.Name, r.Name)
		cube.Sups.Created = append(cube.Sups.Created, r.Created)
		cube.Sups.First = append(cube.Sups.First, r.FirstTask)
		cube.Sups.Closed = append(cube.Sups.Closed, r.Closed)
		cube.Sups.Scanned = append(cube.Sups.Scanned, r.Scanned)
		cube.Sups.Rejected = append(cube.Sups.Rejected, r.Rejected)
		cube.Sups.LagH = append(cube.Sups.LagH, r.LagH)
		cube.Sups.NTasks = append(cube.Sups.NTasks, r.NTasks)
		cube.Sups.CancelPre = append(cube.Sups.CancelPre, r.CancelPre)
		cube.Sups.CancelPost = append(cube.Sups.CancelPost, r.CancelPost)
		cube.Sups.Hist = append(cube.Sups.Hist, r.Hist)

		cube.Meta.Supplies++
		cube.Meta.Tasks += r.NTasks
		if r.Scanned == "" && r.Rejected == 0 {
			cube.Meta.Open++
		}
		if r.Day != "" && (cube.Meta.From == "" || r.Day < cube.Meta.From) {
			cube.Meta.From = r.Day
		}
		if r.Day != "" && r.Day > cube.Meta.To {
			cube.Meta.To = r.Day
		}
	}
	return cube
}

// dayList — отсортированный список дней формирования (для проверок/тестов).
func dayList(cube *SuppliesData) []string {
	seen := map[string]bool{}
	var out []string
	for _, d := range cube.Sups.Day {
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	sort.Strings(out)
	return out
}
