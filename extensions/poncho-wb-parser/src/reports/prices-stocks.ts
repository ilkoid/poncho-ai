// src/reports/prices-stocks.ts — Цены и остатки. A 10-bucket price histogram (price_product, in
// kopecks) and the out-of-print (OOP) list — products whose total warehouse stock is 0.
//
// Three indexed scans scoped to the snapshot: cards (for brand lookup), prices (histogram),
// stocks (OOP). min/max are computed in a loop (NOT Math.min(...arr) — the spread blows the stack
// on a ~1M-price real session).

import { db } from '../db/dexie';

export interface PriceBucket {
  lo: number; // kopecks
  hi: number;
  count: number;
}

export interface OutOfStockItem {
  nm_id: number;
  brand: string;
  total_qty: number;
}

export interface PriceStockRow {
  nm_id: number;
  name: string;
  brand: string;
  supplier: string;
  price_min: number | null; // kopecks
  price_avg: number | null; // kopecks; mean of price_product across sizes/warehouses
  total_qty: number; // summed warehouse stock
}

export interface PricesStocksReport {
  snapshot: string;
  query_id: number | null;
  histogram: PriceBucket[];
  price_count: number;
  out_of_stock: OutOfStockItem[];
  in_stock_count: number;
  rows: PriceStockRow[]; // one row per captured card, for the per-product table
}

// Per-table typed collection builders (a generic helper would union the row types and lose
// .price_product / .qty / .brand). Each returns the correctly-typed Dexie Collection.
const cardsOf = (snapshot: string, qid: number | null) =>
  qid != null ? db.competitor_cards.where('[query_id+snapshot_ts]').equals([qid, snapshot]) : db.competitor_cards.where('snapshot_ts').equals(snapshot);
const pricesOf = (snapshot: string, qid: number | null) =>
  qid != null ? db.competitor_card_prices.where('[query_id+snapshot_ts]').equals([qid, snapshot]) : db.competitor_card_prices.where('snapshot_ts').equals(snapshot);
const stocksOf = (snapshot: string, qid: number | null) =>
  qid != null ? db.competitor_card_stocks.where('[query_id+snapshot_ts]').equals([qid, snapshot]) : db.competitor_card_stocks.where('snapshot_ts').equals(snapshot);

export async function buildPricesStocks(snapshot: string, queryId: number | null): Promise<PricesStocksReport> {
  // nm_id → card identity (name/brand/supplier) — one cards scan, feeds both the OOP list and the
  // per-product table. First capture wins (a nm_id repeats across pages/queries within a snapshot).
  const cardByNm = new Map<number, { name: string; brand: string; supplier: string }>();
  await cardsOf(snapshot, queryId).each((c) => {
    if (!cardByNm.has(c.nm_id)) cardByNm.set(c.nm_id, { name: c.name, brand: c.brand, supplier: c.supplier });
  });

  // prices → histogram bounds + per-nm min/avg (folded into one pass, not three)
  let min = Number.POSITIVE_INFINITY;
  let max = Number.NEGATIVE_INFINITY;
  let priceCount = 0;
  const nmPrice = new Map<number, { min: number; sum: number; n: number }>();
  await pricesOf(snapshot, queryId).each((p) => {
    const v = p.price_product;
    if (v < min) min = v;
    if (v > max) max = v;
    priceCount++;
    const cur = nmPrice.get(p.nm_id);
    if (cur) {
      if (v < cur.min) cur.min = v;
      cur.sum += v;
      cur.n++;
    } else {
      nmPrice.set(p.nm_id, { min: v, sum: v, n: 1 });
    }
  });
  let histogram: PriceBucket[] = [];
  if (priceCount > 0 && Number.isFinite(min) && Number.isFinite(max)) {
    const span = Math.max(1, max - min);
    const w = span / 10;
    const counts = new Array<number>(10).fill(0);
    await pricesOf(snapshot, queryId).each((p) => {
      let b = Math.floor((p.price_product - min) / w);
      if (b > 9) b = 9;
      if (b < 0) b = 0;
      counts[b] = (counts[b] ?? 0) + 1;
    });
    histogram = counts.map((c, i) => ({ lo: Math.round(min + i * w), hi: Math.round(min + (i + 1) * w), count: c }));
  }

  // stocks → OOP (total qty per nm_id; <=0 = out of print)
  const qtyByNm = new Map<number, number>();
  await stocksOf(snapshot, queryId).each((s) => {
    qtyByNm.set(s.nm_id, (qtyByNm.get(s.nm_id) ?? 0) + s.qty);
  });
  const out_of_stock: OutOfStockItem[] = [];
  let inStockCount = 0;
  for (const [nm, qty] of qtyByNm) {
    if (qty <= 0) out_of_stock.push({ nm_id: nm, brand: cardByNm.get(nm)?.brand ?? '', total_qty: qty });
    else inStockCount++;
  }
  out_of_stock.sort((a, b) => a.nm_id - b.nm_id);

  // per-product table: one row per captured card, with min/avg price and total stock.
  const rows: PriceStockRow[] = [];
  for (const [nm, card] of cardByNm) {
    const pr = nmPrice.get(nm);
    rows.push({
      nm_id: nm,
      name: card.name,
      brand: card.brand,
      supplier: card.supplier,
      price_min: pr ? pr.min : null,
      price_avg: pr ? Math.round(pr.sum / pr.n) : null,
      total_qty: qtyByNm.get(nm) ?? 0,
    });
  }
  rows.sort((a, b) => b.total_qty - a.total_qty || (a.price_avg ?? Infinity) - (b.price_avg ?? Infinity));

  return { snapshot, query_id: queryId, histogram, price_count: priceCount, out_of_stock, in_stock_count: inStockCount, rows };
}
