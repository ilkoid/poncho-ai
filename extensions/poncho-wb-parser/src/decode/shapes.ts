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
