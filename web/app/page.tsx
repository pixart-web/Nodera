"use client";

import { useEffect } from "react";
import { useRouter } from "next/navigation";
import { getCurrentOrgId, getSessionToken } from "@/lib/session";

export default function RootPage() {
  const router = useRouter();

  useEffect(() => {
    if (!getSessionToken()) {
      router.replace("/login");
    } else if (!getCurrentOrgId()) {
      router.replace("/orgs");
    } else {
      router.replace("/dashboard");
    }
  }, [router]);

  return (
    <div className="flex min-h-screen items-center justify-center text-base-400">
      Loading Nodera…
    </div>
  );
}
