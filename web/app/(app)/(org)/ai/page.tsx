"use client";

import { useState } from "react";
import { BarChart3, BrainCircuit, Cpu, MessageSquare, Pencil, Plus, Send, Server, Trash2 } from "lucide-react";
import { api } from "@/lib/api";
import { useApi } from "@/lib/useApi";
import { useAction } from "@/lib/useAction";
import { PageHeader } from "@/components/ui/PageHeader";
import { Card, Section } from "@/components/ui/Card";
import { Button } from "@/components/ui/Button";
import { Alert, EmptyState, LoadingState } from "@/components/ui/Feedback";
import { Badge } from "@/components/ui/Status";
import { DataTable, type Column } from "@/components/ui/DataTable";
import { ConfirmDialog, Modal } from "@/components/ui/Overlay";
import { Field, Input, Select } from "@/components/ui/Forms";
import { RowMenu } from "@/components/ui/RowMenu";
import { TabPanel, Tabs } from "@/components/ui/Tabs";
import { StatusBadge } from "@/components/StatusBadge";
import type { AIModel, AIProfile, AIProvider, AIUsageRecord, ChatResult, Page } from "@/lib/types";

const parseList = (raw: string): string[] => raw.split(",").map((s) => s.trim()).filter(Boolean);
const PRIVACY = ["public", "internal", "confidential", "restricted"];
const PRIVACY_TONE: Record<string, "green" | "blue" | "orange" | "red"> = { public: "green", internal: "blue", confidential: "orange", restricted: "red" };

function Chips({ items }: { items: string[] }) {
  return items.length === 0 ? <span className="text-nd-faint">—</span> : <div className="flex flex-wrap gap-1">{items.map((i) => <Badge key={i}>{i}</Badge>)}</div>;
}

function ChatPanel({ profiles, onSent }: { profiles: AIProfile[]; onSent: () => void }) {
  const action = useAction();
  const [profileKey, setProfileKey] = useState(""); const [message, setMessage] = useState("");
  const [asked, setAsked] = useState<string | null>(null); const [result, setResult] = useState<ChatResult | null>(null);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setResult(null); setAsked(message);
    let res: ChatResult | null = null;
    await action.run(async () => { res = await api.post<ChatResult>("/api/v1/ai/chat", { profile_key: profileKey, messages: [{ role: "user", content: message }] }); }, undefined, "Não foi possível contactar o AI Gateway");
    if (res) { setResult(res); setMessage(""); }
    // ai.chat records a usage row whether it succeeds or fails — reload either way.
    onSent();
  }

  if (profiles.length === 0) return <Card><EmptyState icon={<MessageSquare />} title="Cria primeiro um perfil" description="O chat passa sempre por um perfil de IA — nunca diretamente por um provider." /></Card>;
  return (
    <Card className="p-5">
      <div className="nd-scroll mb-4 min-h-[160px] space-y-3 rounded-nd bg-nd-elevated p-4">
        {!asked && !action.busy && <p className="text-sm text-nd-faint">Escolhe um perfil e envia uma mensagem. O router decide o provider/modelo segundo a política de privacidade do perfil.</p>}
        {asked && <div className="ml-auto max-w-[80%] rounded-nd bg-nd-primary/20 px-3.5 py-2.5 text-sm text-nd-text">{asked}</div>}
        {action.busy && <p className="text-sm text-nd-muted">A pensar…</p>}
        {action.error && <Alert tone="error">{action.error}</Alert>}
        {result && (
          <div className="max-w-[85%] rounded-nd border border-nd-border bg-nd-surface px-3.5 py-2.5">
            <p className="whitespace-pre-wrap text-sm text-nd-text">{result.content}</p>
            <p className="mt-2 text-xs text-nd-faint">{result.provider_key} / {result.model} — {result.input_tokens} in · {result.output_tokens} out</p>
          </div>)}
      </div>
      <form onSubmit={submit} className="grid gap-3 sm:grid-cols-[220px_1fr_auto]">
        <Select aria-label="Perfil" value={profileKey} onChange={(e) => setProfileKey(e.target.value)} required>
          <option value="" disabled>Perfil…</option>
          {profiles.map((p) => <option key={p.key} value={p.key}>{p.key} ({p.privacy_level})</option>)}
        </Select>
        <Input aria-label="Mensagem" value={message} onChange={(e) => setMessage(e.target.value)} placeholder="Escreve uma mensagem…" required />
        <Button variant="primary" type="submit" disabled={action.busy} icon={<Send className="h-4 w-4" />}>Enviar</Button>
      </form>
    </Card>
  );
}

function ProfileModal({ profile, open, onClose, onSaved }: { profile: AIProfile | null; open: boolean; onClose: () => void; onSaved: () => void }) {
  const action = useAction();
  const blank = { key: "", description: "", privacy: "internal", preferred: "", fallback: "", caps: "", temp: "0.7", max: "2048", timeout: "60" };
  const [f, setF] = useState(blank);
  const [loaded, setLoaded] = useState<string | null>(null);
  const key = open ? (profile?.id ?? "new") : null;
  if (key !== loaded) {
    setLoaded(key);
    if (open) setF(profile ? { key: profile.key, description: profile.description, privacy: profile.privacy_level, preferred: profile.preferred_model_ids.join(", "), fallback: profile.fallback_model_ids.join(", "), caps: profile.required_capabilities.join(", "), temp: String(profile.temperature), max: String(profile.max_tokens), timeout: String(profile.timeout_seconds) } : blank);
  }
  const set = (k: keyof typeof f) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) => setF((s) => ({ ...s, [k]: e.target.value }));

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    const body = { description: f.description, privacy_level: f.privacy, preferred_model_ids: parseList(f.preferred), fallback_model_ids: parseList(f.fallback), required_capabilities: parseList(f.caps), temperature: Number(f.temp), max_tokens: Number(f.max), timeout_seconds: Number(f.timeout) };
    const ok = await action.run(() => (profile ? api.put(`/api/v1/ai/profiles/${profile.id}`, body) : api.post("/api/v1/ai/profiles", { key: f.key, ...body })), profile ? "Perfil atualizado." : "Perfil criado.");
    if (ok) { onSaved(); onClose(); }
  }
  return (
    <Modal open={open} onClose={onClose} size="lg" title={profile ? `Editar ${profile.key}` : "Novo perfil de IA"} description="Um perfil descreve o que a aplicação precisa; o router escolhe o provider/modelo. A key é imutável."
      footer={<><Button onClick={onClose}>Cancelar</Button><Button variant="primary" type="submit" form="profile-form" disabled={action.busy}>{action.busy ? "A guardar…" : "Guardar"}</Button></>}>
      <form id="profile-form" onSubmit={submit} className="space-y-4">
        {action.error && <Alert tone="error">{action.error}</Alert>}
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="Key">{(id) => <Input id={id} className="font-mono" value={f.key} onChange={set("key")} disabled={!!profile} required />}</Field>
          <Field label="Nível de privacidade" hint="restricted nunca é encaminhado para a cloud.">{(id) => <Select id={id} value={f.privacy} onChange={set("privacy")}>{PRIVACY.map((p) => <option key={p}>{p}</option>)}</Select>}</Field>
        </div>
        <Field label="Descrição">{(id) => <Input id={id} value={f.description} onChange={set("description")} />}</Field>
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="Modelos preferidos (provider/modelo)">{(id) => <Input id={id} className="font-mono" value={f.preferred} onChange={set("preferred")} placeholder="ollama/qwen3" />}</Field>
          <Field label="Modelos de fallback">{(id) => <Input id={id} className="font-mono" value={f.fallback} onChange={set("fallback")} />}</Field>
        </div>
        <Field label="Capabilities necessárias (vírgulas)">{(id) => <Input id={id} className="font-mono" value={f.caps} onChange={set("caps")} placeholder="chat" />}</Field>
        <div className="grid gap-4 sm:grid-cols-3">
          <Field label="Temperature">{(id) => <Input id={id} type="number" step="0.1" value={f.temp} onChange={set("temp")} />}</Field>
          <Field label="Max tokens">{(id) => <Input id={id} type="number" value={f.max} onChange={set("max")} />}</Field>
          <Field label="Timeout (s)">{(id) => <Input id={id} type="number" value={f.timeout} onChange={set("timeout")} />}</Field>
        </div>
      </form>
    </Modal>
  );
}

function ProviderModal({ open, onClose, onSaved }: { open: boolean; onClose: () => void; onSaved: () => void }) {
  const action = useAction();
  const [key, setKey] = useState(""); const [kind, setKind] = useState("cloud"); const [name, setName] = useState(""); const [status, setStatus] = useState("active");
  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (await action.run(() => api.post("/api/v1/ai/providers", { key, kind, display_name: name, status }), "Provider guardado.")) { setKey(""); setName(""); onSaved(); onClose(); }
  }
  return (
    <Modal open={open} onClose={onClose} title="Registar / atualizar provider" description="Regista apenas uma linha descobrível — se é realmente invocável depende de um adapter Go registado no arranque. Requer permissão de plataforma."
      footer={<><Button onClick={onClose}>Cancelar</Button><Button variant="primary" type="submit" form="provider-form" disabled={action.busy}>Guardar</Button></>}>
      <form id="provider-form" onSubmit={submit} className="space-y-4">
        {action.error && <Alert tone="error">{action.error}</Alert>}
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="Key">{(id) => <Input id={id} className="font-mono" value={key} onChange={(e) => setKey(e.target.value)} required />}</Field>
          <Field label="Nome">{(id) => <Input id={id} value={name} onChange={(e) => setName(e.target.value)} required />}</Field>
          <Field label="Tipo">{(id) => <Select id={id} value={kind} onChange={(e) => setKind(e.target.value)}><option>cloud</option><option>local</option></Select>}</Field>
          <Field label="Estado">{(id) => <Select id={id} value={status} onChange={(e) => setStatus(e.target.value)}>{["active", "disabled", "unconfigured", "unavailable"].map((s) => <option key={s}>{s}</option>)}</Select>}</Field>
        </div>
      </form>
    </Modal>
  );
}

function ModelModal({ providers, open, onClose, onSaved }: { providers: AIProvider[]; open: boolean; onClose: () => void; onSaved: () => void }) {
  const action = useAction();
  const [pk, setPk] = useState(""); const [mid, setMid] = useState(""); const [name, setName] = useState(""); const [caps, setCaps] = useState(""); const [ctx, setCtx] = useState("8192"); const [status, setStatus] = useState("available");
  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (await action.run(() => api.post("/api/v1/ai/models", { provider_key: pk, model_identifier: mid, display_name: name, capabilities: parseList(caps), context_window: Number(ctx), status }), "Modelo guardado.")) { setMid(""); setName(""); setCaps(""); onSaved(); onClose(); }
  }
  return (
    <Modal open={open} onClose={onClose} title="Registar / atualizar modelo" description="Requer permissão de plataforma (platform.ai.models.manage)."
      footer={<><Button onClick={onClose}>Cancelar</Button><Button variant="primary" type="submit" form="model-form" disabled={action.busy}>Guardar</Button></>}>
      <form id="model-form" onSubmit={submit} className="space-y-4">
        {action.error && <Alert tone="error">{action.error}</Alert>}
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="Provider">{(id) => <Select id={id} value={pk} onChange={(e) => setPk(e.target.value)} required><option value="" disabled>Escolher…</option>{providers.map((p) => <option key={p.key} value={p.key}>{p.key}</option>)}</Select>}</Field>
          <Field label="Identificador do modelo">{(id) => <Input id={id} className="font-mono" value={mid} onChange={(e) => setMid(e.target.value)} required />}</Field>
          <Field label="Nome">{(id) => <Input id={id} value={name} onChange={(e) => setName(e.target.value)} />}</Field>
          <Field label="Context window">{(id) => <Input id={id} type="number" value={ctx} onChange={(e) => setCtx(e.target.value)} />}</Field>
          <Field label="Capabilities (vírgulas)">{(id) => <Input id={id} className="font-mono" value={caps} onChange={(e) => setCaps(e.target.value)} placeholder="chat" />}</Field>
          <Field label="Estado">{(id) => <Select id={id} value={status} onChange={(e) => setStatus(e.target.value)}>{["available", "unavailable", "deprecated"].map((s) => <option key={s}>{s}</option>)}</Select>}</Field>
        </div>
      </form>
    </Modal>
  );
}

const TABS = [
  { id: "chat", label: "Chat", icon: <MessageSquare className="h-4 w-4" /> },
  { id: "profiles", label: "Perfis", icon: <BrainCircuit className="h-4 w-4" /> },
  { id: "registry", label: "Providers & Modelos", icon: <Server className="h-4 w-4" /> },
  { id: "usage", label: "Uso", icon: <BarChart3 className="h-4 w-4" /> },
];

export default function AIPage() {
  const profiles = useApi(() => api.get<AIProfile[]>("/api/v1/ai/profiles"), []);
  const providers = useApi(() => api.get<AIProvider[]>("/api/v1/ai/providers"), []);
  const models = useApi(() => api.get<AIModel[]>("/api/v1/ai/models"), []);
  const [usageLimit, setUsageLimit] = useState(20);
  const [uProfile, setUProfile] = useState(""); const [uProvider, setUProvider] = useState("");
  const usage = useApi(() => api.get<Page<AIUsageRecord>>(`/api/v1/ai/usage?limit=${usageLimit}${uProfile ? `&profile_key=${encodeURIComponent(uProfile)}` : ""}${uProvider ? `&provider_key=${encodeURIComponent(uProvider)}` : ""}`), [usageLimit, uProfile, uProvider]);

  const row = useAction();
  const [tab, setTab] = useState("chat");
  const [profileModal, setProfileModal] = useState<{ profile: AIProfile | null } | null>(null);
  const [providerModal, setProviderModal] = useState(false); const [modelModal, setModelModal] = useState(false);
  const [delProfile, setDelProfile] = useState<AIProfile | null>(null);
  const [delProvider, setDelProvider] = useState<AIProvider | null>(null);
  const [delModel, setDelModel] = useState<AIModel | null>(null);

  async function deleteProfile() { if (delProfile && await row.run(() => api.del(`/api/v1/ai/profiles/${delProfile.id}`), "Perfil eliminado.")) profiles.reload(); setDelProfile(null); }
  async function deleteProvider() { if (delProvider && await row.run(() => api.del(`/api/v1/ai/providers/${encodeURIComponent(delProvider.key)}`), "Provider eliminado (e os seus modelos).")) { providers.reload(); models.reload(); } setDelProvider(null); }
  async function deleteModel() { if (delModel && await row.run(() => api.del(`/api/v1/ai/providers/${encodeURIComponent(delModel.provider_key)}/models/${encodeURIComponent(delModel.model_identifier)}`), "Modelo eliminado.")) models.reload(); setDelModel(null); }

  const profileCols: Column<AIProfile>[] = [
    { key: "key", header: "Perfil", primary: true, cell: (p) => <div><p className="font-mono text-xs font-medium">{p.key}</p><p className="text-xs text-nd-muted">{p.description || "—"}</p></div> },
    { key: "priv", header: "Privacidade", cell: (p) => <Badge tone={PRIVACY_TONE[p.privacy_level] ?? "neutral"}>{p.privacy_level}</Badge> },
    { key: "pref", header: "Preferidos", cell: (p) => <Chips items={p.preferred_model_ids} /> },
    { key: "fb", header: "Fallback", cell: (p) => <Chips items={p.fallback_model_ids} /> },
    { key: "params", header: "Parâmetros", cell: (p) => <span className="text-xs text-nd-muted">T {p.temperature} · {p.max_tokens} tok · {p.timeout_seconds}s</span> },
    { key: "act", header: "", className: "w-12 text-right", hideOnMobile: true, cell: (p) => (
      <RowMenu label={`Ações para ${p.key}`} items={[
        { label: "Editar", icon: <Pencil />, onSelect: () => setProfileModal({ profile: p }) },
        { label: "Eliminar", icon: <Trash2 />, danger: true, separatorBefore: true, onSelect: () => setDelProfile(p) },
      ]} />) },
  ];
  const providerCols: Column<AIProvider>[] = [
    { key: "key", header: "Provider", primary: true, cell: (p) => <div><p className="font-mono text-xs font-medium">{p.key}</p><p className="text-xs text-nd-muted">{p.display_name}</p></div> },
    { key: "kind", header: "Tipo", cell: (p) => <Badge tone={p.kind === "local" ? "green" : "blue"}>{p.kind}</Badge> },
    { key: "st", header: "Estado", cell: (p) => <StatusBadge status={p.status} /> },
    { key: "act", header: "", className: "w-12 text-right", hideOnMobile: true, cell: (p) => <RowMenu label={`Ações para ${p.key}`} items={[{ label: "Eliminar", icon: <Trash2 />, danger: true, onSelect: () => setDelProvider(p) }]} /> },
  ];
  type ModelRow = AIModel;
  const modelCols: Column<ModelRow>[] = [
    { key: "m", header: "Modelo", primary: true, cell: (m) => <span className="font-mono text-xs font-medium">{m.provider_key}/{m.model_identifier}</span> },
    { key: "name", header: "Nome", cell: (m) => m.display_name },
    { key: "caps", header: "Capabilities", cell: (m) => <Chips items={m.capabilities} /> },
    { key: "ctx", header: "Contexto", cell: (m) => <span className="tabular-nums text-nd-muted">{m.context_window}</span> },
    { key: "st", header: "Estado", cell: (m) => <StatusBadge status={m.status} /> },
    { key: "act", header: "", className: "w-12 text-right", hideOnMobile: true, cell: (m) => <RowMenu label={`Ações para ${m.model_identifier}`} items={[{ label: "Eliminar", icon: <Trash2 />, danger: true, onSelect: () => setDelModel(m) }]} /> },
  ];
  const usageCols: Column<AIUsageRecord>[] = [
    { key: "p", header: "Perfil", primary: true, cell: (u) => <span className="font-mono text-xs font-medium">{u.profile_key}</span> },
    { key: "m", header: "Provider / modelo", cell: (u) => <span className="font-mono text-xs text-nd-muted">{u.provider_key}/{u.model_identifier}</span> },
    { key: "c", header: "Classificação", cell: (u) => <Badge>{u.classification}</Badge> },
    { key: "t", header: "Tokens", cell: (u) => <span className="tabular-nums text-nd-muted">{u.input_tokens} → {u.output_tokens}</span> },
    { key: "l", header: "Latência", cell: (u) => <span className="tabular-nums text-nd-muted">{u.latency_ms} ms</span> },
    { key: "s", header: "Estado", cell: (u) => <StatusBadge status={u.status} /> },
    { key: "w", header: "Quando", cell: (u) => <span className="text-xs text-nd-faint">{new Date(u.created_at).toLocaleString("pt-PT")}</span> },
  ];

  return (
    <div>
      <PageHeader title="AI Gateway" description="Routing determinístico com política de privacidade. Providers e modelos são um registo da plataforma — uma linha aqui é descobrível, não necessariamente invocável." />
      {row.error && <Alert tone="error" className="mb-4">{row.error}</Alert>}
      <Tabs label="Secções do AI Gateway" tabs={TABS} active={tab} onChange={setTab} />

      <TabPanel id="chat" active={tab}>
        {profiles.loading ? <LoadingState /> : <ChatPanel profiles={profiles.data ?? []} onSent={() => usage.reload()} />}
      </TabPanel>

      <TabPanel id="profiles" active={tab}>
        <Section title="Perfis" description="Específicos da organização." actions={<Button variant="primary" size="sm" icon={<Plus className="h-4 w-4" />} onClick={() => setProfileModal({ profile: null })}>Novo perfil</Button>}>
          <DataTable caption="Perfis de IA" columns={profileCols} rows={profiles.data} loading={profiles.loading} error={profiles.error} onRetry={profiles.reload} empty={{ icon: <BrainCircuit />, title: "Nenhum perfil", action: { label: "Novo perfil", onClick: () => setProfileModal({ profile: null }) } }} />
        </Section>
      </TabPanel>

      <TabPanel id="registry" active={tab}>
        <div className="space-y-6">
          <Section title="Providers" actions={<Button variant="primary" size="sm" icon={<Plus className="h-4 w-4" />} onClick={() => setProviderModal(true)}>Registar provider</Button>}>
            <DataTable caption="Providers" columns={providerCols} rows={providers.data} loading={providers.loading} error={providers.error} onRetry={providers.reload} empty={{ icon: <Server />, title: "Nenhum provider registado" }} />
          </Section>
          <Section title="Modelos" actions={<Button variant="primary" size="sm" icon={<Plus className="h-4 w-4" />} onClick={() => setModelModal(true)}>Registar modelo</Button>}>
            <DataTable caption="Modelos" columns={modelCols} rows={models.data} loading={models.loading} error={models.error} onRetry={models.reload} empty={{ icon: <Cpu />, title: "Nenhum modelo registado" }} />
          </Section>
        </div>
      </TabPanel>

      <TabPanel id="usage" active={tab}>
        <Section title="Uso" description="Registo real de chamadas ao gateway — nunca o conteúdo dos prompts.">
          <div className="mb-4 grid gap-3 sm:grid-cols-2 lg:grid-cols-[220px_220px]">
            <Select aria-label="Filtrar por perfil" value={uProfile} onChange={(e) => { setUProfile(e.target.value); setUsageLimit(20); }}><option value="">Todos os perfis</option>{(profiles.data ?? []).map((p) => <option key={p.key}>{p.key}</option>)}</Select>
            <Select aria-label="Filtrar por provider" value={uProvider} onChange={(e) => { setUProvider(e.target.value); setUsageLimit(20); }}><option value="">Todos os providers</option>{(providers.data ?? []).map((p) => <option key={p.key}>{p.key}</option>)}</Select>
          </div>
          <DataTable caption="Uso de IA" columns={usageCols} rows={usage.data?.items ?? null} loading={usage.loading && !usage.data} error={usage.error} onRetry={usage.reload} pageSize={10}
            empty={{ icon: <BarChart3 />, title: "Ainda sem uso registado", description: "Preenche-se à medida que o chat é usado." }} />
          {usage.data?.has_more && <div className="mt-3"><Button onClick={() => setUsageLimit((n) => n + 20)}>Carregar mais</Button></div>}
        </Section>
      </TabPanel>

      <ProfileModal open={!!profileModal} profile={profileModal?.profile ?? null} onClose={() => setProfileModal(null)} onSaved={() => profiles.reload()} />
      <ProviderModal open={providerModal} onClose={() => setProviderModal(false)} onSaved={() => providers.reload()} />
      <ModelModal open={modelModal} providers={providers.data ?? []} onClose={() => setModelModal(false)} onSaved={() => models.reload()} />
      <ConfirmDialog open={!!delProfile} onClose={() => setDelProfile(null)} onConfirm={deleteProfile} danger confirmLabel="Eliminar" title="Eliminar perfil?" description={`“${delProfile?.key}” — as aplicações que o usam por key deixam de funcionar.`} />
      <ConfirmDialog open={!!delProvider} onClose={() => setDelProvider(null)} onConfirm={deleteProvider} danger confirmLabel="Eliminar" title="Eliminar provider?" description={`“${delProvider?.key}” e todos os seus modelos serão eliminados do registo da plataforma.`} />
      <ConfirmDialog open={!!delModel} onClose={() => setDelModel(null)} onConfirm={deleteModel} danger confirmLabel="Eliminar" title="Eliminar modelo?" description={`${delModel?.provider_key}/${delModel?.model_identifier}`} />
    </div>
  );
}
