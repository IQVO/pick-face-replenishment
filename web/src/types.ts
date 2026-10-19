/**
 * Wire shapes of pick-face-replenishment's REST API (apis/openapi.yaml; the
 * JSON field names are the truth in internal/adapters/inbound/http/dto.go).
 * JSON is camelCase; optional parts of a resource are OMITTED, never null.
 */

export const REQUEST_STATES = ["Requested", "Completed", "Cancelled"] as const;
export type RequestState = (typeof REQUEST_STATES)[number];

export const PICK_FACE_STATUSES = ["BELOW_MIN", "OK", "NO_SOURCE", "NO_POLICY"] as const;
export type PickFaceStatus = (typeof PICK_FACE_STATUSES)[number];

export const PRIORITIES = ["NORMAL", "URGENT"] as const;
export type Priority = (typeof PRIORITIES)[number];

export const CANCEL_REASONS = ["OPERATOR", "ASSIGNMENT_CHANGED", "POLICY_REMOVED"] as const;
export type CancelReason = (typeof CANCEL_REASONS)[number];

export interface Policy {
  siteId: string;
  sku: string;
  minQty: number;
  maxQty: number;
  multiple: number;
  /** The If-Match value of the next change. */
  version: number;
  declaredAt: string;
}

export interface PolicyPage {
  items: Policy[];
  /** Absent on the last page. */
  nextCursor?: string;
}

/** The body of PUT /policies/{siteId}/{sku}. */
export interface DeclarePolicyBody {
  minQty: number;
  maxQty: number;
  /** Omitted = the service's default of 1. */
  multiple?: number;
}

export interface PickFace {
  siteId: string;
  forwardBin: string;
  sku: string;
  onHand: number;
  reserved: number;
  available: number;
  /** Absent without a policy. */
  minQty?: number;
  maxQty?: number;
  /** Absent without an open request. */
  openRequestId?: string;
  status: PickFaceStatus;
}

export interface PickFacePage {
  items: PickFace[];
  nextCursor?: string;
}

export interface Replenishment {
  requestId: string;
  siteId: string;
  sku: string;
  forwardBin: string;
  sourceBin: string;
  quantity: number;
  /** Absent until the request is completed. */
  movedQuantity?: number;
  priority: Priority;
  state: RequestState;
  policyMin: number;
  policyMax: number;
  requestedAt: string;
  completedAt?: string;
  cancelledAt?: string;
  cancelReason?: CancelReason;
  version: number;
}

export interface ReplenishmentPage {
  items: Replenishment[];
  nextCursor?: string;
}

/** The body of POST /replenishments. `quantity` omitted = the detector's size. */
export interface RequestReplenishmentBody {
  siteId: string;
  sku: string;
  quantity?: number;
}

export interface ListPoliciesQuery {
  limit?: number;
  cursor?: string;
  siteId?: string;
  sku?: string;
}

export interface ListPickFacesQuery {
  /** Required by the API. */
  siteId: string;
  status?: PickFaceStatus;
  limit?: number;
  cursor?: string;
}

export interface ListReplenishmentsQuery {
  limit?: number;
  cursor?: string;
  siteId?: string;
  sku?: string;
  state?: RequestState;
}
