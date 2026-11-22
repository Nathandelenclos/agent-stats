import { createFromSource } from "fumadocs-core/search/server";
import { publishedPath } from "../../lib/project";
import { source } from "../../lib/source";

export const dynamic = "force-static";
export const GET = createFromSource(source, {
  buildIndex: ({ data, url }) => ({
    title: data.title,
    description: data.description,
    structuredData: data.structuredData,
    id: url,
    url: publishedPath(url),
  }),
}).staticGET;
