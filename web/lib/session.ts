// Client-only session storage. As of the browser-authentication hardening
// pass, the human session itself lives in an HttpOnly cookie the API sets
// on login (docs/SECURITY.md "Browser authentication") — this module
// never stores the raw session token, and no client code can read it via
// document.cookie either, since HttpOnly cookies are invisible to
// JavaScript by design. What's stored here is a non-sensitive "who's
// probably logged in" hint used only for immediate client-side routing
// (e.g. "skip the login page") — the server-side cookie is the actual
// source of truth for every real authorization decision; a stale/cleared
// hint just means an API call 404s/401s and lib/api.ts's fetch wrapper
// redirects to /login, same as an expired cookie would. This module is
// only ever imported from client components.

const ORG_ID_KEY = "nodera.org_id";
const USER_KEY = "nodera.user";

export interface StoredUser {
  id: string;
  email: string;
  display_name: string;
}

function isBrowser(): boolean {
  return typeof window !== "undefined";
}

// getCSRFToken reads the (deliberately non-HttpOnly) CSRF cookie the API
// sets alongside the session cookie on login — see lib/api.ts, which
// echoes this value back as the X-CSRF-Token header on every
// state-changing request (the double-submit CSRF pattern). It is not a
// secret: its security property comes from same-origin policy preventing
// a different origin's JavaScript from reading it, not from hiding it
// from this origin's own code.
export function getCSRFToken(): string | null {
  if (!isBrowser()) return null;
  const match = document.cookie.match(/(?:^|;\s*)nodera_csrf=([^;]+)/);
  return match?.[1] ? decodeURIComponent(match[1]) : null;
}

export function getStoredUser(): StoredUser | null {
  if (!isBrowser()) return null;
  const raw = window.localStorage.getItem(USER_KEY);
  if (!raw) return null;
  try {
    return JSON.parse(raw) as StoredUser;
  } catch {
    return null;
  }
}

export function setStoredUser(user: StoredUser): void {
  if (!isBrowser()) return;
  window.localStorage.setItem(USER_KEY, JSON.stringify(user));
}

export function getCurrentOrgId(): string | null {
  if (!isBrowser()) return null;
  return window.localStorage.getItem(ORG_ID_KEY);
}

export function setCurrentOrgId(orgId: string): void {
  if (!isBrowser()) return;
  window.localStorage.setItem(ORG_ID_KEY, orgId);
}

export function clearSession(): void {
  if (!isBrowser()) return;
  window.localStorage.removeItem(ORG_ID_KEY);
  window.localStorage.removeItem(USER_KEY);
}
