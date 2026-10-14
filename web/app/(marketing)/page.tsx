import { AiDemo } from "@/components/site/AiDemo";
import { Automation } from "@/components/site/Automation";
import { FinalCta, Pricing, SiteFooter } from "@/components/site/Closing";
import { ControlPlane } from "@/components/site/ControlPlane";
import { Fragmentation } from "@/components/site/Fragmentation";
import { Hero } from "@/components/site/Hero";
import { InfraScene } from "@/components/site/InfraScene";
import { Intelligence } from "@/components/site/Intelligence";
import { LayerIndicator } from "@/components/site/LayerIndicator";
import { ProductShowcase } from "@/components/site/ProductShowcase";
import { Resilience } from "@/components/site/Resilience";
import { Security } from "@/components/site/Security";
import { SiteNav } from "@/components/site/SiteNav";

// Public marketing site. The product lives behind /app (→ /login, /orgs,
// /dashboard); see docs/FRONTEND.md "Marketing site".
const AUDIENCE = ["Hosting companies", "Managed service providers", "Agencies running hundreds of sites", "DevOps & platform teams", "SaaS & technology companies"];

export default function MarketingPage() {
  return (
    <>
      <a href="#main" className="skip-link">Skip to content</a>
      <SiteNav />
      <LayerIndicator />
      <main id="main">
        <Hero />

        <section className="statement" aria-labelledby="statement-title">
          <div className="container statement-inner">
            <h2 id="statement-title" className="statement-text">
              Built for infrastructure teams that cannot afford <span className="t-accent">operational blind spots.</span>
            </h2>
            <ul className="audience" aria-label="Who Nodera is for">
              {AUDIENCE.map((a) => <li key={a}>{a}</li>)}
            </ul>
          </div>
        </section>

        <Fragmentation />
        <ControlPlane />
        <Intelligence />
        <AiDemo />
        <Automation />
        <InfraScene />
        <Security />
        <Resilience />
        <ProductShowcase />
        <Pricing />
        <FinalCta />
      </main>
      <SiteFooter />
    </>
  );
}
