import type { RequestState } from "../hooks/useRequest";
import type { Paging } from "../hooks/usePaging";
import { SubmitButton } from "./formkit";

/** Previous/Next buttons for a cursor-paged request (`page` is the request
 *  state of the current page). */
export function Pager({
  paging,
  page,
}: {
  paging: Paging;
  page: RequestState<{ nextCursor?: string }>;
}) {
  const nextCursor = page.status === "success" ? page.data.nextCursor : undefined;
  return (
    <div style={{ display: "flex", gap: "var(--wh-space-2)" }}>
      <SubmitButton
        type="button"
        secondary
        disabled={paging.isFirst || page.status === "loading"}
        onClick={paging.previous}
      >
        Previous page
      </SubmitButton>
      <SubmitButton
        type="button"
        secondary
        disabled={!nextCursor}
        onClick={() => {
          if (nextCursor) paging.next(nextCursor);
        }}
      >
        Next page
      </SubmitButton>
    </div>
  );
}
