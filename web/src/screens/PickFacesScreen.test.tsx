import { describe, expect, it } from "vitest";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { PickFacesScreen } from "./PickFacesScreen";
import { json, mockApi } from "../test/fetchMock";
import { REQUEST_ID, pickFace } from "../test/fixtures";

function mount() {
  return render(
    <MemoryRouter>
      <PickFacesScreen />
    </MemoryRouter>,
  );
}

describe("PickFacesScreen", () => {
  it("asks for a site and calls nothing until one is entered", async () => {
    const api = mockApi({});
    mount();
    expect(screen.getByText("Enter a site to see its pick faces.")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Show pick faces" }));
    expect(screen.getByText("A site id is required.")).toBeInTheDocument();
    expect(api.calls).toHaveLength(0);
  });

  it("lists the board, filters by status and links requests", async () => {
    const api = mockApi({
      "GET /pick-faces": (call) =>
        json({
          items:
            call.query.get("status") === "BELOW_MIN"
              ? [pickFace()]
              : [
                  pickFace(),
                  pickFace({ forwardBin: "FWD-2", sku: "SKU-2", status: "OK", openRequestId: REQUEST_ID, onHand: 50, available: 50 }),
                  pickFace({ forwardBin: "FWD-3", sku: "SKU-3", status: "NO_POLICY", minQty: undefined, maxQty: undefined }),
                ],
        }),
    });
    mount();
    await userEvent.type(screen.getByLabelText(/Site/), "SITE-1");
    await userEvent.click(screen.getByRole("button", { name: "Show pick faces" }));

    const rows = await screen.findAllByRole("row");
    expect(rows).toHaveLength(4);
    expect(api.to("GET /pick-faces")[0].query.get("siteId")).toBe("SITE-1");
    expect(within(rows[3]).getAllByText("—")).toHaveLength(2);
    expect(screen.getByRole("link", { name: REQUEST_ID })).toHaveAttribute("href", `/replenishments/${REQUEST_ID}`);
    expect(screen.getByRole("link", { name: "Request replenishment for SKU-1" })).toHaveAttribute(
      "href",
      "/replenishments/new?siteId=SITE-1&sku=SKU-1",
    );

    await userEvent.selectOptions(screen.getByLabelText("Status"), "BELOW_MIN");
    await userEvent.click(screen.getByRole("button", { name: "Show pick faces" }));
    await screen.findByText("FWD-1");
    expect(screen.queryByText("FWD-2")).not.toBeInTheDocument();
    expect(api.to("GET /pick-faces").at(-1)?.query.get("status")).toBe("BELOW_MIN");
  });

  it("pages forward and back with the cursors", async () => {
    const api = mockApi({
      "GET /pick-faces": (call) =>
        call.query.get("cursor") === "c2"
          ? json({ items: [pickFace({ forwardBin: "FWD-9" })] })
          : json({ items: [pickFace()], nextCursor: "c2" }),
    });
    mount();
    await userEvent.type(screen.getByLabelText(/Site/), "SITE-1");
    await userEvent.click(screen.getByRole("button", { name: "Show pick faces" }));
    await screen.findByText("FWD-1");
    await userEvent.click(screen.getByRole("button", { name: "Next page" }));
    await screen.findByText("FWD-9");
    expect(api.to("GET /pick-faces").at(-1)?.query.get("cursor")).toBe("c2");
    await userEvent.click(screen.getByRole("button", { name: "Previous page" }));
    await screen.findByText("FWD-1");
  });
});
