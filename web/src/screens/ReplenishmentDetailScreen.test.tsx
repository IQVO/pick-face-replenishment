import { describe, expect, it } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { ReplenishmentDetailRoute } from "./ReplenishmentDetailScreen";
import { json, mockApi, problem } from "../test/fetchMock";
import { REQUEST_ID, replenishment } from "../test/fixtures";

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

function mount() {
  return render(
    <MemoryRouter initialEntries={[`/replenishments/${REQUEST_ID}`]}>
      <Routes>
        <Route path="/">
          <Route path="replenishments/:requestId" element={<ReplenishmentDetailRoute />} />
        </Route>
      </Routes>
    </MemoryRouter>,
  );
}

describe("ReplenishmentDetailScreen", () => {
  it("shows a completed request with its outcome and no cancel", async () => {
    mockApi({
      [`GET /replenishments/${REQUEST_ID}`]: json(
        replenishment({ state: "Completed", movedQuantity: 54, completedAt: "2026-10-17T11:00:00Z" }),
      ),
    });
    mount();
    expect(await screen.findByRole("heading", { name: REQUEST_ID })).toBeInTheDocument();
    expect(screen.getByText("2026-10-17 11:00Z")).toBeInTheDocument();
    expect(screen.getByText("min 10 · max 60")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Cancel request/ })).not.toBeInTheDocument();
  });

  it("cancels an open request after a confirmation, with an Idempotency-Key, and reloads", async () => {
    let state: "Requested" | "Cancelled" = "Requested";
    const api = mockApi({
      [`GET /replenishments/${REQUEST_ID}`]: () =>
        json(replenishment({ state, ...(state === "Cancelled" ? { cancelReason: "OPERATOR" as const } : {}) })),
      [`POST /replenishments/${REQUEST_ID}/cancel`]: () => {
        state = "Cancelled";
        return json(replenishment({ state: "Cancelled" }));
      },
    });
    mount();
    await userEvent.click(await screen.findByRole("button", { name: "Cancel request…" }));
    expect(api.to(`POST /replenishments/${REQUEST_ID}/cancel`)).toHaveLength(0);
    await userEvent.click(screen.getByRole("button", { name: "Confirm cancellation" }));
    expect(await screen.findByText("Replenishment cancelled.")).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByRole("button", { name: /Cancel request/ })).not.toBeInTheDocument());
    expect(screen.getByText("OPERATOR")).toBeInTheDocument();
    const [post] = api.to(`POST /replenishments/${REQUEST_ID}/cancel`);
    expect(post.headers["Idempotency-Key"]).toMatch(UUID);
  });

  it("keeps the request and explains a 409 replenishment-not-open", async () => {
    mockApi({
      [`GET /replenishments/${REQUEST_ID}`]: json(replenishment()),
      [`POST /replenishments/${REQUEST_ID}/cancel`]: problem(409, "replenishment-not-open", "Not open", "already completed"),
    });
    mount();
    await userEvent.click(await screen.findByRole("button", { name: "Cancel request…" }));
    await userEvent.click(screen.getByRole("button", { name: "Confirm cancellation" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(/no longer open/);
    expect(screen.getByRole("alert")).toHaveTextContent("already completed");
  });

  it("can back out of the confirmation", async () => {
    const api = mockApi({ [`GET /replenishments/${REQUEST_ID}`]: json(replenishment()) });
    mount();
    await userEvent.click(await screen.findByRole("button", { name: "Cancel request…" }));
    await userEvent.click(screen.getByRole("button", { name: "Keep request" }));
    expect(screen.getByRole("button", { name: "Cancel request…" })).toBeInTheDocument();
    expect(api.calls.filter((c) => c.method === "POST")).toHaveLength(0);
  });

  it("shows the problem for an unknown request", async () => {
    mockApi({ [`GET /replenishments/${REQUEST_ID}`]: problem(404, "replenishment-not-found", "Not found", "no such request") });
    mount();
    expect(await screen.findByRole("alert")).toHaveTextContent("no such request");
  });
});
