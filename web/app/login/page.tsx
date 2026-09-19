"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import { api, ApiError } from "@/lib/api";
import { setCurrentOrgId, setStoredUser } from "@/lib/session";
import type { Organization, User } from "@/lib/types";

interface LoginResponse {
  session_token: string;
  user: User;
  organizations: Organization[] | null;
}

interface SignUpResponse {
  id: string;
  email: string;
  display_name: string;
}

export default function LoginPage() {
  const router = useRouter();
  const [mode, setMode] = useState<"login" | "signup">("login");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setBusy(true);
    try {
      if (mode === "signup") {
        await api.postPublic<SignUpResponse>("/api/v1/auth/signup", {
          email,
          password,
          display_name: displayName,
        });
      }

      // The API also sets the real session as an HttpOnly cookie on this
      // response (docs/SECURITY.md) — this client deliberately never
      // reads or stores res.session_token; setStoredUser below is only a
      // non-sensitive "who's probably logged in" hint for client-side
      // routing (see lib/session.ts).
      const res = await api.postPublic<LoginResponse>("/api/v1/auth/login", { email, password });
      setStoredUser(res.user);

      const orgs = res.organizations ?? [];
      if (orgs.length === 1 && orgs[0]) {
        setCurrentOrgId(orgs[0].id);
        router.replace("/dashboard");
      } else {
        router.replace("/orgs");
      }
    } catch (err) {
      if (err instanceof ApiError) {
        setError(err.message);
      } else {
        setError("Something went wrong. Is the Nodera API running?");
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="flex min-h-screen items-center justify-center px-4">
      <div className="w-full max-w-sm">
        <div className="mb-8 text-center">
          <div className="mb-1 font-mono text-2xl font-semibold tracking-tight text-base-100">
            nodera
          </div>
          <div className="text-sm text-base-400">Infrastructure &amp; AI control plane</div>
        </div>

        <div className="card p-6">
          <div className="mb-5 flex gap-1 rounded-md bg-base-800 p-1 text-sm">
            <button
              type="button"
              className={`flex-1 rounded px-3 py-1.5 transition-colors ${
                mode === "login" ? "bg-base-700 text-base-100" : "text-base-400"
              }`}
              onClick={() => setMode("login")}
            >
              Sign in
            </button>
            <button
              type="button"
              className={`flex-1 rounded px-3 py-1.5 transition-colors ${
                mode === "signup" ? "bg-base-700 text-base-100" : "text-base-400"
              }`}
              onClick={() => setMode("signup")}
            >
              Create account
            </button>
          </div>

          <form onSubmit={handleSubmit} className="space-y-4">
            {mode === "signup" && (
              <div>
                <label className="label" htmlFor="displayName">
                  Name
                </label>
                <input
                  id="displayName"
                  className="input"
                  value={displayName}
                  onChange={(e) => setDisplayName(e.target.value)}
                  required
                />
              </div>
            )}
            <div>
              <label className="label" htmlFor="email">
                Email
              </label>
              <input
                id="email"
                type="email"
                className="input"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                required
              />
            </div>
            <div>
              <label className="label" htmlFor="password">
                Password
              </label>
              <input
                id="password"
                type="password"
                className="input"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                minLength={12}
                required
              />
              {mode === "signup" && (
                <p className="mt-1 text-xs text-base-400">
                  At least 12 characters, mixing letters with a number or symbol.
                </p>
              )}
            </div>

            {error && (
              <div className="rounded-md border border-danger/30 bg-danger/10 px-3 py-2 text-sm text-danger">
                {error}
              </div>
            )}

            <button type="submit" className="btn-primary w-full" disabled={busy}>
              {busy ? "Working…" : mode === "login" ? "Sign in" : "Create account"}
            </button>
          </form>
        </div>
      </div>
    </div>
  );
}
