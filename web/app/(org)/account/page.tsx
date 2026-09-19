"use client";

import { useState } from "react";
import { api, ApiError } from "@/lib/api";
import { useApi } from "@/lib/useApi";
import { PageHeader } from "@/components/PageHeader";
import { ErrorBanner } from "@/components/ErrorBanner";
import { getStoredUser, setStoredUser } from "@/lib/session";
import type { Session, User } from "@/lib/types";

function ProfileForm() {
  const stored = getStoredUser();
  const [displayName, setDisplayName] = useState(stored?.display_name ?? "");
  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState(false);
  const [busy, setBusy] = useState(false);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setSuccess(false);
    setBusy(true);
    try {
      const updated = await api.put<User>("/api/v1/account/profile", { display_name: displayName });
      setStoredUser({ id: updated.id, email: updated.email, display_name: updated.display_name });
      setSuccess(true);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to update profile");
    } finally {
      setBusy(false);
    }
  }

  return (
    <form onSubmit={submit} className="card space-y-3 p-4">
      {error && <ErrorBanner message={error} />}
      {success && <div className="text-xs text-ok">Profile updated.</div>}
      <div>
        <label className="label" htmlFor="account-email">
          Email
        </label>
        <input id="account-email" className="input" value={stored?.email ?? ""} disabled />
        <p className="mt-1 text-xs text-base-500">Email cannot be changed here.</p>
      </div>
      <div>
        <label className="label" htmlFor="account-display-name">
          Display name
        </label>
        <input
          id="account-display-name"
          className="input"
          value={displayName}
          onChange={(e) => setDisplayName(e.target.value)}
          required
        />
      </div>
      <button type="submit" className="btn-primary" disabled={busy}>
        {busy ? "Saving…" : "Save"}
      </button>
    </form>
  );
}

function ChangePasswordForm() {
  const [currentPassword, setCurrentPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState(false);
  const [busy, setBusy] = useState(false);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setSuccess(false);
    setBusy(true);
    try {
      await api.post("/api/v1/account/password", { current_password: currentPassword, new_password: newPassword });
      setCurrentPassword("");
      setNewPassword("");
      setSuccess(true);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to change password");
    } finally {
      setBusy(false);
    }
  }

  return (
    <form onSubmit={submit} className="card space-y-3 p-4">
      {error && <ErrorBanner message={error} />}
      {success && (
        <div className="text-xs text-ok">
          Password changed. Every other active session was signed out; this one was not.
        </div>
      )}
      <div>
        <label className="label" htmlFor="current-password">
          Current password
        </label>
        <input
          id="current-password"
          className="input"
          type="password"
          value={currentPassword}
          onChange={(e) => setCurrentPassword(e.target.value)}
          required
        />
      </div>
      <div>
        <label className="label" htmlFor="new-password">
          New password
        </label>
        <input
          id="new-password"
          className="input"
          type="password"
          value={newPassword}
          onChange={(e) => setNewPassword(e.target.value)}
          placeholder="At least 12 characters, mixing letters with a number or symbol"
          required
        />
      </div>
      <button type="submit" className="btn-primary" disabled={busy}>
        {busy ? "Changing…" : "Change password"}
      </button>
    </form>
  );
}

function SessionsList() {
  const sessions = useApi(() => api.get<Session[]>("/api/v1/account/sessions"), []);
  const [error, setError] = useState<string | null>(null);

  async function revoke(id: string) {
    setError(null);
    try {
      await api.del(`/api/v1/account/sessions/${id}`);
      sessions.reload();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to revoke session");
    }
  }

  return (
    <div>
      {sessions.error && <ErrorBanner message={sessions.error} />}
      {error && <ErrorBanner message={error} />}
      <div className="card">
        {sessions.loading ? (
          <div className="p-4 text-sm text-base-400">Loading…</div>
        ) : (sessions.data ?? []).length === 0 ? (
          <div className="p-4 text-sm text-base-400">No active sessions.</div>
        ) : (
          <table className="data-table">
            <thead>
              <tr>
                <th>Device / IP</th>
                <th>Created</th>
                <th>Expires</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {sessions.data!.map((s) => (
                <tr key={s.id}>
                  <td className="text-xs text-base-300">
                    {s.user_agent || "unknown device"} <span className="text-base-500">({s.ip_address || "unknown IP"})</span>
                    {s.is_current && <span className="badge ml-2 bg-ok/15 text-ok">this session</span>}
                  </td>
                  <td className="text-xs text-base-400">{new Date(s.created_at).toLocaleString()}</td>
                  <td className="text-xs text-base-400">{new Date(s.expires_at).toLocaleString()}</td>
                  <td>
                    {!s.is_current && (
                      <button className="text-xs text-base-400 hover:text-danger" onClick={() => revoke(s.id)}>
                        Log out
                      </button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}

export default function AccountPage() {
  return (
    <div>
      <PageHeader title="Account" description="Your own profile and password. Not organization-scoped." />

      <div className="mb-8">
        <h2 className="mb-3 text-sm font-medium text-base-100">Profile</h2>
        <ProfileForm />
      </div>

      <div className="mb-8">
        <h2 className="mb-3 text-sm font-medium text-base-100">Password</h2>
        <ChangePasswordForm />
      </div>

      <div>
        <h2 className="mb-3 text-sm font-medium text-base-100">Active sessions</h2>
        <SessionsList />
      </div>
    </div>
  );
}
