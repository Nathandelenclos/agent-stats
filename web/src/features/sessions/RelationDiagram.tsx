import { useEffect, useId, useRef, useState } from "react";

const renderer = () => import("mermaid").then(({ default: mermaid }) => {
  mermaid.initialize({
    startOnLoad: false,
    suppressErrorRendering: true,
    securityLevel: "strict",
    htmlLabels: false,
    theme: "base",
  });
  return mermaid;
});

let mermaidModule: ReturnType<typeof renderer> | undefined;

interface Props {
  source: string;
  names: string[];
  selectedIndex: number;
  onSelect: (index: number) => void;
}

export function RelationDiagram({ source, names, selectedIndex, onSelect }: Props) {
  const host = useRef<HTMLDivElement>(null);
  const select = useRef(onSelect);
  const [status, setStatus] = useState<"loading" | "ready" | "failed">("loading");
  const id = useId().replace(/[^A-Za-z0-9]/g, "");
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
        svg { display: block; min-width: 24rem; margin: auto; }
        g[role="button"] { cursor: pointer; }
        g[aria-pressed="true"] rect { stroke: #1d4ed8 !important; stroke-width: 2px !important; }
        g[role="button"]:focus-visible { outline: 2px solid #1d4ed8; outline-offset: 2px; }
      `);
      styles.forEach((style) => style.remove());
      shadow.adoptedStyleSheets = [sheet];
      chart.setAttribute("role", "group");
      chart.setAttribute("aria-label", "Recorded session relations");
      for (const node of chart.querySelectorAll("g.node")) {
        const match = /-classId-agent(\d+)-\d+$/.exec(node.id);
        if (!match) continue;
        const index = Number(match[1]);
        const name = names[index];
        if (!name) continue;
        node.setAttribute("tabindex", "0");
        node.setAttribute("role", "button");
        node.setAttribute("aria-label", name);
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
    <figure className="space-y-2" aria-label="Mermaid relation diagram">
      <div className="overflow-auto rounded-md bg-white p-2 text-slate-950">
        <div ref={host} />
        {status === "loading" && <p className="text-sm" role="status">Drawing relations…</p>}
        {status === "failed" && <p className="text-sm" role="status">The diagram could not be drawn. Select an agent in the list below.</p>}
      </div>
      <figcaption className="text-xs text-muted">Select a node to inspect it. Solid lines show recorded parent links. A dashed line marks a fork.</figcaption>
      <span className="sr-only" aria-live="polite">{names[selectedIndex]} selected</span>
    </figure>
  );
}
