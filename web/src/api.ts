import { REPLENISHMENT_API_BASE } from "./config";
import type {
  DeclarePolicyBody,
  ListPickFacesQuery,
  ListPoliciesQuery,
  ListReplenishmentsQuery,
  PickFacePage,
  Policy,
  PolicyPage,
  Replenishment,
  ReplenishmentPage,
  RequestReplenishmentBody,
} from "./types";

/** RFC 7807 problem+json body every error response from pick-face-replenishment
 *  returns (`type` = https://errors.pick-face-replenishment.warehouse-systems.dev/<slug>). */
export interface ProblemDetails {
  type?: string;
  title?: string;
  status?: number;
  detail?: string;
  instance?: string;
}

/**
 * Every failed call -- an HTTP error with a problem+json body, an HTTP error
 * without one, or a network failure -- surfaces as one ApiError, so a screen
 * has a single shape to render: `title` (the problem's category) and `detail`
 * (what exactly was wrong), plus `slug` to branch on a specific problem
 * (e.g. `concurrent-modification`, `replenishment-not-open`).
 */
export class ApiError extends Error {
  readonly status: number;
  readonly problem: ProblemDetails | null;
  readonly title: string;
  readonly detail: string;
  /** Last path segment of problem.type (e.g. "policy-already-exists"); "" when unknown. */
  readonly slug: string;

  constructor(status: number, problem: ProblemDetails | null, fallbackTitle: string) {
    const title = problem?.title || fallbackTitle;
    const detail = problem?.detail ?? "";
    super(detail || title);
    this.name = "ApiError";
    this.status = status;
    this.problem = problem;
    this.title = title;
    this.detail = detail;
    this.slug = problem?.type ? (problem.type.split("/").pop() ?? "") : "";
  }
}

/** Normalizes anything thrown by a request into an ApiError. */
export function toApiError(err: unknown): ApiError {
  if (err instanceof ApiError) return err;
  const message = err instanceof Error ? err.message : String(err);
  return new ApiError(0, { detail: message }, "Network error");
}

/**
 * A fresh RFC 4122 v4 UUID for an Idempotency-Key header: minted once per
 * POST. `crypto.randomUUID` only exists in secure contexts, and the console
 * can be opened over plain http on a non-localhost host, so fall back to
 * getRandomValues.
 */
export function newIdempotencyKey(): string {
  if (typeof crypto.randomUUID === "function") return crypto.randomUUID();
  const b = crypto.getRandomValues(new Uint8Array(16));
  b[6] = (b[6] & 0x0f) | 0x40;
  b[8] = (b[8] & 0x3f) | 0x80;
  const h = Array.from(b, (x) => x.toString(16).padStart(2, "0")).join("");
  return `${h.slice(0, 8)}-${h.slice(8, 12)}-${h.slice(12, 16)}-${h.slice(16, 20)}-${h.slice(20)}`;
}

type Query = Record<string, string | number | boolean | undefined>;

function withQuery(path: string, query?: Query): string {
  if (!query) return path;
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(query)) {
    if (value !== undefined && value !== "") params.set(key, String(value));
  }
  const qs = params.toString();
  return qs ? `${path}?${qs}` : path;
}

interface RequestOptions {
  body?: unknown;
  /** The policy version a PUT replaces (sent as If-Match). */
  ifMatch?: number;
}

/**
 * Every POST (a manual request and a cancel, both of which change state and
 * REQUIRE the key in every deployed mode) carries its own `Idempotency-Key`, a
 * UUID minted for that request. A PUT that replaces a policy carries `If-Match`
 * with the version the form was loaded at. GETs send no custom header, so they
 * stay simple CORS requests.
 */
async function request<T>(method: "GET" | "POST" | "PUT", path: string, opts: RequestOptions = {}): Promise<T> {
  const headers: Record<string, string> = {};
  if (opts.body !== undefined) headers["Content-Type"] = "application/json";
  if (method === "POST") headers["Idempotency-Key"] = newIdempotencyKey();
  if (opts.ifMatch !== undefined) headers["If-Match"] = String(opts.ifMatch);
  let res: Response;
  try {
    res = await fetch(`${REPLENISHMENT_API_BASE}${path}`, {
      method,
      ...(Object.keys(headers).length > 0 ? { headers } : {}),
      ...(opts.body === undefined ? {} : { body: JSON.stringify(opts.body) }),
    });
  } catch (err) {
    throw toApiError(err);
  }
  if (!res.ok) {
    let problem: ProblemDetails | null = null;
    try {
      problem = (await res.json()) as ProblemDetails;
    } catch {
      // non-JSON error body -- fall through with problem = null
    }
    throw new ApiError(res.status, problem, `${res.status} ${res.statusText}`.trim());
  }
  return (await res.json()) as T;
}

const seg = encodeURIComponent;

/** GET /policies?limit=&cursor=&siteId=&sku= */
export async function listPolicies(q: ListPoliciesQuery): Promise<PolicyPage> {
  const res = await request<PolicyPage>(
    "GET",
    withQuery("/policies", { limit: q.limit, cursor: q.cursor, siteId: q.siteId, sku: q.sku }),
  );
  return { items: res.items ?? [], nextCursor: res.nextCursor || undefined };
}

/** GET /policies/{siteId}/{sku} (404 policy-not-found). */
export function getPolicy(siteId: string, sku: string): Promise<Policy> {
  return request<Policy>("GET", `/policies/${seg(siteId)}/${seg(sku)}`);
}

/**
 * PUT /policies/{siteId}/{sku}. Without `ifMatch` it creates the policy (409
 * policy-already-exists when one exists); with it, it replaces the policy only
 * when that is the current version (409 concurrent-modification otherwise).
 */
export function declarePolicy(siteId: string, sku: string, body: DeclarePolicyBody, ifMatch?: number): Promise<Policy> {
  return request<Policy>("PUT", `/policies/${seg(siteId)}/${seg(sku)}`, { body, ifMatch });
}

/** GET /pick-faces?siteId=&status=&limit=&cursor= (siteId is required). */
export async function listPickFaces(q: ListPickFacesQuery): Promise<PickFacePage> {
  const res = await request<PickFacePage>(
    "GET",
    withQuery("/pick-faces", { siteId: q.siteId, status: q.status, limit: q.limit, cursor: q.cursor }),
  );
  return { items: res.items ?? [], nextCursor: res.nextCursor || undefined };
}

/** GET /replenishments?limit=&cursor=&siteId=&sku=&state= */
export async function listReplenishments(q: ListReplenishmentsQuery): Promise<ReplenishmentPage> {
  const res = await request<ReplenishmentPage>(
    "GET",
    withQuery("/replenishments", { limit: q.limit, cursor: q.cursor, siteId: q.siteId, sku: q.sku, state: q.state }),
  );
  return { items: res.items ?? [], nextCursor: res.nextCursor || undefined };
}

/** GET /replenishments/{requestId} (404 replenishment-not-found). */
export function getReplenishment(requestId: string): Promise<Replenishment> {
  return request<Replenishment>("GET", `/replenishments/${seg(requestId)}`);
}

/** POST /replenishments: a manual request (201). 422 no-forward-assignment /
 *  no-policy / no-source-stock / nothing-to-replenish / quantity-exceeds-max,
 *  409 replenishment-already-open. */
export function requestReplenishment(body: RequestReplenishmentBody): Promise<Replenishment> {
  return request<Replenishment>("POST", "/replenishments", { body });
}

/** POST /replenishments/{requestId}/cancellations (409 replenishment-not-open). */
export function cancelReplenishment(requestId: string): Promise<Replenishment> {
  return request<Replenishment>("POST", `/replenishments/${seg(requestId)}/cancellations`);
}
