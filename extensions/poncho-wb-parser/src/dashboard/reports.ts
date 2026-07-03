// src/dashboard/reports.ts — the "Отчёты и экспорт" tab. Picks one snapshot (and an optional second
// for compare-mode), then renders the three report families by reading Dexie directly. Export
// buttons ([xlsx]/[csv]) are wired to a stub until S6 implements the export module.

import { listSnapshots, listQueriesForSnapshot } from '../reports/snapshots';
import { buildVisibility, type VisibilityReport } from '../reports/visibility';
import { buildCompetitorMap, type CompetitorMapReport } from '../reports/competitor-map';
import { buildPricesStocks, type PricesStocksReport } from '../reports/prices-stocks';
import { loadHighlightBrands } from '../storage/config';
import { downloadCSV } from '../export/csv';
import { downloadXLSX } from '../export/xlsx';
import { dumpSnapshot, downloadJSON } from '../export/json-dump';
import { visibilityToTables, competitorsToTables, pricesToTables } from '../export/tables';

// last-built reports (cached so the export buttons can serialize them without rebuilding)
let lastVis: VisibilityReport | null = null;
let lastMap: CompetitorMapReport | null = null;
let lastPs: PricesStocksReport | null = null;

function stamp(): string {
  return new Date().toISOString().slice(0, 19).replace(/[:T]/g, '').replace(' ', '-');
}

/** kopecks → "1 234,50 ₽" */
function fmtRub(kop: number | null): string {
  if (kop == null) return '—';
  return (kop / 100).toLocaleString('ru-RU', { minimumFractionDigits: 2, maximumFractionDigits: 2 }) + ' ₽';
}
/** delta arrow: lower rank is better, so a negative delta (improved) is green ▼. */
function fmtDelta(d: number | null): string {
  if (d == null) return '';
  if (d === 0) return '0';
  const cls = d < 0 ? 'delta-up' : 'delta-down';
  const arrow = d < 0 ? '▼' : '▲';
  return `<span class="${cls}">${arrow}${Math.abs(d)}</span>`;
}
function escapeHtml(s: string): string {
  return s.replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c]!);
}

/** shared search box rendered above each report table; wired by the renderer to a fillBody closure. */
const FILTER_INPUT = '<input class="rpt-filter" type="search" placeholder="фильтр по товару / бренду / поставщику…" />';

/** lowercase concatenation of a row's text fields — substring match against it = the table filter. */
function textOf(...parts: string[]): string {
  return parts.join(' ').toLowerCase();
}

export async function initReports(): Promise<void> {
  await refreshPickers();
  document.getElementById('rpt-snap-a')?.addEventListener('change', () => void populateQueries(currentSnapA()));
  document.getElementById('rpt-build')?.addEventListener('click', () => void buildAndRender());
  // full raw JSON dump of the selected snapshot (all 6 fact tables + queries) — for sharing/inspection
  document.getElementById('rpt-json')?.addEventListener('click', async () => {
    const snap = currentSnapA();
    if (!snap) {
      alert('Сначала выберите Снимок A.');
      return;
    }
    const dump = await dumpSnapshot(snap);
    const base = `poncho-snapshot-${snap.slice(0, 19).replace(/[:T]/g, '-')}`;
    downloadJSON(base, dump);
  });
  // export buttons: serialize the last-built report to xlsx (one sheet per table) or csv (primary table).
  document.querySelectorAll<HTMLButtonElement>('.export-btn').forEach((btn) => {
    btn.addEventListener('click', async () => {
      const report = btn.dataset.report;
      const fmt = btn.dataset.fmt;
      const tables =
        report === 'visibility' ? (lastVis && visibilityToTables(lastVis)) :
        report === 'competitors' ? (lastMap && competitorsToTables(lastMap)) :
        report === 'prices' ? (lastPs && pricesToTables(lastPs)) : null;
      if (!tables || tables.length === 0) {
        alert('Сначала нажмите «Построить».');
        return;
      }
      const base = `poncho-${report}-${stamp()}`;
      if (fmt === 'csv') downloadCSV(base, tables);
      else await downloadXLSX(base, tables);
    });
  });
}

function currentSnapA(): string {
  return (document.getElementById('rpt-snap-a') as HTMLSelectElement | null)?.value ?? '';
}

/**
 * Rebuild the snapshot/query dropdowns from the current DB state, PRESERVING the current selection
 * (so a refresh mid-session doesn't yank the user's pick back to newest). Called on init, on
 * COLLECT_DONE, on switch-to-reports, and after 🧹 clear — without this the pickers go stale and the
 * user sees old snapshots/queries ("носки instead of футболки").
 */
export async function refreshPickers(): Promise<void> {
  const snaps = await listSnapshots();
  const a = document.getElementById('rpt-snap-a') as HTMLSelectElement | null;
  const b = document.getElementById('rpt-snap-b') as HTMLSelectElement | null;
  if (!a || !b) return;
  const prevA = a.value; // save selection
  const prevB = b.value;
  const opts = snaps.map((s) => `<option value="${s}">${s.replace('T', ' ').replace('Z', '')}</option>`).join('');
  a.innerHTML = opts;
  b.innerHTML = '<option value="">—</option>' + opts;
  if (snaps.length > 0) {
    // restore the user's pick if still present; else default to newest (A=snaps[0], B=snaps[1])
    a.value = snaps.includes(prevA) ? prevA : snaps[0]!;
    b.value = prevB && snaps.includes(prevB) ? prevB : snaps.length > 1 ? snaps[1]! : '';
  }
  await populateQueries(currentSnapA());
}

async function populateQueries(snap: string): Promise<void> {
  const q = document.getElementById('rpt-query') as HTMLSelectElement | null;
  if (!q) return;
  const queries = snap ? await listQueriesForSnapshot(snap) : [];
  q.innerHTML = '<option value="">все запросы</option>' + queries.map((x) => `<option value="${x.query_id}">${escapeHtml(x.query)}</option>`).join('');
}

async function buildAndRender(): Promise<void> {
  const snapA = currentSnapA();
  if (!snapA) return;
  const snapB = (document.getElementById('rpt-snap-b') as HTMLSelectElement | null)?.value || null;
  const qidRaw = (document.getElementById('rpt-query') as HTMLSelectElement | null)?.value;
  const queryId = qidRaw ? Number(qidRaw) : null;
  // focus brands → lowercase set for case-insensitive match against card.brand / position.brand.
  const focusRaw = await loadHighlightBrands();
  const focus = new Set(focusRaw.map((b) => b.trim().toLowerCase()));

  const banner = document.getElementById('rpt-banner');
  if (banner) {
    banner.textContent = focus.size ? `★ Фокус-бренды: ${focusRaw.join(', ')}.` : 'ℹ фокус-бренды не заданы — подсветки нет (см. Настройки → Фильтр по бренду).';
  }

  const [vis, map, ps] = await Promise.all([
    buildVisibility(snapA, snapB, queryId, focus),
    buildCompetitorMap(snapA, queryId, focus),
    buildPricesStocks(snapA, queryId),
  ]);
  lastVis = vis;
  lastMap = map;
  lastPs = ps;
  renderVisibility(vis);
  renderCompetitors(map);
  renderPrices(ps);
}

function renderVisibility(v: VisibilityReport): void {
  const host = document.getElementById('rpt-visibility');
  if (!host) return;
  const s = v.summary;
  const head = `<div class="summary">Снимок A: ${s.total_a} тов. · B: ${s.total_b} · ▼улучшилось ${s.improved} · ▲ухудшилось ${s.deteriorated} · исчезло ${s.disappeared} · появилось ${s.appeared} · промо: ${s.promo_panels} панел(ей) × ${s.promo_covered} тов.</div>`;
  const table =
    '<table class="rpt"><colgroup>' +
    '<col class="c-id" /><col class="c-name" /><col class="c-brand" /><col class="c-supplier" />' +
    '<col class="c-num" /><col class="c-num" /><col class="c-num" />' +
    '</colgroup><thead><tr><th>nm_id</th><th>Товар</th><th>Бренд</th><th>Поставщик</th><th>Поз. A</th><th>Поз. B</th><th>Δ</th></tr></thead><tbody></tbody></table>';
  host.innerHTML = head + FILTER_INPUT + table + '<p class="hint rpt-rest"></p>';
  const tbody = host.querySelector('tbody')!;
  const rest = host.querySelector('.rpt-rest')!;
  const fill = (ft: string): void => {
    const q = ft.trim().toLowerCase();
    const matched = q ? v.rows.filter((r) => textOf(r.name, r.brand, r.supplier_name).includes(q)) : v.rows;
    const cap = q ? 200 : 100;
    tbody.innerHTML = matched
      .slice(0, cap)
      .map(
        (r) => `<tr class="${r.is_focus ? 'focus' : ''}">
        <td>${r.nm_id}${r.promo_id != null ? ' <span class="oobadge">промо</span>' : ''}${r.is_focus ? ' <span class="oobadge">★</span>' : ''}</td>
        <td class="wrap"><a href="https://www.wildberries.ru/catalog/${r.nm_id}/detail.aspx" target="_blank" rel="noopener noreferrer">${escapeHtml(r.name) || String(r.nm_id)}</a></td>
        <td>${escapeHtml(r.brand)}</td>
        <td>${escapeHtml(r.supplier_name)}</td>
        <td class="num">${r.pos_a ?? '—'}</td>
        <td class="num">${r.pos_b ?? '—'}</td>
        <td class="num">${fmtDelta(r.delta)}</td>
      </tr>`,
      )
      .join('');
    rest.textContent = matched.length > cap ? `…и ещё ${matched.length - cap} строк${q ? ' (фильтр, топ-200)' : ' (топ-100 по позиции)'}.` : '';
  };
  host.querySelector('input.rpt-filter')?.addEventListener('input', (e) => fill((e.target as HTMLInputElement).value));
  fill('');
}

function renderCompetitors(m: CompetitorMapReport): void {
  const host = document.getElementById('rpt-competitors');
  if (!host) return;
  const head =
    `<div class="summary">Поставщиков: ${m.rows.length}. Ранжированы по числу товаров в выдаче. ` +
    `«Запросов» — в скольких запросах засветился поставщик; «Брендов» — число разных брендов у него.</div>`;
  const table =
    '<table class="rpt"><colgroup>' +
    '<col class="c-id" /><col class="c-supplier" /><col class="c-num" /><col class="c-num" /><col class="c-num" /><col class="c-num" /><col class="c-num" />' +
    '</colgroup><thead><tr><th>supplier_id</th><th>Поставщик</th><th>Товаров</th><th>Запросов</th><th>Брендов</th><th>Ср. рейтинг</th><th>Ср. цена</th></tr></thead><tbody></tbody></table>';
  host.innerHTML = head + FILTER_INPUT + table + '<p class="hint rpt-rest"></p>';
  const tbody = host.querySelector('tbody')!;
  const rest = host.querySelector('.rpt-rest')!;
  const fill = (ft: string): void => {
    const q = ft.trim().toLowerCase();
    const matched = q ? m.rows.filter((r) => textOf(r.supplier_name, String(r.supplier_id)).includes(q)) : m.rows;
    const cap = q ? 200 : 100;
    tbody.innerHTML = matched
      .slice(0, cap)
      .map(
        (r) => `<tr class="${r.is_focus ? 'focus' : ''}">
        <td>${r.supplier_id}${r.is_focus ? ' <span class="oobadge">★</span>' : ''}</td>
        <td class="wrap">${escapeHtml(r.supplier_name)}</td>
        <td class="num">${r.nm_count}</td>
        <td class="num">${r.query_count}</td>
        <td class="num">${r.brand_count}</td>
        <td class="num">${r.avg_rating.toFixed(2)}</td>
        <td class="num">${fmtRub(r.avg_price)}</td>
      </tr>`,
      )
      .join('');
    rest.textContent = matched.length > cap ? `…и ещё ${matched.length - cap} поставщиков.` : '';
  };
  host.querySelector('input.rpt-filter')?.addEventListener('input', (e) => fill((e.target as HTMLInputElement).value));
  fill('');
}

function renderPrices(p: PricesStocksReport): void {
  const host = document.getElementById('rpt-prices');
  if (!host) return;
  const maxCount = Math.max(1, ...p.histogram.map((b) => b.count));
  const histo = p.histogram.length
    ? `<div class="histo">${p.histogram.map((b) => `<div class="bar" style="height:${(b.count / maxCount) * 100}%"><span>${b.count}</span></div>`).join('')}</div>`
    : '<p class="hint">нет данных о ценах</p>';
  const oop = p.out_of_stock.length
    ? `<p class="hint">Out of stock: ${p.out_of_stock.slice(0, 50).map((o) => `${o.nm_id} (${escapeHtml(o.brand) || '—'})`).join(', ')}${p.out_of_stock.length > 50 ? '…' : ''}</p>`
    : '';
  const table =
    '<table class="rpt"><colgroup>' +
    '<col class="c-id" /><col class="c-name" /><col class="c-brand" /><col class="c-supplier" /><col class="c-num" /><col class="c-num" /><col class="c-num" />' +
    '</colgroup><thead><tr><th>nm_id</th><th>Товар</th><th>Бренд</th><th>Поставщик</th><th>мин. цена</th><th>ср. цена</th><th>остаток</th></tr></thead><tbody></tbody></table>';
  host.innerHTML =
    `<div class="summary">Цен: ${p.price_count} · в наличии: ${p.in_stock_count} · out of stock: ${p.out_of_stock.length}</div>` +
    histo +
    FILTER_INPUT +
    table +
    '<p class="hint rpt-rest"></p>' +
    oop;
  const tbody = host.querySelector('tbody')!;
  const rest = host.querySelector('.rpt-rest')!;
  const fill = (ft: string): void => {
    const q = ft.trim().toLowerCase();
    const matched = q ? p.rows.filter((r) => textOf(r.name, r.brand, r.supplier).includes(q)) : p.rows;
    const cap = q ? 200 : 100;
    tbody.innerHTML = matched
      .slice(0, cap)
      .map(
        (r) => `<tr>
        <td>${r.nm_id}</td>
        <td class="wrap"><a href="https://www.wildberries.ru/catalog/${r.nm_id}/detail.aspx" target="_blank" rel="noopener noreferrer">${escapeHtml(r.name) || String(r.nm_id)}</a></td>
        <td>${escapeHtml(r.brand)}</td>
        <td>${escapeHtml(r.supplier)}</td>
        <td class="num">${fmtRub(r.price_min)}</td>
        <td class="num">${fmtRub(r.price_avg)}</td>
        <td class="num">${r.total_qty}</td>
      </tr>`,
      )
      .join('');
    rest.textContent = matched.length > cap ? `…и ещё ${matched.length - cap} строк.` : '';
  };
  host.querySelector('input.rpt-filter')?.addEventListener('input', (e) => fill((e.target as HTMLInputElement).value));
  fill('');
}
