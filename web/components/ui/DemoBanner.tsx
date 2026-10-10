"use client";

import { Alert } from "./Feedback";
import { usePlatform } from "@/components/providers/Platform";

// Shown whenever the API runs on simulated providers. It is driven by the
// server (/system/info), so it cannot be forgotten on a page.
export function DemoBanner() {
  const { system } = usePlatform();
  if (!system) return null;
  if (system.provider_mode === "mock") {
    return <Alert tone="warning" title="MODO DEMO — infraestrutura simulada" className="mb-5">Os providers são simulados em memória: nada aqui toca em containers, bases de dados, DNS ou certificados reais.</Alert>;
  }
  const missing = Object.entries(system.capabilities).filter(([, v]) => v === "not_configured").map(([k]) => k);
  if (missing.length === 0) return null;
  return <Alert tone="info" title="Alguns recursos não estão configurados neste ambiente" className="mb-5">Sem provider para: {missing.join(", ")}. As operações que dependem deles recusam-se a correr em vez de simular.</Alert>;
}
