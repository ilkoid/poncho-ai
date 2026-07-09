// src/db/dexie.ts — the Poncho WB Parser IndexedDB schema (Dexie wrapper).
//
// Mirrors pkg/storage/sqlite/wbscraper_schema.go (1 dimension + 6 append-only fact tables),
// adding compound indexes tuned for the report access patterns in src/reports/.
//
// DB name is its own ('poncho_wb_parser') so v2 never touches v1's 'wb-scraper' IndexedDB —
// both extensions can run side by side against the same wildberries.ru origin.
//
// Dexie schema syntax: the FIRST token is the primary key ('++id' = auto-incrementing surrogate);
// a token prefixed with '&' is a UNIQUE index (used on `query` — the upsert anchor that makes a
// query text resolve to one stable query_id across sessions); other comma tokens are non-unique
// indexes; '[a+b+c]' is a compound index.
//
// Index design:
//   - search_queries: ++query_id PK + &query UNIQUE → stable id per query text.
//   - Facts: ++id surrogate (append-only has no natural PK); compound indexes give access patterns:
//     [query_id+snapshot_ts] → "all rows of one query in one snapshot";
//     [nm_id+snapshot_ts]   → "one product across snapshots" (visibility delta);
//     [query_id+region_dest+snapshot_ts] → region-scoped visibility.

import Dexie, { type Table } from 'dexie';
import type {
  CompetitorCard,
  CompetitorCardColor,
  CompetitorCardComposition,
  CompetitorCardDetail,
  CompetitorCardMeta,
  CompetitorCardOption,
  CompetitorCardPrice,
  CompetitorCardSize,
  CompetitorCardStock,
  SearchPosition,
  SearchQuery,
  VitrineAd,
} from './types';

export class PonchoDB extends Dexie {
  search_queries!: Table<SearchQuery, number>;
  search_positions!: Table<SearchPosition, number>;
  vitrine_ads!: Table<VitrineAd, number>;
  competitor_cards!: Table<CompetitorCard, number>;
  competitor_card_prices!: Table<CompetitorCardPrice, number>;
  competitor_card_details!: Table<CompetitorCardDetail, number>;
  competitor_card_stocks!: Table<CompetitorCardStock, number>;
  competitor_card_meta!: Table<CompetitorCardMeta, number>;
  competitor_card_options!: Table<CompetitorCardOption, number>;
  competitor_card_compositions!: Table<CompetitorCardComposition, number>;
  competitor_card_sizes!: Table<CompetitorCardSize, number>;
  competitor_card_colors!: Table<CompetitorCardColor, number>;

  constructor() {
    super('poncho_wb_parser');
    this.version(1).stores({
      search_queries: '++query_id, &query, subject, gender, season, age',
      search_positions:
        '++id, [query_id+snapshot_ts], [nm_id+snapshot_ts], [query_id+region_dest+snapshot_ts], snapshot_ts, query_id, nm_id, supplier_id',
      vitrine_ads: '++id, [query_id+snapshot_ts], snapshot_ts, query_id, advertiser_inn, erid',
      competitor_cards:
        '++id, [nm_id+snapshot_ts], [query_id+snapshot_ts], snapshot_ts, query_id, nm_id, supplier_id, brand, subject_id',
      competitor_card_prices: '++id, [nm_id+snapshot_ts], [query_id+snapshot_ts], snapshot_ts, query_id, nm_id',
      competitor_card_details: '++id, [nm_id+snapshot_ts], [query_id+snapshot_ts], snapshot_ts, query_id, nm_id',
      competitor_card_stocks: '++id, [nm_id+snapshot_ts], [query_id+snapshot_ts], snapshot_ts, query_id, nm_id',
    });
    // v2: index the two new cartesian-axis dimensions on search_queries (material, purpose) so a
    // future report can filter "all queries with material=текстиль". `comment` is deliberately NOT
    // indexed — it's free-text, equality lookups on it are meaningless. Adding indexes to an
    // existing DB is the safest Dexie upgrade (auto-built from current rows, no upgrade() callback,
    // no data loss); the other 6 stores keep their v1 schema untouched.
    this.version(2).stores({
      search_queries: '++query_id, &query, subject, gender, season, age, material, purpose',
    });
    // v3: index the brand cartesian-axis dimension on search_queries (same safe index-only upgrade;
    // existing rows get brand='' auto-magically via the SearchQuery default — no data migration).
    this.version(3).stores({
      search_queries: '++query_id, &query, subject, brand, gender, season, age, material, purpose',
    });
    // v4: two new fact tables for competitor characteristics captured from the wbbasket.ru card.json
    // CDN file. Adding NEW stores in a new version is safe (existing stores untouched, new ones
    // created empty). Indexes mirror the other competitor_card_* tables (access by nm across
    // snapshots, by query within a snapshot); options additionally indexes char_name for "all
    // Состав values" lookups.
    this.version(4).stores({
      competitor_card_meta: '++id, [nm_id+snapshot_ts], [query_id+snapshot_ts], snapshot_ts, query_id, nm_id',
      competitor_card_options: '++id, [nm_id+snapshot_ts], [query_id+snapshot_ts], snapshot_ts, query_id, nm_id, char_name',
    });
    // v5: three more card.json EAV tables for full content capture (Этап A): materials composition,
    // size grid, color variants. Same safe add-new-stores upgrade (existing stores untouched).
    // Indexes mirror the other competitor_card_* tables; sizes additionally indexes tech_size +
    // prop_name ("all Рост values"), colors indexes color_nm_id.
    this.version(5).stores({
      competitor_card_compositions:
        '++id, [nm_id+snapshot_ts], [query_id+snapshot_ts], snapshot_ts, query_id, nm_id',
      competitor_card_sizes:
        '++id, [nm_id+snapshot_ts], [query_id+snapshot_ts], snapshot_ts, query_id, nm_id, tech_size, prop_name',
      competitor_card_colors:
        '++id, [nm_id+snapshot_ts], [query_id+snapshot_ts], snapshot_ts, query_id, nm_id, color_nm_id',
    });
  }
}

export const db = new PonchoDB();
