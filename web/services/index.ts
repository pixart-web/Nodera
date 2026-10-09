// Data-access layer. Components call these functions only — never fetch()
// directly. Today they resolve mock/ data; swap each body for a real API
// call (lib/api.ts) when the backend endpoint exists. Signatures stay stable.
import type { Activity, Backup, Certificate, Client, Container, Deployment, Domain, LogLine, MetricRange, MetricSnapshot, Project, ServiceHealth, User, WordPressSite } from "@/lib/domain";
import { MOCK_ACTIVITY, MOCK_SERVICES, mockLogs, mockMetrics } from "@/mock/services.mock";
import { MOCK_BACKUPS, MOCK_CERTS, MOCK_CLIENTS, MOCK_CONTAINERS, MOCK_DEPLOYMENTS, MOCK_DOMAINS, MOCK_PROJECTS, MOCK_USERS, MOCK_WP } from "@/mock/projects.mock";

const latency = <T,>(v: T, ms = 220): Promise<T> => new Promise((r) => setTimeout(() => r(v), ms));

export const projectsService = {
  list: (): Promise<Project[]> => latency(MOCK_PROJECTS),
  get: async (id: string): Promise<Project | null> => latency(MOCK_PROJECTS.find((p) => p.id === id) ?? null),
  containers: (_id: string): Promise<Container[]> => latency(MOCK_CONTAINERS),
  deployments: (id: string): Promise<Deployment[]> => latency(MOCK_DEPLOYMENTS.filter((d) => MOCK_PROJECTS.find((p) => p.id === id)?.name === d.project)),
};
export const clientsService = { list: (): Promise<Client[]> => latency(MOCK_CLIENTS) };
export const domainsService = { list: (): Promise<Domain[]> => latency(MOCK_DOMAINS) };
export const certificatesService = { list: (): Promise<Certificate[]> => latency(MOCK_CERTS) };
export const backupsService = { list: (): Promise<Backup[]> => latency(MOCK_BACKUPS) };
export const wordpressService = { list: (): Promise<WordPressSite[]> => latency(MOCK_WP) };
export const usersService = { list: (): Promise<User[]> => latency(MOCK_USERS) };
export const servicesHealthService = { list: (): Promise<ServiceHealth[]> => latency(MOCK_SERVICES) };
export const activityService = { list: (): Promise<Activity[]> => latency(MOCK_ACTIVITY) };
export const metricsService = { get: (range: MetricRange): Promise<MetricSnapshot> => latency(mockMetrics(range), 150) };
export const logsService = { list: (): Promise<LogLine[]> => latency(mockLogs()) };
