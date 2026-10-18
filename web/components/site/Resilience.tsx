"use client";

import { useInView } from "./hooks";
import { DemoTag, LayerHeader } from "./primitives";

const STAGES = [
  { name: "Backup", detail: "streamed archive · 2.4 GB" },
  { name: "Verify", detail: "sha256 recorded" },
  { name: "Store", detail: "off-site · SFTP · pinned host key" },
  { name: "Test", detail: "remote copy re-read · checksum match" },
  { name: "Restore", detail: "approved · safety snapshot first" },
  { name: "Verify", detail: "finalized · auto-rollback armed" },
];

export function Resilience() {
  const [ref, inView] = useInView<HTMLElement>({ once: false, rootMargin: "0px" });

  return (
    <section ref={ref} id="resilience" className="layer res" data-inview={inView} aria-labelledby="res-title" data-layer="07" data-layer-name="Resilience">
      <div className="container">
        <LayerHeader
          n="07"
          name="RESILIENCE"
          id="res-title"
          title="Backups are only useful if you can restore them."
          lede="Nodera turns backup into operational resilience: archives are checksummed, copied off-site and verified on read-back. A restore is approved, takes a safety snapshot first, refuses a corrupt source and rolls itself back automatically if anything fails."
        />
        <div className="pipeline-wrap">
          <div className="pipeline-head"><span className="label">Recovery pipeline · mariadb-shop</span><DemoTag /></div>
          <div className="pipeline-rail">
          <ol className="pipeline" style={{ ["--n" as string]: STAGES.length }}>
            {STAGES.map((s, i) => (
              <li key={i} className="stage" style={{ ["--i" as string]: i }}>
                <span className="stage-node" aria-hidden><span className="stage-fill" /></span>
                <span className="mono stage-n">{String(i + 1).padStart(2, "0")}</span>
                <span className="stage-name">{s.name}</span>
                <span className="stage-detail mono">{s.detail}</span>
              </li>
            ))}
          </ol>
          <div className="packet-track" aria-hidden><span className="packet" /></div>
          </div>
        </div>
        <p className="res-note">A backup that has never been read back is a hope. Nodera reads it back.</p>
      </div>
    </section>
  );
}
