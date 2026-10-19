import type { PickFace, Policy, Replenishment } from "../types";

export const REQUEST_ID = "repl-3b0e8d2a-7c41-4f6a-9d15-2a8b6c4e1f07";
export const OTHER_REQUEST_ID = "repl-1a2b3c4d-5e6f-4a5b-8c9d-0e1f2a3b4c5d";

export function policy(over: Partial<Policy> = {}): Policy {
  return {
    siteId: "SITE-1",
    sku: "SKU-1",
    minQty: 10,
    maxQty: 60,
    multiple: 6,
    version: 2,
    declaredAt: "2026-10-17T09:30:00Z",
    ...over,
  };
}

export function pickFace(over: Partial<PickFace> = {}): PickFace {
  return {
    siteId: "SITE-1",
    forwardBin: "FWD-1",
    sku: "SKU-1",
    onHand: 4,
    reserved: 1,
    available: 3,
    minQty: 10,
    maxQty: 60,
    status: "BELOW_MIN",
    ...over,
  };
}

export function replenishment(over: Partial<Replenishment> = {}): Replenishment {
  return {
    requestId: REQUEST_ID,
    siteId: "SITE-1",
    sku: "SKU-1",
    forwardBin: "FWD-1",
    sourceBin: "RES-1",
    quantity: 54,
    priority: "NORMAL",
    state: "Requested",
    policyMin: 10,
    policyMax: 60,
    requestedAt: "2026-10-17T10:00:00Z",
    version: 1,
    ...over,
  };
}
