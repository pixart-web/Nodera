"use client";

import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { AppShell } from "@/components/shell/AppShell";
import { getCurrentOrgId, getStoredUser } from "@/lib/session";

export default function OrgLayout({ children }: { children: React.ReactNode }) {
  const router = useRouter();
  const [ready, setReady] = useState(false);

  useEffect(() => {
    if (!getStoredUser()) { router.replace("/login"); return; }
    if (!getCurrentOrgId()) { router.replace("/orgs"); return; }
    setReady(true);
  }, [router]);

  if (!ready) {
    return <div className="flex min-h-screen items-center justify-center bg-nd-bg text-nd-muted" role="status">A carregar…</div>;
  }
  return <AppShell>{children}</AppShell>;
}
