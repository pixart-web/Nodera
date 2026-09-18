// Client-only session storage. The Nodera API uses bearer tokens, not
// cookies (ADR-005), so there is no CSRF surface here — storing the token
// in localStorage is the correct tradeoff for a bearer-token API, not a
// workaround. This module is only ever imported from client components.

const SESSION_TOKEN_KEY = "nodera.session_token";
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

export function getSessionToken(): string | null {
  if (!isBrowser()) return null;
  return window.localStorage.getItem(SESSION_TOKEN_KEY);
}

export function setSessionToken(token: string): void {
  if (!isBrowser()) return;
  window.localStorage.setItem(SESSION_TOKEN_KEY, token);
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
  window.localStorage.removeItem(SESSION_TOKEN_KEY);
  window.localStorage.removeItem(ORG_ID_KEY);
  window.localStorage.removeItem(USER_KEY);
}
