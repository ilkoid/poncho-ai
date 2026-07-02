// src/export/types.ts — the shared tabular shape both CSV and xlsx exporters consume. A report
// renders to one or more ExportTables (xlsx → one sheet each; csv → the first/primary table).

export type Cell = string | number | null;

export interface ExportTable {
  /** Sheet/tab name (xlsx: <=31 chars, sanitized); also a filename hint for csv. */
  name: string;
  columns: string[];
  rows: Cell[][];
}
