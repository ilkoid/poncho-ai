// src/dashboard/dashboard.ts — the full-page UI (3 tabs).
// S1: tab switching + DB counts (forces the DB open → creates the 7 stores).
// S3: collect-tab controls (mock session, single query/nmId, stop) + a live PROGRESS log.
// The dashboard reads Dexie DIRECTLY for counts/reports (the SW only relays; it does not own data).

import { db } from '../db/dexie';
import type { FactCounts } from '../messages';
import { initSettings } from './settings';
import { initReports, refreshPickers } from './reports';
import { clearFacts } from '../db/write';

// ---- tab switching ----
const buttons = document.querySelectorAll<HTMLButtonElement>('nav button');
buttons.forEach((btn) => {
  btn.addEventListener('click', () => {
    buttons.forEach((b) => b.classList.remove('active'));
    btn.classList.add('active');
    document.querySelectorAll<HTMLElement>('.tab-panel').forEach((p) => p.classList.remove('active'));
    document.getElementById(`tab-${btn.dataset.tab}`)?.classList.add('active');
    // coming to Reports → refresh the snapshot/query dropdowns so the user sees fresh data
    if (btn.dataset.tab === 'reports') void refreshPickers();
  });
});

// ---- DB counts (forces the DB open → creates the 7 stores; refreshed on PROGRESS) ----
const LABELS: Record<keyof FactCounts, string> = {
  search_positions: 'Позиции в поиске',
  vitrine_ads: 'Баннеры/реклама',
  competitor_cards: 'Карточки',
  competitor_card_prices: 'Цены',
  competitor_card_details: 'Детали',
  competitor_card_stocks: 'Остатки',
};

async function renderCounts(): Promise<void> {
  const host = document.getElementById('counts');
  if (!host) return;
  const entries = await Promise.all([
    db.search_positions.count(),
    db.vitrine_ads.count(),
    db.competitor_cards.count(),
    db.competitor_card_prices.count(),
    db.competitor_card_details.count(),
    db.competitor_card_stocks.count(),
  ]).catch(() => [0, 0, 0, 0, 0, 0] as const);
  const keys = Object.keys(LABELS) as (keyof FactCounts)[];
  host.innerHTML = entries
    .map((n, i) => `<div class="count-card"><div class="n">${n}</div><div class="lbl">${LABELS[keys[i]!]}</div></div>`)
    .join('');
}
void renderCounts();
void initSettings();
void initReports();

// Best-effort: ask for persistent storage on first dashboard open so a large (~hundreds-MB)
// session isn't evicted under browser quota pressure. Extensions opened from the toolbar usually
// have user activation; if not, this silently no-ops. The Settings tab has a manual button too.
if (navigator.storage?.persist) {
  navigator.storage.persist().catch(() => {});
}

// ---- live log (PROGRESS from the offscreen, relayed through chrome.runtime) ----
const logEl = document.getElementById('collect-log');
function logLine(line: string): void {
  if (!logEl) return;
  const ts = new Date().toLocaleTimeString('ru-RU');
  logEl.textContent += `[${ts}] ${line}\n`;
  logEl.scrollTop = logEl.scrollHeight;
  // cap the buffer so a long session does not bloat the DOM
  const lines = logEl.textContent!.split('\n');
  if (lines.length > 300) logEl.textContent = lines.slice(-300).join('\n');
}

interface ProgressMsg {
  type: 'PROGRESS' | 'COLLECT_DONE';
  phase?: string;
  counts?: FactCounts;
}

chrome.runtime.onMessage.addListener((msg: unknown) => {
  if (!msg || typeof msg !== 'object') return;
  const m = msg as ProgressMsg;
  if (m.type === 'PROGRESS') {
    const c = m.counts;
    const total = c ? c.search_positions + c.vitrine_ads + c.competitor_cards + c.competitor_card_prices + c.competitor_card_details + c.competitor_card_stocks : 0;
    logLine(`${m.phase ?? 'progress'} — всего строк: ${total}`);
    void renderCounts();
  } else if (m.type === 'COLLECT_DONE') {
    // session finished → final refresh so the Reports pickers reflect the new snapshot
    logLine('✓ сбор завершён');
    void refreshPickers();
    void renderCounts();
  }
});

// ---- collect controls ----
function snapshotTs(): string {
  return new Date().toISOString();
}
function send(msg: unknown): void {
  void chrome.runtime.sendMessage(msg).catch((e) => logLine(`⚠ ${String(e)}`));
}

document.getElementById('btn-mock')?.addEventListener('click', () => {
  logEl!.textContent = '';
  logLine('запуск mock-сессии…');
  send({ type: 'RUN_MOCK_SESSION', snapshotTs: snapshotTs() });
});
document.getElementById('btn-stop')?.addEventListener('click', () => {
  send({ type: 'COLLECT_STOP' });
  logLine('стоп отправлен');
});
document.getElementById('btn-query')?.addEventListener('click', () => {
  const q = (document.getElementById('single-query') as HTMLInputElement | null)?.value?.trim();
  if (!q) return;
  logLine(`live-сбор: запрос «${q}» (откроется вкладка WB)…`);
  send({ type: 'COLLECT_START', collect: { source: 'single', singleQuery: q }, snapshotTs: snapshotTs() });
});
document.getElementById('btn-nmid')?.addEventListener('click', () => {
  const raw = (document.getElementById('single-nmid') as HTMLInputElement | null)?.value?.trim();
  const nm = raw ? Number(raw) : NaN;
  if (!Number.isFinite(nm)) return;
  logLine(`live-сбор: карточка ${nm} (откроется вкладка WB)…`);
  send({ type: 'COLLECT_START', collect: { source: 'single', singleNmId: nm }, snapshotTs: snapshotTs() });
});

// wipe all collected fact rows (keeps the search_queries dimension = constructor + stable query_ids)
document.getElementById('btn-clear')?.addEventListener('click', async () => {
  if (!confirm('Удалить все собранные данные снимков? Позиции/карточки/цены/остатки/реклама будут стёрты. Конструктор запросов сохранится.')) {
    return;
  }
  await clearFacts();
  await renderCounts();
  void refreshPickers(); // wipe cleared snapshots from the Reports dropdowns
  logLine('🧹 база очищена (fact-таблицы; конструктор сохранён)');
});
