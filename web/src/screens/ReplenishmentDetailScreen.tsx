import { useState } from "react";
import { Link, useParams } from "react-router-dom";
import { Card } from "@warehouse/ui-kit";
import { cancelReplenishment, getReplenishment, toApiError } from "../api";
import type { ApiError } from "../api";
import { InlineSuccess, SubmitButton } from "../components/formkit";
import { Facts, PageHeader, PriorityPill, RequestStatePill, Stack } from "../components/layout";
import { ProblemAlert, RequestView } from "../components/ProblemAlert";
import { useRequest } from "../hooks/useRequest";
import { utcLabel } from "../lib";
import type { Replenishment } from "../types";

/** Reads :requestId and keys the screen on it, so moving between requests
 *  never carries cancel state over. */
export function ReplenishmentDetailRoute() {
  const { requestId = "" } = useParams();
  return <ReplenishmentDetailScreen key={requestId} requestId={requestId} />;
}

/**
 * One replenishment request: where the stock moves, how much, the policy it
 * was sized with and how it ended; while it is Requested, the operator can
 * cancel it. The cancel is a two-step flow (the button arms a confirmation,
 * only the confirmed button POSTs) and its POST carries an Idempotency-Key.
 */
export function ReplenishmentDetailScreen({ requestId }: { requestId: string }) {
  const request = useRequest(() => getReplenishment(requestId), `request|${requestId}`);
  // The outcome note lives HERE: a successful cancel reloads the request, the
  // reloaded request is no longer Requested and the cancel card unmounts with it.
  const [cancelled, setCancelled] = useState(false);
  return (
    <Stack gap={5}>
      <div>
        <Link to="../replenishments">← All replenishments</Link>
      </div>
      <RequestView state={request} what="replenishment">
        {(r) => (
          <RequestBody
            request={r}
            cancelled={cancelled}
            onCancelled={() => {
              setCancelled(true);
              request.reload();
            }}
          />
        )}
      </RequestView>
    </Stack>
  );
}

function RequestBody({
  request,
  cancelled,
  onCancelled,
}: {
  request: Replenishment;
  cancelled: boolean;
  onCancelled: () => void;
}) {
  return (
    <>
      <PageHeader title={request.requestId} subtitle="pick-face-replenishment · replenishment request" />
      {cancelled && <InlineSuccess message="Replenishment cancelled." />}
      <Card title="Request">
        <Facts
          items={[
            { label: "State", value: <RequestStatePill state={request.state} /> },
            { label: "Priority", value: <PriorityPill priority={request.priority} /> },
            { label: "Site", value: request.siteId },
            { label: "SKU", value: request.sku },
            { label: "Reserve bin", value: request.sourceBin },
            { label: "Forward bin", value: request.forwardBin },
            { label: "Quantity", value: String(request.quantity) },
            ...(request.movedQuantity !== undefined ? [{ label: "Moved", value: String(request.movedQuantity) }] : []),
            { label: "Policy", value: `min ${request.policyMin} · max ${request.policyMax}` },
            { label: "Requested", value: utcLabel(request.requestedAt) },
            ...(request.completedAt ? [{ label: "Completed", value: utcLabel(request.completedAt) }] : []),
            ...(request.cancelledAt ? [{ label: "Cancelled", value: utcLabel(request.cancelledAt) }] : []),
            ...(request.cancelReason ? [{ label: "Cancel reason", value: request.cancelReason }] : []),
            { label: "Version", value: String(request.version) },
          ]}
        />
      </Card>
      {request.state === "Requested" && <CancelCard request={request} onCancelled={onCancelled} />}
    </>
  );
}

function CancelCard({ request, onCancelled }: { request: Replenishment; onCancelled: () => void }) {
  const [armed, setArmed] = useState(false);
  const [problem, setProblem] = useState<ApiError | null>(null);
  const [saving, setSaving] = useState(false);

  const cancel = async () => {
    setProblem(null);
    setSaving(true);
    try {
      await cancelReplenishment(request.requestId);
      setArmed(false);
      onCancelled();
    } catch (err) {
      setProblem(toApiError(err));
    } finally {
      setSaving(false);
    }
  };

  const lead =
    problem?.slug === "replenishment-not-open"
      ? "This request is no longer open -- it was completed or cancelled meanwhile. Reload to see the latest state."
      : undefined;

  return (
    <Card title="Cancel">
      <Stack>
        {!armed ? (
          <div>
            <SubmitButton type="button" danger onClick={() => setArmed(true)}>
              Cancel request…
            </SubmitButton>
          </div>
        ) : (
          <Stack gap={3}>
            <div role="group" aria-label="Confirm cancellation">
              Cancel {request.requestId}? The floor will not move {request.quantity} of {request.sku} to {request.forwardBin}.
            </div>
            <div style={{ display: "flex", gap: "var(--wh-space-2)" }}>
              <SubmitButton type="button" danger disabled={saving} onClick={() => void cancel()}>
                Confirm cancellation
              </SubmitButton>
              <SubmitButton type="button" secondary disabled={saving} onClick={() => setArmed(false)}>
                Keep request
              </SubmitButton>
            </div>
          </Stack>
        )}
        {problem && <ProblemAlert error={problem} lead={lead} />}
      </Stack>
    </Card>
  );
}
