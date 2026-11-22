import type { Metadata } from "next";
import type { ReactNode } from "react";
import { RootProvider } from "fumadocs-ui/provider/next";
import { siteUrl } from "../lib/project";
import "@fontsource-variable/manrope";
import "@fontsource-variable/jetbrains-mono";
import "./styles.css";

export const metadata: Metadata = {
  metadataBase: new URL(siteUrl),
  title: { default: "agent-stats | Local usage analytics for AI coding agents", template: "%s | agent-stats" },
  description: "Track usage, estimated cost, and sessions for AI coding agents using the logs on your machine.",
  openGraph: { siteName: "agent-stats", type: "website" },
};

export default function RootLayout({ children }: { children: ReactNode }) {
  return (
    <html lang="en" suppressHydrationWarning>
      <body className="flex min-h-screen flex-col">
        <RootProvider search={{ options: { type: "static", api: "/search-index.json" } }}>
          {children}
        </RootProvider>
      </body>
    </html>
  );
}
