// Single source of truth for the COMMERCIAL, user-facing product name.
//
// Internal/technical identifiers stay NODERA (env vars, API paths, headers such as
// X-Nodera-Org, cookies, container names, repository, docs). Only text a person reads
// in the UI comes from here, so the commercial name can change in one place.
//
// SERVON = SERV (server) + ON (online). A customer installs it at
// servon.<customer-domain>; nothing here depends on any Pixart hostname.
export const brand = {
  name: "Servon",
  tagline: "Infrastructure Operating System",
  shortTagline: "Infra Manager",
  version: "v1.0",
} as const;
