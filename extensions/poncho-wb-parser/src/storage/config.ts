// src/storage/config.ts — user-editable settings persisted in chrome.storage.local (always
// available via the "storage" permission; survives SW death + browser restart).
//   - constructor: 6 cartesian lists (subject/gender/season/age/material/purpose) + a single
//     `comment` appended to every query + max_queries + dedup (the query generator config).
//   - own_supplier_id: the seller's own supplier id, used in reports to highlight "our" rank/card.
//   - detail_k: how many top cards (by position) to open per query for /detail capture
//     (per-wh stocks + promotions). >0 = top-N; <=0 = unlimited (all). Default 8.

import type { ConstructorConfig } from '../querygen/static';
import { DEFAULT_CONSTRUCTOR } from '../querygen/static';

const KEY_CONSTRUCTOR = 'constructor';
const KEY_OWN_SUPPLIER = 'own_supplier_id';
const KEY_DETAIL_K = 'detail_k';
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

/** The seller's own supplier_id (null = not set → reports show no "own" highlight). */
export async function loadOwnSupplierId(): Promise<number | null> {
  const s = await chrome.storage.local.get(KEY_OWN_SUPPLIER).catch(() => ({}) as Record<string, unknown>);
  const v = s[KEY_OWN_SUPPLIER];
  return typeof v === 'number' ? v : null;
}

export async function saveOwnSupplierId(id: number | null): Promise<void> {
  await chrome.storage.local.set({ [KEY_OWN_SUPPLIER]: id });
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
