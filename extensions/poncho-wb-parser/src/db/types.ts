// src/db/types.ts — the persisted data contract. A 1:1 port of pkg/wbscraper/types.go
// (field names mirror the Go json tags / SQL columns on purpose: this is an on-disk schema,
// not an internal API, so snake_case keeps Dexie indexes, decode output, and reports aligned
// with v1 without a mapping layer).
//
// Conventions:
//   - Fact rows (append-only by snapshot_ts) carry an optional `id` — Dexie's ++id surrogate,
//     assigned on insert, present after read. Callers never set it.
//   - Nullable numerics are `number | null`, NEVER `undefined`: Dexie does not index undefined
//     members of a compound key ([query_id+snapshot_ts]), which would silently break
//     query_id=null rows (direct nmId/url targets). Coerce to null at the write boundary.
//   - Prices are int64 kopecks (WB serves search/list prices already in kopecks).

/** ISO-8601 timestamp tagging one capture session; all fact rows of a session share one value. */
export type SnapshotTs = string;

/** search_queries dimension: one row per constructed query text; &query is the UNIQUE upsert anchor. */
export interface SearchQuery {
  query_id?: number; // ++query_id (Dexie autoinc); undefined on insert, set on read
  query: string; // normalized search text (the WB search box string)
  subject: string;
  brand: string; // cartesian-axis provenance (empty for single-query targets)
  gender: string;
  season: string;
  age: string;
  material: string; // cartesian-axis provenance (empty for single-query targets)
  purpose: string; // cartesian-axis provenance (empty for single-query targets)
  comment: string; // the single free-text suffix appended to every query (empty if none)
}

/** One captured WB API response (the wire shape of an intercept forwarded MAIN→SW→offscreen). */
export interface Intercept {
  // string (not a union): WB endpoints drift (v4/v18/…), and an unrecognized kind must route to
  // Decode's default (empty, no error) rather than fail at the type level. Mirrors Go's `Kind string`.
  kind: string;
  url: string;
  query_id: number | null; // null = direct nmId/url target (NoQuery sentinel)
  status: number;
  /** Already-parsed WB response JSON (the interceptor JSON.parse's the body before forwarding). */
  body: unknown;
}

/** Per-table row bundle produced by Decode for one Intercept; only relevant slices are filled. */
export interface Decoded {
  search_positions: SearchPosition[];
  vitrine_ads: VitrineAd[];
  competitor_cards: CompetitorCard[];
  competitor_card_prices: CompetitorCardPrice[];
  competitor_card_details: CompetitorCardDetail[];
  competitor_card_stocks: CompetitorCardStock[];
  competitor_card_meta: CompetitorCardMeta[];
  competitor_card_options: CompetitorCardOption[];
  competitor_card_compositions: CompetitorCardComposition[];
  competitor_card_sizes: CompetitorCardSize[];
  competitor_card_colors: CompetitorCardColor[];
}

export interface SearchPosition {
  id?: number;
  snapshot_ts: SnapshotTs;
  query_id: number | null; // always a real query for a search/brand capture
  region_dest: number | null; // WB dest/cluster; snapshots without a region are not comparable
  page: number;
  position: number; // global rank: (page-1)*100 + index + 1
  nm_id: number;
  name: string; // product title (WB /search products[].name)
  brand: string;
  supplier_id: number | null;
  panel_promo_id: number | null; // non-null = inside a WB promo panel (NOT a per-item CPC ad — see reports/visibility.ts note)
  price_basic: number; // kopecks
  price_product: number; // kopecks
  rating: number;
  feedbacks: number;
}

export interface VitrineAd {
  id?: number;
  snapshot_ts: SnapshotTs;
  query_id: number | null;
  advertiser_name: string;
  advertiser_inn: string;
  erid: string;
  promo_id: number | null; // v2/banners exposes no numeric promo id → null
  banner_type: string;
  creative_url: string;
  landing_href: string;
}

export interface CompetitorCard {
  id?: number;
  snapshot_ts: SnapshotTs;
  query_id: number | null;
  nm_id: number;
  name: string; // product title (WB /list + /detail products[].name)
  brand: string;
  supplier: string;
  supplier_id: number | null;
  rating: number;
  feedbacks: number;
  pics: number;
  weight: number; // kg, as WB sends it (fractional, e.g. 0.09)
  volume: number;
  colors: string;
  subject_id: number | null;
  panel_promo_id: number | null;
}

export interface CompetitorCardPrice {
  id?: number;
  snapshot_ts: SnapshotTs;
  query_id: number | null;
  nm_id: number;
  size_name: string;
  price_basic: number; // kopecks
  price_product: number; // kopecks
  wh_id: number | null;
  // NOTE: delivery timing is NOT on the price row — WB exposes it per-warehouse in stocks
  // (CompetitorCardStock.time1/time2), which are captured at 100%. There is no delivery field
  // on sizes[].price in the recon-verified WB shape, so no delivery_days column here.
}

export interface CompetitorCardDetail {
  id?: number;
  snapshot_ts: SnapshotTs;
  query_id: number | null;
  nm_id: number;
  total_quantity: number;
  promotions: string; // JSON text (variable-shape array)
}

export interface CompetitorCardStock {
  id?: number;
  snapshot_ts: SnapshotTs;
  query_id: number | null;
  nm_id: number;
  size_name: string;
  wh_id: number | null;
  qty: number;
  time1: number | null;
  time2: number | null;
}

/** Per-nm scalar content from the wbbasket.ru card.json CDN file (1 row per nm per snapshot).
 *  vendor_code = артикул продавца (год выпуска = символы 2-3); need_kiz = маркировка. Joins on nm_id.
 *  Этап A: expanded to capture EVERY known scalar of card.json (title, brand, media, subject ids,
 *  colors name, contents, kinds, seller-recommendation flag) — nothing dropped. Nullable numerics
 *  stay `number | null` (Dexie compound-index rule — never undefined). */
export interface CompetitorCardMeta {
  id?: number;
  snapshot_ts: SnapshotTs;
  query_id: number | null;
  nm_id: number;
  vendor_code: string; // артикул продавца
  subj_name: string; // человекочитаемая категория (WB subject)
  subj_root_name: string; // корневая категория
  description: string; // полное описание (markdown_description при наличии)
  need_kiz: number; // 1 = требуется маркировка, 0 = нет
  create_date: string; // ISO создания карточки ('' если нет)
  update_date: string; // ISO обновления ('' если нет)
  imt_id: number | null; // WB imt (родительский артукул карточки)
  imt_name: string; // название товара (заголовок карточки)
  slug: string; // URL-slug товара
  brand_name: string; // selling.brand_name
  brand_hash: string; // selling.brand_hash
  supplier_id: number | null; // selling.supplier_id
  photo_count: number; // media.photo_count
  has_video: number; // media.has_video (0/1)
  subject_id: number | null; // data.subject_id (числовой ID категории)
  subject_root_id: number | null; // data.subject_root_id
  nm_colors_names: string; // имена цветов (скаляр из card.json)
  contents: string; // комплектация ("Рубашка 1 шт")
  has_seller_recommendations: number; // 0/1
  user_flags: number;
  kinds: string; // JSON-text массив (rawJSONOrEmpty), '' если нет
}

/** One product characteristic (Состав / Цвет / Покрой / …) from card.json `options[]` — N rows per
 *  nm per snapshot. variable_values = JSON-массив значений для варьируемых характеристик ('' если нет).
 *  group_name — раздел из grouped_options[] («Основная информация» / «Дополнительная информация»). */
export interface CompetitorCardOption {
  id?: number;
  snapshot_ts: SnapshotTs;
  query_id: number | null;
  nm_id: number;
  char_name: string; // "Состав" / "Цвет" / "Покрой" …
  char_value: string; // "хлопок 60%; полиэстер 40%"
  charc_type: number; // WB charc_type (1 = обычная характеристика)
  is_variable: number; // 1 = варьируется (есть variable_values), 0 = нет
  variable_values: string; // JSON-массив значений (rawJSONOrEmpty), '' если нет
  group_name: string; // группа из grouped_options[] ('' если без группы)
}

/** One material component from card.json `compositions[]` (хлопок 60% / полиэстер 40%). N rows per nm
 *  per snapshot; `ord` preserves the on-card order. */
export interface CompetitorCardComposition {
  id?: number;
  snapshot_ts: SnapshotTs;
  query_id: number | null;
  nm_id: number;
  name: string; // "хлопок 60%"
  ord: number; // позиция в compositions[]
}

/** One cell of the card.json size grid: a single measurement (prop_name=prop_value) for one tech_size.
 *  Built by zipping sizes_table.details_props[i] × values[k].details[i]. Empty cells are skipped
 *  (a sparse grid cell conveys no data and would only bloat the table). */
export interface CompetitorCardSize {
  id?: number;
  snapshot_ts: SnapshotTs;
  query_id: number | null;
  nm_id: number;
  tech_size: string; // "128" / "42" …
  chrt_id: number | null; // WB размерный chrt_id
  prop_name: string; // "RU" / "Рост, см" / "Обхват груди, в см" …
  prop_value: string; // "128" / "63.6" …
  prop_order: number; // индекс в details_props[]
}

/** One color-variant nm_id from card.json colors[]/full_colors[] (другие цвета того же товара).
 *  N rows per nm per snapshot; `ord` preserves the on-card order. */
export interface CompetitorCardColor {
  id?: number;
  snapshot_ts: SnapshotTs;
  query_id: number | null;
  nm_id: number;
  color_nm_id: number; // nm_id варианта цвета
  ord: number; // позиция в colors[]
}

