"use client";

import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { Building2, LogOut, Pencil, Plus, ShieldCheck, Trash2, UserMinus, UserPlus, Users } from "lucide-react";
import { api } from "@/lib/api";
import { useApi } from "@/lib/useApi";
import { useAction } from "@/lib/useAction";
import { PageHeader } from "@/components/ui/PageHeader";
import { ChannelsPanel, FeatureFlagsPanel, RetentionPanel } from "@/components/settings/PlatformPanels";
import { Card, Section } from "@/components/ui/Card";
import { Button } from "@/components/ui/Button";
import { Alert, ErrorState, LoadingState } from "@/components/ui/Feedback";
import { Badge } from "@/components/ui/Status";
import { DataTable, type Column } from "@/components/ui/DataTable";
import { ConfirmDialog, Modal } from "@/components/ui/Overlay";
import { Field, Input, Select } from "@/components/ui/Forms";
import { RowMenu } from "@/components/ui/RowMenu";
import { TabPanel, Tabs } from "@/components/ui/Tabs";
import type { Member, Organization, Role } from "@/lib/types";

const parseList = (raw: string): string[] => raw.split(",").map((s) => s.trim()).filter(Boolean);

function OrganizationPanel({ org, onUpdated }: { org: Organization; onUpdated: () => void }) {
  const router = useRouter();
  const save = useAction();
  const leave = useAction();
  const [name, setName] = useState(org.name); const [slug, setSlug] = useState(org.slug);
  const [confirmLeave, setConfirmLeave] = useState(false);
  // Keep the form in sync if the org reloads with a different value.
  useEffect(() => { setName(org.name); setSlug(org.slug); }, [org.name, org.slug]);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (await save.run(() => api.put("/api/v1/organization", { name, slug }), "Organização guardada.")) onUpdated();
  }
  async function doLeave() {
    if (await leave.run(() => api.post("/api/v1/organization/leave"), "Saíste da organização.")) router.push("/orgs");
  }

  return (
    <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_380px]">
      <Section title="Detalhes da organização">
        <form onSubmit={submit} className="space-y-4">
          {save.error && <Alert tone="error">{save.error}</Alert>}
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label="Nome">{(id) => <Input id={id} value={name} onChange={(e) => setName(e.target.value)} required />}</Field>
            <Field label="Slug">{(id) => <Input id={id} className="font-mono" value={slug} onChange={(e) => setSlug(e.target.value)} placeholder="minusculas-com-hifens" required />}</Field>
          </div>
          <Button variant="primary" type="submit" disabled={save.busy}>{save.busy ? "A guardar…" : "Guardar organização"}</Button>
        </form>
      </Section>
      <Card className="border-nd-danger/25 p-5">
        <h2 className="text-base font-semibold text-nd-text">Zona de risco</h2>
        <p className="mt-1.5 text-sm text-nd-muted">Remove a tua própria pertença a esta organização. Manténs a conta e as outras organizações. Recusado se fores o último owner.</p>
        {leave.error && <Alert tone="error" className="mt-3">{leave.error}</Alert>}
        <Button variant="danger" className="mt-4" icon={<LogOut className="h-4 w-4" />} onClick={() => setConfirmLeave(true)}>Sair desta organização</Button>
        <ConfirmDialog open={confirmLeave} onClose={() => setConfirmLeave(false)} onConfirm={doLeave} danger confirmLabel="Sair" title="Sair da organização?" description="Perdes o acesso aos recursos desta organização até seres adicionado de novo." />
      </Card>
    </div>
  );
}

function RoleModal({ role, open, onClose, onSaved }: { role: Role | null; open: boolean; onClose: () => void; onSaved: () => void }) {
  const action = useAction();
  const [name, setName] = useState(""); const [description, setDescription] = useState(""); const [perms, setPerms] = useState("");
  const [loaded, setLoaded] = useState<string | null>(null);
  const key = open ? (role?.id ?? "new") : null;
  if (key !== loaded) { setLoaded(key); if (open) { setName(role?.name ?? ""); setDescription(role?.description ?? ""); setPerms(""); } }

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    const ok = await action.run(() => (role ? api.put(`/api/v1/roles/${role.id}`, { name, description }) : api.post("/api/v1/roles", { name, description, permissions: parseList(perms) })), role ? "Role atualizada." : "Role criada.");
    if (ok) { onSaved(); onClose(); }
  }
  return (
    <Modal open={open} onClose={onClose} title={role ? `Editar ${role.name}` : "Criar role"} description={role ? "Só o nome e a descrição mudam aqui." : "As permissões nunca podem exceder as tuas — a API rejeita qualquer escalada."}
      footer={<><Button onClick={onClose}>Cancelar</Button><Button variant="primary" type="submit" form="role-form" disabled={action.busy}>{action.busy ? "A guardar…" : "Guardar"}</Button></>}>
      <form id="role-form" onSubmit={submit} className="space-y-4">
        {action.error && <Alert tone="error">{action.error}</Alert>}
        <Field label="Nome">{(id) => <Input id={id} value={name} onChange={(e) => setName(e.target.value)} required />}</Field>
        <Field label="Descrição">{(id) => <Input id={id} value={description} onChange={(e) => setDescription(e.target.value)} />}</Field>
        {!role && <Field label="Permissões (vírgulas)">{(id) => <Input id={id} className="font-mono" value={perms} onChange={(e) => setPerms(e.target.value)} placeholder="audit.read, infrastructure.read" />}</Field>}
      </form>
    </Modal>
  );
}

function AssignModal({ member, roles, onClose, onDone }: { member: Member | null; roles: Role[]; onClose: () => void; onDone: () => void }) {
  const action = useAction();
  const assignable = member ? roles.filter((r) => !member.roles.some((mr) => mr.role_id === r.id)) : [];
  const [roleID, setRoleID] = useState("");
  const chosen = roleID || assignable[0]?.id || "";
  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (!member) return;
    if (await action.run(() => api.post(`/api/v1/organization/members/${member.user_id}/roles`, { role_id: chosen }), "Role atribuída.")) { onDone(); onClose(); }
  }
  return (
    <Modal open={!!member} onClose={onClose} size="sm" title="Atribuir role" description={member ? `${member.display_name} (${member.email})` : undefined}
      footer={<><Button onClick={onClose}>Cancelar</Button><Button variant="primary" type="submit" form="assign-form" disabled={action.busy || assignable.length === 0}>Atribuir</Button></>}>
      <form id="assign-form" onSubmit={submit} className="space-y-4">
        {action.error && <Alert tone="error">{action.error}</Alert>}
        {assignable.length === 0 ? <p className="text-sm text-nd-muted">Este membro já tem todas as roles.</p>
          : <Field label="Role">{(id) => <Select id={id} value={chosen} onChange={(e) => setRoleID(e.target.value)}>{assignable.map((r) => <option key={r.id} value={r.id}>{r.name}</option>)}</Select>}</Field>}
      </form>
    </Modal>
  );
}

function AddMemberModal({ open, onClose, onDone }: { open: boolean; onClose: () => void; onDone: () => void }) {
  const action = useAction();
  const [email, setEmail] = useState("");
  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (await action.run(() => api.post("/api/v1/organization/members", { email }), "Membro adicionado.")) { setEmail(""); onDone(); onClose(); }
  }
  return (
    <Modal open={open} onClose={onClose} title="Adicionar membro" description="Adiciona uma conta Nodera existente com a role member. Não cria contas nem envia convites por email."
      footer={<><Button onClick={onClose}>Cancelar</Button><Button variant="primary" type="submit" form="member-form" disabled={action.busy}>{action.busy ? "A adicionar…" : "Adicionar"}</Button></>}>
      <form id="member-form" onSubmit={submit} className="space-y-4">
        {action.error && <Alert tone="error">{action.error}</Alert>}
        <Field label="Email">{(id) => <Input id={id} type="email" value={email} onChange={(e) => setEmail(e.target.value)} placeholder="o utilizador já tem de ter conta" required />}</Field>
      </form>
    </Modal>
  );
}

const TABS = [
  { id: "org", label: "Organização", icon: <Building2 className="h-4 w-4" /> },
  { id: "roles", label: "Roles", icon: <ShieldCheck className="h-4 w-4" /> },
  { id: "members", label: "Membros", icon: <Users className="h-4 w-4" /> },
  { id: "features", label: "Funcionalidades", icon: <ShieldCheck className="h-4 w-4" /> },
  { id: "channels", label: "Notificações", icon: <Users className="h-4 w-4" /> },
  { id: "retention", label: "Retenção", icon: <Building2 className="h-4 w-4" /> },
];

export default function SettingsPage() {
  const org = useApi(() => api.get<Organization>("/api/v1/organization"), []);
  const roles = useApi(() => api.get<Role[]>("/api/v1/roles"), []);
  const members = useApi(() => api.get<Member[]>("/api/v1/organization/members"), []);
  const row = useAction();
  const [tab, setTab] = useState("org");
  const [roleModal, setRoleModal] = useState<{ role: Role | null } | null>(null);
  const [deletingRole, setDeletingRole] = useState<Role | null>(null);
  const [addMember, setAddMember] = useState(false);
  const [assigning, setAssigning] = useState<Member | null>(null);
  const [removing, setRemoving] = useState<Member | null>(null);
  const [revokingRole, setRevokingRole] = useState<{ m: Member; roleId: string; name: string } | null>(null);

  async function deleteRole() {
    if (!deletingRole) return;
    if (await row.run(() => api.del(`/api/v1/roles/${deletingRole.id}`), "Role eliminada.")) roles.reload();
    setDeletingRole(null);
  }
  async function removeMember() {
    if (!removing) return;
    if (await row.run(() => api.del(`/api/v1/organization/members/${removing.user_id}`), "Membro removido.")) members.reload();
    setRemoving(null);
  }
  async function revokeRole() {
    if (!revokingRole) return;
    if (await row.run(() => api.del(`/api/v1/organization/members/${revokingRole.m.user_id}/roles/${revokingRole.roleId}`), "Role revogada.")) members.reload();
    setRevokingRole(null);
  }

  const roleCols: Column<Role>[] = [
    { key: "name", header: "Role", primary: true, cell: (r) => <span className="inline-flex items-center gap-2 font-mono text-xs font-medium">{r.name}{r.is_system && <Badge tone="purple">system</Badge>}</span> },
    { key: "desc", header: "Descrição", cell: (r) => <span className="text-nd-muted">{r.description || "—"}</span> },
    { key: "perm", header: "Permissões", cell: (r) => <span className="text-xs text-nd-muted">{r.permissions.length > 8 ? `${r.permissions.length} permissões` : r.permissions.join(", ") || "—"}</span> },
    { key: "act", header: "", className: "w-12 text-right", hideOnMobile: true, cell: (r) => r.is_system ? null : (
      <RowMenu label={`Ações para ${r.name}`} items={[
        { label: "Editar", icon: <Pencil />, onSelect: () => setRoleModal({ role: r }) },
        { label: "Eliminar", icon: <Trash2 />, danger: true, separatorBefore: true, onSelect: () => setDeletingRole(r) },
      ]} />) },
  ];
  const memberCols: Column<Member & { id: string }>[] = [
    { key: "m", header: "Membro", primary: true, cell: (m) => <div><p className="text-sm font-semibold">{m.display_name}</p><p className="text-xs text-nd-muted">{m.email}</p></div> },
    { key: "roles", header: "Roles", cell: (m) => m.roles.length === 0 ? <span className="text-nd-faint">sem roles</span> : (
      <div className="flex flex-wrap gap-1.5">{m.roles.map((r) => (
        <Badge key={r.role_id} tone="blue">{r.name}<button type="button" aria-label={`Revogar ${r.name}`} title="Revogar" onClick={() => setRevokingRole({ m, roleId: r.role_id, name: r.name })} className="ml-0.5 text-nd-faint hover:text-nd-danger">×</button></Badge>))}</div>) },
    { key: "act", header: "", className: "w-12 text-right", hideOnMobile: true, cell: (m) => (
      <RowMenu label={`Ações para ${m.display_name}`} items={[
        { label: "Atribuir role", icon: <ShieldCheck />, onSelect: () => setAssigning(m) },
        { label: "Remover da organização", icon: <UserMinus />, danger: true, separatorBefore: true, onSelect: () => setRemoving(m) },
      ]} />) },
  ];

  return (
    <div>
      <PageHeader title="Definições" description="Organização, roles e membros. As roles base (owner/admin/member) são do sistema; aqui geres quem tem cada uma." />
      {row.error && <Alert tone="error" className="mb-4">{row.error}</Alert>}
      <Tabs label="Secções das definições" tabs={TABS} active={tab} onChange={setTab} />

      <TabPanel id="org" active={tab}>
        {org.loading ? <LoadingState /> : org.error ? <ErrorState message={org.error} onRetry={org.reload} /> : org.data && <OrganizationPanel org={org.data} onUpdated={() => org.reload()} />}
      </TabPanel>
      <TabPanel id="roles" active={tab}>
        <Section title="Roles" actions={<Button variant="primary" size="sm" icon={<Plus className="h-4 w-4" />} onClick={() => setRoleModal({ role: null })}>Criar role</Button>}>
          <DataTable caption="Roles" columns={roleCols} rows={roles.data} loading={roles.loading} error={roles.error} onRetry={roles.reload} empty={{ icon: <ShieldCheck />, title: "Sem roles visíveis nesta organização" }} />
        </Section>
      </TabPanel>
      <TabPanel id="members" active={tab}>
        <Section title="Membros" actions={<Button variant="primary" size="sm" icon={<UserPlus className="h-4 w-4" />} onClick={() => setAddMember(true)}>Adicionar membro</Button>}>
          <DataTable caption="Membros" columns={memberCols} rows={members.data ? members.data.map((m) => ({ ...m, id: m.user_id })) : null} loading={members.loading} error={members.error} onRetry={members.reload} empty={{ icon: <Users />, title: "Sem membros" }} />
        </Section>
      </TabPanel>

      <TabPanel id="features" active={tab}><FeatureFlagsPanel /></TabPanel>
      <TabPanel id="channels" active={tab}><ChannelsPanel /></TabPanel>
      <TabPanel id="retention" active={tab}><RetentionPanel /></TabPanel>

      <RoleModal open={!!roleModal} role={roleModal?.role ?? null} onClose={() => setRoleModal(null)} onSaved={() => roles.reload()} />
      <AddMemberModal open={addMember} onClose={() => setAddMember(false)} onDone={() => members.reload()} />
      <AssignModal key={assigning?.user_id ?? "none"} member={assigning} roles={roles.data ?? []} onClose={() => setAssigning(null)} onDone={() => members.reload()} />
      <ConfirmDialog open={!!deletingRole} onClose={() => setDeletingRole(null)} onConfirm={deleteRole} danger confirmLabel="Eliminar" title="Eliminar role?" description={`“${deletingRole?.name}” será eliminada.`} />
      <ConfirmDialog open={!!removing} onClose={() => setRemoving(null)} onConfirm={removeMember} danger confirmLabel="Remover" title="Remover membro?" description={`${removing?.display_name} perde o acesso a esta organização.`} />
      <ConfirmDialog open={!!revokingRole} onClose={() => setRevokingRole(null)} onConfirm={revokeRole} danger confirmLabel="Revogar" title="Revogar role?" description={`${revokingRole?.m.display_name} deixa de ter a role “${revokingRole?.name}”.`} />
    </div>
  );
}
