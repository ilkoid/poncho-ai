// src/decode/content.ts — decode the flat card.json content object served by the wbbasket.ru CDN
// (basket-{N}.wbbasket.ru/vol{a}/part{b}/{nmId}/info/ru/card.json). Distinct from card.ts: that
// handles /list + /detail {products:[...]}; here the WHOLE body is ONE product's content (options,
// description, vendor_code, need_kiz, …). Characteristics were never collected by v1 — this is new.
//
// Этап A (полнота): emit EVERY known scalar/array of card.json — title (imt_name), brand, media,
// subject ids, colors name, contents, kinds, compositions, размерная сетка, color-variants. Nothing
// is dropped. Intentionally omitted: `certificate` (variable-shape, no analytics value yet),
// `data.chrt_ids[]` (redundant — every chrt_id is already on a sizes[] row), `kinds_id[]` (internal
// WB enum, opaque). If WB adds a new field, add it here + a column/table — parse-only, no raw JSONB.
//
// The URL path is deterministic from nmId, but we don't construct it: the WB card page fetches this
// file itself, and the injector (now wbbasket-aware) forwards it. nm_id comes from the body
// (authoritative), with the URL path as a fallback for bodies that omit it.

import type {
  CompetitorCardColor,
  CompetitorCardComposition,
  CompetitorCardMeta,
  CompetitorCardOption,
  CompetitorCardSize,
  Intercept,
  SnapshotTs,
} from '../db/types';
import type {
  WBCardContent,
  WBCardOptionGroup,
  WBCardSizeTable,
  WBCardSizeValue,
} from './shapes';
import { asObject, rawJSONOrEmpty } from './helpers';

/** /part445378/445378637/info/ru/card.json → 445378637. Fallback only; the body carries nm_id. */
const RE_NM_FROM_PATH = /\/part\d+\/(\d+)\/info\/ru\/card\.json/i;

function nmFromUrl(url: string): number | null {
  const m = url.match(RE_NM_FROM_PATH);
  if (m?.[1]) {
    const n = Number(m[1]);
    if (Number.isFinite(n) && n > 0) return n;
  }
  return null;
}

/** Build a char_name → group_name map from grouped_options[] (each option's group on the card). */
function groupMap(groups: (WBCardOptionGroup | null)[] | null | undefined): Map<string, string> {
  const m = new Map<string, string>();
  if (!groups) return m;
  for (const g of groups) {
    if (!g) continue;
    const groupName = g.group_name ?? '';
    for (const o of g.options ?? []) {
      if (o?.name) m.set(o.name, groupName);
    }
  }
  return m;
}

/** Expand the sizes_table grid into one row per non-empty cell (details_props[i] × details[i]). */
function decodeSizes(
  p: WBCardContent,
  snapshot: SnapshotTs,
  queryId: number | null,
  nmId: number,
): CompetitorCardSize[] {
  const out: CompetitorCardSize[] = [];
  const table = p.sizes_table as WBCardSizeTable | null | undefined;
  const props = table?.details_props ?? [];
  const values = table?.values ?? [];
  if (props.length === 0 || values.length === 0) return out;
  for (const v of values) {
    const row = v as WBCardSizeValue | null;
    if (!row) continue;
    const techSize = row.tech_size ?? '';
    const chrtId = row.chrt_id ?? null;
    const details = row.details ?? [];
    for (let i = 0; i < props.length && i < details.length; i++) {
      const propName = props[i] ?? '';
      const cell = details[i];
      if (cell === null || cell === undefined || cell === '') continue; // sparse grid → skip empties
      out.push({
        snapshot_ts: snapshot,
        query_id: queryId,
        nm_id: nmId,
        tech_size: techSize,
        chrt_id: chrtId,
        prop_name: propName,
        prop_value: String(cell),
        prop_order: i,
      });
    }
  }
  return out;
}

/** Color-variant nm_ids: prefer the bare colors[] (numbers), fall back to full_colors[].nm_id. */
function decodeColors(
  p: WBCardContent,
  snapshot: SnapshotTs,
  queryId: number | null,
  nmId: number,
): CompetitorCardColor[] {
  const ids: number[] = [];
  if (Array.isArray(p.colors)) {
    for (const c of p.colors) if (typeof c === 'number' && c > 0) ids.push(c);
  } else if (Array.isArray(p.full_colors)) {
    for (const c of p.full_colors) if (c?.nm_id && c.nm_id > 0) ids.push(c.nm_id);
  }
  return ids.map((colorNmId, ord) => ({
    snapshot_ts: snapshot,
    query_id: queryId,
    nm_id: nmId,
    color_nm_id: colorNmId,
    ord,
  }));
}

export interface DecodedCardContent {
  competitor_card_meta: CompetitorCardMeta[];
  competitor_card_options: CompetitorCardOption[];
  competitor_card_compositions: CompetitorCardComposition[];
  competitor_card_sizes: CompetitorCardSize[];
  competitor_card_colors: CompetitorCardColor[];
}

/** decodeCardContent turns the flat CDN object into 1 meta row + N option/composition/size/color
 *  rows. nm_id is taken from the body (authoritative) with the URL path as a fallback; if neither
 *  yields one, emits nothing. Throws on a non-object body (malformed capture) — same contract as
 *  search/card. */
export function decodeCardContent(it: Intercept, snapshot: SnapshotTs): DecodedCardContent {
  const p = asObject(it.body, 'card_content') as unknown as WBCardContent;
  const nmId = p.nm_id ?? nmFromUrl(it.url);
  if (nmId == null) {
    return {
      competitor_card_meta: [],
      competitor_card_options: [],
      competitor_card_compositions: [],
      competitor_card_sizes: [],
      competitor_card_colors: [],
    };
  }

  // markdown_description carries the formatted text; fall back to plain description.
  const description = p.markdown_description || p.description || '';
  const groups = groupMap(p.grouped_options);
  const selling = p.selling;
  const media = p.media;
  const data = p.data;

  const meta: CompetitorCardMeta = {
    snapshot_ts: snapshot,
    query_id: it.query_id,
    nm_id: nmId,
    vendor_code: p.vendor_code ?? '',
    subj_name: p.subj_name ?? '',
    subj_root_name: p.subj_root_name ?? '',
    description,
    need_kiz: p.need_kiz ? 1 : 0,
    create_date: p.create_date ?? '',
    update_date: p.update_date ?? '',
    imt_id: p.imt_id ?? null,
    imt_name: p.imt_name ?? '',
    slug: p.slug ?? '',
    brand_name: selling?.brand_name ?? '',
    brand_hash: selling?.brand_hash ?? '',
    supplier_id: selling?.supplier_id ?? null,
    photo_count: media?.photo_count ?? 0,
    has_video: media?.has_video ? 1 : 0,
    subject_id: data?.subject_id ?? null,
    subject_root_id: data?.subject_root_id ?? null,
    nm_colors_names: p.nm_colors_names ?? '',
    contents: p.contents ?? '',
    has_seller_recommendations: p.has_seller_recommendations ? 1 : 0,
    user_flags: p.user_flags ?? 0,
    kinds: rawJSONOrEmpty(p.kinds),
  };

  const options: CompetitorCardOption[] = [];
  for (const o of p.options ?? []) {
    if (!o) continue;
    const name = o.name ?? '';
    options.push({
      snapshot_ts: snapshot,
      query_id: it.query_id,
      nm_id: nmId,
      char_name: name,
      char_value: o.value ?? '',
      charc_type: o.charc_type ?? 0,
      is_variable: o.is_variable ? 1 : 0,
      variable_values: rawJSONOrEmpty(o.variable_values),
      group_name: groups.get(name) ?? '',
    });
  }

  const compositions: CompetitorCardComposition[] = (p.compositions ?? [])
    .filter((c): c is NonNullable<typeof c> => c != null)
    .map((c, ord) => ({
      snapshot_ts: snapshot,
      query_id: it.query_id,
      nm_id: nmId,
      name: c.name ?? '',
      ord,
    }));

  return {
    competitor_card_meta: [meta],
    competitor_card_options: options,
    competitor_card_compositions: compositions,
    competitor_card_sizes: decodeSizes(p, snapshot, it.query_id, nmId),
    competitor_card_colors: decodeColors(p, snapshot, it.query_id, nmId),
  };
}
