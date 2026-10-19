import { useState } from "react";
import { Link } from "react-router-dom";
import { Card, DataTable } from "@warehouse/ui-kit";
import { listPolicies } from "../api";
import { Form, FormRow, SubmitButton, TextField } from "../components/formkit";
import { PageHeader, Stack } from "../components/layout";
import { Pager } from "../components/Pager";
import { RequestView } from "../components/ProblemAlert";
import { usePaging } from "../hooks/usePaging";
import { useRequest } from "../hooks/useRequest";
import { optionalIdProblem, utcLabel } from "../lib";
import type { Policy } from "../types";

interface Filters {
  siteId: string;
  sku: string;
}

const INITIAL: Filters = { siteId: "", sku: "" };

/**
 * Policy list: GET /policies, ordered by site then SKU, a page at a time,
 * optionally narrowed to one site and one SKU. Each row links to the edit form;
 * "Declare policy" opens the empty one.
 */
export function PolicyListScreen() {
  const [draft, setDraft] = useState<Filters>(INITIAL);
  const [applied, setApplied] = useState<Filters>(INITIAL);
  const paging = usePaging();
  const page = useRequest(
    () =>
      listPolicies({
        limit: 25,
        cursor: paging.cursor || undefined,
        siteId: applied.siteId || undefined,
        sku: applied.sku || undefined,
      }),
    `policies|${applied.siteId}|${applied.sku}|${paging.cursor}`,
  );

  const siteProblem = optionalIdProblem("site id", draft.siteId.trim());
  const skuProblem = optionalIdProblem("SKU", draft.sku.trim());
  const apply = () => {
    if (siteProblem || skuProblem) return;
    setApplied({ siteId: draft.siteId.trim(), sku: draft.sku.trim() });
    paging.reset();
  };

  return (
    <Stack gap={5}>
      <PageHeader
        title="Replenishment policies"
        subtitle="pick-face-replenishment · the min/max level kept in each forward pick face"
      />
      <Card title="Filters">
        <Form label="Policy filters" onSubmit={apply}>
          <FormRow>
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
            <Link to="../policies/new">Declare policy</Link>
            <Pager paging={paging} page={page} />
          </div>
        }
      >
        <RequestView
          state={page}
          what="policies"
          isEmpty={(p) => p.items.length === 0}
          empty={paging.isFirst ? "No policies match these filters." : "No more policies on this page."}
        >
          {(p) => <PolicyTable policies={p.items} />}
        </RequestView>
      </Card>
    </Stack>
  );
}

function PolicyTable({ policies }: { policies: Policy[] }) {
  return (
    <DataTable
      rowKey={(p) => `${p.siteId}/${p.sku}`}
      rows={policies}
      columns={[
        { key: "site", header: "Site", render: (p) => p.siteId },
        { key: "sku", header: "SKU", render: (p) => p.sku },
        { key: "min", header: "Min", align: "right", render: (p) => String(p.minQty) },
        { key: "max", header: "Max", align: "right", render: (p) => String(p.maxQty) },
        { key: "multiple", header: "Multiple", align: "right", render: (p) => String(p.multiple) },
        { key: "version", header: "Version", align: "right", render: (p) => String(p.version) },
        { key: "declared", header: "Declared", render: (p) => utcLabel(p.declaredAt) },
        {
          key: "edit",
          header: "",
          // Relative: `..` is this remote's mount point under any prefix.
          render: (p) => (
            <Link
              to={`../policies/${encodeURIComponent(p.siteId)}/${encodeURIComponent(p.sku)}`}
              aria-label={`Edit policy ${p.siteId} ${p.sku}`}
            >
              Edit
            </Link>
          ),
        },
      ]}
    />
  );
}
