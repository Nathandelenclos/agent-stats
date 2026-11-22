import { act } from "react";
import { expect, it, vi } from "vitest";
import { eventually, render } from "../../test/render";
import { RelationDiagram } from "./RelationDiagram";

it("renders actual Mermaid nodes and opens an agent from the keyboard", async () => {
  const properties = [
    [SVGElement.prototype, "getBBox", () => ({ x: 0, y: 0, width: 80, height: 30 })],
    [SVGElement.prototype, "getComputedTextLength", () => 80],
    [CSSStyleSheet.prototype, "replaceSync", () => {}],
  ] as const;
  const originals = properties.map(([target, name]) => Object.getOwnPropertyDescriptor(target, name));
  for (const [target, name, value] of properties) {
    Object.defineProperty(target, name, { configurable: true, value });
  }

  try {
    const select = vi.fn();
    const { container } = render(<RelationDiagram source={'classDiagram\nclass agent0["Session"]\nclass agent1["Check"]\nagent0 --> agent1'}
      names={["Session", "Check"]} selectedIndex={1} onSelect={select} />);
    const host = [...container.querySelectorAll("div")].find((el) => el.shadowRoot);
    const agent = () => host?.shadowRoot?.querySelector('[role="button"][aria-label="Check"]');
    await eventually(() => expect(agent()?.getAttribute("aria-pressed")).toBe("true"));

    act(() => agent()?.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true })));
    expect(select).toHaveBeenCalledWith(1);
    expect(host?.shadowRoot?.querySelector("svg style")).toBeNull();
  } finally {
    properties.forEach(([target, name], index) => {
      const original = originals[index];
      if (original) Object.defineProperty(target, name, original);
      else Reflect.deleteProperty(target, name);
    });
  }
});
