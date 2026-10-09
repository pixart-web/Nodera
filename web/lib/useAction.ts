"use client";

import { useCallback, useState } from "react";
import { ApiError } from "./api";
import { useToast } from "@/components/ui/Toast";

// Runs an async mutation with busy/error state and a success toast, so
// pages don't repeat the same try/catch/finally block per action.
export function useAction() {
  const toast = useToast();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const run = useCallback(async (fn: () => Promise<unknown>, success?: string, failure = "A operação falhou"): Promise<boolean> => {
    setBusy(true);
    setError(null);
    try {
      await fn();
      if (success) toast.push("success", success);
      return true;
    } catch (err) {
      const msg = err instanceof ApiError ? err.message : failure;
      setError(msg);
      return false;
    } finally {
      setBusy(false);
    }
  }, [toast]);

  return { run, busy, error, setError };
}
