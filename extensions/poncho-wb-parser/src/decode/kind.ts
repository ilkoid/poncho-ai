// src/decode/kind.ts — URL → capture-kind classifier. Direct port of COLLECT_PATTERNS in
// extensions/wb-scraper/src/background.js (verified against wb-recon-*.json, 2026-06-28).
//
// WB serves storefront APIs under www.wildberries.ru/__internal/... (same-origin proxy), NOT
// card.wb.ru / search.wb.ru. Versions are versioned (v4 cards, v18 search) and drift, so we match
// /v\d+/. Re-verify after WB layout changes. Order matters: card_detail before card_list is safe
// (distinct paths); the first matching pattern wins.

export type CaptureKind = 'search' | 'brand' | 'card_list' | 'card_detail' | 'ad';

const COLLECT_PATTERNS: { kind: CaptureKind; re: RegExp }[] = [
  { kind: 'card_detail', re: /\/__internal\/card\/cards\/v\d+\/detail\b/i }, // ONE card: chars/dimensions/per-wh qty
  { kind: 'card_list', re: /\/__internal\/card\/cards\/v\d+\/list\b/i }, // batch hydration (search shape, no chars)
  { kind: 'search', re: /\/__internal\/search\/exactmatch\/[^/]+\/common\/v\d+\/search\b/i }, // positions
  { kind: 'ad', re: /(\/__internal\/banners\/shelfs\/search|banners-website\.wildberries\.ru)/i }, // banners/erid
  { kind: 'brand', re: /\/__internal\/catalog\/brands\/v\d+\/(catalog|filters)\b/i }, // brand page
];

/** classify returns the first matching capture kind for a WB URL, or null if it is not a storefront
 *  endpoint we collect (then the SW skips the intercept). */
export function classify(url: string): CaptureKind | null {
  for (const p of COLLECT_PATTERNS) {
    if (p.re.test(url)) return p.kind;
  }
  return null;
}
