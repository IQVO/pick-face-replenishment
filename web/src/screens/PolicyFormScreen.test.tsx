import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { PolicyEditRoute, PolicyNewScreen } from "./PolicyFormScreen";
import { json, mockApi, problem } from "../test/fetchMock";
import { policy } from "../test/fixtures";

function mountNew() {
  return render(
    <MemoryRouter initialEntries={["/policies/new"]}>
      <Routes>
        <Route path="/">
          <Route path="policies/new" element={<PolicyNewScreen />} />
        </Route>
      </Routes>
    </MemoryRouter>,
  );
}

function mountEdit() {
  return render(
    <MemoryRouter initialEntries={["/policies/SITE-1/SKU-1"]}>
      <Routes>
        <Route path="/">
          <Route path="policies/:siteId/:sku" element={<PolicyEditRoute />} />
        </Route>
      </Routes>
    </MemoryRouter>,
  );
}

describe("declaring a policy", () => {
  it("PUTs without If-Match and reports the saved policy", async () => {
    const api = mockApi({ "PUT /policies/SITE-1/SKU-1": json(policy({ version: 1 }), 201) });
    mountNew();
    await userEvent.type(screen.getByLabelText(/Site/), "SITE-1");
    await userEvent.type(screen.getByLabelText(/SKU/), "SKU-1");
    await userEvent.type(screen.getByLabelText(/Minimum/), "10");
    await userEvent.type(screen.getByLabelText(/Maximum/), "60");
    await userEvent.type(screen.getByLabelText(/Multiple/), "6");
    await userEvent.click(screen.getByRole("button", { name: "Declare policy" }));
    expect(await screen.findByText(/Policy saved: 10 to 60 in multiples of 6 \(version 1\)/)).toBeInTheDocument();
    const [put] = api.calls;
    expect(put.body).toEqual({ minQty: 10, maxQty: 60, multiple: 6 });
    expect(put.headers["If-Match"]).toBeUndefined();
  });

  it("omits an empty multiple and validates bounds before calling", async () => {
    const api = mockApi({ "PUT /policies/SITE-1/SKU-1": json(policy({ version: 1, multiple: 1 }), 201) });
    mountNew();
    await userEvent.type(screen.getByLabelText(/Site/), "SITE-1");
    await userEvent.type(screen.getByLabelText(/SKU/), "SKU-1");
    await userEvent.type(screen.getByLabelText(/Minimum/), "60");
    await userEvent.type(screen.getByLabelText(/Maximum/), "60");
    await userEvent.click(screen.getByRole("button", { name: "Declare policy" }));
    expect(screen.getByText("Minimum must be below the maximum.")).toBeInTheDocument();
    expect(api.calls).toHaveLength(0);
    await userEvent.clear(screen.getByLabelText(/Maximum/));
    await userEvent.type(screen.getByLabelText(/Maximum/), "90");
    await userEvent.click(screen.getByRole("button", { name: "Declare policy" }));
    await screen.findByText(/Policy saved/);
    expect(api.calls[0].body).toEqual({ minQty: 60, maxQty: 90, multiple: 1 });
  });

  it("explains a 409 policy-already-exists", async () => {
    mockApi({ "PUT /policies/SITE-1/SKU-1": problem(409, "policy-already-exists", "Exists", "SITE-1/SKU-1 has one") });
    mountNew();
    await userEvent.type(screen.getByLabelText(/Site/), "SITE-1");
    await userEvent.type(screen.getByLabelText(/SKU/), "SKU-1");
    await userEvent.type(screen.getByLabelText(/Minimum/), "1");
    await userEvent.type(screen.getByLabelText(/Maximum/), "5");
    await userEvent.click(screen.getByRole("button", { name: "Declare policy" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(/Edit it from the policy list/);
  });
});

describe("editing a policy", () => {
  it("PUTs with If-Match = the loaded version, then the next save uses the new version", async () => {
    let n = 0;
    const api = mockApi({
      "GET /policies/SITE-1/SKU-1": json(policy({ version: 2 })),
      "PUT /policies/SITE-1/SKU-1": (call) => {
        n += 1;
        return json(policy({ version: 2 + n, maxQty: Number((call.body as { maxQty: number }).maxQty) }));
      },
    });
    mountEdit();
    const max = await screen.findByLabelText(/Maximum/);
    expect(max).toHaveValue("60");
    expect(screen.queryByLabelText(/Site/)).not.toBeInTheDocument();
    await userEvent.clear(max);
    await userEvent.type(max, "80");
    await userEvent.click(screen.getByRole("button", { name: "Save policy" }));
    await screen.findByText(/Policy saved: 10 to 80 .* \(version 3\)/);
    expect(api.to("PUT /policies/SITE-1/SKU-1")[0].headers["If-Match"]).toBe("2");

    await userEvent.click(screen.getByRole("button", { name: "Save policy" }));
    await screen.findByText(/version 4/);
    expect(api.to("PUT /policies/SITE-1/SKU-1")[1].headers["If-Match"]).toBe("3");
  });

  it("answers a 409 concurrent-modification with a reload that picks up the latest version", async () => {
    let version = 2;
    const api = mockApi({
      "GET /policies/SITE-1/SKU-1": () => json(policy({ version })),
      "PUT /policies/SITE-1/SKU-1": problem(409, "concurrent-modification", "Changed", "version is 5"),
    });
    mountEdit();
    await screen.findByLabelText(/Maximum/);
    await userEvent.click(screen.getByRole("button", { name: "Save policy" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(/Someone changed this policy/);
    version = 5;
    await userEvent.click(screen.getByRole("button", { name: "Reload policy" }));
    await screen.findByText("5", { selector: "dd" });
    expect(api.to("GET /policies/SITE-1/SKU-1")).toHaveLength(2);
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });
});
