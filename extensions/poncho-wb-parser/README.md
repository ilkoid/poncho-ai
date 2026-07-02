# Poncho WB Parser

Browser-only WB storefront scraper — **v2** of `extensions/wb-scraper/`. No Go backend, no SQLite
file: data lives in IndexedDB (Dexie). TypeScript + Vite + `@crxjs/vite-plugin`.

Coexists with v1: its own IndexedDB (`poncho_wb_parser`, v1 uses `wb-scraper`) and its own dir, so
both extensions can be loaded at once.

## Build & load (operator)

```bash
cd extensions/poncho-wb-parser
npm install
npm run build        # → dist/
```

Then in Chrome: `chrome://extensions` → Developer mode ON → **Load unpacked** → pick `dist/`.
Click the toolbar icon → **Открыть панель управления** (opens the 3-tab dashboard).

## Scripts

| script | what |
|---|---|
| `npm run build` | production build → `dist/` |
| `npm test` | vitest (decode port + DB round-trips, no live WB) |
| `npm run typecheck` | `tsc --noEmit` |
| `npm run dev` | vite dev (HMR) |

## Status

All six delivery stages are implemented and verified without live WB (50 unit tests, typecheck clean):

- **S1** ✅ scaffold + build + Dexie schema (7 stores)
- **S2** ✅ decode port (decode.go → TS, 1:1 with `TestDecode*`) + upsert + write layer
- **S3** ✅ intercept (MAIN fetch/XHR wrap) + bridge + SW router + offscreen orchestrator + SW-death resilience
- **S4** ✅ constructor (cartesian, port of querygen.go) + query_id stability e2e + Settings tab
- **S5** ✅ three report families (Видимость/Карта конкурентов/Цены и остатки)
- **S6** ✅ export (xlsx via dynamic-import SheetJS + CSV with UTF-8 BOM) + `storage.persist()`

## Run Book (operator — browser verification)

After `npm run build`, load `dist/` unpacked in `chrome://extensions`, then:

1. **Schema** — open the dashboard (toolbar icon → «Открыть панель управления»). The «Сбор данных» tab shows 6 count cards → IndexedDB `poncho_wb_parser` with 7 stores exists.
2. **Mock session** — «Сбор данных» → **Run mock session**. The live log shows `mock decode (search)/(card_detail)`, counts update to 2 positions + 1 card + 1 price + 1 detail + 1 stock. No WB traffic.
3. **Constructor + reports** — «Настройки» → edit the 4 lists → «Сохранить» (preview shows cartesian count) → «Сбор данных» → «Run mock session» again → «Отчёты» → pick snapshot → **Построить** → three panels render → `[xlsx]`/`[csv]` download.
4. **SW-death test** — start a session, then in `chrome://extensions` click ↻ on the extension (kills the SW). The offscreen keeps the run-loop + writes alive (writes live in the offscreen, not the SW). Resume is automatic.
5. **Live WB session** (real traffic — run only when needed) — «Сбор данных» → enter a query → **Старт (запрос)**. A WB tab opens and navigates at human pace; intercepts flow MAIN→bridge→SW→offscreen→Dexie.

## Notes

- `xlsx` is the npm 0.18.5 build (SheetJS). Its known high-severity advisory is in the **parse** path; this extension only **writes** (aoa→sheet), so it is not exploitable here. It is dynamically imported → a separate 424 KB chunk, not in the initial bundle.
- v2 never touches v1: own IndexedDB name (`poncho_wb_parser` vs `wb-scraper`), own directory, own postMessage marker (`PONCHO_INJECT` vs `WB_SCRAPER`). Both extensions can run at once.

## Architecture (data flow)

```
inject.ts (MAIN) ─postMessage─► bridge.ts (ISOLATED) ─INTERCEPT─► sw.ts ─CAPTURE─► offscreen.ts
                                                                          ├─ decode/*.ts (port of decode.go)
                                                                          └─ db.bulkAdd → Dexie
dashboard.html ◄─ reads Dexie directly (reports/export)
```

The **offscreen document owns the run-loop + Dexie writes** (the SW dies ~30s idle; offscreen is the
one long-lived MV3 context).
