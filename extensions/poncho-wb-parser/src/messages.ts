// src/messages.ts — the runtime message protocol shared by SW, offscreen, content bridge, and
// dashboard. A discriminated union (`type`) keeps every handler exhaustively typed; later stages
// (S3 collect, S4 constructor, S5 reports) add cases here, not ad-hoc string literals.
//
// Topology:
//   inject (MAIN) ──postMessage──► bridge (ISOLATED) ──INTERCEPT──► SW ──CAPTURE──► offscreen
//   popup/dashboard ──COLLECT_*──► SW ──COLLECT_LOOP──► offscreen ──NAVIGATE/SCROLL──► SW ──► tab
//   offscreen ──PROGRESS──► SW ──broadcast──► dashboard

import type { Intercept } from './db/types';

/** One collect target handed to the run loop (constructor query or a direct nmId/url). */
export interface Target {
  kind: 'search' | 'card' | 'url';
  query_id: number | null;
  query: string; // human text (kind=search); empty otherwise
  url: string;
  subject: string;
  brand: string;
  gender: string;
  season: string;
  age: string;
  material: string;
  purpose: string;
  comment: string;
}

/** Source of targets for a collect session. */
export type CollectSource =
  | { source: 'constructor' } // cartesian from saved constructor lists
  | { source: 'single'; singleQuery?: string; singleNmId?: number }; // debug one query / one NM

/** Row counts per fact table, for progress + final summary. */
export interface FactCounts {
  search_positions: number;
  vitrine_ads: number;
  competitor_cards: number;
  competitor_card_prices: number;
  competitor_card_details: number;
  competitor_card_stocks: number;
  competitor_card_meta: number;
  competitor_card_options: number;
  competitor_card_compositions: number;
  competitor_card_sizes: number;
  competitor_card_colors: number;
}

export const EMPTY_COUNTS: FactCounts = {
  search_positions: 0,
  vitrine_ads: 0,
  competitor_cards: 0,
  competitor_card_prices: 0,
  competitor_card_details: 0,
  competitor_card_stocks: 0,
  competitor_card_meta: 0,
  competitor_card_options: 0,
  competitor_card_compositions: 0,
  competitor_card_sizes: 0,
  competitor_card_colors: 0,
};

/** Messages addressed TO the service worker. */
export type ToSW =
  | { type: 'OPEN_DASHBOARD' }
  | { type: 'COLLECT_START'; collect: CollectSource; snapshotTs: string }
  | { type: 'COLLECT_STOP' }
  | { type: 'CLEAR_ALL' }
  | { type: 'RUN_MOCK_SESSION'; snapshotTs: string } // S3 testing hook: feed MockIntercepts
  | { type: 'INTERCEPT'; payload: { url: string; status: number; body: unknown } }; // from bridge

/** Messages addressed TO the offscreen document (run-loop owner). */
export type ToOffscreen =
  | { type: 'COLLECT_LOOP'; targets: Target[]; snapshotTs: string; detailK: number }
  | { type: 'MOCK_DECODE'; intercepts: Intercept[]; snapshotTs: string } // S3: decode+persist mock
  | { type: 'CAPTURE'; item: Intercept }
  | { type: 'COLLECT_STOP' };

/** Messages the offscreen sends to the SW (SW relays NAVIGATE/SCROLL to tabs, PROGRESS to dashboard). */
export type FromOffscreen =
  | { type: 'NAVIGATE'; target: Target }
  | { type: 'SCROLL' } // → reply { ok: boolean; grew: boolean }
  | { type: 'GET_NMIDS'; limit: number } // → reply { nmids: number[] }
  | { type: 'COLLECT_DONE' }
  | { type: 'PROGRESS'; target?: Target; phase: string; counts: FactCounts };

/** Broadcast (SW → dashboard) summarizing a snapshot shipment attempt. Fired by the SW after a
 *  push (on COLLECT_DONE) or a sweep retry. status 'shipped' = landed on the server (counts set);
 *  'skipped' = browser-only mode (no URL); 'failed' = network/HTTP error (stays queued for retry). */
export type ShipmentMsg =
  | { type: 'SHIPMENT'; snapshot: string; status: 'shipped' | 'skipped' | 'failed'; counts?: Record<string, number>; error?: string };
