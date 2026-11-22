import { publishedPath, siteUrl } from "../../lib/project";
import { source } from "../../lib/source";

export const dynamic = "force-static";

export function GET() {
  const pages = source.getPages().map(({ data, url }) => {
    const title = data.title.replace(/[\[\]]/g, "\\$&");
    return `- [${title}](${siteUrl}${publishedPath(url)}): ${data.description?.replace(/\s+/g, " ").trim() ?? ""}`;
  });
  const index = [
    "# agent-stats",
    "",
    "> MIT-licensed CLI and local dashboard for requests, tokens, cache usage, and estimated costs from Claude Code, Codex, and other coding agent logs.",
    "",
    "## Documentation",
    "",
    ...pages,
    "",
  ].join("\n");

  return new Response(index, { headers: { "Content-Type": "text/plain; charset=utf-8" } });
}
