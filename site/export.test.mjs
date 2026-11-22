import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { test } from "node:test";
import { staticClient } from "fumadocs-core/search/client/orama-static";

const output = new URL("./out/", import.meta.url);
const read = (file) => readFile(new URL(file, output), "utf8");

test("the homepage and every documentation chapter contain prerendered content", async () => {
  const home = await read("index.html");
  assert.equal([...home.matchAll(/<main\b/g)].length, 1);
  assert.match(home, /<h1[^>]*>.*coding agent/i);
  assert.match(home, /brew install nathandelenclos\/tap\/agent-stats/);

  const metadata = JSON.parse(await readFile(new URL("./content/docs/meta.json", import.meta.url), "utf8"));
  for (const chapter of metadata.pages.filter((page) => !page.startsWith("---"))) {
    const folder = chapter === "index" ? "docs" : `docs/${chapter}`;
    const html = await read(`${folder}/index.html`);
    assert.match(html, /<h1[^>]*>[^<]+<\/h1>/, chapter);
    assert.match(html, /rel="canonical"/, chapter);
  }
});

test("the generated index returns results with the browser search client", async (t) => {
  const searchIndex = await read("search-index.json");
  t.mock.method(globalThis, "fetch", async (url) => {
    assert.equal(url, "/search-index.json");
    return new Response(searchIndex);
  });
  const results = await staticClient({ from: "/search-index.json" }).search("loopback");
  assert.ok(results.some(({ url }) => url.startsWith("/docs/privacy")), JSON.stringify(results));
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

test("documentation links point to exported pages and headings", async () => {
  const sitemap = await read("sitemap.xml");
  const pages = new Map();
  for (const [, path] of sitemap.matchAll(/<loc>https:\/\/agent-stats\.delenclosnathan\.fr(\/[^<]*)<\/loc>/g)) {
    const folder = path.replace(/\/$/, "");
    pages.set(folder || "/", await read(`${folder.slice(1)}${folder ? "/" : ""}index.html`));
  }
  assert.ok(pages.has("/"));
  assert.ok(pages.has("/docs"));
  for (const [source, html] of pages) {
    for (const [, href] of html.matchAll(/href="(\/docs(?:[\/#][^"]*)?)"/g)) {
      const [target, heading] = href.split("#");
      const destination = pages.get(target.replace(/\/$/, ""));
      assert.ok(destination, `${source} links to missing page ${href}`);
      if (heading) assert.ok(destination.includes(`id="${heading}"`), `${source} links to missing heading ${href}`);
    }
  }
});
