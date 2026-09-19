"use client";

import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { api, ApiError } from "@/lib/api";
import { clearSession, getStoredUser, setCurrentOrgId } from "@/lib/session";
import type { Organization } from "@/lib/types";

export default function OrgsPage() {
  const router = useRouter();
  const [orgs, setOrgs] = useState<Organization[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [name, setName] = useState("");
  const [slug, setSlug] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (!getStoredUser()) {
      router.replace("/login");
      return;
    }
    api
      .get<Organization[]>("/api/v1/organizations", false)
      .then((data) => setOrgs(data ?? []))
      .catch((err) => setError(err instanceof ApiError ? err.message : "Failed to load organizations"));
  }, [router]);

  function selectOrg(id: string) {
    setCurrentOrgId(id);
    router.replace("/dashboard");
  }

  async function createOrg(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setBusy(true);
    try {
      const org = await api.post<Organization>("/api/v1/organizations", { name, slug }, false);
      setCurrentOrgId(org.id);
      router.replace("/dashboard");
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to create organization");
    } finally {
      setBusy(false);
    }
  }

  async function logout() {
    try {
      await api.post("/api/v1/auth/logout", undefined, false);
    } catch {
      // Best-effort — see the (org)/layout.tsx logout handler.
    }
    clearSession();
    router.replace("/login");
  }

  return (
    <div className="mx-auto max-w-lg px-4 py-16">
      <div className="mb-6 flex items-center justify-between">
        <h1 className="text-lg font-medium text-base-100">Choose an organization</h1>
        <button className="text-sm text-base-400 hover:text-base-200" onClick={logout}>
          Sign out
        </button>
      </div>

      {error && (
        <div className="mb-4 rounded-md border border-danger/30 bg-danger/10 px-3 py-2 text-sm text-danger">
          {error}
        </div>
      )}

      {orgs === null ? (
        <div className="text-sm text-base-400">Loading…</div>
      ) : orgs.length > 0 ? (
        <div className="card mb-6 divide-y divide-base-700">
          {orgs.map((o) => (
            <button
              key={o.id}
              onClick={() => selectOrg(o.id)}
              className="flex w-full items-center justify-between px-4 py-3 text-left hover:bg-base-800/60"
            >
              <div>
                <div className="text-sm font-medium text-base-100">{o.name}</div>
                <div className="font-mono text-xs text-base-400">{o.slug}</div>
              </div>
              <span className="text-base-500">→</span>
            </button>
          ))}
        </div>
      ) : (
        <p className="mb-6 text-sm text-base-400">You don&apos;t belong to any organization yet.</p>
      )}

      <div className="card p-4">
        <h2 className="mb-3 text-sm font-medium text-base-100">Create a new organization</h2>
        <form onSubmit={createOrg} className="space-y-3">
          <div>
            <label className="label" htmlFor="org-name">
              Name
            </label>
            <input
              id="org-name"
              className="input"
              value={name}
              onChange={(e) => setName(e.target.value)}
              required
            />
          </div>
          <div>
            <label className="label" htmlFor="org-slug">
              Slug
            </label>
            <input
              id="org-slug"
              className="input"
              placeholder="lowercase-with-hyphens"
              value={slug}
              onChange={(e) => setSlug(e.target.value)}
              pattern="[a-z0-9]+(-[a-z0-9]+)*"
              required
            />
          </div>
          <button type="submit" className="btn-primary w-full" disabled={busy}>
            {busy ? "Creating…" : "Create organization"}
          </button>
        </form>
      </div>
    </div>
  );
}
