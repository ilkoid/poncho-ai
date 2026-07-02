// src/decode/helpers.ts — defensive JSON readers. Ports the Go helpers in decode.go, adapted to
// TypeScript where the WB response is already a parsed value (not json.RawMessage): each helper
// accepts `unknown` and narrows, defaulting to a safe empty value rather than throwing.

/** asObject validates that a decoded body is a non-null object record; throws otherwise.
 *  Preserves the Go semantics where a malformed search/card body returns an error (not a silent
 *  empty result) so the caller can log the bad capture. Decode routes unknown kinds to the default
 *  BEFORE calling this, so an unknown-but-valid `{}` body still yields an empty result (no throw). */
export function asObject(body: unknown, label: string): Record<string, unknown> {
  if (body === null || typeof body !== 'object' || Array.isArray(body)) {
    throw new Error(`decode ${label} body: expected JSON object, got ${body === null ? 'null' : typeof body}`);
  }
  return body as Record<string, unknown>;
}

/** picsCount interprets a "pics" field that may be a count number or a URL array. */
export function picsCount(v: unknown): number {
  if (typeof v === 'number') return v;
  if (Array.isArray(v)) return v.length;
  return 0;
}

/** joinColors joins color names from an array of {name} objects or plain strings. */
export function joinColors(v: unknown): string {
  if (!Array.isArray(v)) return '';
  const names: string[] = [];
  for (const c of v) {
    if (typeof c === 'string') {
      if (c !== '') names.push(c);
    } else if (c !== null && typeof c === 'object') {
      const name = (c as { name?: unknown }).name;
      if (typeof name === 'string' && name !== '') names.push(name);
    }
  }
  return names.join(', ');
}

/** rawJSONOrEmpty re-serializes an arbitrary JSON value to text; null/undefined → "". Used for the
 *  promotions blob (variable-shape array). Differs from Go only in that it re-serializes compactly
 *  rather than preserving verbatim bytes — the blob is opaque and only checked for non-emptiness. */
export function rawJSONOrEmpty(v: unknown): string {
  if (v === null || v === undefined) return '';
  return JSON.stringify(v);
}
