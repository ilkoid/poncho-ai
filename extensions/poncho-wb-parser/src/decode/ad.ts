// src/decode/ad.ts — decode vitrine banner ads. Port of decodeAd + adFromBanner + parseOrdMark +
// eridFromHref in pkg/wbscraper/decode.go.
//
// WB serves ads from two endpoints with different shapes (Stage 6.5):
//   1. banners-website v2/banners  → top-level ARRAY of banners (primary source).
//   2. __internal/banners/shelfs/search → {data:{banners:{data:[]}, shelfs:{data:[]}}}.
// decodeAd NEVER throws: an unrecognized shape yields 0 ads (defensive — a future third shape must
// not break the pipeline).

import type { Intercept, SnapshotTs, VitrineAd } from '../db/types';
import type { WBAdSlotResponse, WBBanner } from './shapes';

const RE_ORD_INN = /ИНН\s*(\d+)/;
const RE_ORD_ERID = /ЕРИД\s*([A-Za-z0-9]+)/;
const RE_ERID_PARAM = /[?&]erid=([A-Za-z0-9]+)/;
const INN_LABEL = 'ИНН';

/** decodeAd routes the body by shape (array first, then object with data.{banners,shelfs}). */
export function decodeAd(it: Intercept, snapshot: SnapshotTs): VitrineAd[] {
  // Shape 1: top-level array (banners-website v2/banners).
  if (Array.isArray(it.body)) {
    return adsFromBanners(it.body as WBBanner[], it.query_id, snapshot);
  }
  // Shape 2: object wrapper with data.{banners,shelfs} slots.
  if (it.body !== null && typeof it.body === 'object') {
    const resp = it.body as WBAdSlotResponse;
    const banners: WBBanner[] = [
      ...(resp.data?.banners?.data ?? []),
      ...(resp.data?.shelfs?.data ?? []),
    ];
    return adsFromBanners(banners, it.query_id, snapshot);
  }
  // Neither known shape matched — defensive: yield nothing, never error.
  return [];
}

function adsFromBanners(banners: WBBanner[], qid: number | null, snapshot: SnapshotTs): VitrineAd[] {
  return banners.map((b) => adFromBanner(b, qid, snapshot));
}

/** adFromBanner builds one VitrineAd. Identity comes from the ОРД marker; for internal WB promos
 *  (no marker) the promo/alt text is the name. PromoID is always null (v2/banners exposes none). */
function adFromBanner(b: WBBanner, qid: number | null, snapshot: SnapshotTs): VitrineAd {
  let { name, inn, erid } = parseOrdMark(b.ordBannerMark);
  if (erid === '') erid = eridFromHref(b.href ?? ''); // external ad hrefs carry ?erid=
  if (name === '') {
    // Internal WB promo (no ОРД marker): "Хозяйственные товары", "Wb Клуб", …
    name = b.promoText ?? '';
    if (name === '') name = b.alt ?? '';
  }
  return {
    snapshot_ts: snapshot,
    query_id: qid,
    advertiser_name: name,
    advertiser_inn: inn,
    erid,
    promo_id: null,
    banner_type: b.bannerType ?? '',
    creative_url: b.src ?? '',
    landing_href: b.href ?? '',
  };
}

/** parseOrdMark extracts advertiser name/INN/erid. v2/banners sends a string
 *  "NAME, ИНН <digits>, ЕРИД <token>"; an object form is also tolerated. */
export function parseOrdMark(raw: unknown): { name: string; inn: string; erid: string } {
  if (raw === null || raw === undefined) return { name: '', inn: '', erid: '' };
  // Object form (not observed in v2/banners, but tolerated).
  if (typeof raw === 'object') {
    const o = raw as { advertiserName?: unknown; advertiserInn?: unknown; erid?: unknown };
    const name = typeof o.advertiserName === 'string' ? o.advertiserName : '';
    const inn = typeof o.advertiserInn === 'string' ? o.advertiserInn : '';
    if (name !== '' || inn !== '') {
      const erid = typeof o.erid === 'string' ? o.erid : '';
      return { name, inn, erid };
    }
    return { name: '', inn: '', erid: '' };
  }
  if (typeof raw !== 'string') return { name: String(raw), inn: '', erid: '' };
  const str = raw;
  const innM = str.match(RE_ORD_INN);
  const eridM = str.match(RE_ORD_ERID);
  const inn = innM?.[1] ?? '';
  const erid = eridM?.[1] ?? '';
  // Name = everything before the "ИНН" marker, trimmed of surrounding commas/whitespace
  // (mirrors Go's strings.Trim(strings.TrimSpace(...), ",")).
  const upper = str.toUpperCase();
  const idx = upper.indexOf(INN_LABEL);
  const name = idx > 0 ? str.slice(0, idx).trim().replace(/^,+|,+$/g, '') : str.trim();
  return { name, inn, erid };
}

/** eridFromHref pulls the erid token from a landing href's ?erid= query param. */
export function eridFromHref(href: string): string {
  return href.match(RE_ERID_PARAM)?.[1] ?? '';
}
