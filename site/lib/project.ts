export const repository = "https://github.com/Nathandelenclos/agent-stats";
export const releases = `${repository}/releases`;
export const siteUrl = "https://agent-stats.delenclosnathan.fr";
export const docsPath = "/docs/";
export const installCommand = "brew install nathandelenclos/tap/agent-stats";

// Fumadocs retire le slash final de ses chemins internes.
export function publishedPath(path: string) {
  return path.endsWith("/") ? path : `${path}/`;
}
