"use client";

import { useCallback, useEffect, useState } from "react";

// Explicit loading / success / error — callers render all three plus empty.
export function useAsync<T>(fn: () => Promise<T>, deps: unknown[] = []) {
  const [state, setState] = useState<{ data: T | null; loading: boolean; error: string | null }>({ data: null, loading: true, error: null });
  const run = useCallback(() => {
    setState((s) => ({ ...s, loading: true, error: null }));
    fn().then((data) => setState({ data, loading: false, error: null }))
      .catch((e) => setState({ data: null, loading: false, error: e instanceof Error ? e.message : "Erro desconhecido" }));
    // The caller supplies the dependency list explicitly (like useEffect); `fn` is intentionally not a dependency.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps);
  useEffect(() => { run(); }, [run]);
  return { ...state, reload: run };
}
