import { useState } from "react";
import { Link, useParams } from "react-router-dom";
import { Card } from "@warehouse/ui-kit";
import { declarePolicy, getPolicy, toApiError } from "../api";
import type { ApiError } from "../api";
import { Form, FormRow, InlineError, InlineSuccess, SubmitButton, TextField } from "../components/formkit";
import { Facts, PageHeader, Stack } from "../components/layout";
import { ProblemAlert, RequestView } from "../components/ProblemAlert";
import { useRequest } from "../hooks/useRequest";
import { idProblem, parsePolicy, utcLabel } from "../lib";
import type { Policy } from "../types";

/** Declares a new policy: PUT without If-Match (409 policy-already-exists when
 *  the site and SKU already have one). */
export function PolicyNewScreen() {
  const [siteId, setSiteId] = useState("");
  const [sku, setSku] = useState("");
  return (
    <Stack gap={5}>
      <div>
        <Link to="../policies">← All policies</Link>
      </div>
      <PageHeader title="Declare policy" subtitle="pick-face-replenishment · a new min/max policy for a site and SKU" />
      <PolicyForm
        key="new"
        identity={{ siteId, sku, onSite: setSiteId, onSku: setSku }}
        loaded={null}
      />
    </Stack>
  );
}

/** Reads :siteId/:sku, loads the policy and keys the form on its identity, so
 *  moving between policies never carries draft values over. */
export function PolicyEditRoute() {
  const { siteId = "", sku = "" } = useParams();
  return <PolicyEditScreen key={`${siteId}/${sku}`} siteId={siteId} sku={sku} />;
}

/** Changes a policy: PUT with `If-Match: <version the form was loaded at>`. A
 *  stale version is a 409 concurrent-modification, answered with a reload. */
export function PolicyEditScreen({ siteId, sku }: { siteId: string; sku: string }) {
  const policy = useRequest(() => getPolicy(siteId, sku), `policy|${siteId}|${sku}`);
  return (
    <Stack gap={5}>
      <div>
        <Link to="../policies">← All policies</Link>
      </div>
      <PageHeader title={`Policy ${siteId} / ${sku}`} subtitle="pick-face-replenishment · edit the min/max policy" />
      <RequestView state={policy} what="policy">
        {(p) => <PolicyForm key={p.version} identity={null} loaded={p} onStale={policy.reload} />}
      </RequestView>
    </Stack>
  );
}

interface Identity {
  siteId: string;
  sku: string;
  onSite: (v: string) => void;
  onSku: (v: string) => void;
}

function PolicyForm({
  identity,
  loaded,
  onStale,
}: {
  /** The editable site and SKU of a NEW policy; null when editing. */
  identity: Identity | null;
  /** The policy being edited (its version is the If-Match); null when new. */
  loaded: Policy | null;
  onStale?: () => void;
}) {
  const [minQty, setMinQty] = useState(loaded ? String(loaded.minQty) : "");
  const [maxQty, setMaxQty] = useState(loaded ? String(loaded.maxQty) : "");
  const [multiple, setMultiple] = useState(loaded ? String(loaded.multiple) : "");
  const [problem, setProblem] = useState<ApiError | null>(null);
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState<Policy | null>(null);
  const [shown, setShown] = useState<string | null>(null);
  // The version the NEXT save replaces: what the form was loaded at, then what
  // the last save answered (so a second save is not a stale one).
  const [version, setVersion] = useState<number | undefined>(loaded?.version);

  const siteId = loaded ? loaded.siteId : (identity?.siteId.trim() ?? "");
  const sku = loaded ? loaded.sku : (identity?.sku.trim() ?? "");

  const submit = async () => {
    setProblem(null);
    setSaved(null);
    const idErr = loaded ? null : (idProblem("site id", siteId) ?? idProblem("SKU", sku));
    const parsed = parsePolicy({ minQty, maxQty, multiple });
    const message = idErr ?? (parsed.ok ? null : parsed.message);
    setShown(message);
    if (message !== null || !parsed.ok) return;
    setSaving(true);
    try {
      // New: no If-Match. Edit: the version this form was loaded at.
      const result = await declarePolicy(siteId, sku, parsed.values, version);
      setSaved(result);
      setVersion(result.version);
    } catch (err) {
      setProblem(toApiError(err));
    } finally {
      setSaving(false);
    }
  };

  const lead =
    problem?.slug === "policy-already-exists"
      ? "A policy already exists for this site and SKU. Edit it from the policy list instead."
      : problem?.slug === "concurrent-modification"
        ? "Someone changed this policy after you opened it. Reload to see the latest version, then re-apply your change."
        : undefined;

  return (
    <Card title={loaded ? "Edit policy" : "New policy"}>
      <Stack>
        {loaded && (
          <Facts
            items={[
              { label: "Site", value: loaded.siteId },
              { label: "SKU", value: loaded.sku },
              { label: "Version", value: String(version ?? loaded.version) },
              { label: "Declared", value: utcLabel(loaded.declaredAt) },
            ]}
          />
        )}
        <Form label={loaded ? "Edit policy" : "Declare policy"} onSubmit={() => void submit()}>
          {identity && (
            <FormRow>
              <TextField label="Site" value={identity.siteId} onChange={identity.onSite} required />
              <TextField label="SKU" value={identity.sku} onChange={identity.onSku} required />
            </FormRow>
          )}
          <FormRow>
            <TextField label="Minimum" value={minQty} onChange={setMinQty} inputMode="numeric" required hint="Replenish when the level reaches this" />
            <TextField label="Maximum" value={maxQty} onChange={setMaxQty} inputMode="numeric" required hint="Never fill above this" />
            <TextField label="Multiple" value={multiple} onChange={setMultiple} inputMode="numeric" hint="Optional; 1 when empty" />
          </FormRow>
          <InlineError message={shown} />
          {problem && <ProblemAlert error={problem} lead={lead} />}
          {saved && (
            <InlineSuccess
              message={`Policy saved: ${saved.minQty} to ${saved.maxQty} in multiples of ${saved.multiple} (version ${saved.version}).`}
            />
          )}
          <FormRow>
            <SubmitButton disabled={saving}>{loaded ? "Save policy" : "Declare policy"}</SubmitButton>
            {problem?.slug === "concurrent-modification" && onStale && (
              <SubmitButton type="button" secondary onClick={onStale}>
                Reload policy
              </SubmitButton>
            )}
          </FormRow>
        </Form>
      </Stack>
    </Card>
  );
}
