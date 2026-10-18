import { describe, expect, it } from "vitest";
import { idProblem, optionalIdProblem, parsePolicy, positiveWhole, utcLabel, wholeNumber } from "./lib";

describe("lib", () => {
  it("formats instants in UTC", () => {
    expect(utcLabel("2026-10-17T09:30:45Z")).toBe("2026-10-17 09:30Z");
    expect(utcLabel("not a date")).toBe("not a date");
    expect(utcLabel(undefined)).toBe("—");
  });

  it("accepts only strictly whole numbers", () => {
    expect(wholeNumber("12")).toBe(12);
    expect(wholeNumber(" 0 ")).toBe(0);
    for (const bad of ["", "12.5", "1e3", "abc", "-1", "99999999999999999999"]) expect(wholeNumber(bad)).toBeNull();
    expect(positiveWhole("0")).toBeNull();
    expect(positiveWhole("7")).toBe(7);
  });

  it("validates ids like the API (1..64, not blank)", () => {
    expect(idProblem("SKU", "")).toMatch(/required/);
    expect(idProblem("SKU", "   ")).toMatch(/required/);
    expect(idProblem("site id", "x".repeat(65))).toMatch(/64/);
    expect(idProblem("SKU", "SKU-1")).toBeNull();
    expect(optionalIdProblem("SKU", "")).toBeNull();
  });

  it("applies the policy bounds rule", () => {
    expect(parsePolicy({ minQty: "10", maxQty: "60", multiple: "" })).toEqual({
      ok: true,
      values: { minQty: 10, maxQty: 60, multiple: 1 },
    });
    expect(parsePolicy({ minQty: "0", maxQty: "1", multiple: "6" }).ok).toBe(true);
    for (const d of [
      { minQty: "", maxQty: "60", multiple: "" },
      { minQty: "10", maxQty: "x", multiple: "" },
      { minQty: "10", maxQty: "60", multiple: "0" },
      { minQty: "60", maxQty: "60", multiple: "" },
      { minQty: "61", maxQty: "60", multiple: "" },
    ]) {
      expect(parsePolicy(d).ok).toBe(false);
    }
  });
});
