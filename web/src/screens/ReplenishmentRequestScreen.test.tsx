import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { ReplenishmentRequestScreen } from "./ReplenishmentRequestScreen";
import { json, mockApi, problem } from "../test/fetchMock";
import { REQUEST_ID, replenishment } from "../test/fixtures";

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

function mount(path = "/replenishments/new") {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/">
          <Route path="replenishments/new" element={<ReplenishmentRequestScreen />} />
          <Route path="replenishments/:id" element={<p>landed on a request</p>} />
        </Route>
      </Routes>
    </MemoryRouter>,
  );
}

describe("ReplenishmentRequestScreen", () => {
  it("prefills site and SKU from the board link, posts with an Idempotency-Key and opens the request", async () => {
    const api = mockApi({ "POST /replenishments": json(replenishment(), 201) });
    mount("/replenishments/new?siteId=SITE-1&sku=SKU-1");
    expect(screen.getByLabelText(/Site/)).toHaveValue("SITE-1");
    expect(screen.getByLabelText(/SKU/)).toHaveValue("SKU-1");
    await userEvent.click(screen.getByRole("button", { name: "Request replenishment" }));
    expect(await screen.findByText("landed on a request")).toBeInTheDocument();
    const [post] = api.to("POST /replenishments");
    expect(post.body).toEqual({ siteId: "SITE-1", sku: "SKU-1" });
    expect(post.headers["Idempotency-Key"]).toMatch(UUID);
    expect(REQUEST_ID).toBeTruthy();
  });

  it("sends an explicit quantity and rejects a bad one without calling", async () => {
    const api = mockApi({ "POST /replenishments": json(replenishment(), 201) });
    mount("/replenishments/new?siteId=SITE-1&sku=SKU-1");
    await userEvent.type(screen.getByLabelText(/Quantity/), "0");
    await userEvent.click(screen.getByRole("button", { name: "Request replenishment" }));
    expect(screen.getByText("Quantity must be a whole number, 1 or more.")).toBeInTheDocument();
    expect(api.calls).toHaveLength(0);
    await userEvent.clear(screen.getByLabelText(/Quantity/));
    await userEvent.type(screen.getByLabelText(/Quantity/), "12");
    await userEvent.click(screen.getByRole("button", { name: "Request replenishment" }));
    await screen.findByText("landed on a request");
    expect(api.to("POST /replenishments")[0].body).toEqual({ siteId: "SITE-1", sku: "SKU-1", quantity: 12 });
  });

  it("requires a site and SKU", async () => {
    const api = mockApi({});
    mount();
    await userEvent.click(screen.getByRole("button", { name: "Request replenishment" }));
    expect(screen.getByText("A site id is required.")).toBeInTheDocument();
    expect(api.calls).toHaveLength(0);
  });

  it.each([
    ["no-forward-assignment", /no forward pick face/],
    ["no-policy", /Declare a replenishment policy/],
    ["no-source-stock", /No reserve bin/],
    ["nothing-to-replenish", /above its minimum/],
    ["quantity-exceeds-max", /policy maximum/],
    ["replenishment-already-open", /already open/],
  ])("explains a %s refusal", async (slug, lead) => {
    mockApi({ "POST /replenishments": problem(slug === "replenishment-already-open" ? 409 : 422, slug, "Refused", "server detail") });
    mount("/replenishments/new?siteId=SITE-1&sku=SKU-1");
    await userEvent.click(screen.getByRole("button", { name: "Request replenishment" }));
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent(lead);
    expect(alert).toHaveTextContent("server detail");
  });
});
