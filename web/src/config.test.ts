import { describe, expect, it } from "vitest";
import { REPLENISHMENT_API_BASE, resolveReplenishmentApiBase } from "./config";

describe("resolveReplenishmentApiBase", () => {
  it("builds the production API base from the runtime API origin", () => {
    expect(resolveReplenishmentApiBase({ apiOrigin: "http://localhost:8000" }, true)).toBe(
      "http://localhost:8000/api/pick-face-replenishment",
    );
  });

  it("normalizes trailing slashes on the runtime API origin", () => {
    expect(resolveReplenishmentApiBase({ apiOrigin: "https://warehouse.example/" }, true)).toBe(
      "https://warehouse.example/api/pick-face-replenishment",
    );
    expect(resolveReplenishmentApiBase({ apiOrigin: "https://warehouse.example///" }, true)).toBe(
      "https://warehouse.example/api/pick-face-replenishment",
    );
  });

  it("fails loudly when production runtime configuration has no API origin", () => {
    expect(() => resolveReplenishmentApiBase({}, true)).toThrow(
      "window.__WAREHOUSE_CONFIG__.apiOrigin is required in production",
    );
    expect(() => resolveReplenishmentApiBase({ apiOrigin: "" }, true)).toThrow(
      "window.__WAREHOUSE_CONFIG__.apiOrigin is required in production",
    );
  });

  it("falls back to the service's own dev port outside production", () => {
    expect(resolveReplenishmentApiBase({}, false)).toBe("http://localhost:8080");
  });

  it("still prefers a runtime origin over the dev fallback", () => {
    expect(resolveReplenishmentApiBase({ apiOrigin: "http://localhost:8000" }, false)).toBe(
      "http://localhost:8000/api/pick-face-replenishment",
    );
  });
});

describe("REPLENISHMENT_API_BASE", () => {
  it("loads without /config.json (window.__WAREHOUSE_CONFIG__ absent) in a non-production build", () => {
    expect(window.__WAREHOUSE_CONFIG__).toBeUndefined();
    expect(REPLENISHMENT_API_BASE).toBe("http://localhost:8080");
  });
});
