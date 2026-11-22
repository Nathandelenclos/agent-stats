import type { Metadata } from "next";
import { notFound } from "next/navigation";
import { DocsBody, DocsDescription, DocsPage, DocsTitle } from "fumadocs-ui/layouts/docs/page";
import mdxComponents from "fumadocs-ui/mdx";
import { Tab, Tabs } from "fumadocs-ui/components/tabs";
import { docsPath, publishedPath, siteUrl } from "../../../lib/project";
import { JsonLd, pageMetadata } from "../../../lib/seo";
import { source } from "../../../lib/source";

type DocPageProps = { params: Promise<{ slug?: string[] }> };

export function generateStaticParams() {
  return source.generateParams();
}

export async function generateMetadata({ params }: DocPageProps): Promise<Metadata> {
  const page = source.getPage((await params).slug);
  if (!page) notFound();

  return pageMetadata(publishedPath(page.url), page.data.title, page.data.description);
}

export default async function DocumentationPage({ params }: DocPageProps) {
  const page = source.getPage((await params).slug);
  if (!page) notFound();
  const Content = page.data.body;

  const trail = [
    { name: "agent-stats", url: "/" },
    { name: "Documentation", url: docsPath },
  ];
  const path = publishedPath(page.url);
  if (path !== docsPath) trail.push({ name: page.data.title, url: path });
  const breadcrumbs = {
    "@context": "https://schema.org",
    "@type": "BreadcrumbList",
    itemListElement: trail.map(({ name, url }, index) => ({
      "@type": "ListItem",
      position: index + 1,
      name,
      item: `${siteUrl}${url}`,
    })),
  };

  return (
    <DocsPage toc={page.data.toc}>
      <JsonLd data={breadcrumbs} />
      <DocsTitle>{page.data.title}</DocsTitle>
      <DocsDescription>{page.data.description}</DocsDescription>
      <DocsBody>
        <Content components={{ ...mdxComponents, Tab, Tabs }} />
      </DocsBody>
    </DocsPage>
  );
}
