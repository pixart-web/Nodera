"use client";

import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { AppShell } from "@/components/shell/AppShell";
import { getCurrentOrgId, getStoredUser, setCurrentOrgId } from "@/lib/session";
import { api } from "@/lib/api";
import type { Organization } from "@/lib/types";

export default function OrgLayout({ children }: { children: React.ReactNode }) {
  const router = useRouter();
  const [ready, setReady] = useState(false);

  useEffect(() => {
    if (!getStoredUser()) { router.replace("/login"); return; }
    const current = getCurrentOrgId();
    if (!current) {
      api.get<Organization[]>("/api/v1/organizations", false).then((orgs) => {
        const preferred = orgs.find((o) => o.slug === "pixart" || o.name.toLowerCase() === "pixart") ?? (orgs.length === 1 ? orgs[0] : null);
        if (preferred) { setCurrentOrgId(preferred.id); setReady(true); } else router.replace("/orgs");
      }).catch(() => router.replace("/orgs"));
      return;
    }
    setReady(true);
  }, [router]);

  if (!ready) {
    return <div className="flex min-h-screen items-center justify-center bg-nd-bg text-nd-muted" role="status">A carregar…</div>;
  }
  return <AppShell>{children}</AppShell>;
}
