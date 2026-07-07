// src/reports/filters.ts — structured report-level filter applied to ALREADY-COMPUTED report rows
// before rendering (a drill-down on top of the full snapshot, NOT a collection/write filter).
//
// Layering with the existing per-table substring search: the dashboard renders each report with a
// free-text `textOf(name, brand, supplier)` filter inside a `fill(text)` closure (see reports.ts).
// This structured ReportFilter is applied ONCE to `report.rows` in buildAndRender, BEFORE the render
// closure runs — so the two filters compose (structured slice → then substring within the slice).
//
// Semantics (deliberate):
//   - The filter NEVER recomputes aggregates (HHI, medians, percentiles). It only hides rows from
//     the per-product table. UI surfaces this honestly ("фильтр прячет строки, не пересчитывает
//     агрегаты"). Aggregates always describe the FULL snapshot.
//   - If a filter criterion is SET but the row's matching field is absent (undefined/null), the row
//     is EXCLUDED — "I only want price 1000-2000" drops rows without a price. This is safer than the
//     opposite (silently keeping them), which would skew a manual count.
//   - Empty filter (all null/[]) = pass-through (no allocation effect in reports).
//
// Prices: user enters rubles in the UI; rows carry kopecks. Comparison multiplies the ruble bound
// by 100 (Math.round to dodge float artifacts) once per criterion.

/** Structured drill-down filter. `null`/`[]` = criterion inactive. */
export interface ReportFilter {
  price_min_rub: number | null; // inclusive, rubles
  price_max_rub: number | null; // inclusive, rubles
  brands_include: string[]; // lowercase substrings; row passes if brand contains ANY
  brands_exclude: string[]; // lowercase substrings; row passes if brand contains NONE
  rating_min: number | null; // inclusive
  feedbacks_min: number | null; // inclusive
  suppliers_include: number[]; // supplier_ids; row passes if its supplier_id is in the set
  suppliers_exclude: number[]; // row passes if its supplier_id is NOT in the set
}

export const DEFAULT_REPORT_FILTER: ReportFilter = {
  price_min_rub: null,
  price_max_rub: null,
  brands_include: [],
  brands_exclude: [],
  rating_min: null,
  feedbacks_min: null,
  suppliers_include: [],
  suppliers_exclude: [],
};

/** True when no criterion is active — callers can skip the filter pass entirely. */
export function isEmptyFilter(f: ReportFilter): boolean {
  return (
    f.price_min_rub == null &&
    f.price_max_rub == null &&
    f.brands_include.length === 0 &&
    f.brands_exclude.length === 0 &&
    f.rating_min == null &&
    f.feedbacks_min == null &&
    f.suppliers_include.length === 0 &&
    f.suppliers_exclude.length === 0
  );
}

/** The subset of row fields the filter can test. Each report supplies a per-row extractor. */
export interface FilterableRow {
  price_kop?: number | null;
  brand?: string;
  rating?: number;
  feedbacks?: number;
  supplier_id?: number | null;
}

/** Normalize a brand for case-insensitive substring matching. Empty/undefined → ''. */
function normBrand(s: string | undefined): string {
  return (s ?? '').toLowerCase();
}

/**
 * Apply the filter to already-computed rows. Returns a NEW array; does not mutate input.
 * Generic in T so each report keeps its row type — it only has to provide a `fields` extractor
 * mapping T → FilterableRow. Fields not relevant to a report simply stay undefined.
 */
export function applyFilter<T>(rows: T[], f: ReportFilter, fields: (r: T) => FilterableRow): T[] {
  if (isEmptyFilter(f)) return rows; // pass-through, no copy
  const minKop = f.price_min_rub != null ? Math.round(f.price_min_rub * 100) : null;
  const maxKop = f.price_max_rub != null ? Math.round(f.price_max_rub * 100) : null;
  const inc = f.brands_include.map((b) => b.toLowerCase());
  const exc = f.brands_exclude.map((b) => b.toLowerCase());
  const out: T[] = [];
  for (const r of rows) {
    const f0 = fields(r);
    // price band — inclusive on both ends; absent price fails an active band
    if (minKop != null || maxKop != null) {
      const p = f0.price_kop;
      if (p == null) continue;
      if (minKop != null && p < minKop) continue;
      if (maxKop != null && p > maxKop) continue;
    }
    // brand include: must contain at least one include-substring
    if (inc.length > 0) {
      const b = normBrand(f0.brand);
      if (!inc.some((s) => b.includes(s))) continue;
    }
    // brand exclude: must contain none of the exclude-substrings
    if (exc.length > 0) {
      const b = normBrand(f0.brand);
      if (exc.some((s) => b.includes(s))) continue;
    }
    // rating floor
    if (f.rating_min != null) {
      if (f0.rating == null || f0.rating < f.rating_min) continue;
    }
    // feedbacks floor
    if (f.feedbacks_min != null) {
      if (f0.feedbacks == null || f0.feedbacks < f.feedbacks_min) continue;
    }
    // supplier include/exclude
    if (f.suppliers_include.length > 0) {
      const sid = f0.supplier_id;
      if (sid == null || !f.suppliers_include.includes(sid)) continue;
    }
    if (f.suppliers_exclude.length > 0) {
      const sid = f0.supplier_id;
      if (sid != null && f.suppliers_exclude.includes(sid)) continue;
    }
    out.push(r);
  }
  return out;
}
