# Branding: SERVON (commercial) vs NODERA (technical)

* **SERVON** = SERV (server) + ON (online): the commercial name. A customer installs it at `servon.<customer-domain>`; it must never depend on a Pixart hostname.
* **NODERA** stays the internal/technical name: repository, Go module, `NODERA_*` variables, `X-Nodera-*` headers, cookies, container names (`nodera-cp-*`), API paths, docs.
* Rule: text a person reads in the web UI comes from `web/lib/brand.ts`. Do not globally rename technical identifiers (it would break the agent guard's `nodera-cp-` namespace, cookies, env, signed protocols).
* Not done: marketing site, billing (€99/month target), customer portal, subscriptions/invoices/VAT/SSO — NOT IMPLEMENTED; see `docs/ROADMAP.md`.
