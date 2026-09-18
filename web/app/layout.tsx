import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "Nodera",
  description: "Infrastructure, AI, Agent, and Operations control plane",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en" className="dark">
      <body className="min-h-screen antialiased">{children}</body>
    </html>
  );
}
