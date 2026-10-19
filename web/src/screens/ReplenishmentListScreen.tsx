import { useState } from "react";
import { Link } from "react-router-dom";
import { Card, DataTable } from "@warehouse/ui-kit";
import { listReplenishments } from "../api";
import { Form, FormRow, SelectField, SubmitButton, TextField } from "../components/formkit";
import { PageHeader, PriorityPill, RequestStatePill, Stack } from "../components/layout";
import { Pager } from "../components/Pager";
import { RequestView } from "../components/ProblemAlert";
import { usePaging } from "../hooks/usePaging";
import { useRequest } from "../hooks/useRequest";
import { optionalIdProblem, utcLabel } from "../lib";
import { REQUEST_STATES } from "../types";
import type { Replenishment, RequestState } from "../types";

const STATE_OPTIONS = REQUEST_STATES.map((s) => ({ value: s, label: s }));

interface Filters {
  state: "" | RequestState;
  siteId: string;
  sku: string;
}

const INITIAL: Filters = { state: "", siteId: "", sku: "" };

/**
 * Replenishment list: GET /replenishments, newest first, a page at a time,
 * optionally narrowed to a site, a SKU and a lifecycle state. "Request
 * replenishment" opens the manual-request form.
 */
export function ReplenishmentListScreen() {
  const [draft, setDraft] = useState<Filters>(INITIAL);
  const [applied, setApplied] = useState<Filters>(INITIAL);
  const paging = usePaging();
  const page = useRequest(
    () =>
      listReplenishments({
        limit: 25,
        cursor: paging.cursor || undefined,
        siteId: applied.siteId || undefined,
        sku: applied.sku || undefined,
        state: applied.state || undefined,
      }),
    `requests|${applied.state}|${applied.siteId}|${applied.sku}|${paging.cursor}`,
  );

  const siteProblem = optionalIdProblem("site id", draft.siteId.trim());
  const skuProblem = optionalIdProblem("SKU", draft.sku.trim());
  const apply = () => {
    if (siteProblem || skuProblem) return;
    setApplied({ state: draft.state, siteId: draft.siteId.trim(), sku: draft.sku.trim() });
    paging.reset();
  };

  return (
    <Stack gap={5}>
      <PageHeader
        title="Replenishments"
        subtitle="pick-face-replenishment · moves of stock from a reserve bin to a forward pick face"
      />
      <Card title="Filters">
        <Form label="Replenishment filters" onSubmit={apply}>
          <FormRow>
            <SelectField
              label="State"
              value={draft.state}
              onChange={(v) => setDraft({ ...draft, state: v as Filters["state"] })}
              options={STATE_OPTIONS}
              emptyLabel="Any state"
            />
            <TextField label="Site" value={draft.siteId} onChange={(v) => setDraft({ ...draft, siteId: v })} placeholder="Every site" />
            <TextField label="SKU" value={draft.sku} onChange={(v) => setDraft({ ...draft, sku: v })} placeholder="Every SKU" />
            <SubmitButton disabled={siteProblem !== null || skuProblem !== null}>Apply filters</SubmitButton>
          </FormRow>
        </Form>
      </Card>
      <Card
        title={`Page ${paging.pageNo}`}
        actions={
          <div style={{ display: "flex", gap: "var(--wh-space-3)", alignItems: "center" }}>
            <Link to="../replenishments/new">Request replenishment</Link>
            <Pager paging={paging} page={page} />
          </div>
        }
      >
        <RequestView
          state={page}
          what="replenishments"
          isEmpty={(p) => p.items.length === 0}
          empty={paging.isFirst ? "No replenishments match these filters." : "No more replenishments on this page."}
        >
          {(p) => <RequestTable requests={p.items} />}
        </RequestView>
      </Card>
    </Stack>
  );
}

function RequestTable({ requests }: { requests: Replenishment[] }) {
  return (
    <DataTable
      rowKey={(r) => r.requestId}
      rows={requests}
      columns={[
        {
          key: "request",
          header: "Request",
          render: (r) => <Link to={`../replenishments/${encodeURIComponent(r.requestId)}`}>{r.requestId}</Link>,
        },
        { key: "site", header: "Site", render: (r) => r.siteId },
        { key: "sku", header: "SKU", render: (r) => r.sku },
        { key: "state", header: "State", render: (r) => <RequestStatePill state={r.state} /> },
        { key: "priority", header: "Priority", render: (r) => <PriorityPill priority={r.priority} /> },
        { key: "route", header: "From → to", render: (r) => `${r.sourceBin} → ${r.forwardBin}` },
        { key: "quantity", header: "Quantity", align: "right", render: (r) => String(r.quantity) },
        { key: "requested", header: "Requested", render: (r) => utcLabel(r.requestedAt) },
      ]}
    />
  );
}
