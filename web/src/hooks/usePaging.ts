import { useState } from "react";

/**
 * Cursor paging for a list screen. The API only moves forward (`nextCursor`),
 * so the cursors already visited are kept and "Previous" walks back without
 * the API having to support it. `reset()` starts again at the first page (the
 * filters changed).
 */
export function usePaging() {
  // cursors[i] is the cursor of page i+1 ("" = the first page).
  const [cursors, setCursors] = useState<string[]>([""]);
  return {
    /** The cursor to send ("" on the first page). */
    cursor: cursors[cursors.length - 1],
    /** 1-based page number. */
    pageNo: cursors.length,
    isFirst: cursors.length === 1,
    next: (nextCursor: string) => setCursors((c) => [...c, nextCursor]),
    previous: () => setCursors((c) => c.slice(0, -1)),
    reset: () => setCursors([""]),
  };
}

export type Paging = ReturnType<typeof usePaging>;
