// Central configuration for the public marketing site (app/(marketing)).
//
// Nothing here is a final production value. Empty strings are deliberate
// placeholders: every consumer falls back to something that works today
// (the in-app sign-up, an on-page contact anchor) instead of pretending a
// domain or address exists. Set the NEXT_PUBLIC_NODERA_* variables at build
// time, or edit the defaults below, once the real values are decided.

const env = (v: string | undefined) => (v ?? "").trim();

export const siteConfig = {
  name: "Nodera",
  /** Canonical origin, e.g. "https://nodera.example". Empty = no canonical/OG URLs emitted. */
  domain: env(process.env.NEXT_PUBLIC_NODERA_SITE_DOMAIN),
  /** Sales/contact address. Empty = a clearly marked placeholder is shown. */
  contactEmail: env(process.env.NEXT_PUBLIC_NODERA_CONTACT_EMAIL),
  /** Booking/demo link for "Talk to our team". Empty = falls back to contactEmail, then the contact anchor. */
  demoUrl: env(process.env.NEXT_PUBLIC_NODERA_DEMO_URL),
  /** Where "Start with Nodera" goes. Empty = this deployment's own sign-up. */
  signupUrl: env(process.env.NEXT_PUBLIC_NODERA_SIGNUP_URL),
  /** Optional muted background loop for the hero (mp4/webm). Empty = topology only. */
  heroVideoUrl: env(process.env.NEXT_PUBLIC_NODERA_HERO_VIDEO_URL),
  price: { display: "€99", period: "/month", amount: 99, currency: "EUR" },
} as const;

export const siteLinks = {
  start: siteConfig.signupUrl || "/login?mode=signup",
  signIn: "/app",
  talk: siteConfig.demoUrl || (siteConfig.contactEmail ? `mailto:${siteConfig.contactEmail}` : "#contact"),
  privacy: "#legal-privacy",
  terms: "#legal-terms",
};

export const siteMeta = {
  title: "Nodera — Infrastructure, Under Control",
  description:
    "Nodera is an intelligent infrastructure control platform for managing servers, deployments, applications, backups and operations from one place.",
};
