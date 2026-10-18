import { Navigate, NavLink, Outlet, Route, Routes } from "react-router-dom";
import { PickFacesScreen } from "./screens/PickFacesScreen";
import { PolicyEditRoute, PolicyNewScreen } from "./screens/PolicyFormScreen";
import { PolicyListScreen } from "./screens/PolicyListScreen";
import { ReplenishmentDetailRoute } from "./screens/ReplenishmentDetailScreen";
import { ReplenishmentListScreen } from "./screens/ReplenishmentListScreen";
import { ReplenishmentRequestScreen } from "./screens/ReplenishmentRequestScreen";

const SUB_NAV = [
  { to: "pick-faces", label: "Pick faces", end: false },
  { to: "replenishments", label: "Replenishments", end: false },
  { to: "policies", label: "Policies", end: false },
];

const linkStyle = ({ isActive }: { isActive: boolean }) => ({
  display: "inline-flex",
  padding: "6px 12px",
  borderRadius: "var(--wh-radius-pill)",
  fontSize: "var(--wh-font-size-sm)",
  fontWeight: isActive ? 600 : 500,
  color: isActive ? "var(--wh-color-text)" : "var(--wh-color-text-muted)",
  background: isActive ? "var(--wh-color-accent-muted)" : "transparent",
  textDecoration: "none",
});

/** The sub-nav, rendered by a layout route with the path "/".
 *
 *  The console mounts this component inside its own `<Route
 *  path="/pick-face-replenishment/*">`. A relative `<NavLink>` rendered straight
 *  in that splat route resolves against the FULL current URL (react-router 7
 *  uses the last path-contributing match's pathname, splat included), so from
 *  /pick-face-replenishment/policies the link `pick-faces` would point at
 *  /pick-face-replenishment/policies/pick-faces. A pathless layout route does
 *  not help (react-router drops pathless routes from relative resolution); a
 *  layout route WITH path "/" does: its own match is the mount point, under any
 *  prefix and standalone at `/`. (Same fix as the slotting and inbound-receiving
 *  remotes; App.test.tsx mounts under the host's splat route to prove it.) */
function ReplenishmentLayout() {
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "var(--wh-space-5)" }}>
      <nav aria-label="Pick-face replenishment sections" style={{ display: "flex", gap: "var(--wh-space-2)" }}>
        {SUB_NAV.map((item) => (
          <NavLink key={item.to} to={item.to} end={item.end} style={linkStyle}>
            {item.label}
          </NavLink>
        ))}
      </nav>
      <Outlet />
    </div>
  );
}

/** Exposed as replenishment_mfe/App via Module Federation. Takes NO props: the
 *  console mounts it under `/pick-face-replenishment/*` and provides the
 *  BrowserRouter, the design tokens and `window.__WAREHOUSE_CONFIG__`. Routes
 *  here are RELATIVE, so the component works identically under that prefix (in
 *  the shell) or at / (standalone dev, see main.tsx).
 *
 *   - (index): redirects to pick-faces.
 *   - pick-faces: the per-site board with a status filter.
 *   - replenishments: the request list with state/site/SKU filters and paging.
 *   - replenishments/new: the manual request (Idempotency-Key on the POST).
 *   - replenishments/:requestId: one request and, while open, its cancel
 *     (Idempotency-Key on the POST).
 *   - policies: the policy list with site/SKU filters and paging.
 *   - policies/new: declare a policy; policies/:siteId/:sku: edit it with
 *     If-Match. */
export default function App() {
  return (
    <Routes>
      <Route path="/" element={<ReplenishmentLayout />}>
        <Route index element={<Navigate to="pick-faces" replace />} />
        <Route path="pick-faces" element={<PickFacesScreen />} />
        <Route path="replenishments" element={<ReplenishmentListScreen />} />
        <Route path="replenishments/new" element={<ReplenishmentRequestScreen />} />
        <Route path="replenishments/:requestId" element={<ReplenishmentDetailRoute />} />
        <Route path="policies" element={<PolicyListScreen />} />
        <Route path="policies/new" element={<PolicyNewScreen />} />
        <Route path="policies/:siteId/:sku" element={<PolicyEditRoute />} />
      </Route>
    </Routes>
  );
}
