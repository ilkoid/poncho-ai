// src/decode/shapes.ts — TypeScript views over the parsed WB storefront JSON responses.
// Ports the Go struct tags in pkg/wbscraper/decode.go (wbSearchResponse, wbCardProduct, wbSize,
// wbPrice, wbStock, wbBanner). All fields optional: WB responses are large & partially-populated,
// and decode must read defensively (missing field → default), never throw on a sparse product.

export interface WBPrice {
  basic?: number; // kopecks
  product?: number; // kopecks
}

export interface WBStock {
  wh?: number;
  qty?: number;
  time1?: number | null;
  time2?: number | null;
}

export interface WBSize {
  name?: string;
  price?: WBPrice | null;
  wh?: number | null;
  stocks?: WBStock[];
}

/** Shared product shape: search/brand/list/detail all carry this core; card-only fields are optional. */
export interface WBProduct {
  id: number;
  name?: string; // product title — WB sends it on /search, /list, /detail
  brand?: string;
  supplier?: string;
  supplierId?: number | null;
  // promo-panel/campaign id. Recon-verified field, BUT semantics = panel membership, NOT a per-item
  // CPC "is this an ad" flag: one panel id routinely covers most of a query's results (observed
  // id 1041330 across 167/200 items). Treat non-null as "in a promo panel"; a definitive per-item
  // ad signal needs a separate recon pass.
  panelPromoId?: number | null;
  rating?: number;
  feedbacks?: number;
  sizes?: WBSize[];
  // card-only (/list + /detail):
  pics?: unknown; // number (count) OR string[] (URLs)
  weight?: number; // kg, fractional (WB sends floats)
  volume?: number;
  colors?: unknown; // {name}[] OR string[]
  subjectId?: number | null;
  totalQuantity?: number; // /detail-exclusive
  promotions?: unknown; // /detail-exclusive, arbitrary JSON
}

export interface WBSearchResponse {
  resultset?: string; // "filters" → facet-only response, no products
  products?: (WBProduct | null)[] | null;
  metadata?: { name?: string };
}

export interface WBProductListResponse {
  products?: (WBProduct | null)[] | null;
}

/** One characteristic from the card.json `options[]` array (Состав / Цвет / Покрой / …). */
export interface WBCardOption {
  name?: string;
  value?: string;
  charc_type?: number;
  is_variable?: boolean;
  variable_values?: unknown; // string[] typically
}

/** One material component from the card.json `compositions[]` array (хлопок 60% / полиэстер 40%). */
export interface WBCardComposition {
  name?: string;
}

/** One row of the card.json `sizes_table.values[]`: a tech size with its measurement cells. */
export interface WBCardSizeValue {
  tech_size?: string;
  chrt_id?: number;
  details?: (string | number | null)[] | null; // parallel to sizes_table.details_props
}

/** The card.json `sizes_table` — a sparse size grid: column headers (details_props) × rows (values). */
export interface WBCardSizeTable {
  details_props?: (string | null)[];
  values?: (WBCardSizeValue | null)[] | null;
}

/** One color-variant from card.json `full_colors[]` (the bare `colors[]` carries the same nm_ids as numbers). */
export interface WBCardFullColor {
  nm_id?: number;
}

/** One group from card.json `grouped_options[]` — partitions the flat `options[]` by group_name. */
export interface WBCardOptionGroup {
  group_name?: string;
  options?: (WBCardOption | null)[] | null;
}

export interface WBCardSelling {
  brand_name?: string;
  brand_hash?: string;
  supplier_id?: number;
}

export interface WBCardMedia {
  photo_count?: number;
  has_video?: boolean;
}

export interface WBCardData {
  subject_id?: number;
  subject_root_id?: number;
  chrt_ids?: number[];
}

/** The flat product-content object served as a STATIC file at
 *  `basket-{N}.wbbasket.ru/vol{a}/part{b}/{nmId}/info/ru/card.json` — description, characteristics
 *  (options), composition, размерная сетка, colors, brand/media. Distinct from WBProduct: there is
 *  no `products[]` wrapper — the WHOLE body IS one product's content. All fields optional (decode
 *  reads defensively). Этап A captures EVERY known scalar/array; certificate / data.chrt_ids /
 *  kinds_id are intentionally omitted (variable-shape / redundant — see decode/content.ts note). */
export interface WBCardContent {
  imt_id?: number;
  nm_id?: number;
  imt_name?: string; // product title
  slug?: string;
  vendor_code?: string;
  subj_name?: string;
  subj_root_name?: string;
  description?: string;
  markdown_description?: string;
  need_kiz?: boolean;
  create_date?: string;
  update_date?: string;
  options?: (WBCardOption | null)[] | null;
  compositions?: (WBCardComposition | null)[] | null;
  sizes_table?: WBCardSizeTable | null;
  colors?: (number | null)[] | null; // color-variant nm_ids (bare array)
  full_colors?: (WBCardFullColor | null)[] | null;
  nm_colors_names?: string;
  contents?: string; // комплектация ("Рубашка 1 шт")
  kinds?: (string | null)[] | null;
  has_seller_recommendations?: boolean;
  user_flags?: number;
  selling?: WBCardSelling;
  media?: WBCardMedia;
  data?: WBCardData;
  grouped_options?: (WBCardOptionGroup | null)[] | null;
}

/** One banner from banners-website v2/banners or a shelfs/search slot. */
export interface WBBanner {
  href?: string;
  src?: string;
  alt?: string;
  promoText?: string;
  ordBannerMark?: unknown; // string "NAME, ИНН N, ЕРИД E" | object | null
  bannerType?: string;
}

/** Shape 2 of the ad endpoint: {data:{banners:{data:[]}, shelfs:{data:[]}}}. */
export interface WBAdSlotResponse {
  data?: {
    banners?: { data?: WBBanner[] };
    shelfs?: { data?: WBBanner[] };
  };
}
