import { afterEach, describe, expect, it, vi } from "vitest";

// lib/site-config reads env at import time, so each case re-imports it.
async function load(env: Record<string, string>) {
  vi.resetModules();
  for (const [k, v] of Object.entries(env)) vi.stubEnv(k, v);
  return import("@/lib/site-config");
}

afterEach(() => vi.unstubAllEnvs());

describe("site config", () => {
  it("falls back to working in-app links when no production values are set", async () => {
    const { siteConfig, siteLinks } = await load({
      NEXT_PUBLIC_NODERA_SITE_DOMAIN: "",
      NEXT_PUBLIC_NODERA_CONTACT_EMAIL: "",
      NEXT_PUBLIC_NODERA_DEMO_URL: "",
      NEXT_PUBLIC_NODERA_SIGNUP_URL: "",
    });
    expect(siteConfig.domain).toBe("");
    expect(siteLinks.start).toBe("/login?mode=signup");
    expect(siteLinks.signIn).toBe("/app");
    expect(siteLinks.talk).toBe("#contact");
  });

  it("uses configured values, preferring the demo URL over the contact email", async () => {
    const { siteLinks } = await load({
      NEXT_PUBLIC_NODERA_SIGNUP_URL: "https://app.example/signup",
      NEXT_PUBLIC_NODERA_CONTACT_EMAIL: "team@example.com",
      NEXT_PUBLIC_NODERA_DEMO_URL: "",
    });
    expect(siteLinks.start).toBe("https://app.example/signup");
    expect(siteLinks.talk).toBe("mailto:team@example.com");

    const withDemo = await load({ NEXT_PUBLIC_NODERA_DEMO_URL: " https://cal.example/nodera " });
    expect(withDemo.siteLinks.talk).toBe("https://cal.example/nodera");
  });

  it("keeps the single €99/month price", async () => {
    const { siteConfig } = await load({});
    expect(`${siteConfig.price.display}${siteConfig.price.period}`).toBe("€99/month");
  });
});
