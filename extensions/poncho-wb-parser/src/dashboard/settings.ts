// src/dashboard/settings.ts — the "Настройки" tab logic: constructor editing with live cartesian
// preview, save → upsert (stable query_id), highlight-brands filter, detail_k, storage estimate.
// Imported and invoked by dashboard.ts. readConfigFromForm is exported for the collect-tab run button.

import { upsertQueries } from '../db/upsert';
import { cartesian, parseTextarea, type ConstructorConfig } from '../querygen/static';
import { loadConstructor, saveConstructor, loadHighlightBrands, saveHighlightBrands, loadDetailK, saveDetailK, loadServerUrl, saveServerUrl, loadScheduleTimes, saveScheduleTimes } from '../storage/config';
import { enqueueShipment, shipPending } from '../export/push';
import { listSnapshots } from '../reports/snapshots';
import { parseScheduleTimes } from '../background/schedule';

/** Russian pluralization of "снимок" (1 снимок / 2 снимка / 5 снимков). Used by the push button. */
function snaps(n: number): string {
  const mod10 = n % 10;
  const mod100 = n % 100;
  const form = mod10 === 1 && mod100 !== 11 ? 'снимок' : mod10 >= 2 && mod10 <= 4 && (mod100 < 10 || mod100 >= 20) ? 'снимка' : 'снимков';
  return `${n} ${form}`;
}

/** Read the 7 textareas + the comment input + knobs into a ConstructorConfig. Exported because the
 *  collect-tab "Собрать по поиску" button reads the SAME form (tabs are display:none, the DOM lives
 *  in one document) to preserve the save-then-run behaviour without duplicating the parse logic. */
export function readConfigFromForm(): ConstructorConfig {
  const val = (id: string): string[] => parseTextarea((document.getElementById(id) as HTMLTextAreaElement | null)?.value ?? '');
  const raw = (id: string): string => ((document.getElementById(id) as HTMLInputElement | null)?.value ?? '').trim();
  const max = Number((document.getElementById('ctor-max') as HTMLInputElement | null)?.value);
  const dedup = (document.getElementById('ctor-dedup') as HTMLInputElement | null)?.checked ?? true;
  return {
    subjects: val('ctor-subjects'),
    brand: val('ctor-brand'),
    gender: val('ctor-gender'),
    season: val('ctor-season'),
    age: val('ctor-age'),
    material: val('ctor-material'),
    purpose: val('ctor-purpose'),
    comment: raw('ctor-comment'),
    max_queries: Number.isFinite(max) ? max : 0,
    dedup,
  };
}

function renderPreview(c: ConstructorConfig): void {
  const el = document.getElementById('ctor-preview');
  if (!el) return;
  // eff: an empty axis still iterates once in cartesian() (via dim()), so the effective length is
  // max(1, n) — using raw .length would show "×0 = 0 комбинаций" for an unconfigured dimension,
  // which looks like a bug. This matches what cartesian() actually produces.
  const eff = (n: number): number => Math.max(1, n);
  const total = eff(c.subjects.length) * eff(c.brand.length) * eff(c.gender.length) * eff(c.season.length) * eff(c.age.length) * eff(c.material.length) * eff(c.purpose.length);
  const seeds = cartesian(c);
  const capped = c.max_queries > 0 ? Math.min(seeds.length, c.max_queries) : seeds.length;
  const commentSuffix = c.comment ? ` (+ «${c.comment}» к каждому)` : '';
  el.textContent = `${eff(c.subjects.length)}×${eff(c.brand.length)}×${eff(c.gender.length)}×${eff(c.season.length)}×${eff(c.age.length)}×${eff(c.material.length)}×${eff(c.purpose.length)} = ${total} комбинаций → ${seeds.length} уникальных → ${capped} к сбору${commentSuffix}`;
}

/** Populate the form from storage, then wire preview + save + collect + own_id + storage. */
export async function initSettings(): Promise<void> {
  const cfg = await loadConstructor();
  const setVal = (id: string, v: string): void => {
    const el = document.getElementById(id) as HTMLTextAreaElement | HTMLInputElement | null;
    if (el) el.value = v;
  };
  setVal('ctor-subjects', cfg.subjects.join('\n'));
  setVal('ctor-brand', cfg.brand.join('\n'));
  setVal('ctor-gender', cfg.gender.join('\n'));
  setVal('ctor-season', cfg.season.join('\n'));
  setVal('ctor-age', cfg.age.join('\n'));
  setVal('ctor-material', cfg.material.join('\n'));
  setVal('ctor-purpose', cfg.purpose.join('\n'));
  setVal('ctor-comment', cfg.comment);
  const maxEl = document.getElementById('ctor-max') as HTMLInputElement | null;
  if (maxEl) maxEl.value = String(cfg.max_queries);
  const dedupEl = document.getElementById('ctor-dedup') as HTMLInputElement | null;
  if (dedupEl) dedupEl.checked = cfg.dedup;
  renderPreview(cfg);

  // live preview on any input change
  for (const id of ['ctor-subjects', 'ctor-brand', 'ctor-gender', 'ctor-season', 'ctor-age', 'ctor-material', 'ctor-purpose', 'ctor-comment', 'ctor-max', 'ctor-dedup']) {
    document.getElementById(id)?.addEventListener('input', () => renderPreview(readConfigFromForm()));
  }

  // save → upsert all query texts (stable query_id across sessions)
  document.getElementById('ctor-save')?.addEventListener('click', async () => {
    const c = readConfigFromForm();
    await saveConstructor(c);
    const seeds = cartesian(c);
    const map = await upsertQueries(seeds);
    const el = document.getElementById('ctor-result');
    if (el) el.textContent = `✓ Сохранено: ${seeds.length} запрос(ов), ${map.size} с стабильными query_id (id переживают перезагрузку).`;
  });

  // highlight brands (replaces the old supplier_id highlighter) — a cosmetic accent in reports;
  // does NOT exclude any data. The actual highlight is applied in reports.ts via focusBrands set.
  const focusRaw = await loadHighlightBrands();
  const focusEl = document.getElementById('highlight-brands') as HTMLTextAreaElement | null;
  if (focusEl) focusEl.value = focusRaw.join('\n');
  document.getElementById('highlight-brands-save')?.addEventListener('click', async () => {
    const text = (document.getElementById('highlight-brands') as HTMLTextAreaElement | null)?.value ?? '';
    const list = parseTextarea(text);
    await saveHighlightBrands(list);
    const est = document.getElementById('ctor-result');
    if (est) est.textContent = list.length ? `✓ бренды для подсветки: ${list.join(', ')}` : '✓ подсветка брендов сброшена';
  });

  // detail_k: top-N cards to open per query for /detail capture (per-wh stocks, promotions)
  const dk = await loadDetailK();
  const dkEl = document.getElementById('detail-k') as HTMLInputElement | null;
  if (dkEl) dkEl.value = String(dk);
  document.getElementById('detail-k-save')?.addEventListener('click', async () => {
    const raw = (document.getElementById('detail-k') as HTMLInputElement | null)?.value?.trim();
    const n = raw ? Number(raw) : NaN;
    const val = Number.isFinite(n) && n >= 0 ? Math.floor(n) : 8;
    await saveDetailK(val);
    const est = document.getElementById('ctor-result');
    if (est) est.textContent = val > 0 ? `✓ детализация: топ-${val} карточек на запрос` : '✓ детализация: все карточки (без лимита)';
  });

  // storage estimate + persist
  refreshStorageEstimate();
  document.getElementById('storage-persist')?.addEventListener('click', async () => {
    if (navigator.storage?.persist) {
      const granted = await navigator.storage.persist();
      refreshStorageEstimate();
      const el = document.getElementById('storage-estimate');
      if (el) el.textContent = granted ? '✓ Постоянное хранилище предоставлено.' : '✗ Браузер отклонил запрос (нельзя детерминированно — зависит от настроек сайта).';
    }
  });

  // server URL for snapshot push (POST ${url}/snapshot). Empty = browser-only (push disabled).
  const srvUrl = await loadServerUrl();
  setVal('server-url', srvUrl);
  document.getElementById('server-url-save')?.addEventListener('click', async () => {
    const url = ((document.getElementById('server-url') as HTMLInputElement | null)?.value ?? '').trim();
    await saveServerUrl(url);
    const el = document.getElementById('server-url-result');
    if (el) el.textContent = url ? `✓ снимки будут уходить на ${url}/snapshot после каждого сбора` : '✓ режим «только браузер» — отправка на сервер выключена';
  });
  // push the pending queue immediately (manual sweep) — useful after setting a URL for the first
  // time or retrying after a server outage. The SW's automatic COLLECT_DONE / start sweep covers
  // the normal path; this button is the explicit "ship now" control.
  document.getElementById('server-url-push')?.addEventListener('click', async () => {
    const el = document.getElementById('server-url-result');
    if (el) el.textContent = 'отправляю…';
    // Rebuild the queue from Dexie FIRST: snapshots that finished collecting but aren't queued (they
    // completed before a server URL was set, or were drained by the old skip logic) are folded back
    // in via listSnapshots(). Then shipPending pushes everything. The server's replace-by-snapshot
    // makes re-pushing an already-shipped snapshot safe — DELETE WHERE snapshot_ts first, no
    // duplicates — so this is a true "ship everything I've collected". Tradeoff: re-clicking re-pushes
    // all Dexie snapshots (cheap at test scale, idempotent).
    const all = await listSnapshots();
    for (const snap of all) await enqueueShipment(snap);
    const url = await loadServerUrl();
    const results = await shipPending();
    if (el) {
      if (!url) {
        el.textContent = all.length ? `URL сервера не задан — ${snaps(all.length)} ждут в очереди. Задайте URL и повторите.` : 'URL сервера не задан — собранных снимков нет.';
      } else if (results.length === 0) {
        el.textContent = 'нет собранных снимков для отправки';
      } else {
        const shipped = results.filter((r) => r.shipped).length;
        const failed = results.filter((r) => !r.ok).length;
        el.textContent = `обработано ${results.length}: отправлено ${shipped}, ошибок ${failed}` + (failed ? ' (остались в очереди, повтор позже)' : '');
      }
    }
  });

  // daily collect schedule (chrome.alarms). One HH:MM per line; parsed + validated before save.
  // Saving triggers the SW's storage.onChanged listener → rebuildSchedule (creates one daily alarm
  // per time). Empty = manual-only (no scheduled collects).
  const schedRaw = await loadScheduleTimes();
  setVal('schedule-times', schedRaw.join('\n'));
  document.getElementById('schedule-times-save')?.addEventListener('click', async () => {
    const text = (document.getElementById('schedule-times') as HTMLTextAreaElement | null)?.value ?? '';
    const times = parseScheduleTimes(text);
    await saveScheduleTimes(times);
    const el = document.getElementById('schedule-times-result');
    if (el) el.textContent = times.length ? `✓ расписание: ${times.join(', ')} (ежедневно, будильники перестроены)` : '✓ расписание пусто — сбор только вручную';
  });
}

async function refreshStorageEstimate(): Promise<void> {
  const el = document.getElementById('storage-estimate');
  if (!el) return;
  if (!navigator.storage?.estimate) {
    el.textContent = 'Storage API недоступен в этом браузере.';
    return;
  }
  const { usage = 0, quota = 0 } = await navigator.storage.estimate();
  const pct = quota > 0 ? Math.round((usage / quota) * 100) : 0;
  let suffix = '';
  if (typeof navigator.storage.persisted === 'function') {
    suffix = (await navigator.storage.persisted()) ? ' [persisted]' : ' [не persisted]';
  }
  el.textContent = `Использовано ${(usage / 1048576).toFixed(1)} МБ из ${(quota / 1048576).toFixed(0)} МБ (${pct}%).${suffix}`;
}
