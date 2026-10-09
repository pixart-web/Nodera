import type { Metadata } from "next";
import { Inter } from "next/font/google";
import "./globals.css";

const inter = Inter({ subsets: ["latin"], variable: "--font-inter", display: "swap" });

export const metadata: Metadata = {
  title: "Nodera",
  description: "Infrastructure, AI, Agent, and Operations control plane",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="pt" className={`dark ${inter.variable}`}>
      <body className="min-h-screen bg-nd-bg font-sans antialiased">{children}</body>
    </html>
  );
}
