"use client";

import { useState } from "react";
import { Laptop, LogOut } from "lucide-react";
import { api } from "@/lib/api";
import { useApi } from "@/lib/useApi";
import { useAction } from "@/lib/useAction";
import { PageHeader } from "@/components/ui/PageHeader";
import { Section } from "@/components/ui/Card";
import { Button } from "@/components/ui/Button";
import { Alert } from "@/components/ui/Feedback";
import { Badge } from "@/components/ui/Status";
import { Avatar } from "@/components/ui/Avatar";
import { DataTable, type Column } from "@/components/ui/DataTable";
import { ConfirmDialog } from "@/components/ui/Overlay";
import { Field, Input } from "@/components/ui/Forms";
import { getStoredUser, setStoredUser } from "@/lib/session";
import type { Session, User } from "@/lib/types";

function ProfileSection() {
  const stored = getStoredUser();
  const action = useAction();
  const [displayName, setDisplayName] = useState(stored?.display_name ?? "");
  async function submit(e: React.FormEvent) {
    e.preventDefault();
    await action.run(async () => {
      const updated = await api.put<User>("/api/v1/account/profile", { display_name: displayName });
      setStoredUser({ id: updated.id, email: updated.email, display_name: updated.display_name });
    }, "Perfil atualizado.");
  }
  const initials = (stored?.display_name ?? "U").split(" ").map((p) => p[0]).slice(0, 2).join("").toUpperCase();
  return (
    <Section title="Perfil">
      <div className="mb-5 flex items-center gap-4"><Avatar initial={initials} color="#334155" round size={56} /><div><p className="text-base font-semibold text-nd-text">{stored?.display_name}</p><p className="text-sm text-nd-muted">{stored?.email}</p></div></div>
      <form onSubmit={submit} className="space-y-4">
        {action.error && <Alert tone="error">{action.error}</Alert>}
        <Field label="Email" hint="O email não pode ser alterado aqui.">{(id) => <Input id={id} value={stored?.email ?? ""} disabled />}</Field>
        <Field label="Nome">{(id) => <Input id={id} value={displayName} onChange={(e) => setDisplayName(e.target.value)} required />}</Field>
        <Button variant="primary" type="submit" disabled={action.busy}>{action.busy ? "A guardar…" : "Guardar"}</Button>
      </form>
    </Section>
  );
}

function PasswordSection() {
  const action = useAction();
  const [current, setCurrent] = useState(""); const [next, setNext] = useState("");
  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (await action.run(() => api.post("/api/v1/account/password", { current_password: current, new_password: next }), "Palavra-passe alterada. As outras sessões foram terminadas.")) { setCurrent(""); setNext(""); }
  }
  return (
    <Section title="Palavra-passe" description="Ao alterar, todas as outras sessões ativas são terminadas; esta mantém-se.">
      <form onSubmit={submit} className="space-y-4">
        {action.error && <Alert tone="error">{action.error}</Alert>}
        <Field label="Palavra-passe atual">{(id) => <Input id={id} type="password" value={current} onChange={(e) => setCurrent(e.target.value)} required autoComplete="current-password" />}</Field>
        <Field label="Nova palavra-passe" hint="Pelo menos 12 caracteres, misturando letras com um número ou símbolo.">{(id) => <Input id={id} type="password" value={next} onChange={(e) => setNext(e.target.value)} required autoComplete="new-password" />}</Field>
        <Button variant="primary" type="submit" disabled={action.busy}>{action.busy ? "A alterar…" : "Alterar palavra-passe"}</Button>
      </form>
    </Section>
  );
}

function SessionsSection() {
  const sessions = useApi(() => api.get<Session[]>("/api/v1/account/sessions"), []);
  const action = useAction();
  const [confirmAll, setConfirmAll] = useState(false);
  const others = (sessions.data ?? []).filter((s) => !s.is_current).length;

  async function revoke(id: string) { if (await action.run(() => api.del(`/api/v1/account/sessions/${id}`), "Sessão terminada.")) sessions.reload(); }
  async function revokeOthers() { if (await action.run(() => api.post("/api/v1/account/sessions/revoke-others"), "Outros dispositivos terminados.")) sessions.reload(); }

  const columns: Column<Session>[] = [
    { key: "dev", header: "Dispositivo / IP", primary: true, cell: (s) => <div className="flex items-center gap-2"><Laptop className="h-4 w-4 shrink-0 text-nd-muted" aria-hidden /><span className="text-sm">{s.user_agent || "dispositivo desconhecido"} <span className="text-nd-faint">({s.ip_address || "IP desconhecido"})</span></span>{s.is_current && <Badge tone="green">esta sessão</Badge>}</div> },
    { key: "cr", header: "Criada", cell: (s) => <span className="text-xs text-nd-faint">{new Date(s.created_at).toLocaleString("pt-PT")}</span> },
    { key: "ex", header: "Expira", cell: (s) => <span className="text-xs text-nd-faint">{new Date(s.expires_at).toLocaleString("pt-PT")}</span> },
    { key: "act", header: "", className: "text-right", hideOnMobile: true, cell: (s) => s.is_current ? null : <Button size="sm" variant="danger" icon={<LogOut className="h-3.5 w-3.5" />} onClick={() => revoke(s.id)}>Terminar</Button> },
  ];
  return (
    <Section title="Sessões ativas" actions={others > 0 ? <Button size="sm" variant="danger" onClick={() => setConfirmAll(true)}>Terminar todas as outras ({others})</Button> : undefined}>
      {action.error && <Alert tone="error" className="mb-3">{action.error}</Alert>}
      <DataTable caption="Sessões ativas" columns={columns} rows={sessions.data} loading={sessions.loading} error={sessions.error} onRetry={sessions.reload} empty={{ icon: <Laptop />, title: "Sem sessões ativas" }} />
      <ConfirmDialog open={confirmAll} onClose={() => setConfirmAll(false)} onConfirm={revokeOthers} danger confirmLabel="Terminar" title="Terminar as outras sessões?" description="Todos os outros dispositivos serão desligados. Esta sessão mantém-se." />
    </Section>
  );
}

export default function AccountPage() {
  return (
    <div>
      <PageHeader title="Conta" description="O teu perfil e palavra-passe. Não está associado a uma organização." />
      <div className="grid gap-6 lg:grid-cols-2"><ProfileSection /><PasswordSection /></div>
      <div className="mt-6"><SessionsSection /></div>
    </div>
  );
}
