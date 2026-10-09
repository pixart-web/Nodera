import type { ServiceHealth, Activity, MetricSnapshot, MetricRange, LogLine } from "@/lib/domain";

export const MOCK_SERVICES: ServiceHealth[] = [
  { id: "traefik", name: "Traefik (Proxy)", status: "ONLINE", latencyMs: 0 },
  { id: "docker", name: "Docker Engine", status: "ONLINE", latencyMs: 0 },
  { id: "mariadb", name: "MariaDB", status: "ONLINE", latencyMs: 1 },
  { id: "redis", name: "Redis", status: "ONLINE", latencyMs: 0 },
  { id: "system", name: "Sistema", status: "ONLINE", latencyMs: 1 },
];

export const MOCK_ACTIVITY: Activity[] = [
  { id: "a1", subject: "Motogreen", event: "Deployment concluído", at: "há 12 min", status: "ONLINE", kind: "wordpress" },
  { id: "a2", subject: "Casa das Fardas", event: "Contêineres iniciados", at: "há 28 min", status: "PENDING", kind: "container" },
  { id: "a3", subject: "SSL", event: "Certificado emitido: motogreen.pixart.pt", at: "há 35 min", status: "ONLINE", kind: "ssl" },
  { id: "a4", subject: "Backup", event: "Backup concluído: pixart.pt", at: "há 2 horas", status: "BACKUP", kind: "backup" },
  { id: "a5", subject: "Kiko", event: "Atualização de código (git pull)", at: "há 3 horas", status: "ERROR", kind: "git" },
  { id: "a6", subject: "CyberAudit", event: "Contêineres reiniciados", at: "há 4 horas", status: "DEPLOYING", kind: "app" },
];

// Deterministic pseudo-series so renders are stable (no Math.random in render).
function series(seed: number, base: number, amp: number, n = 24): number[] {
  return Array.from({ length: n }, (_, i) => Math.max(1, base + Math.sin((i + seed) / 2.2) * amp + Math.cos((i * seed) / 5) * amp * 0.4));
}

export function mockMetrics(range: MetricRange): MetricSnapshot {
  const k = { "1h": 1, "6h": 2, "24h": 3, "7d": 4 }[range];
  return {
    cpu: { pct: 12 },
    ram: { usedGb: 2.4, totalGb: 8 },
    disk: { usedGb: 46, totalGb: 200 },
    network: { upMbps: 12, downMbps: 8 },
    series: [
      { id: "cpu", label: "CPU", points: series(k, 14, 6) },
      { id: "ram", label: "Memória RAM", points: series(k + 2, 30, 4) },
      { id: "disk", label: "Disco", points: series(k + 4, 23, 1.2) },
      { id: "network", label: "Rede", points: series(k + 1, 12, 7), secondary: series(k + 6, 8, 5) },
    ],
  };
}

const LEVELS: LogLine["level"][] = ["INFO", "SUCCESS", "INFO", "WARNING", "DEBUG", "ERROR", "INFO"];
const SOURCES = ["traefik", "docker", "app", "mariadb", "deploy"];
const PROJECTS = ["Pixart", "Motogreen", "Kiko", "CyberAudit", "Casa das Fardas"];
const MESSAGES = ["Request completed in 42ms", "Container started", "Health check passed", "Slow query detected (812ms)", "Certificate renewal scheduled", "Connection refused: upstream", "Cache warmed"];

export function mockLogs(n = 60): LogLine[] {
  return Array.from({ length: n }, (_, i) => ({
    id: `l${i}`,
    ts: `12:${String(59 - Math.floor(i / 2)).padStart(2, "0")}:${String((i * 17) % 60).padStart(2, "0")}`,
    level: LEVELS[i % LEVELS.length]!,
    source: SOURCES[i % SOURCES.length]!,
    project: PROJECTS[i % PROJECTS.length]!,
    message: MESSAGES[i % MESSAGES.length]!,
  }));
}
