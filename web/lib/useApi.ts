"use client";

import { useCallback, useEffect, useState } from "react";
import { ApiError } from "./api";

interface State<T> {
  data: T | null;
  error: string | null;
  loading: boolean;
}

// A small data-fetching hook shared by every dashboard page — no page
// fabricates data while loading or on error (rule 36): callers render an
// explicit loading state and an explicit error banner, never a guessed
// placeholder value.
export function useApi<T>(fetcher: () => Promise<T>, deps: unknown[] = []) {
  const [state, setState] = useState<State<T>>({ data: null, error: null, loading: true });

  const reload = useCallback(() => {
    setState((s) => ({ ...s, loading: true, error: null }));
    fetcher()
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

  return { ...state, reload };
}
