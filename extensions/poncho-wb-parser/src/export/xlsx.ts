// src/export/xlsx.ts — xlsx export via SheetJS, dynamically imported so the ~400KB library is split
// into its own chunk and loaded only on the first export click (keeps the dashboard bundle small).
//
// Sheet name rules: Excel limits sheet names to 31 chars and forbids : \ / ? * [ ]. We sanitize.

import type { ExportTable } from './types';

const SHEET_MAX = 31;
function sanitizeSheetName(name: string): string {
  const clean = name.replace(/[:\\/?*[\]]/g, ' ').trim().slice(0, SHEET_MAX);
  return clean === '' ? 'Sheet' : clean;
}

/** Build an xlsx workbook from one or more tables (one sheet per table) and download it. */
export async function downloadXLSX(filenameBase: string, tables: ExportTable[]): Promise<void> {
  if (tables.length === 0) return;
  // Dynamic import → Vite code-splits xlsx into a separate chunk (not in the initial bundle).
  const XLSX = await import('xlsx');
  const wb = XLSX.utils.book_new();
  const used = new Set<string>();
  for (const t of tables) {
    let name = sanitizeSheetName(t.name);
    let n = 1;
    while (used.has(name)) {
      const suffix = ` (${n++})`;
      name = sanitizeSheetName(t.name).slice(0, SHEET_MAX - suffix.length) + suffix;
    }
    used.add(name);
    const ws = XLSX.utils.aoa_to_sheet([t.columns, ...t.rows]);
    XLSX.utils.book_append_sheet(wb, ws, name);
  }
  const out = XLSX.write(wb, { type: 'array', bookType: 'xlsx' }) as ArrayBuffer;
  const blob = new Blob([out], { type: 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet' });
  const url = URL.createObjectURL(blob);
  try {
    await chrome.downloads.download({ url, filename: `${filenameBase}.xlsx`, saveAs: false });
  } finally {
    URL.revokeObjectURL(url);
  }
}
