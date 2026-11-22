import Link from "next/link";
import { BarChart3, List, MessageSquare, ShieldCheck, Terminal } from "lucide-react";
import { CodeBlock, Pre } from "fumadocs-ui/components/codeblock";
import { HomeLayout } from "fumadocs-ui/layouts/home";
import { ReportPreview } from "../components/report-preview";
import { navigation } from "../lib/navigation";
import { installCommand, releases, repository, siteUrl } from "../lib/project";
import { JsonLd, pageMetadata } from "../lib/seo";

export const metadata = pageMetadata(
  "/",
  "Claude Code and Codex usage analytics",
  "Track tokens, cache usage, and estimated costs for Claude Code and Codex. agent-stats reads session logs on your machine and keeps its SQLite index local.",
);

const software = {
  "@context": "https://schema.org",
  "@type": "SoftwareSourceCode",
  name: "agent-stats",
  description: metadata.description,
  url: `${siteUrl}/`,
  codeRepository: repository,
  license: `${repository}/blob/main/LICENSE`,
  author: {
    "@type": "Person",
    name: "Nathan Delenclos",
    url: "https://github.com/Nathandelenclos",
  },
};

const views = [
  {
    title: "Usage and cost",
    summary: "Compare requests, tokens, cache hits, and estimated cost across coding agents, models, and projects.",
    href: "/docs/dashboard/#usage",
    icon: BarChart3,
  },
  {
    title: "Session history",
    summary: "Open a session to see its models, tool calls, subagents, and prompt signals.",
    href: "/docs/dashboard/#sessions",
    icon: List,
  },
  {
    title: "Prompt review",
    summary: "Find requests you keep repeating and decide what belongs in your project instructions.",
    href: "/docs/prompts/",
    icon: MessageSquare,
  },
];

export default function HomePage() {
  return (
    <HomeLayout {...navigation}>
      <JsonLd data={software} />
      <div className="landing">
        <section className="hero" aria-labelledby="home-title">
          <div className="landing-wrap hero-layout">
            <div>
              <h1 id="home-title">Track Claude Code and Codex usage</h1>
              <p className="hero-intro">
                agent-stats is an open-source CLI and local dashboard. See how many tokens your coding agents used,
                how much input came from cache, and what the requests are estimated to cost.
              </p>
              <div className="hero-actions">
                <Link className="primary-action" href="/docs/install/">Get started</Link>
                <Link className="secondary-action" href="/docs/">Read the docs</Link>
              </div>
              <CodeBlock className="install-command" title="Install on macOS with Homebrew" icon={<Terminal size={16} aria-hidden="true" />}>
                <Pre><code>{installCommand}</code></Pre>
              </CodeBlock>
              <Link className="install-alternatives" href="/docs/install/#choose-your-operating-system">Linux and Windows downloads</Link>
            </div>
            <ReportPreview />
          </div>
        </section>

        <section className="landing-wrap integrations" aria-labelledby="integrations-title">
          <h2 id="integrations-title">Works with your coding agents</h2>
          <ul>
            <li>Claude Code</li>
            <li>Codex</li>
            <li>OpenCode</li>
            <li>Cursor</li>
            <li>Gemini CLI</li>
          </ul>
          <Link href="/docs/coding-agents/">View all supported agents</Link>
        </section>

        <section className="landing-wrap views" aria-labelledby="views-title">
          <h2 id="views-title">From totals to individual sessions</h2>
          <div className="view-grid">
            {views.map(({ title, summary, href, icon: Icon }) => (
              <article className="view" key={title}>
                <Icon className="view-icon" size={25} aria-hidden="true" />
                <h3><Link href={href}>{title}</Link></h3>
                <p>{summary}</p>
              </article>
            ))}
          </div>
          <p className="mt-6 max-w-prose leading-relaxed text-fd-muted-foreground">
            Run <code>agent-stats --all --since 7d --plain</code> after a session to see seven days of activity.
            The report flags requests with unknown prices. Cache and other metrics depend on the data each reader can extract.
            <Link className="ml-1 underline" href="/docs/first-report/#read-the-totals">How to read the totals</Link>
          </p>
        </section>

        <section className="landing-wrap privacy-wrap" aria-labelledby="privacy-title">
          <div className="privacy-panel">
            <ShieldCheck size={30} aria-hidden="true" />
            <div>
              <h2 id="privacy-title">Your session logs stay on your machine.</h2>
              <p>agent-stats opens logs read-only and writes a local SQLite index. The dashboard listens on your machine by default.</p>
              <Link href="/docs/privacy/">See what the index stores</Link>
            </div>
          </div>
        </section>
      </div>

      <footer className="site-footer">
        <div className="landing-wrap">
          <span>agent-stats</span>
          <nav aria-label="Project resources">
            <Link href="/docs/">Documentation</Link>
            <a href={releases}>Releases</a>
            <a href={repository}>GitHub</a>
            <a href={`${repository}/blob/main/LICENSE`}>MIT license</a>
          </nav>
        </div>
      </footer>
    </HomeLayout>
  );
}
