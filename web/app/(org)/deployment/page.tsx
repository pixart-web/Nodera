"use client";

import { useEffect, useState } from "react";
import { Check, Rocket } from "lucide-react";
import { Button } from "@/components/ui/Button";
import { Card, Section } from "@/components/ui/Card";
import { PageHeader } from "@/components/ui/PageHeader";
import { Alert, NotConnected } from "@/components/ui/Feedback";
import { Field, Input, Select } from "@/components/ui/Forms";
import { Stepper } from "@/components/ui/Stepper";

const STEPS = ["Source", "Configuration", "Infrastructure", "Deploy", "Health Check", "Complete"];
const SAMPLE = ["Cloning repository...", "Creating database...", "Starting container...", "Configuring Traefik...", "SSL certificate issued...", "Health check passed."];

export default function DeploymentPage() {
  const [step, setStep] = useState(0);
  const [sim, setSim] = useState(false);
  const [shown, setShown] = useState(0);

  useEffect(() => {
    if (!sim) return;
    if (shown >= SAMPLE.length) return;
    const t = setTimeout(() => setShown((n) => n + 1), 600);
    return () => clearTimeout(t);
  }, [sim, shown]);

  return (
    <div className="space-y-6">
      <PageHeader title="Deployment" description="Futuro Deployment Center — publicar projetos na infraestrutura Nodera." />
      <NotConnected what="Pré-visualização do fluxo. O motor de deployment ainda não existe." />
      <Card className="p-5"><Stepper steps={STEPS} current={step} /></Card>
      <div className="grid gap-6 lg:grid-cols-2">
        <Section title={STEPS[step]!}>
          <div className="space-y-4">
            {step === 0 && <><Field label="Repositório Git">{(id) => <Input id={id} placeholder="github.com/pixart/projeto" />}</Field><Field label="Branch">{(id) => <Input id={id} defaultValue="main" />}</Field></>}
            {step === 1 && <><Field label="Domínio">{(id) => <Input id={id} placeholder="projeto.pixart.pt" />}</Field><Field label="Variáveis de ambiente" hint="Segredos pertencem ao módulo Secrets.">{(id) => <Input id={id} placeholder="KEY=value" />}</Field></>}
            {step === 2 && <><Field label="Servidor">{(id) => <Select id={id}><option>pixart-prod-01</option></Select>}</Field><Field label="Base de dados">{(id) => <Select id={id}><option>MariaDB (nova)</option><option>Nenhuma</option></Select>}</Field></>}
            {step >= 3 && <Alert tone="info" title="Pré-visualização">Os passos seguintes mostram a simulação visual. Nenhum deployment real é executado.</Alert>}
            <div className="flex justify-between pt-2">
              <Button disabled={step === 0} onClick={() => { setStep((s) => s - 1); setSim(false); setShown(0); }}>Anterior</Button>
              <Button variant="primary" disabled={step === STEPS.length - 1} onClick={() => { setStep((s) => s + 1); if (step + 1 >= 3) setSim(true); }} icon={<Rocket className="h-4 w-4" />}>{step === 2 ? "Simular deploy" : "Seguinte"}</Button>
            </div>
          </div>
        </Section>
        <Section title="Logs (simulação)">
          <div className="nd-scroll min-h-[220px] rounded-nd bg-[#050C17] p-4 font-mono text-xs leading-6" role="log" aria-live="polite">
            {SAMPLE.slice(0, shown).map((l, i) => <div key={l}><span className="text-nd-faint">[12:41:{String(2 + i * 2).padStart(2, "0")}]</span> <span className="text-nd-text">{l}</span></div>)}
            {!sim && <p className="text-nd-faint">Aguardando deployment…</p>}
          </div>
          {shown >= SAMPLE.length && <p className="mt-3 flex items-center gap-2 text-sm font-semibold text-nd-warning"><Check className="h-4 w-4" aria-hidden />SIMULAÇÃO CONCLUÍDA — nenhum deployment real foi executado</p>}
        </Section>
      </div>
    </div>
  );
}
