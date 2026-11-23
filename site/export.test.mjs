import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { test } from "node:test";
import { staticClient } from "fumadocs-core/search/client/orama-static";

const output = new URL("./out/", import.meta.url);
const read = (file) => readFile(new URL(file, output), "utf8");
const siteUrl = "https://agent-stats.delenclosnathan.fr";
const metadata = JSON.parse(await readFile(new URL("./content/docs/meta.json", import.meta.url), "utf8"));
const docsPaths = metadata.pages.filter((page) => !page.startsWith("---"))
  .map((chapter) => chapter === "index" ? "/docs/" : "/docs/" + chapter + "/");
const readPage = (path) => read(path.slice(1) + "index.html");
const structuredData = (html) => [...html.matchAll(/<script type="application\/ld\+json">([^<]+)<\/script>/g)]
  .map(([, json]) => JSON.parse(json));

test("the homepage and every documentation chapter contain prerendered content", async () => {
  const home = await read("index.html");
  assert.equal([...home.matchAll(/<main\b/g)].length, 1);
  assert.match(home, /<h1[^>]*>[^<]*Claude Code[^<]*Codex/);
  assert.match(home, /brew install nathandelenclos\/tap\/agent-stats/);

  for (const path of docsPaths) {
    assert.match(await readPage(path), /<h1[^>]*>[^<]+<\/h1>/, path);
  }
});

test("the generated index returns results with the browser search client", async (t) => {
  const searchIndex = await read("search-index.json");
  t.mock.method(globalThis, "fetch", async (url) => {
    assert.equal(url, "/search-index.json");
    return new Response(searchIndex);
  });
  const results = await staticClient({ from: "/search-index.json" }).search("loopback");
  assert.ok(results.some(({ url }) => url.split("#")[0] === "/docs/privacy/"), JSON.stringify(results));
});

test("every previously published guide anchor points to a generated section", async () => {
  const guide = await read("guide.html");
  for (const anchor of ["install", "first-report", "dashboard", "usage", "sessions", "prompts", "privacy", "troubleshooting"]) {
    const target = guide.match(new RegExp(`id="${anchor}"[^>]*>\\s*<a href="([.]/docs/[^"]*)"`))?.[1];
    assert.ok(target, anchor);
    const [path, heading] = target.split("#");
    const html = await read(`${path.slice(2)}index.html`);
    if (heading) assert.ok(html.includes(`id="${heading}"`), target);
  }
});

test("the sitemap, canonicals and documentation links use the final exported URLs", async () => {
  const sitemap = await read("sitemap.xml");
  const urls = [...sitemap.matchAll(/<loc>([^<]+)<\/loc>/g)].map(([, url]) => url);
  assert.deepEqual(urls.sort(), ["/", ...docsPaths].map((path) => siteUrl + path).sort());
  assert.doesNotMatch(sitemap, /<lastmod>/);

  const pages = new Map();
  for (const url of urls) {
    const path = new URL(url).pathname;
    const html = await readPage(path);
    const canonicals = [...html.matchAll(/<link rel="canonical" href="([^"]+)"/g)].map(([, href]) => href);
    assert.deepEqual(canonicals, [url], path);
    assert.doesNotMatch(html, /<meta name="robots" content="[^"]*noindex/i, path);
    pages.set(path, html);
  }

  for (const [source, html] of pages) {
    for (const [, href] of html.matchAll(/href="(\/docs(?:[\/#?][^"]*)?)"/g)) {
      const [target, heading] = href.split("#");
      const destination = pages.get(target);
      assert.ok(destination, `${source} links to missing page ${href}`);
      if (heading) assert.ok(destination.includes(`id="${heading}"`), `${source} links to missing heading ${href}`);
    }
  }
});

test("public search crawlers can discover the sitemap and documentation", async () => {
  const robots = await read("robots.txt");
  assert.match(robots, /^User-Agent: \*\r?\nAllow: \/\r?$/im);
  assert.doesNotMatch(robots, /^Disallow:/im);
  assert.ok(robots.includes(`Sitemap: ${siteUrl}/sitemap.xml`));
});

test("the homepage identifies the MIT-licensed source and the guides describe their breadcrumbs", async () => {
  const [software] = structuredData(await readPage("/"));
  const repository = "https://github.com/Nathandelenclos/agent-stats";
  assert.equal(software["@context"], "https://schema.org");
  assert.equal(software["@type"], "SoftwareSourceCode");
  assert.equal(software.name, "agent-stats");
  assert.equal(software.url, `${siteUrl}/`);
  assert.equal(software.codeRepository, repository);
  assert.equal(software.license, `${repository}/blob/main/LICENSE`);
  assert.deepEqual(software.author, {
    "@type": "Person",
    name: "Nathan Delenclos",
    url: "https://github.com/Nathandelenclos",
  });

  for (const path of docsPaths) {
    const html = await readPage(path);
    const [breadcrumbs] = structuredData(html);
    const title = html.match(/<h1[^>]*>([^<]+)<\/h1>/)[1];
    const trail = [["agent-stats", "/"], ["Documentation", "/docs/"]];
    if (path !== "/docs/") trail.push([title, path]);
    assert.equal(breadcrumbs["@context"], "https://schema.org", path);
    assert.equal(breadcrumbs["@type"], "BreadcrumbList", path);
    assert.deepEqual(breadcrumbs.itemListElement, trail.map(([name, item], index) => ({
      "@type": "ListItem", position: index + 1, name, item: siteUrl + item,
    })), path);
  }
});

test("the homepage declares its site name and a published favicon", async () => {
  const home = await readPage("/");
  const website = structuredData(home).find((item) => item["@type"] === "WebSite");
  assert.deepEqual(website, {
    "@context": "https://schema.org",
    "@type": "WebSite",
    name: "agent-stats",
    url: `${siteUrl}/`,
  });

  const icon = home.match(/<link rel="icon" href="(\/favicon\.ico)(?:\?[^"]*)?"/)?.[1];
  assert.equal(icon, "/favicon.ico");
  const bytes = await readFile(new URL(icon.slice(1), output));
  assert.equal(bytes.toString("hex", 0, 4), "00000100");
});

test("the homepage and guides have share previews with an exported image", async () => {
  const imagePath = "/social-preview.png";
  const image = await readFile(new URL(imagePath.slice(1), output));
  assert.equal(image.toString("hex", 0, 8), "89504e470d0a1a0a");
  assert.equal(image.readUInt32BE(16), 1200);
  assert.equal(image.readUInt32BE(20), 630);
  for (const path of ["/", ...docsPaths]) {
    const html = await readPage(path);
    assert.ok(html.includes(`property="og:url" content="${siteUrl}${path}"`), path);
    assert.ok(html.includes(`property="og:image" content="${siteUrl}${imagePath}"`), path);
    assert.ok(html.includes(`name="twitter:image" content="${siteUrl}${imagePath}"`), path);
    assert.match(html, /property="og:image:alt" content="[^"]+"/, path);
    assert.match(html, /name="twitter:card" content="summary_large_image"/, path);
    const title = html.match(/<title>([^<]+)<\/title>/)[1];
    const description = html.match(/name="description" content="([^"]+)"/)[1];
    assert.ok(html.includes(`property="og:title" content="${title}"`), path);
    assert.ok(html.includes(`property="og:description" content="${description}"`), path);
  }
});

test("llms.txt indexes the published documentation chapters once each", async () => {
  const index = await read("llms.txt");
  assert.match(index, /^# agent-stats\r?\n\r?\n> .+/);
  assert.match(index, /^## Documentation$/m);
  const links = [...index.matchAll(/^- \[([^\]]+)\]\(([^)]+)\): (.+)$/gm)];
  assert.deepEqual(links.map(([, , url]) => url).sort(), docsPaths.map((path) => siteUrl + path).sort());
  for (const [, name, url, description] of links) {
    const html = await readPage(new URL(url).pathname);
    assert.ok(html.includes(`>${name}</h1>`), url);
    assert.ok(html.includes(`name="description" content="${description.replaceAll("&", "&amp;").replaceAll('"', "&quot;")}"`), url);
  }
});
