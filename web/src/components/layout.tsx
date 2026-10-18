import type { ReactNode } from "react";
import { StatusPill } from "@warehouse/ui-kit";
import type { PickFaceStatus, Priority, RequestState } from "../types";

export function PageHeader({ title, subtitle }: { title: string; subtitle: ReactNode }) {
  return (
    <div>
      <h1 style={{ fontSize: "var(--wh-font-size-2xl)", margin: 0 }}>{title}</h1>
      <p style={{ color: "var(--wh-color-text-muted)", marginTop: 4 }}>{subtitle}</p>
    </div>
  );
}

export function Stack({ children, gap = 4 }: { children: ReactNode; gap?: 3 | 4 | 5 }) {
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: `var(--wh-space-${gap})` }}>
      {children}
    </div>
  );
}

/** A label/value list (dl) for a record's scalar fields. */
export function Facts({ items }: { items: { label: string; value: ReactNode }[] }) {
  return (
    <dl
      style={{
        display: "grid",
        gridTemplateColumns: "max-content 1fr",
        gap: "var(--wh-space-2) var(--wh-space-4)",
        margin: 0,
        fontSize: "var(--wh-font-size-sm)",
      }}
    >
      {items.map((item) => (
        <div key={item.label} style={{ display: "contents" }}>
          <dt style={{ color: "var(--wh-color-text-muted)" }}>{item.label}</dt>
          <dd style={{ margin: 0 }}>{item.value}</dd>
        </div>
      ))}
    </dl>
  );
}

type Tone = "neutral" | "progress" | "success" | "warning" | "danger";

/** These lifecycles are pick-face-replenishment's own, so StatusPill's tone
 *  override carries them. */
const REQUEST_TONE: Record<RequestState, Tone> = {
  Requested: "progress",
  Completed: "success",
  Cancelled: "neutral",
};

export function RequestStatePill({ state }: { state: RequestState }) {
  return <StatusPill status={state} tone={REQUEST_TONE[state]} size="sm" />;
}

const FACE_TONE: Record<PickFaceStatus, Tone> = {
  BELOW_MIN: "warning",
  OK: "success",
  NO_SOURCE: "danger",
  NO_POLICY: "neutral",
};

export function PickFaceStatusPill({ status }: { status: PickFaceStatus }) {
  return <StatusPill status={status} tone={FACE_TONE[status]} size="sm" />;
}

export function PriorityPill({ priority }: { priority: Priority }) {
  return <StatusPill status={priority} tone={priority === "URGENT" ? "danger" : "neutral"} size="sm" />;
}
