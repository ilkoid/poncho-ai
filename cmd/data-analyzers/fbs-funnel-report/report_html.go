// report_html.go — сборка самодостаточного HTML-дашборда (--html).
//
// Один файл: инлайн echarts 5.5.1 (assets/, Apache-2.0), данные куба JSON
// (script#payload), стили и приложение (web/). Открывается офлайн двойным
// кликом; фильтры и группировки считает браузер без сети.
package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strings"
)

//go:embed assets/echarts.min.js
var echartsJS []byte

//go:embed web/index.tmpl.html
var indexTmpl []byte

//go:embed web/app.js
var appJS []byte

//go:embed web/style.css
var styleCSS []byte

// Токены-плейсхолдеры в web/index.tmpl.html. Обычные {{…}} не используются:
// app.js содержит template-литералы JS.
const (
	tokEcharts  = "/*__ECHARTS_JS__*/"
	tokPayload  = "/*__PAYLOAD_JSON__*/"
	tokApp      = "/*__APP_JS__*/"
	tokStyle    = "/*__STYLE_CSS__*/"
	tokGlossary = "__GLOSSARY_HTML__"
)

// glossaryHTML — HTML выезжающей панели «Глоссарий». Один источник
// определений с листом XLSX: glossarySections() в report.go.
func glossaryHTML() string {
	var b strings.Builder
	for _, sec := range glossarySections() {
		b.WriteString(`<section class="g-sec"><h3>`)
		b.WriteString(htmlEsc(sec.Title))
		b.WriteString(`</h3>`)
		for _, e := range sec.Entries {
			b.WriteString(`<div class="g-item"><div class="g-term">`)
			b.WriteString(htmlEsc(e.Term))
			b.WriteString(`</div><div class="g-def">`)
			b.WriteString(htmlEsc(e.Def))
			b.WriteString(`</div></div>`)
		}
		b.WriteString(`</section>`)
	}
	return b.String()
}

func htmlEsc(s string) string { return html.EscapeString(s) }

// exportHTML собирает дашборд и пишет его по path (папка создаётся).
// Возвращает размер файла.
func exportHTML(cube *CubeData, path string) (int64, error) {
	payload, err := json.Marshal(cube)
	if err != nil {
		return 0, fmt.Errorf("payload json: %w", err)
	}
	// «</» внутри JSON-строк → «<\/»: иначе преждевременный </script>.
	// Вне строковых литералов «</» в JSON не встречается, замена безопасна.
	payload = bytes.ReplaceAll(payload, []byte("</"), []byte(`<\/`))

	html := string(indexTmpl)
	for tok, val := range map[string]string{
		tokEcharts:  string(echartsJS),
		tokPayload:  string(payload),
		tokApp:      string(appJS),
		tokStyle:    string(styleCSS),
		tokGlossary: glossaryHTML(),
	} {
		if !strings.Contains(html, tok) {
			return 0, fmt.Errorf("шаблон не содержит токен %s", tok)
		}
		html = strings.Replace(html, tok, val, 1)
	}

	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return 0, fmt.Errorf("папка %s: %w", dir, err)
		}
	}
	if err := os.WriteFile(path, []byte(html), 0o644); err != nil {
		return 0, fmt.Errorf("запись %s: %w", path, err)
	}
	return int64(len(html)), nil
}
