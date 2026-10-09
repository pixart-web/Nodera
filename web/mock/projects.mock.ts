import type { Client, Domain, Certificate, Backup, Container, Deployment, WordPressSite, User } from "@/lib/domain";
import type { Project } from "@/lib/domain";

// ---- Demo data only. Replaced by real API calls in services/. ----
export const MOCK_PROJECTS: Project[] = [
  { id: "pixart", name: "Pixart", domain: "pixart.pt", type: "WORDPRESS", status: "ONLINE", containers: 2, lastDeploy: "há 2 dias", lastBackup: "há 6 h", initial: "P", color: "#FFFFFF", clientId: "c1" },
  { id: "casa-das-fardas", name: "Casa das Fardas", domain: "casadasfardas.pixart.pt", type: "WORDPRESS", status: "PENDING", statusLabel: "A propagar", containers: 2, lastDeploy: "há 28 min", lastBackup: "há 1 dia", initial: "C", color: "#D97706", clientId: "c2" },
  { id: "motogreen", name: "Motogreen", domain: "motogreen.pixart.pt", type: "WORDPRESS", status: "ONLINE", containers: 2, lastDeploy: "há 12 min", lastBackup: "há 3 h", initial: "M", color: "#16A34A", clientId: "c3" },
  { id: "kiko", name: "Kiko", domain: "kiko.pixart.pt", type: "APPLICATION", status: "ONLINE", containers: 3, lastDeploy: "há 3 h", lastBackup: "há 8 h", initial: "K", color: "#8B5CF6", clientId: "c1" },
  { id: "cyberaudit", name: "CyberAudit", domain: "cyberaudit.pixart.pt", type: "APPLICATION", status: "ONLINE", containers: 4, lastDeploy: "há 4 h", lastBackup: "há 5 h", initial: "C", color: "#2563EB", clientId: "c1" },
  { id: "searchanvil", name: "SearchAnvil", domain: "searchanvil.pixart.pt", type: "APPLICATION", status: "WARNING", statusLabel: "Em testes", containers: 2, lastDeploy: "há 6 h", lastBackup: "—", initial: "S", color: "#3B82F6", clientId: "c1" },
];

export const MOCK_CLIENTS: Client[] = [
  { id: "c1", name: "Pixart", projects: 4 }, { id: "c2", name: "Casa das Fardas, Lda", projects: 1 }, { id: "c3", name: "Motogreen", projects: 1 },
];

export const MOCK_DOMAINS: Domain[] = [
  { id: "d1", name: "pixart.pt", project: "Pixart", dns: "OK", ssl: "OK", status: "ONLINE", expires: "12 Mar 2027" },
  { id: "d2", name: "casadasfardas.pixart.pt", project: "Casa das Fardas", dns: "PENDING", ssl: "PENDING", status: "PENDING", expires: "—" },
  { id: "d3", name: "motogreen.pixart.pt", project: "Motogreen", dns: "OK", ssl: "OK", status: "ONLINE", expires: "—" },
  { id: "d4", name: "kiko.pixart.pt", project: "Kiko", dns: "OK", ssl: "OK", status: "ONLINE", expires: "—" },
  { id: "d5", name: "searchanvil.pixart.pt", project: "SearchAnvil", dns: "MISCONFIGURED", ssl: "NONE", status: "WARNING", expires: "—" },
];

export const MOCK_CERTS: Certificate[] = [
  { id: "s1", domain: "pixart.pt", issuer: "Let's Encrypt R11", validFrom: "12 Set 2026", expires: "11 Dez 2026", daysRemaining: 63, status: "ONLINE", autoRenewal: true },
  { id: "s2", domain: "motogreen.pixart.pt", issuer: "Let's Encrypt R11", validFrom: "24 Set 2026", expires: "23 Dez 2026", daysRemaining: 75, status: "ONLINE", autoRenewal: true },
  { id: "s3", domain: "kiko.pixart.pt", issuer: "Let's Encrypt R11", validFrom: "01 Set 2026", expires: "30 Nov 2026", daysRemaining: 52, status: "ONLINE", autoRenewal: true },
  { id: "s4", domain: "cyberaudit.pixart.pt", issuer: "Let's Encrypt R11", validFrom: "20 Ago 2026", expires: "18 Nov 2026", daysRemaining: 40, status: "WARNING", autoRenewal: false },
];

export const MOCK_BACKUPS: Backup[] = [
  { id: "b1", project: "Pixart", type: "FULL", size: "1.2 GB", created: "há 6 h", status: "ONLINE", retention: "30 dias" },
  { id: "b2", project: "Motogreen", type: "DATABASE", size: "84 MB", created: "há 3 h", status: "ONLINE", retention: "14 dias" },
  { id: "b3", project: "Kiko", type: "FILES", size: "420 MB", created: "há 8 h", status: "ONLINE", retention: "14 dias" },
  { id: "b4", project: "Casa das Fardas", type: "FULL", size: "2.1 GB", created: "há 1 dia", status: "ONLINE", retention: "30 dias" },
];

export const MOCK_CONTAINERS: Container[] = [
  { id: "k1", name: "app", image: "nodera/app:latest", status: "ONLINE", uptime: "2 d 4 h" },
  { id: "k2", name: "db", image: "mariadb:11", status: "ONLINE", uptime: "12 d" },
  { id: "k3", name: "redis", image: "redis:7-alpine", status: "ONLINE", uptime: "12 d" },
];

export const MOCK_DEPLOYMENTS: Deployment[] = [
  { id: "dp1", project: "Motogreen", ref: "main · 3f9c1a2", status: "ONLINE", at: "há 12 min" },
  { id: "dp2", project: "Kiko", ref: "main · a81be07", status: "ONLINE", at: "há 3 h" },
];

export const MOCK_WP: WordPressSite[] = [
  { id: "pixart", name: "Pixart", domain: "pixart.pt", wpVersion: "6.7.1", php: "8.3", woocommerce: false, ssl: "OK", lastBackup: "há 6 h", status: "ONLINE" },
  { id: "casa-das-fardas", name: "Casa das Fardas", domain: "casadasfardas.pixart.pt", wpVersion: "6.6.2", php: "8.2", woocommerce: true, ssl: "PENDING", lastBackup: "há 1 dia", status: "PENDING" },
  { id: "motogreen", name: "Motogreen", domain: "motogreen.pixart.pt", wpVersion: "6.7.1", php: "8.3", woocommerce: true, ssl: "OK", lastBackup: "há 3 h", status: "ONLINE" },
];

export const MOCK_USERS: User[] = [
  { id: "u1", name: "Alexandre Pinto", email: "alexandre@pixart.pt", role: "Administrador", lastSeen: "agora", status: "ONLINE" },
  { id: "u2", name: "Equipa Pixart", email: "equipa@pixart.pt", role: "Operador", lastSeen: "há 2 h", status: "OFFLINE" },
];
