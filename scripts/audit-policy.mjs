#!/usr/bin/env node
// Dependency vulnerability policy for web/.
// Fails on ANY high/critical advisory unless it is listed — by exact GHSA id and
// package — in web/audit-exceptions.json with a reason, exposure, mitigation and
// an expiry date. Expired exceptions fail. New advisories always fail.
import { execSync } from "node:child_process";
import { readFileSync } from "node:fs";

const root = new URL("../web/", import.meta.url).pathname;
let raw;
try { raw = execSync("npm audit --json", { cwd: root, encoding: "utf8", stdio: ["ignore", "pipe", "ignore"] }); }
catch (e) { raw = e.stdout; } // npm audit exits non-zero when it finds anything
const report = JSON.parse(raw);
const exceptions = JSON.parse(readFileSync(root + "audit-exceptions.json", "utf8")).exceptions;
const today = new Date().toISOString().slice(0, 10);
const gate = new Set(["high", "critical"]);

// Collect every advisory (leaf nodes of the `via` graph), keyed by GHSA id.
const advisories = new Map();
for (const [pkg, v] of Object.entries(report.vulnerabilities ?? {})) {
  for (const via of v.via) {
    if (typeof via !== "object") continue;
    const id = via.url.split("/").pop();
    advisories.set(id, { id, pkg: via.name ?? pkg, severity: via.severity, title: via.title });
  }
}

let failed = false;
for (const a of advisories.values()) {
  if (!gate.has(a.severity)) { console.log(`info   ${a.id} ${a.pkg} (${a.severity}) below gate`); continue; }
  const ex = exceptions.find((e) => e.id === a.id && e.package === a.pkg);
  if (!ex) { console.log(`FAIL   ${a.id} ${a.pkg} (${a.severity}): ${a.title}`); failed = true; }
  else if (ex.expires < today) { console.log(`FAIL   ${a.id} ${a.pkg}: exception expired ${ex.expires}`); failed = true; }
  else console.log(`accept ${a.id} ${a.pkg} (${a.severity}) until ${ex.expires}`);
}
// Exceptions that no longer match anything must be removed so the list cannot rot.
for (const ex of exceptions) if (!advisories.has(ex.id)) { console.log(`FAIL   stale exception ${ex.id} — advisory no longer reported; remove it`); failed = true; }
// Transitive-only high findings (no advisory node of their own) are covered by their root advisory above.
process.exit(failed ? 1 : 0);
