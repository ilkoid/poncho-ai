// tests/competitiveness.test.ts — Конкурентность ниши. Seeds search_positions + vitrine_ads directly
// (full control over page/panel_promo_id/erid — the mock seed only sits on page 2 with no banners).
// Verifies HHI (share-of-attention), page-1 promo coverage, distinct banners/advertisers.

import { describe, it, expect, beforeEach } from 'vitest';
import { db } from '../src/db/dexie';
import { buildCompetitiveness } from '../src/reports/competitiveness';
import type { SearchPosition, VitrineAd } from '../src/db/types';

const SNAP = '2026-07-02T10:00:00Z';

beforeEach(async () => {
  await db.transaction('rw', db.tables, async () => {
    await Promise.all(db.tables.map((t) => t.clear()));
  });
});

function pos(nm: number, page: number, supplier: number, panel: number | null): SearchPosition {
  return {
    snapshot_ts: SNAP,
    query_id: 7,
    region_dest: 8038,
    page,
    position: page === 1 ? nm : 100 + nm,
    nm_id: nm,
    name: `t${nm}`,
    brand: nm === 333 ? 'Reebok' : 'Nike',
    supplier_id: supplier,
    panel_promo_id: panel,
    price_basic: 100000,
    price_product: 89900,
    rating: 4.5,
    feedbacks: 10,
  };
}

function ad(erid: string, inn: string): VitrineAd {
  return {
    snapshot_ts: SNAP,
    query_id: 7,
    advertiser_name: 'РеклаКо',
    advertiser_inn: inn,
    erid,
    promo_id: null,
    banner_type: 'shelf',
    creative_url: '',
    landing_href: '',
  };
}

describe('competitiveness', () => {
  it('HHI (share of attention), page-1 promo coverage, banners', async () => {
    await db.search_positions.bulkAdd([
      pos(111, 1, 900, 50), // page 1, under panel 50
      pos(222, 1, 901, null), // page 1, organic
      pos(333, 2, 900, null), // page 2 (NOT counted in page1_*)
    ]);
    await db.vitrine_ads.bulkAdd([ad('X1', '7700000001'), ad('X2', '7700000001')]);

    const r = await buildCompetitiveness(SNAP, 7, new Set(['nike']));

    expect(r.summary.total_suppliers).toBe(2); // 900, 901
    expect(r.summary.total_nms).toBe(3); // 111, 222, 333
    // supplier 900: 2 nms (2/3); 901: 1 nm (1/3) → HHI = 4/9 + 1/9 = 0.5556
    expect(r.summary.hhi).toBeCloseTo(5 / 9, 4);
    expect(r.summary.page1_size).toBe(2); // only page-1 rows
    expect(r.summary.page1_promo_covered).toBe(1); // nm111 under panel 50
    expect(r.summary.page1_promo_coverage_pct).toBe(50);
    expect(r.summary.distinct_banners).toBe(2); // X1, X2
    expect(r.summary.distinct_advertisers).toBe(1); // one INN
    expect(r.summary.total_banner_rows).toBe(2);

    // rows sorted by nm_count desc: 900 (2) before 901 (1)
    expect(r.rows[0]!.supplier_id).toBe(900);
    expect(r.rows[0]!.nm_count).toBe(2);
    expect(r.rows[0]!.share).toBeCloseTo(2 / 3, 4);
    expect(r.rows[1]!.supplier_id).toBe(901);
    expect(r.rows[1]!.nm_count).toBe(1);
  });

  it('is_focus tracks the focus brand at the SUPPLIER level (any ranked product)', async () => {
    await db.search_positions.bulkAdd([
      pos(111, 1, 900, null),
      pos(222, 1, 901, null),
    ]);
    // both suppliers' only product is Nike in the pos() helper → both focus
    const r = await buildCompetitiveness(SNAP, 7, new Set(['nike']));
    expect(r.rows.find((x) => x.supplier_id === 900)!.is_focus).toBe(true);
    expect(r.rows.find((x) => x.supplier_id === 901)!.is_focus).toBe(true);

    // Adidas focus set → neither supplier carries Adidas here
    const r2 = await buildCompetitiveness(SNAP, 7, new Set(['adidas']));
    expect(r2.rows.find((x) => x.supplier_id === 900)!.is_focus).toBe(false);
    expect(r2.rows.find((x) => x.supplier_id === 901)!.is_focus).toBe(false);
  });

  it('no vitrine_ads → zero banners; no page-1 rows → 0% coverage (edge case)', async () => {
    await db.search_positions.bulkAdd([pos(111, 2, 900, 99)]); // page 2 only
    const r = await buildCompetitiveness(SNAP, 7, new Set());
    expect(r.summary.page1_size).toBe(0);
    expect(r.summary.page1_promo_coverage_pct).toBe(0);
    expect(r.summary.distinct_banners).toBe(0);
    expect(r.summary.distinct_advertisers).toBe(0);
    expect(r.summary.total_banner_rows).toBe(0);
  });
});
