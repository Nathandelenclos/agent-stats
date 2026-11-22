import { act } from "react";
import { expect, it, vi } from "vitest";
import { eventually, render } from "../../test/render";
import { RelationDiagram } from "./RelationDiagram";

it("renders the Mermaid flowchart and activates the exact node with the keyboard", async () => {
  // jsdom has no SVG layout. Let Mermaid run with deterministic label measurements.
  const measurements = {
    getBBox: () => ({ x: 0, y: 0, width: 100, height: 20 }),
    getComputedTextLength: () => 90,
  };
  const prototype = SVGElement.prototype;
  const previous = Object.entries(measurements).map(([method, value]) => {
    const descriptor = Object.getOwnPropertyDescriptor(prototype, method);
    Object.defineProperty(prototype, method, { configurable: true, value });
    return { method, descriptor };
  });
  try {
    const source = 'flowchart LR\nagent0("Audit"):::mainSession\nagent1("reviewer")\nagent2("Fork source"):::forkSource\nagent0 --> agent1\nagent2 -.-> agent0';
    const names = ["Audit", "This is the complete title of a long review task", "Fork source"];
    const onSelect = vi.fn();
    const { container, rerender } = render(<RelationDiagram {...{ source, names, onSelect }} hasFork selectedIndex={0} />);
    const shadow = () => container.querySelector('[aria-label="Interactive session relations"] > div')?.shadowRoot;
    const node = (index: number) => shadow()?.querySelector(`[data-agent-index="${index}"]`);
    await eventually(() => expect(node(0)?.getAttribute("aria-pressed")).toBe("true"), 100);
    expect(shadow()?.querySelector("g.mainSession")).toBe(node(0));
    expect(shadow()?.querySelector(".edge-pattern-dotted")).toBeTruthy();
    expect(shadow()?.querySelector("style")).toBeNull();
    expect(node(1)?.getAttribute("aria-label")).toBe(`reviewer: ${names[1]}`);
    expect(container.querySelector("figcaption")?.textContent).toContain("Fork link");

    act(() => node(1)!.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true })));
    expect(onSelect).toHaveBeenCalledWith(1);
    rerender(<RelationDiagram {...{ source, names, onSelect }} hasFork selectedIndex={1} />);
    expect(node(0)?.getAttribute("aria-pressed")).toBe("false");
    expect(node(1)?.getAttribute("aria-pressed")).toBe("true");
  } finally {
    for (const { method, descriptor } of previous) {
      if (descriptor) Object.defineProperty(prototype, method, descriptor);
      else Reflect.deleteProperty(prototype, method);
    }
  }
});
