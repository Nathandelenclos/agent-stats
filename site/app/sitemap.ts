import type { MetadataRoute } from "next";
import { publishedPath, siteUrl } from "../lib/project";
import { source } from "../lib/source";

export const dynamic = "force-static";

export default function sitemap(): MetadataRoute.Sitemap {
  return [{ url: `${siteUrl}/` }, ...source.getPages().map((page) => ({ url: `${siteUrl}${publishedPath(page.url)}` }))];
}
