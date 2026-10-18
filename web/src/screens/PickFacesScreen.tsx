import { useState } from "react";
import { Link } from "react-router-dom";
import { Card, DataTable } from "@warehouse/ui-kit";
import { listPickFaces } from "../api";
import { Form, FormRow, InlineError, SelectField, SubmitButton, TextField } from "../components/formkit";
import { PageHeader, PickFaceStatusPill, Stack } from "../components/layout";
import { Pager } from "../components/Pager";
import { RequestView } from "../components/ProblemAlert";
import { usePaging } from "../hooks/usePaging";
import { useRequest } from "../hooks/useRequest";
import { idProblem } from "../lib";
import { PICK_FACE_STATUSES } from "../types";
import type { PickFace, PickFaceStatus } from "../types";

const STATUS_OPTIONS = PICK_FACE_STATUSES.map((s) => ({ value: s, label: s }));

interface Filters {
  siteId: string;
  status: "" | PickFaceStatus;
}

/**
 * The pick-faces board: GET /pick-faces for ONE site (the API requires it),
 * ordered by forward bin, with the replenishment status the detector computes
 * from the event-fed stock copies, optionally narrowed to one status. Nothing
 * is requested until a site is entered. A row with an open request links to it;
 * a row without one offers a manual request.
 */
export function PickFacesScreen() {
  const [draft, setDraft] = useState<Filters>({ siteId: "", status: "" });
  const [applied, setApplied] = useState<Filters | null>(null);
  const [shown, setShown] = useState<string | null>(null);
  const paging = usePaging();
  const page = useRequest(
    applied
      ? () =>
          listPickFaces({
            siteId: applied.siteId,
            status: applied.status || undefined,
            limit: 25,
            cursor: paging.cursor || undefined,
          })
      : null,
    `faces|${applied?.siteId}|${applied?.status}|${paging.cursor}`,
  );

  const apply = () => {
    const siteId = draft.siteId.trim();
    const message = idProblem("site id", siteId);
    setShown(message);
    if (message) return;
    setApplied({ siteId, status: draft.status });
    paging.reset();
  };

  return (
    <Stack gap={5}>
      <PageHeader
        title="Pick faces"
        subtitle="pick-face-replenishment · forward pick faces and whether they need stock"
      />
      <Card title="Site and status">
        <Form label="Pick face filters" onSubmit={apply}>
          <FormRow>
            <TextField
              label="Site"
              value={draft.siteId}
              onChange={(v) => setDraft({ ...draft, siteId: v })}
              required
              hint="The board is per site"
            />
            <SelectField
              label="Status"
              value={draft.status}
              onChange={(v) => setDraft({ ...draft, status: v as Filters["status"] })}
              options={STATUS_OPTIONS}
              emptyLabel="Any status"
            />
            <SubmitButton>Show pick faces</SubmitButton>
          </FormRow>
          <InlineError message={shown} />
        </Form>
      </Card>
      <Card title={`Page ${paging.pageNo}`} actions={<Pager paging={paging} page={page} />}>
        <RequestView
          state={page}
          what="pick faces"
          whenIdle="Enter a site to see its pick faces."
          isEmpty={(p) => p.items.length === 0}
          empty={paging.isFirst ? "No pick faces match this site and status." : "No more pick faces on this page."}
        >
          {(p) => <FaceTable faces={p.items} />}
        </RequestView>
      </Card>
    </Stack>
  );
}

function FaceTable({ faces }: { faces: PickFace[] }) {
  return (
    <DataTable
      rowKey={(f) => f.forwardBin}
      rows={faces}
      columns={[
        { key: "bin", header: "Forward bin", render: (f) => f.forwardBin },
        { key: "sku", header: "SKU", render: (f) => f.sku },
        { key: "onHand", header: "On hand", align: "right", render: (f) => String(f.onHand) },
        { key: "reserved", header: "Reserved", align: "right", render: (f) => String(f.reserved) },
        { key: "available", header: "Available", align: "right", render: (f) => String(f.available) },
        { key: "min", header: "Min", align: "right", render: (f) => (f.minQty === undefined ? "—" : String(f.minQty)) },
        { key: "max", header: "Max", align: "right", render: (f) => (f.maxQty === undefined ? "—" : String(f.maxQty)) },
        { key: "status", header: "Status", render: (f) => <PickFaceStatusPill status={f.status} /> },
        {
          key: "request",
          header: "Request",
          render: (f) =>
            f.openRequestId ? (
              <Link to={`../replenishments/${encodeURIComponent(f.openRequestId)}`}>{f.openRequestId}</Link>
            ) : (
              <Link
                to={`../replenishments/new?siteId=${encodeURIComponent(f.siteId)}&sku=${encodeURIComponent(f.sku)}`}
                aria-label={`Request replenishment for ${f.sku}`}
              >
                Request
              </Link>
            ),
        },
      ]}
    />
  );
}
