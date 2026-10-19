import { describe, expect, it } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { ReplenishmentListScreen } from "./ReplenishmentListScreen";
import { json, mockApi } from "../test/fetchMock";
import { OTHER_REQUEST_ID, REQUEST_ID, replenishment } from "../test/fixtures";

describe("ReplenishmentListScreen", () => {
  it("lists requests with state, priority and route, and filters by state", async () => {
    const api = mockApi({
      "GET /replenishments": (call) =>
        json({
          items:
            call.query.get("state") === "Completed"
              ? [replenishment({ requestId: OTHER_REQUEST_ID, state: "Completed", movedQuantity: 54 })]
              : [replenishment(), replenishment({ requestId: OTHER_REQUEST_ID, state: "Cancelled", priority: "URGENT" })],
        }),
    });
    render(
      <MemoryRouter>
        <ReplenishmentListScreen />
      </MemoryRouter>,
    );
    expect(await screen.findByRole("link", { name: REQUEST_ID })).toHaveAttribute("href", `/replenishments/${REQUEST_ID}`);
    expect(screen.getAllByText("RES-1 → FWD-1")).toHaveLength(2);
    expect(screen.getByText("URGENT")).toBeInTheDocument();
    expect(api.to("GET /replenishments")[0].query.has("state")).toBe(false);

    await userEvent.selectOptions(screen.getByLabelText("State"), "Completed");
    await userEvent.type(screen.getByLabelText("SKU"), "SKU-1");
    await userEvent.click(screen.getByRole("button", { name: "Apply filters" }));
    await screen.findByRole("link", { name: OTHER_REQUEST_ID });
    await waitFor(() => expect(screen.queryByText("URGENT")).not.toBeInTheDocument());
    expect(screen.queryByRole("link", { name: REQUEST_ID })).not.toBeInTheDocument();
    const last = api.to("GET /replenishments").at(-1);
    expect([last?.query.get("state"), last?.query.get("sku")]).toEqual(["Completed", "SKU-1"]);
  });

  it("shows an empty note and the problem on failure", async () => {
    mockApi({ "GET /replenishments": json({ items: [] }) });
    render(
      <MemoryRouter>
        <ReplenishmentListScreen />
      </MemoryRouter>,
    );
    expect(await screen.findByText("No replenishments match these filters.")).toBeInTheDocument();
  });
});
