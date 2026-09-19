import type { ApiErrorBody } from "./types";
import { clearSession, getCSRFToken, getCurrentOrgId } from "./session";

// Defaults to the local dev API — override with NEXT_PUBLIC_NODERA_API_URL
// for any other environment (see docs/DEPLOYMENT.md; no production URL is
// assumed here per rule 30).
const API_BASE = process.env.NEXT_PUBLIC_NODERA_API_URL ?? "http://localhost:8080";

const MUTATING_METHODS = new Set(["POST", "PUT", "PATCH", "DELETE"]);

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
  const method = opts.method ?? "GET";

  // The human session lives in an HttpOnly cookie the browser attaches
  // automatically (docs/SECURITY.md "Browser authentication") — this
  // client never reads or sends a bearer token itself. `credentials:
  // "include"` is what makes fetch actually send/accept that cookie on a
  // cross-origin request (the API and the web app are different origins
  // in dev, and typically different subdomains in production); the API's
  // CORS layer must (and does) pair this with an exact-origin allow-list
  // and Access-Control-Allow-Credentials, never a wildcard.
  if (!opts.skipAuth && MUTATING_METHODS.has(method)) {
    const csrf = getCSRFToken();
    if (csrf) headers["X-CSRF-Token"] = csrf;
  }
  if (opts.withOrg) {
    const orgId = getCurrentOrgId();
    if (orgId) headers["X-Nodera-Org"] = orgId;
  }

  const res = await fetch(`${API_BASE}${path}`, {
    method,
    headers,
    credentials: "include",
    body: opts.body !== undefined ? JSON.stringify(opts.body) : undefined,
  });

  if (res.status === 204) {
    return undefined as T;
  }

  const text = await res.text();
  const data = text ? JSON.parse(text) : undefined;

  if (!res.ok) {
    const body = data as ApiErrorBody | undefined;
    const code = body?.error?.code ?? "UNKNOWN_ERROR";
    // The session cookie expired/was revoked server-side (not a CSRF
    // failure, which is FORBIDDEN, not UNAUTHENTICATED) — the client-side
    // "probably logged in" hint (lib/session.ts) is now stale. Clear it
    // and send the user back to /login rather than leaving them staring
    // at a page that will fail every subsequent call the same way.
    if (res.status === 401 && code === "UNAUTHENTICATED" && !opts.skipAuth && typeof window !== "undefined") {
      clearSession();
      window.location.href = "/login";
    }
    throw new ApiError(
      res.status,
      code,
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
