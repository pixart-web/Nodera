import type { ApiErrorBody } from "./types";
import { getCurrentOrgId, getSessionToken } from "./session";

// Defaults to the local dev API — override with NEXT_PUBLIC_NODERA_API_URL
// for any other environment (see docs/DEPLOYMENT.md; no production URL is
// assumed here per rule 30).
const API_BASE = process.env.NEXT_PUBLIC_NODERA_API_URL ?? "http://localhost:8080";

export class ApiError extends Error {
  code: string;
  status: number;
  requestId?: string;

  constructor(status: number, code: string, message: string, requestId?: string) {
    super(message);
    this.status = status;
    this.code = code;
    this.requestId = requestId;
  }
}

interface RequestOptions {
  method?: "GET" | "POST" | "PUT" | "PATCH" | "DELETE";
  body?: unknown;
  // Most routes are organization-scoped and need X-Nodera-Org; auth routes
  // (signup/login) and the organization list/create routes are not.
  withOrg?: boolean;
  skipAuth?: boolean;
}

async function request<T>(path: string, opts: RequestOptions = {}): Promise<T> {
  const headers: Record<string, string> = { "Content-Type": "application/json" };

  if (!opts.skipAuth) {
    const token = getSessionToken();
    if (token) headers["Authorization"] = `Bearer ${token}`;
  }
  if (opts.withOrg) {
    const orgId = getCurrentOrgId();
    if (orgId) headers["X-Nodera-Org"] = orgId;
  }

  const res = await fetch(`${API_BASE}${path}`, {
    method: opts.method ?? "GET",
    headers,
    body: opts.body !== undefined ? JSON.stringify(opts.body) : undefined,
  });

  if (res.status === 204) {
    return undefined as T;
  }

  const text = await res.text();
  const data = text ? JSON.parse(text) : undefined;

  if (!res.ok) {
    const body = data as ApiErrorBody | undefined;
    throw new ApiError(
      res.status,
      body?.error?.code ?? "UNKNOWN_ERROR",
      body?.error?.message ?? `request failed with status ${res.status}`,
      body?.error?.request_id,
    );
  }

  return data as T;
}

export const api = {
  get: <T>(path: string, withOrg = true) => request<T>(path, { method: "GET", withOrg }),
  post: <T>(path: string, body?: unknown, withOrg = true) =>
    request<T>(path, { method: "POST", body, withOrg }),
  put: <T>(path: string, body?: unknown, withOrg = true) =>
    request<T>(path, { method: "PUT", body, withOrg }),
  patch: <T>(path: string, body?: unknown, withOrg = true) =>
    request<T>(path, { method: "PATCH", body, withOrg }),
  del: <T>(path: string, withOrg = true) => request<T>(path, { method: "DELETE", withOrg }),
  // Auth endpoints take no bearer token and no org header.
  postPublic: <T>(path: string, body?: unknown) =>
    request<T>(path, { method: "POST", body, skipAuth: true, withOrg: false }),
};
