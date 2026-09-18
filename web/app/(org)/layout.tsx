"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import { clearSession, getCurrentOrgId, getSessionToken, getStoredUser } from "@/lib/session";

const NAV = [
  { href: "/dashboard", label: "Dashboard" },
  { href: "/infrastructure", label: "Infrastructure" },
  { href: "/applications", label: "Applications" },
  { href: "/jobs", label: "Jobs" },
  { href: "/tools", label: "Tools" },
  { href: "/secrets", label: "Secrets" },
  { href: "/access", label: "Access" },
  { href: "/audit", label: "Audit" },
];

export default function OrgLayout({ children }: { children: React.ReactNode }) {
  const router = useRouter();
  const pathname = usePathname();
  const [ready, setReady] = useState(false);

  useEffect(() => {
    if (!getSessionToken()) {
      router.replace("/login");
      return;
    }
    if (!getCurrentOrgId()) {
      router.replace("/orgs");
      return;
    }
    setReady(true);
  }, [router]);

  if (!ready) {
    return <div className="flex min-h-screen items-center justify-center text-base-400">Loading…</div>;
  }

  const user = getStoredUser();

  function logout() {
    clearSession();
    router.replace("/login");
  }

  return (
    <div className="flex min-h-screen">
      <aside className="flex w-56 shrink-0 flex-col border-r border-base-800 bg-base-900">
        <div className="border-b border-base-800 px-4 py-4">
          <div className="font-mono text-lg font-semibold text-base-100">nodera</div>
        </div>
        <nav className="flex-1 space-y-0.5 px-2 py-3">
          {NAV.map((item) => {
            const active = pathname === item.href;
            return (
              <Link
                key={item.href}
                href={item.href}
                className={`block rounded-md px-3 py-1.5 text-sm transition-colors ${
                  active
                    ? "bg-accent-600/15 text-accent-400"
                    : "text-base-300 hover:bg-base-800 hover:text-base-100"
                }`}
              >
                {item.label}
              </Link>
            );
          })}
        </nav>
        <div className="border-t border-base-800 px-4 py-3">
          <button
            onClick={() => router.push("/orgs")}
            className="mb-2 block w-full truncate text-left text-xs text-base-400 hover:text-base-200"
          >
            Switch organization
          </button>
          <div className="flex items-center justify-between">
            <span className="truncate text-xs text-base-400">{user?.email}</span>
            <button onClick={logout} className="text-xs text-base-400 hover:text-danger">
              Sign out
            </button>
          </div>
        </div>
      </aside>
      <main className="min-w-0 flex-1 overflow-x-auto">
        <div className="mx-auto max-w-6xl px-6 py-8">{children}</div>
      </main>
    </div>
  );
}
