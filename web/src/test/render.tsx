import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, type ReactElement } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach } from "vitest";

const mounted: { root: Root; container: HTMLElement }[] = [];

afterEach(() => {
  for (const { root, container } of mounted.splice(0)) {
    act(() => root.unmount());
    container.remove();
  }
});

/** Renders `ui` into the document, inside act, and gives a way to render it again with other props. */
export function render(ui: ReactElement) {
  const container = document.body.appendChild(document.createElement("div"));
  const root = createRoot(container);
  mounted.push({ root, container });
  act(() => root.render(ui));
  return { container, rerender: (next: ReactElement) => act(() => root.render(next)) };
}

/** A query client that neither retries nor shares a cache with another test. */
export function testQueryClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false, refetchOnWindowFocus: false } } });
}

export function withQueries(client: QueryClient, ui: ReactElement) {
  return <QueryClientProvider client={client}>{ui}</QueryClientProvider>;
}

export const click = (el: Element) => act(() => (el as HTMLElement).click());

/** The select a label names, as a person finds it. */
export function selectLabelled(container: HTMLElement, label: string): HTMLSelectElement {
  const el = [...container.querySelectorAll("label")].find((l) => l.textContent === label);
  const select = el && container.querySelector<HTMLSelectElement>(`select#${CSS.escape(el.htmlFor)}`);
  if (!select) throw new Error(`no select labelled "${label}"`);
  return select;
}

export const optionValues = (select: HTMLSelectElement) => [...select.options].map((o) => o.value);

/** Picks an option the way the browser reports it: the value changes, then a change event bubbles. */
export function choose(select: HTMLSelectElement, value: string) {
  act(() => {
    Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, "value")!.set!.call(select, value);
    select.dispatchEvent(new Event("change", { bubbles: true }));
  });
}

/** Retries `assertion` while the page settles: fetches resolve and queries flush between attempts. */
export async function eventually(assertion: () => void, attempts = 50) {
  for (let i = 1; ; i++) {
    try {
      return assertion();
    } catch (error) {
      if (i >= attempts) throw error;
      await act(async () => {
        await new Promise((resolve) => setTimeout(resolve, 10));
      });
    }
  }
}
