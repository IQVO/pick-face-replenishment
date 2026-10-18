import { describe, expect, it } from "vitest";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import App from "./App";
import { json, mockApi } from "./test/fetchMock";
import { REQUEST_ID, pickFace, policy, replenishment } from "./test/fixtures";

function mount(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <App />
    </MemoryRouter>,
  );
}

/** Mounted the way the console mounts the remote: inside the host's own
 *  splat route, where relative links resolve differently than at the root. */
function mountUnderHost(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/pick-face-replenishment/*" element={<App />} />
      </Routes>
    </MemoryRouter>,
  );
}

function routes() {
  return mockApi({
    "GET /policies": json({ items: [policy()] }),
    "GET /pick-faces": json({ items: [pickFace()] }),
    "GET /replenishments": json({ items: [replenishment()] }),
    [`GET /replenishments/${REQUEST_ID}`]: json(replenishment()),
    "GET /policies/SITE-1/SKU-1": json(policy()),
  });
}

function subNavHrefs(): string[] {
  const nav = screen.getByRole("navigation", { name: "Pick-face replenishment sections" });
  return within(nav)
    .getAllByRole("link")
    .map((a) => a.getAttribute("href") ?? "");
}

describe("App (the exposed ./App)", () => {
  it("redirects the index route to the pick-faces board and shows the sub-nav", async () => {
    routes();
    mount("/");
    expect(await screen.findByRole("heading", { name: "Pick faces" })).toBeInTheDocument();
    const nav = screen.getByRole("navigation", { name: "Pick-face replenishment sections" });
    for (const label of ["Pick faces", "Replenishments", "Policies"]) expect(nav).toHaveTextContent(label);
  });

  it("routes relatively at the root: every section and detail route renders", async () => {
    routes();
    const cases: [string, string][] = [
      ["/replenishments", "Replenishments"],
      ["/replenishments/new", "Request replenishment"],
      [`/replenishments/${REQUEST_ID}`, REQUEST_ID],
      ["/policies", "Replenishment policies"],
      ["/policies/new", "Declare policy"],
      ["/policies/SITE-1/SKU-1", "Policy SITE-1 / SKU-1"],
    ];
    for (const [path, heading] of cases) {
      const { unmount } = mount(path);
      expect(await screen.findByRole("heading", { name: heading })).toBeInTheDocument();
      unmount();
    }
  });

  it("degrades without crashing when the API is unreachable", async () => {
    mockApi({});
    mount("/policies");
    expect(await screen.findByRole("heading", { name: "Replenishment policies" })).toBeInTheDocument();
    expect(await screen.findByRole("alert")).toBeInTheDocument();
  });

  it("keeps sub-nav links under the host's mount point from any section", async () => {
    routes();
    mountUnderHost("/pick-face-replenishment/policies");
    await screen.findByRole("heading", { name: "Replenishment policies" });
    expect(subNavHrefs()).toEqual([
      "/pick-face-replenishment/pick-faces",
      "/pick-face-replenishment/replenishments",
      "/pick-face-replenishment/policies",
    ]);
    await userEvent.click(screen.getByRole("link", { name: "Replenishments" }));
    expect(await screen.findByRole("heading", { name: "Replenishments" })).toBeInTheDocument();
    expect(subNavHrefs()[0]).toBe("/pick-face-replenishment/pick-faces");
  });

  it("only ever calls its own API (no sibling base)", async () => {
    const api = routes();
    mount("/replenishments");
    await screen.findByText(REQUEST_ID);
    for (const c of api.calls) expect(c.url.startsWith("http://localhost:8080/")).toBe(true);
  });
});
