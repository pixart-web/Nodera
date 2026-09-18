"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { ApiError } from "./api";

interface State<T> {
  data: T | null;
  error: string | null;
  loading: boolean;
}

interface UseApiOptions {
  // When set, re-fetches in the background every pollMs milliseconds —
  // for pages whose data changes without the user taking an action here
  // (job status, pending approvals). A poll tick never flips `loading`
  // back to true and never clears already-displayed data on failure — it
  // would otherwise flash a loading state or blank the page on every
  // transient network hiccup, which is worse than briefly-stale data.
  pollMs?: number;
}

// A small data-fetching hook shared by every dashboard page — no page
// fabricates data while loading or on error (rule 36): callers render an
// explicit loading state and an explicit error banner, never a guessed
// placeholder value.
export function useApi<T>(fetcher: () => Promise<T>, deps: unknown[] = [], options: UseApiOptions = {}) {
  const [state, setState] = useState<State<T>>({ data: null, error: null, loading: true });
  const fetcherRef = useRef(fetcher);
  fetcherRef.current = fetcher;

  const reload = useCallback(() => {
    setState((s) => ({ ...s, loading: true, error: null }));
    fetcherRef.current()
      .then((data) => setState({ data, error: null, loading: false }))
      .catch((err) =>
        setState({
          data: null,
          error: err instanceof ApiError ? err.message : "Failed to reach the Nodera API",
          loading: false,
        }),
      );
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps);

  useEffect(() => {
    reload();
  }, [reload]);

  const pollMs = options.pollMs;
  useEffect(() => {
    if (!pollMs) return;
    const id = setInterval(() => {
      fetcherRef.current()
        .then((data) => setState((s) => ({ ...s, data, error: null })))
        .catch(() => {
          // Silent — a transient failure mid-poll shouldn't blank a page
          // that was showing good data a moment ago. reload() (triggered
          // by a manual action) still surfaces errors normally.
        });
    }, pollMs);
    return () => clearInterval(id);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [pollMs, ...deps]);

  return { ...state, reload };
}
