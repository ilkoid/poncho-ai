// src/storage/config.ts — user-editable settings persisted in chrome.storage.local (always
// available via the "storage" permission; survives SW death + browser restart).
//   - constructor: 7 cartesian lists (subject/brand/gender/season/age/material/purpose) + a single
//     `comment` appended to every query + max_queries + dedup (the query generator config).
//   - highlight_brands: brand names to highlight in reports (Видимость, Карта конкурентов);
//     cosmetic accent only — does not filter/exclude any collected data. Empty = no highlight.
//   - detail_k: how many top cards (by position) to open per query for /detail capture
//     (per-wh stocks + promotions). >0 = top-N; <=0 = unlimited (all). Default 8.
//   - report_filter: structured drill-down filter (price band, brand include/exclude, rating floor,
//     etc.) applied to ALREADY-COMPUTED report rows before render. Does NOT affect collection/write.

import type { ConstructorConfig } from '../querygen/static';
import { DEFAULT_CONSTRUCTOR } from '../querygen/static';
import type { ReportFilter } from '../reports/filters';
import { DEFAULT_REPORT_FILTER } from '../reports/filters';

const KEY_CONSTRUCTOR = 'constructor';
const KEY_HIGHLIGHT_BRANDS = 'highlight_brands';
const KEY_DETAIL_K = 'detail_k';
const KEY_REPORT_FILTER = 'report_filter';
const KEY_SERVER_URL = 'server_url';
// Exported: the SW's storage.onChanged listener filters on this key to rebuild the alarms schedule
// when the user edits the times in the dashboard (the other KEY_* are config.ts-private).
export const KEY_SCHEDULE_TIMES = 'schedule_times';
export const DEFAULT_DETAIL_K = 8;

/** Load the constructor config, falling back to the default for any missing field. */
export async function loadConstructor(): Promise<ConstructorConfig> {
  const s = await chrome.storage.local.get(KEY_CONSTRUCTOR).catch(() => ({}) as Record<string, unknown>);
  const c = (s[KEY_CONSTRUCTOR] ?? null) as ConstructorConfig | null;
  return { ...DEFAULT_CONSTRUCTOR, ...(c ?? {}) };
}

export async function saveConstructor(c: ConstructorConfig): Promise<void> {
  await chrome.storage.local.set({ [KEY_CONSTRUCTOR]: c });
}

/** Brand names to highlight in reports (empty = no highlight). Survives SW death + restart. */
export async function loadHighlightBrands(): Promise<string[]> {
  const s = await chrome.storage.local.get(KEY_HIGHLIGHT_BRANDS).catch(() => ({}) as Record<string, unknown>);
  const v = s[KEY_HIGHLIGHT_BRANDS];
  return Array.isArray(v) ? (v as string[]) : [];
}

export async function saveHighlightBrands(brands: string[]): Promise<void> {
  await chrome.storage.local.set({ [KEY_HIGHLIGHT_BRANDS]: brands });
}

/** detail_k: top-N cards (by position) to open per query for /detail capture. >0 = top-N;
 *  <=0 = unlimited. Falls back to the default (8) when unset, non-numeric, OR if chrome.storage is
 *  unavailable in the calling context (the offscreen document reads storage here for the first
 *  time — a sync throw would otherwise kill runLoop → no tab opens). */
export async function loadDetailK(): Promise<number> {
  try {
    const s = await chrome.storage.local.get(KEY_DETAIL_K);
    const v = (s as Record<string, unknown> | undefined)?.[KEY_DETAIL_K];
    return typeof v === 'number' && Number.isFinite(v) ? v : DEFAULT_DETAIL_K;
  } catch {
    return DEFAULT_DETAIL_K; // chrome.storage missing/unavailable in this context → safe default
  }
}

export async function saveDetailK(n: number): Promise<void> {
  await chrome.storage.local.set({ [KEY_DETAIL_K]: n });
}

/** Structured report filter (price band, brand include/exclude, rating/feedbacks floors, supplier_id
 *  include/exclude). Applied to computed report rows before render — never affects collection/write.
 *  Survives SW death + restart. Falls back to DEFAULT_REPORT_FILTER for any missing field. */
export async function loadReportFilter(): Promise<ReportFilter> {
  const s = await chrome.storage.local.get(KEY_REPORT_FILTER).catch(() => ({}) as Record<string, unknown>);
  const v = (s[KEY_REPORT_FILTER] ?? null) as Partial<ReportFilter> | null;
  return { ...DEFAULT_REPORT_FILTER, ...(v ?? {}) };
}

export async function saveReportFilter(f: ReportFilter): Promise<void> {
  await chrome.storage.local.set({ [KEY_REPORT_FILTER]: f });
}

/** Go collector base URL for snapshot push (POST ${url}/snapshot). Empty (default) = browser-only
 *  mode — the extension keeps everything in Dexie and ships nothing. Survives SW death + restart.
 *  Trailing slashes are stripped at the push site. Falls back to '' on any storage error so the
 *  push silently no-ops rather than throwing in a context without storage access. */
export async function loadServerUrl(): Promise<string> {
  const s = await chrome.storage.local.get(KEY_SERVER_URL).catch(() => ({}) as Record<string, unknown>);
  const v = s[KEY_SERVER_URL];
  return typeof v === 'string' ? v.trim() : '';
}

export async function saveServerUrl(url: string): Promise<void> {
  await chrome.storage.local.set({ [KEY_SERVER_URL]: url.trim() });
}

/** Daily collect times as "HH:MM" strings (e.g. ["11:00","17:00","21:00"]). Empty = no schedule
 *  (collect only via the dashboard button). Survives SW death + restart. Validated/normalized at
 *  the settings layer (parseScheduleTimes) before saving. */
export async function loadScheduleTimes(): Promise<string[]> {
  const s = await chrome.storage.local.get(KEY_SCHEDULE_TIMES).catch(() => ({}) as Record<string, unknown>);
  const v = s[KEY_SCHEDULE_TIMES];
  return Array.isArray(v) ? (v as unknown[]).filter((x): x is string => typeof x === 'string') : [];
}

export async function saveScheduleTimes(times: string[]): Promise<void> {
  await chrome.storage.local.set({ [KEY_SCHEDULE_TIMES]: times });
}
