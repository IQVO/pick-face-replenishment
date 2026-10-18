import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { PolicyListScreen } from "./PolicyListScreen";
import { json, mockApi } from "../test/fetchMock";
import { policy } from "../test/fixtures";

describe("PolicyListScreen", () => {
  it("lists policies with edit links and filters by site and SKU", async () => {
    const api = mockApi({ "GET /policies": json({ items: [policy(), policy({ sku: "SKU 2", minQty: 0 })] }) });
    render(
      <MemoryRouter>
        <PolicyListScreen />
      </MemoryRouter>,
    );
    expect(await screen.findByRole("link", { name: "Edit policy SITE-1 SKU-1" })).toHaveAttribute("href", "/policies/SITE-1/SKU-1");
    expect(screen.getByRole("link", { name: "Edit policy SITE-1 SKU 2" })).toHaveAttribute("href", "/policies/SITE-1/SKU%202");
    expect(screen.getByRole("link", { name: "Declare policy" })).toHaveAttribute("href", "/policies/new");
    expect(screen.getAllByText("2026-10-17 09:30Z", { selector: "td" })).toHaveLength(2);

    await userEvent.type(screen.getByLabelText("Site"), "SITE-1");
    await userEvent.click(screen.getByRole("button", { name: "Apply filters" }));
    await screen.findByRole("link", { name: "Edit policy SITE-1 SKU-1" });
    expect(api.to("GET /policies").at(-1)?.query.get("siteId")).toBe("SITE-1");
  });

  it("disables the filter on an over-long id", async () => {
    mockApi({ "GET /policies": json({ items: [] }) });
    render(
      <MemoryRouter>
        <PolicyListScreen />
      </MemoryRouter>,
    );
    await screen.findByText("No policies match these filters.");
    await userEvent.type(screen.getByLabelText("SKU"), "x".repeat(65));
    expect(screen.getByRole("button", { name: "Apply filters" })).toBeDisabled();
  });
});
