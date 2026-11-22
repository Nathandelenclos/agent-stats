import { useEffect, useId, useRef, useState } from "react";

const renderer = () => import("mermaid").then(({ default: mermaid }) => {
  mermaid.initialize({
    startOnLoad: false,
    suppressErrorRendering: true,
    securityLevel: "strict",
    htmlLabels: false,
    theme: "base",
    themeVariables: { fontFamily: 'system-ui, -apple-system, "Segoe UI", sans-serif', fontSize: "14px" },
    flowchart: { useMaxWidth: false, nodeSpacing: 16, rankSpacing: 46, padding: 12, diagramPadding: 16, curve: "monotoneX" },
  });
  return mermaid;
});

let mermaidModule: ReturnType<typeof renderer> | undefined;

interface Props {
  source: string;
  names: string[];
  hasFork: boolean;
  selectedIndex: number;
  onSelect: (index: number) => void;
}

export function RelationDiagram({ source, names, hasFork, selectedIndex, onSelect }: Props) {
  const host = useRef<HTMLDivElement>(null);
  const select = useRef(onSelect);
  const [status, setStatus] = useState<"loading" | "ready" | "failed">("loading");
  const id = useId().replace(/[^A-Za-z0-9]/g, "");
  const captionId = `relation-legend-${id}`;
  useEffect(() => {
    select.current = onSelect;
  }, [onSelect]);

  useEffect(() => {
    const element = host.current;
    if (!element) return;
    const shadow = element.shadowRoot ?? element.attachShadow({ mode: "open" });
    let mounted = true;
    setStatus("loading");
    shadow.replaceChildren();

    mermaidModule ??= renderer().catch((error: unknown) => {
      mermaidModule = undefined;
      throw error;
    });
    void mermaidModule.then((mermaid) => mermaid.render(`relations${id}`, source)).then(({ svg }) => {
      if (!mounted) return;
      const chart = new DOMParser().parseFromString(svg, "image/svg+xml").documentElement;
      if (chart.localName !== "svg") throw new Error("Invalid Mermaid SVG");

      // The dashboard's CSP disallows inline style elements. Keep the generated CSS scoped to this diagram.
      const sheet = new CSSStyleSheet();
      const styles = [...chart.querySelectorAll("style")];
      sheet.replaceSync(styles.map((style) => style.textContent).join("\n") + `
        svg { display: block; max-width: none !important; margin-inline: auto; }
        svg .node .label-container {
          fill: var(--surface) !important;
          stroke: var(--border) !important;
          stroke-width: 1px !important;
        }
        svg .node text, svg .node tspan { fill: var(--ink) !important; }
        svg .mainSession .label-container {
          fill: color-mix(in srgb, var(--accent) 14%, var(--surface)) !important;
          stroke: var(--accent) !important;
          stroke-width: 2px !important;
        }
        svg .forkSource .label-container { stroke-dasharray: 4 3; }
        svg .edge-thickness-normal { stroke: var(--border) !important; }
        svg .edge-pattern-dotted { stroke-dasharray: 5 4 !important; }
        svg marker path { fill: var(--border) !important; stroke: var(--border) !important; }
        svg g[role="button"] { cursor: pointer; }
        svg g[aria-pressed="true"] .label-container, svg g[role="button"]:hover .label-container {
          stroke: var(--accent) !important;
          stroke-width: 2.5px !important;
        }
        svg g[role="button"]:focus-visible { outline: 2px solid var(--focus); outline-offset: 3px; }
      `);
      styles.forEach((style) => style.remove());
      shadow.adoptedStyleSheets = [sheet];
      chart.setAttribute("role", "group");
      chart.setAttribute("aria-label", "Recorded session relations");
      for (const node of chart.querySelectorAll("g.node")) {
        const match = /(?:^|-)flowchart-agent(\d+)-\d+$/.exec(node.id);
        if (!match) continue;
        const index = Number(match[1]);
        const name = names[index];
        if (!name) continue;
        node.setAttribute("tabindex", "0");
        node.setAttribute("role", "button");
        const visible = node.textContent?.trim();
        node.setAttribute("aria-label", visible && visible !== name ? `${visible}: ${name}` : name);
        node.setAttribute("data-agent-index", String(index));
        node.addEventListener("click", () => select.current(index));
        node.addEventListener("keydown", (event) => {
          if (!(event instanceof KeyboardEvent) || (event.key !== "Enter" && event.key !== " ")) return;
          event.preventDefault();
          select.current(index);
        });
      }
      shadow.replaceChildren(chart);
      setStatus("ready");
    }).catch(() => {
      if (mounted) setStatus("failed");
    });

    return () => {
      mounted = false;
    };
  }, [source, names, id]);

  useEffect(() => {
    for (const node of host.current?.shadowRoot?.querySelectorAll("[data-agent-index]") ?? []) {
      node.setAttribute("aria-pressed", String(node.getAttribute("data-agent-index") === String(selectedIndex)));
    }
  }, [selectedIndex, source, status]);

  return (
    <figure className="min-w-0 space-y-2 xl:col-span-2" aria-label="Session relations diagram">
      <div className="max-h-[min(32rem,60vh)] overflow-auto rounded-md border border-rule bg-bg p-2"
        role="region" aria-label="Interactive session relations" aria-describedby={captionId} tabIndex={0}>
        <div ref={host} />
        {status === "loading" && <p className="text-sm text-muted" role="status">Drawing relations…</p>}
        {status === "failed" && <p className="text-sm text-muted" role="status">The diagram could not be drawn. Select an agent in the list below.</p>}
      </div>
      <figcaption id={captionId} className="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-muted">
        <span className="inline-flex items-center gap-1.5"><span aria-hidden="true" className="w-4 border-t-2 border-border" />Parent link</span>
        {hasFork && <span className="inline-flex items-center gap-1.5"><span aria-hidden="true" className="w-4 border-t-2 border-dashed border-border" />Fork link</span>}
        <span>Select a node to inspect its activity.</span>
      </figcaption>
      <span className="sr-only" aria-live="polite">{names[selectedIndex]} selected</span>
    </figure>
  );
}
