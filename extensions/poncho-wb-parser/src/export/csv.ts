// src/export/csv.ts — CSV serialization with a UTF-8 BOM (so Excel reads Cyrillic without
// mangling) + a download helper via chrome.downloads.

import type { ExportTable } from './types';

/** Quote a CSV cell per RFC 4180: wrap in quotes if it contains comma/quote/newline, double inner quotes. */
function escapeCell(c: string | number): string {
  const s = String(c);
  return /[",\n]/.test(s) ? '"' + s.replace(/"/g, '""') + '"' : s;
}

/** Serialize one table to CSV text (comma-delimited, UTF-8 BOM prepended). */
export function toCSV(t: ExportTable): string {
  const lines = [t.columns.map((c) => escapeCell(c)).join(','), ...t.rows.map((r) => r.map((c) => escapeCell(c ?? '')).join(','))];
  return '﻿' + lines.join('\n'); // BOM so Excel decodes UTF-8
}

/** Trigger a browser download of `content` as `filename`. */
export function downloadText(filename: string, content: string, mime: string): void {
  const blob = new Blob([content], { type: mime });
  const url = URL.createObjectURL(blob);
  void chrome.downloads
    .download({ url, filename, saveAs: false })
    .then(() => URL.revokeObjectURL(url))
    .catch(() => URL.revokeObjectURL(url));
}

/** Download the FIRST table of a report as CSV (the primary/summary table). */
export function downloadCSV(filenameBase: string, tables: ExportTable[]): void {
  if (tables.length === 0) return;
  downloadText(`${filenameBase}.csv`, toCSV(tables[0]!), 'text/csv;charset=utf-8');
}
