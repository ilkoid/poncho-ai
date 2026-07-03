// src/reports/suppliers.ts — shared supplier_id → name resolution.
//
// search_positions carries only the numeric supplier_id; the human-readable name lives on
// competitor_cards (hydrated from WB /list + /detail). Both visibility and competitor-map reports
// need this same resolution, so it lives here once instead of being duplicated per report.

import { db } from '../db/dexie';

/** supplier_id → human-readable name. One indexed scan per snapshot; ids never seen on a card → ''
 *  (rare — /list hydrates every visible nm). Scans ignore query_id so a name resolved in one query
 *  is reused everywhere. */
export async function supplierNameMap(snapshots: string[]): Promise<Map<number, string>> {
  const m = new Map<number, string>();
  for (const snap of snapshots) {
    await db.competitor_cards.where('snapshot_ts').equals(snap).each((c) => {
      if (c.supplier_id != null && c.supplier !== '' && !m.has(c.supplier_id)) m.set(c.supplier_id, c.supplier);
    });
  }
  return m;
}
