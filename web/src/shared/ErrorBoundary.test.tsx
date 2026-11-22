import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { click, render } from "../test/render";
import { ErrorBoundary } from "./ErrorBoundary";

// A boundary and React both log what they catch; the tests expect those failures.
beforeEach(() => vi.spyOn(console, "error").mockImplementation(() => {}));
afterEach(() => vi.restoreAllMocks());

function Draw({ data }: { data: { ok: boolean; label: string } }) {
  if (!data.ok) throw new Error("cannot draw " + data.label);
  return <p>drawn {data.label}</p>;
}

const zone = (data: { ok: boolean; label: string }, resetKey: unknown = data) => (
  <ErrorBoundary resetKey={resetKey} fallback={(retry) => <button onClick={retry}>failed, retry</button>}>
    <Draw data={data} />
  </ErrorBoundary>
);

describe("ErrorBoundary", () => {
  it("keeps a failure inside its zone", () => {
    // Given two zones, one of which cannot draw its data
    const { container } = render(<>{zone({ ok: false, label: "a" })}{zone({ ok: true, label: "b" })}</>);

    // Then the failing one shows its fallback and the other is drawn
    expect(container.textContent).toContain("failed, retry");
    expect(container.textContent).toContain("drawn b");
  });

  it("tries again when the data it draws changes", () => {
    // Given a zone in error on some data
    const { container, rerender } = render(zone({ ok: false, label: "old" }));
    expect(container.textContent).toContain("failed, retry");

    // When new, valid data arrives
    rerender(zone({ ok: true, label: "new" }));

    // Then it is drawn, without anyone clicking
    expect(container.textContent).toContain("drawn new");
    expect(container.textContent).not.toContain("failed, retry");
  });

  it("stays in error while the data is the same", () => {
    // Given a zone in error on some data
    const data = { ok: false, label: "same" };
    const { container, rerender } = render(zone(data));

    // When the page draws again with that very data
    rerender(zone(data));

    // Then it is not retried: it would fail the same way
    expect(container.textContent).toContain("failed, retry");
  });

  it("tries again when asked to", () => {
    // Given a zone in error that has since been given valid data under the same key
    const data = { ok: false, label: "x" };
    const { container, rerender } = render(zone(data, "key"));
    data.ok = true;
    rerender(zone(data, "key"));
    expect(container.textContent).toContain("failed, retry");

    // When the person clicks the retry button
    click(container.querySelector("button")!);

    // Then it is drawn
    expect(container.textContent).toContain("drawn x");
  });
});
