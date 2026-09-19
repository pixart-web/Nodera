"use client";

import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { api, ApiError } from "@/lib/api";
import { useApi } from "@/lib/useApi";
import { PageHeader } from "@/components/PageHeader";
import { ErrorBanner } from "@/components/ErrorBanner";
import type { Member, Organization, Role } from "@/lib/types";

function OrganizationForm({ org, onUpdated }: { org: Organization; onUpdated: () => void }) {
  const [name, setName] = useState(org.name);
  const [slug, setSlug] = useState(org.slug);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  // Keep the form in sync if the org reloads with a different value (e.g.
  // another admin renamed it in another tab).
  useEffect(() => {
    setName(org.name);
    setSlug(org.slug);
  }, [org.name, org.slug]);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setBusy(true);
    try {
      await api.put("/api/v1/organization", { name, slug });
      onUpdated();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to update organization");
    } finally {
      setBusy(false);
    }
  }

  return (
    <form onSubmit={submit} className="card mb-8 space-y-3 p-4">
      {error && <ErrorBanner message={error} />}
      <div className="grid grid-cols-2 gap-3">
        <div>
          <label className="label" htmlFor="org-name">
            Name
          </label>
          <input id="org-name" className="input" value={name} onChange={(e) => setName(e.target.value)} required />
        </div>
        <div>
          <label className="label" htmlFor="org-slug">
            Slug
          </label>
          <input
            id="org-slug"
            className="input font-mono"
            value={slug}
            onChange={(e) => setSlug(e.target.value)}
            placeholder="lowercase-with-hyphens"
            required
          />
        </div>
      </div>
      <button type="submit" className="btn-primary" disabled={busy}>
        {busy ? "Saving…" : "Save organization"}
      </button>
    </form>
  );
}

function LeaveOrganizationSection() {
  const router = useRouter();
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function leave() {
    setError(null);
    setBusy(true);
    try {
      await api.post("/api/v1/organization/leave");
      router.push("/orgs");
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to leave organization");
      setBusy(false);
    }
  }

  return (
    <div className="card mb-8 space-y-2 border-danger/30 p-4">
      {error && <ErrorBanner message={error} />}
      <p className="text-xs text-base-400">
        Removes your own membership from this organization. You&apos;ll keep your account and any other
        organizations you belong to — this only affects this one. Refused if you&apos;re the organization&apos;s
        last remaining owner.
      </p>
      <button className="text-xs text-base-400 hover:text-danger" disabled={busy} onClick={leave}>
        {busy ? "Leaving…" : "Leave this organization"}
      </button>
    </div>
  );
}

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

function parseList(raw: string): string[] {
  return raw
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean);
}

function CreateRoleForm({ onDone }: { onDone: () => void }) {
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [permissions, setPermissions] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setBusy(true);
    try {
      await api.post("/api/v1/roles", { name, description, permissions: parseList(permissions) });
      setName("");
      setDescription("");
      setPermissions("");
      onDone();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to create role");
    } finally {
      setBusy(false);
    }
  }

  return (
    <form onSubmit={submit} className="card mb-4 space-y-3 p-4">
      {error && <ErrorBanner message={error} />}
      <div className="grid grid-cols-2 gap-3">
        <div>
          <label className="label" htmlFor="role-name">
            Name
          </label>
          <input id="role-name" className="input" value={name} onChange={(e) => setName(e.target.value)} required />
        </div>
        <div>
          <label className="label" htmlFor="role-description">
            Description
          </label>
          <input
            id="role-description"
            className="input"
            value={description}
            onChange={(e) => setDescription(e.target.value)}
          />
        </div>
      </div>
      <div>
        <label className="label" htmlFor="role-permissions">
          Permissions (comma-separated)
        </label>
        <input
          id="role-permissions"
          className="input font-mono"
          value={permissions}
          onChange={(e) => setPermissions(e.target.value)}
          placeholder="audit.read, infrastructure.read"
        />
      </div>
      <p className="text-xs text-base-400">
        Permissions can never exceed your own held permissions — the API rejects anything broader (no privilege
        escalation).
      </p>
      <button type="submit" className="btn-primary" disabled={busy}>
        {busy ? "Creating…" : "Create role"}
      </button>
    </form>
  );
}

function EditRoleForm({ role, onDone }: { role: Role; onDone: () => void }) {
  const [name, setName] = useState(role.name);
  const [description, setDescription] = useState(role.description);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setBusy(true);
    try {
      await api.put(`/api/v1/roles/${role.id}`, { name, description });
      onDone();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to update role");
    } finally {
      setBusy(false);
    }
  }

  return (
    <form onSubmit={submit} className="flex items-center gap-2">
      {error && <span className="text-xs text-danger">{error}</span>}
      <input
        className="input w-28 py-1 text-xs"
        value={name}
        onChange={(e) => setName(e.target.value)}
        required
      />
      <input
        className="input w-40 py-1 text-xs"
        value={description}
        onChange={(e) => setDescription(e.target.value)}
        placeholder="description"
      />
      <button type="submit" className="text-xs text-ok hover:underline" disabled={busy}>
        {busy ? "Saving…" : "Save"}
      </button>
      <button type="button" className="text-xs text-base-500 hover:text-base-300" onClick={onDone}>
        Cancel
      </button>
    </form>
  );
}

function AddMemberForm({ onDone }: { onDone: () => void }) {
  const [email, setEmail] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setBusy(true);
    try {
      await api.post("/api/v1/organization/members", { email });
      setEmail("");
      onDone();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to add member");
    } finally {
      setBusy(false);
    }
  }

  return (
    <form onSubmit={submit} className="card mb-4 space-y-3 p-4">
      {error && <ErrorBanner message={error} />}
      <div>
        <label className="label" htmlFor="add-member-email">
          Email
        </label>
        <input
          id="add-member-email"
          className="input"
          type="email"
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          placeholder="user must already have an account"
          required
        />
      </div>
      <p className="text-xs text-base-400">
        Adds an existing Nodera account as a member of this organization (starting with the `member` role) — this
        does not create an account or send an invite email.
      </p>
      <button type="submit" className="btn-primary" disabled={busy}>
        {busy ? "Adding…" : "Add member"}
      </button>
    </form>
  );
}

export default function SettingsPage() {
  const org = useApi(() => api.get<Organization>("/api/v1/organization"), []);
  const roles = useApi(() => api.get<Role[]>("/api/v1/roles"), []);
  const members = useApi(() => api.get<Member[]>("/api/v1/organization/members"), []);
  const [error, setError] = useState<string | null>(null);
  const [assigningFor, setAssigningFor] = useState<string | null>(null);
  const [showAddMember, setShowAddMember] = useState(false);
  const [showCreateRole, setShowCreateRole] = useState(false);
  const [roleError, setRoleError] = useState<string | null>(null);
  const [editingRole, setEditingRole] = useState<string | null>(null);

  async function revoke(userID: string, roleID: string) {
    setError(null);
    try {
      await api.del(`/api/v1/organization/members/${userID}/roles/${roleID}`);
      members.reload();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to revoke role");
    }
  }

  async function deleteRole(roleID: string) {
    setRoleError(null);
    try {
      await api.del(`/api/v1/roles/${roleID}`);
      roles.reload();
    } catch (err) {
      setRoleError(err instanceof ApiError ? err.message : "Failed to delete role");
    }
  }

  const [removingMemberID, setRemovingMemberID] = useState<string | null>(null);

  async function removeMember(userID: string) {
    setError(null);
    setRemovingMemberID(userID);
    try {
      await api.del(`/api/v1/organization/members/${userID}`);
      members.reload();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to remove member");
    } finally {
      setRemovingMemberID(null);
    }
  }

  return (
    <div>
      <PageHeader
        title="Settings"
        description="Organization details, roles, and membership. Roles are seeded (owner/admin/member) and not yet creatable from here — this page manages who holds which of them."
      />

      {org.error && <ErrorBanner message={org.error} />}
      {org.data && (
        <div className="mb-8">
          <h2 className="mb-3 text-sm font-medium text-base-100">Organization</h2>
          <OrganizationForm org={org.data} onUpdated={() => org.reload()} />
          <LeaveOrganizationSection />
        </div>
      )}

      <div className="mb-8">
        <div className="mb-3 flex items-center justify-between">
          <h2 className="text-sm font-medium text-base-100">Roles</h2>
          <button className="btn-primary" onClick={() => setShowCreateRole((v) => !v)}>
            {showCreateRole ? "Cancel" : "Create role"}
          </button>
        </div>
        {showCreateRole && (
          <CreateRoleForm
            onDone={() => {
              setShowCreateRole(false);
              roles.reload();
            }}
          />
        )}
        {roles.error && <ErrorBanner message={roles.error} />}
        {roleError && <ErrorBanner message={roleError} />}
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
                  <th></th>
                </tr>
              </thead>
              <tbody>
                {roles.data!.map((r) =>
                  editingRole === r.id ? (
                    <tr key={r.id}>
                      <td colSpan={4}>
                        <EditRoleForm
                          role={r}
                          onDone={() => {
                            setEditingRole(null);
                            roles.reload();
                          }}
                        />
                      </td>
                    </tr>
                  ) : (
                    <tr key={r.id}>
                      <td className="font-mono text-xs">{r.name}</td>
                      <td className="text-xs text-base-300">{r.description}</td>
                      <td className="text-xs text-base-400">
                        {r.permissions.length > 8 ? `${r.permissions.length} permissions` : r.permissions.join(", ") || "—"}
                      </td>
                      <td>
                        {r.is_system ? (
                          <span className="text-xs text-base-500">system</span>
                        ) : (
                          <span className="space-x-3">
                            <button
                              className="text-xs text-accent-400 hover:text-accent-300"
                              onClick={() => setEditingRole(r.id)}
                            >
                              Edit
                            </button>
                            <button className="text-xs text-base-400 hover:text-danger" onClick={() => deleteRole(r.id)}>
                              Delete
                            </button>
                          </span>
                        )}
                      </td>
                    </tr>
                  ),
                )}
              </tbody>
            </table>
          )}
        </div>
      </div>

      <div>
        <div className="mb-3 flex items-center justify-between">
          <h2 className="text-sm font-medium text-base-100">Members</h2>
          <button className="btn-primary" onClick={() => setShowAddMember((v) => !v)}>
            {showAddMember ? "Cancel" : "Add member"}
          </button>
        </div>
        {showAddMember && (
          <AddMemberForm
            onDone={() => {
              setShowAddMember(false);
              members.reload();
            }}
          />
        )}
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
                    <td className="space-x-3">
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
                        <>
                          <button
                            className="text-xs text-accent-400 hover:text-accent-300"
                            onClick={() => setAssigningFor(m.user_id)}
                          >
                            Assign role
                          </button>
                          <button
                            className="text-xs text-base-400 hover:text-danger disabled:text-base-600"
                            disabled={removingMemberID === m.user_id}
                            onClick={() => removeMember(m.user_id)}
                          >
                            {removingMemberID === m.user_id ? "Removing…" : "Remove"}
                          </button>
                        </>
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
