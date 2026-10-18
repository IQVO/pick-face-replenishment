import { useState } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { Card } from "@warehouse/ui-kit";
import { requestReplenishment, toApiError } from "../api";
import type { ApiError } from "../api";
import { Form, FormRow, InlineError, SubmitButton, TextField } from "../components/formkit";
import { PageHeader, Stack } from "../components/layout";
import { ProblemAlert } from "../components/ProblemAlert";
import { idProblem, positiveWhole } from "../lib";

/** What to tell the operator for each refusal of a manual request. */
const LEADS: Record<string, string> = {
  "no-forward-assignment": "The site's approved slot plan gives this SKU no forward pick face.",
  "no-policy": "Declare a replenishment policy for this site and SKU first.",
  "no-source-stock": "No reserve bin of the site holds enough usable stock of this SKU.",
  "nothing-to-replenish": "The pick face is above its minimum. Enter a quantity to replenish it anyway.",
  "quantity-exceeds-max": "That quantity would take the pick face above the policy maximum.",
  "replenishment-already-open": "A request is already open for this pick face. Cancel or complete it first.",
};

/**
 * The manual replenishment request: POST /replenishments for a site and SKU,
 * with an optional quantity (empty = the quantity the detector would raise). The
 * site and SKU can be prefilled from the pick-faces board (?siteId=&sku=). The
 * POST carries an Idempotency-Key; on 201 the screen moves to the new request.
 */
export function ReplenishmentRequestScreen() {
  const [params] = useSearchParams();
  const navigate = useNavigate();
  const [siteId, setSiteId] = useState(params.get("siteId") ?? "");
  const [sku, setSku] = useState(params.get("sku") ?? "");
  const [quantity, setQuantity] = useState("");
  const [shown, setShown] = useState<string | null>(null);
  const [problem, setProblem] = useState<ApiError | null>(null);
  const [saving, setSaving] = useState(false);

  const submit = async () => {
    setProblem(null);
    const qty = quantity.trim() === "" ? undefined : positiveWhole(quantity);
    const message =
      idProblem("site id", siteId.trim()) ??
      idProblem("SKU", sku.trim()) ??
      (qty === null ? "Quantity must be a whole number, 1 or more." : null);
    setShown(message);
    if (message !== null || qty === null) return;
    setSaving(true);
    try {
      const created = await requestReplenishment({
        siteId: siteId.trim(),
        sku: sku.trim(),
        ...(qty === undefined ? {} : { quantity: qty }),
      });
      void navigate(`../replenishments/${encodeURIComponent(created.requestId)}`);
    } catch (err) {
      setProblem(toApiError(err));
      setSaving(false);
    }
  };

  return (
    <Stack gap={5}>
      <div>
        <Link to="../replenishments">← All replenishments</Link>
      </div>
      <PageHeader
        title="Request replenishment"
        subtitle="pick-face-replenishment · ask for stock to be moved to a forward pick face now"
      />
      <Card title="New request">
        <Form label="Request replenishment" onSubmit={() => void submit()}>
          <FormRow>
            <TextField label="Site" value={siteId} onChange={setSiteId} required />
            <TextField label="SKU" value={sku} onChange={setSku} required />
            <TextField
              label="Quantity"
              value={quantity}
              onChange={setQuantity}
              inputMode="numeric"
              hint="Optional; the detector's quantity when empty"
            />
          </FormRow>
          <InlineError message={shown} />
          {problem && <ProblemAlert error={problem} lead={LEADS[problem.slug]} />}
          <div>
            <SubmitButton disabled={saving}>Request replenishment</SubmitButton>
          </div>
        </Form>
      </Card>
    </Stack>
  );
}
