"use client";

import { useState } from "react";
import { api, ApiError } from "@/lib/api";
import { useApi } from "@/lib/useApi";
import { PageHeader } from "@/components/PageHeader";
import { ErrorBanner } from "@/components/ErrorBanner";
import type { Member, Role } from "@/lib/types";

function AssignRoleForm({ member, roles, onDone }: { member: Member; roles: Role[]; onDone: () => void }) {
  const assignable = roles.filter((r) => !member.roles.some((mr) => mr.role_id === r.id));
  const [roleID, setRoleID] = useState(assignable[0]?.id ?? "");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setBusy(true);
    try {
      await api.post(`/api/v1/organization/members/${member.user_id}/roles`, { role_id: roleID });
      onDone();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to assign role");
    } finally {
      setBusy(false);
    }
  }

  if (assignable.length === 0) {
    return <span className="text-xs text-base-500">Already holds every role.</span>;
  }

  return (
    <form onSubmit={submit} className="flex items-center gap-2">
      {error && <span className="text-xs text-danger">{error}</span>}
      <select className="input w-32 py-1 text-xs" value={roleID} onChange={(e) => setRoleID(e.target.value)}>
        {assignable.map((r) => (
          <option key={r.id} value={r.id}>
            {r.name}
          </option>
        ))}
      </select>
      <button type="submit" className="text-xs text-accent-400 hover:text-accent-300" disabled={busy}>
        {busy ? "Assigning…" : "Assign"}
      </button>
    </form>
  );
}

export default function SettingsPage() {
  const roles = useApi(() => api.get<Role[]>("/api/v1/roles"), []);
  const members = useApi(() => api.get<Member[]>("/api/v1/organization/members"), []);
  const [error, setError] = useState<string | null>(null);
  const [assigningFor, setAssigningFor] = useState<string | null>(null);

  async function revoke(userID: string, roleID: string) {
    setError(null);
    try {
      await api.del(`/api/v1/organization/members/${userID}/roles/${roleID}`);
      members.reload();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to revoke role");
    }
  }

  return (
    <div>
      <PageHeader
        title="Settings"
        description="Roles and organization membership. Roles are seeded (owner/admin/member) and not yet creatable from here — this page manages who holds which of them."
      />

      <div className="mb-8">
        <h2 className="mb-3 text-sm font-medium text-base-100">Roles</h2>
        {roles.error && <ErrorBanner message={roles.error} />}
        <div className="card">
          {roles.loading ? (
            <div className="p-4 text-sm text-base-400">Loading…</div>
          ) : (roles.data ?? []).length === 0 ? (
            <div className="p-4 text-sm text-base-400">No roles visible to this organization.</div>
          ) : (
            <table className="data-table">
              <thead>
                <tr>
                  <th>Name</th>
                  <th>Description</th>
                  <th>Permissions</th>
                </tr>
              </thead>
              <tbody>
                {roles.data!.map((r) => (
                  <tr key={r.id}>
                    <td className="font-mono text-xs">{r.name}</td>
                    <td className="text-xs text-base-300">{r.description}</td>
                    <td className="text-xs text-base-400">
                      {r.permissions.length > 8 ? `${r.permissions.length} permissions` : r.permissions.join(", ") || "—"}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      </div>

      <div>
        <h2 className="mb-3 text-sm font-medium text-base-100">Members</h2>
        {members.error && <ErrorBanner message={members.error} />}
        {error && <ErrorBanner message={error} />}
        <div className="card">
          {members.loading ? (
            <div className="p-4 text-sm text-base-400">Loading…</div>
          ) : (members.data ?? []).length === 0 ? (
            <div className="p-4 text-sm text-base-400">No members.</div>
          ) : (
            <table className="data-table">
              <thead>
                <tr>
                  <th>Member</th>
                  <th>Roles</th>
                  <th></th>
                </tr>
              </thead>
              <tbody>
                {members.data!.map((m) => (
                  <tr key={m.user_id}>
                    <td className="text-xs text-base-300">
                      {m.display_name} <span className="text-base-500">({m.email})</span>
                    </td>
                    <td className="space-x-2 text-xs">
                      {m.roles.length === 0 ? (
                        <span className="text-base-500">no roles</span>
                      ) : (
                        m.roles.map((r) => (
                          <span key={r.role_id} className="badge bg-base-500/20 text-base-300">
                            {r.name}{" "}
                            <button
                              className="ml-1 text-base-500 hover:text-danger"
                              title="Revoke"
                              onClick={() => revoke(m.user_id, r.role_id)}
                            >
                              ×
                            </button>
                          </span>
                        ))
                      )}
                    </td>
                    <td>
                      {assigningFor === m.user_id ? (
                        <AssignRoleForm
                          member={m}
                          roles={roles.data ?? []}
                          onDone={() => {
                            setAssigningFor(null);
                            members.reload();
                          }}
                        />
                      ) : (
                        <button
                          className="text-xs text-accent-400 hover:text-accent-300"
                          onClick={() => setAssigningFor(m.user_id)}
                        >
                          Assign role
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
    </div>
  );
}
