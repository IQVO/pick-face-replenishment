import { describe, expect, it } from "vitest";
import {
  ApiError,
  cancelReplenishment,
  declarePolicy,
  listPickFaces,
  listPolicies,
  listReplenishments,
  newIdempotencyKey,
  requestReplenishment,
  toApiError,
} from "./api";
import { json, mockApi, problem } from "./test/fetchMock";
import { policy, replenishment } from "./test/fixtures";

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

describe("api", () => {
  it("sends only non-empty query parameters and no custom header on a GET", async () => {
    const api = mockApi({
      "GET /replenishments": json({ items: [replenishment()] }),
      "GET /pick-faces": json({ items: [] }),
      "GET /policies": json({ items: [policy()], nextCursor: "c2" }),
    });
    await listReplenishments({ limit: 25, siteId: "SITE-1", sku: "", state: "Requested" });
    const [call] = api.to("GET /replenishments");
    expect(call.query.get("siteId")).toBe("SITE-1");
    expect(call.query.get("state")).toBe("Requested");
    expect(call.query.has("sku")).toBe(false);
    expect(call.headers).toEqual({});

    await listPickFaces({ siteId: "SITE-1", status: "BELOW_MIN" });
    expect(api.to("GET /pick-faces")[0].query.get("status")).toBe("BELOW_MIN");

    const page = await listPolicies({});
    expect(page.nextCursor).toBe("c2");
  });

  it("mints a fresh Idempotency-Key per POST", async () => {
    const api = mockApi({
      "POST /replenishments": json(replenishment(), 201),
      "POST /replenishments/repl-1/cancel": json(replenishment({ state: "Cancelled" })),
    });
    await requestReplenishment({ siteId: "SITE-1", sku: "SKU-1" });
    await requestReplenishment({ siteId: "SITE-1", sku: "SKU-1", quantity: 6 });
    await cancelReplenishment("repl-1");
    const keys = [...api.to("POST /replenishments"), ...api.to("POST /replenishments/repl-1/cancel")].map(
      (c) => c.headers["Idempotency-Key"],
    );
    expect(keys).toHaveLength(3);
    for (const k of keys) expect(k).toMatch(UUID);
    expect(new Set(keys).size).toBe(3);
    expect(api.to("POST /replenishments")[1].body).toEqual({ siteId: "SITE-1", sku: "SKU-1", quantity: 6 });
  });

  it("sends If-Match only when replacing a policy", async () => {
    const api = mockApi({ "PUT /policies/SITE-1/SKU%201": json(policy()) });
    await declarePolicy("SITE-1", "SKU 1", { minQty: 1, maxQty: 5 });
    await declarePolicy("SITE-1", "SKU 1", { minQty: 1, maxQty: 5 }, 3);
    const [create, replace] = api.calls;
    expect(create.headers["If-Match"]).toBeUndefined();
    expect(replace.headers["If-Match"]).toBe("3");
    expect(replace.headers["Content-Type"]).toBe("application/json");
    expect(replace.headers["Idempotency-Key"]).toBeUndefined();
  });

  it("surfaces a problem+json body as an ApiError with its slug", async () => {
    mockApi({ "POST /replenishments": problem(422, "no-policy", "No policy", "SITE-1/SKU-1 has no policy") });
    const err = await requestReplenishment({ siteId: "SITE-1", sku: "SKU-1" }).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    const e = err as ApiError;
    expect([e.status, e.slug, e.title, e.detail]).toEqual([422, "no-policy", "No policy", "SITE-1/SKU-1 has no policy"]);
  });

  it("surfaces a non-JSON error and a network failure as ApiErrors", async () => {
    mockApi({ "GET /policies": new Response("<html>bad gateway</html>", { status: 502, statusText: "Bad Gateway" }) });
    const e1 = await listPolicies({}).then(() => { throw new Error("expected a rejection"); }, (e: unknown) => e as ApiError);
    expect([e1.status, e1.title, e1.slug]).toEqual([502, "502 Bad Gateway", ""]);
    mockApi({});
    const e2 = await listPolicies({}).then(() => { throw new Error("expected a rejection"); }, (e: unknown) => e as ApiError);
    expect([e2.status, e2.title]).toEqual([0, "Network error"]);
    expect(toApiError("boom").detail).toBe("boom");
  });

  it("newIdempotencyKey is a v4 UUID", () => {
    expect(newIdempotencyKey()).toMatch(UUID);
  });
});
