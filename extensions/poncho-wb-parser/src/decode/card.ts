// src/decode/card.ts — decode card_list / card_detail responses.
// Port of decodeCard + extractCard + extractDetail in pkg/wbscraper/decode.go.
// card_list → CompetitorCards + CompetitorCardPrices; card_detail adds Details + Stocks.

import type {
  CompetitorCard,
  CompetitorCardDetail,
  CompetitorCardPrice,
  CompetitorCardStock,
  Intercept,
  SnapshotTs,
} from '../db/types';
import type { WBProduct, WBProductListResponse } from './shapes';
import { asObject, joinColors, picsCount, rawJSONOrEmpty } from './helpers';

/** decodeCard extracts competitor cards (always), per-size prices, and — only for /detail — the
 *  aggregate details and per-warehouse stocks. Throws on a non-object body. */
export function decodeCard(it: Intercept, snapshot: SnapshotTs, detail: boolean): {
  competitor_cards: CompetitorCard[];
  competitor_card_prices: CompetitorCardPrice[];
  competitor_card_details: CompetitorCardDetail[];
  competitor_card_stocks: CompetitorCardStock[];
} {
  const resp = asObject(it.body, 'card') as unknown as WBProductListResponse;
  const cards: CompetitorCard[] = [];
  const prices: CompetitorCardPrice[] = [];
  const details: CompetitorCardDetail[] = [];
  const stocks: CompetitorCardStock[] = [];

  for (const p of resp.products ?? []) {
    if (!p) continue;
    const { card, prices: pPrices, stocks: pStocks } = extractCard(p, it.query_id, snapshot);
    cards.push(card);
    prices.push(...pPrices);
    if (detail) {
      stocks.push(...pStocks);
      details.push(extractDetail(p, it.query_id, snapshot));
    }
  }
  return { competitor_cards: cards, competitor_card_prices: prices, competitor_card_details: details, competitor_card_stocks: stocks };
}

/** extractCard builds the core card row plus per-size price rows and per-wh stock rows (stocks are
 *  only produced when sizes carry them, i.e. /detail). Nullable fields coerced to null (never
 *  undefined) to satisfy Dexie compound-index requirements. */
function extractCard(
  p: WBProduct,
  qid: number | null,
  snapshot: SnapshotTs,
): { card: CompetitorCard; prices: CompetitorCardPrice[]; stocks: CompetitorCardStock[] } {
  const card: CompetitorCard = {
    snapshot_ts: snapshot,
    query_id: qid,
    nm_id: p.id,
    name: p.name ?? '',
    brand: p.brand ?? '',
    supplier: p.supplier ?? '',
    supplier_id: p.supplierId ?? null,
    rating: p.rating ?? 0,
    feedbacks: p.feedbacks ?? 0,
    pics: picsCount(p.pics),
    weight: p.weight ?? 0,
    volume: p.volume ?? 0,
    colors: joinColors(p.colors),
    subject_id: p.subjectId ?? null,
    panel_promo_id: p.panelPromoId ?? null,
  };

  const prices: CompetitorCardPrice[] = [];
  const stocks: CompetitorCardStock[] = [];
  for (const sz of p.sizes ?? []) {
    if (sz.price) {
      // delivery timing is per-warehouse (stocks[].time1/time2 below), never on the price block.
      prices.push({
        snapshot_ts: snapshot,
        query_id: qid,
        nm_id: p.id,
        size_name: sz.name ?? '',
        price_basic: sz.price.basic ?? 0,
        price_product: sz.price.product ?? 0,
        wh_id: sz.wh ?? null,
      });
    }
    for (const st of sz.stocks ?? []) {
      stocks.push({
        snapshot_ts: snapshot,
        query_id: qid,
        nm_id: p.id,
        size_name: sz.name ?? '',
        wh_id: st.wh ?? null,
        qty: st.qty ?? 0,
        time1: st.time1 ?? null,
        time2: st.time2 ?? null,
      });
    }
  }
  return { card, prices, stocks };
}

/** extractDetail builds the /detail-exclusive aggregate row (total stock + promotions blob). */
function extractDetail(p: WBProduct, qid: number | null, snapshot: SnapshotTs): CompetitorCardDetail {
  return {
    snapshot_ts: snapshot,
    query_id: qid,
    nm_id: p.id,
    total_quantity: p.totalQuantity ?? 0,
    promotions: rawJSONOrEmpty(p.promotions),
  };
}
