import type { Metadata } from "next";

const shareImage = {
  url: "/social-preview.png",
  width: 1200,
  height: 630,
  alt: "agent-stats. Local usage analytics for Claude Code and Codex.",
};

export function pageMetadata(path: string, title: string, description?: string): Metadata {
  const brandedTitle = `${title} | agent-stats`;
  return {
    title: brandedTitle,
    description,
    alternates: { canonical: path },
    openGraph: {
      type: "website",
      siteName: "agent-stats",
      url: path,
      title: brandedTitle,
      description,
      images: [shareImage],
    },
    twitter: { card: "summary_large_image", title: brandedTitle, description, images: [shareImage] },
  };
}

export function JsonLd({ data }: { data: Record<string, unknown> }) {
  const json = JSON.stringify(data).replaceAll("<", "\\u003c");
  return <script type="application/ld+json" dangerouslySetInnerHTML={{ __html: json }} />;
}
