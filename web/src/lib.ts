import { NO_VALUE } from "@warehouse/ui-kit";

/** "2026-10-17 09:30Z" for an RFC 3339 instant; the raw text when unparseable. */
export function utcLabel(iso: string | undefined): string {
  if (!iso) return NO_VALUE;
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return iso;
  const d = new Date(t).toISOString();
  return `${d.slice(0, 10)} ${d.slice(11, 16)}Z`;
}

/**
 * A strictly whole number typed into a field (zero allowed), or null when the
 * text is empty or not a whole number ("12.5", "1e3", "abc", "-1").
 */
export function wholeNumber(raw: string): number | null {
  const s = raw.trim();
  if (!/^\d+$/.test(s)) return null;
  const n = Number(s);
  return Number.isSafeInteger(n) ? n : null;
}

/** A strictly whole, positive number, or null ("0" is not positive). */
export function positiveWhole(raw: string): number | null {
  const n = wholeNumber(raw);
  return n !== null && n > 0 ? n : null;
}

/** An id of the API (site id, SKU): 1..64 characters and not blank
 *  (invalid-site-id / invalid-sku otherwise). */
export function idProblem(what: "site id" | "SKU", value: string): string | null {
  if (value.trim().length === 0) return `A ${what} is required.`;
  if (value.length > 64) return `A ${what} is at most 64 characters.`;
  return null;
}

/** The optional filter form of idProblem: empty means "no filter". */
export function optionalIdProblem(what: "site id" | "SKU", value: string): string | null {
  return value.length === 0 ? null : idProblem(what, value);
}

export interface PolicyDraft {
  minQty: string;
  maxQty: string;
  multiple: string;
}

export interface PolicyValues {
  minQty: number;
  maxQty: number;
  multiple: number;
}

/** The policy bounds rule of the API: 0 <= minQty < maxQty and multiple >= 1
 *  (invalid-policy otherwise). Returns the parsed values or a message. */
export function parsePolicy(d: PolicyDraft): { ok: true; values: PolicyValues } | { ok: false; message: string } {
  const minQty = wholeNumber(d.minQty);
  const maxQty = wholeNumber(d.maxQty);
  const multiple = d.multiple.trim() === "" ? 1 : positiveWhole(d.multiple);
  if (minQty === null) return { ok: false, message: "Minimum must be a whole number, 0 or more." };
  if (maxQty === null) return { ok: false, message: "Maximum must be a whole number." };
  if (multiple === null) return { ok: false, message: "Multiple must be a whole number, 1 or more." };
  if (minQty >= maxQty) return { ok: false, message: "Minimum must be below the maximum." };
  return { ok: true, values: { minQty, maxQty, multiple } };
}
