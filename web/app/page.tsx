"use client";

import { useEffect } from "react";
import { useRouter } from "next/navigation";
import { getCurrentOrgId, getStoredUser } from "@/lib/session";

export default function RootPage() {
  const router = useRouter();

  useEffect(() => {
    // getStoredUser is a client-side hint only (docs/SECURITY.md — the
    // real session lives in an HttpOnly cookie this code can't read); a
    // stale hint just means the next API call 401s and lib/api.ts sends
    // the user back to /login anyway.
    if (!getStoredUser()) {
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
