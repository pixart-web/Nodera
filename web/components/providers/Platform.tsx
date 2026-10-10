"use client";

import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { api } from "@/lib/api";
import type { ApiSystemInfo } from "@/lib/types";

interface PlatformState {
  system: ApiSystemInfo | null;
  permissions: Set<string> | null;
  // can() only decides what the UI SHOWS. The API authorises every request
  // regardless, so hiding a button is a convenience, never the control.
  can: (perm: string) => boolean;
  reload: () => void;
}

const Ctx = createContext<PlatformState>({ system: null, permissions: null, can: () => true, reload: () => {} });
export const usePlatform = () => useContext(Ctx);

export function PlatformProvider({ children }: { children: ReactNode }) {
  const [system, setSystem] = useState<ApiSystemInfo | null>(null);
  const [permissions, setPermissions] = useState<Set<string> | null>(null);
  const load = useCallback(() => {
    api.get<ApiSystemInfo>("/api/v1/system/info").then(setSystem).catch(() => setSystem(null));
    api.get<{ permissions: string[] }>("/api/v1/permissions/mine").then((r) => setPermissions(new Set(r.permissions))).catch(() => setPermissions(null));
  }, []);
  useEffect(load, [load]);
  const value = useMemo<PlatformState>(() => ({
    system, permissions, reload: load,
    // While permissions are unknown we show everything: the API will refuse anything not allowed.
    can: (perm) => (permissions ? permissions.has(perm) : true),
  }), [system, permissions, load]);
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}
