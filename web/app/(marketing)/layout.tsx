import type { Metadata, Viewport } from "next";
import { siteConfig, siteMeta } from "@/lib/site-config";
import "@/styles/nodera-tokens.css";
import "./site.css";


export const metadata: Metadata = {
  // No final domain yet (lib/site-config.ts): canonical and absolute OG URLs
  // are only emitted once NEXT_PUBLIC_NODERA_SITE_DOMAIN is configured.
  ...(siteConfig.domain ? { metadataBase: new URL(siteConfig.domain), alternates: { canonical: "/" } } : {}),
  title: siteMeta.title,
  description: siteMeta.description,
  applicationName: "Nodera",
  openGraph: {
    type: "website",
    siteName: "Nodera",
    title: siteMeta.title,
    description: siteMeta.description,
    locale: "en",
    // Share image needs an absolute URL, so it is only advertised once the domain is known.
    ...(siteConfig.domain ? { url: "/", images: [{ url: "/brand/nodera-og.png", width: 1200, height: 630, alt: "NODERA — Infrastructure, under control." }] } : {}),
  },
  twitter: {
    card: siteConfig.domain ? "summary_large_image" : "summary",
    title: siteMeta.title,
    description: siteMeta.description,
    ...(siteConfig.domain ? { images: ["/brand/nodera-og.png"] } : {}),
  },
};

export const viewport: Viewport = { themeColor: "#05070A", colorScheme: "dark" };

export default function MarketingLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en" suppressHydrationWarning>
      <head>
        {/* Lets CSS hide pre-reveal states only when JS will reveal them. */}
        <script dangerouslySetInnerHTML={{ __html: "document.documentElement.classList.add('js')" }} />
      </head>
      <body className="site">{children}</body>
    </html>
  );
}
