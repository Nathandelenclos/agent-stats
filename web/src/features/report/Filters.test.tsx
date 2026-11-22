import { describe, expect, it, vi } from "vitest";
import { meta as serverMeta, metaInProject } from "../../test/fixtures";
import { click, optionValues, render, selectLabelled } from "../../test/render";
import { FilterBar } from "./Filters";
import type { Filters } from "./types";

const meta = (since: string) => serverMeta({ defaults: { since, harness: "", project: "all" } });
const filters = (over: Partial<Filters> = {}): Filters => ({ since: "7d", harness: "", project: "all", ...over });
const scopeButtons = (container: HTMLElement) => [...container.querySelectorAll('[role="group"][aria-label="Projects"] button')] as HTMLButtonElement[];

describe("FilterBar", () => {
  it("offers the presets and the harnesses the server knows", () => {
    // Given a server that opens on the last 7 days
    // When the bar is drawn
    const { container } = render(<FilterBar meta={meta("7d")} value={filters()} onChange={() => {}} />);

    // Then the periods are the presets and the harnesses are the server's
    expect(optionValues(selectLabelled(container, "Period"))).toEqual(["24h", "7d", "30d", ""]);
    expect(optionValues(selectLabelled(container, "Harness"))).toEqual(["", "claude", "codex"]);
  });

  it("keeps the period the server was launched on, whichever period is chosen", () => {
    // Given a server launched with --since 12h, which is not a preset, and a person who chose Last 7 days
    const launched = meta("12h");

    // When the bar is drawn
    const { container } = render(<FilterBar meta={launched} value={filters()} onChange={() => {}} />);

    // Then 12h is still there to go back to
    expect(optionValues(selectLabelled(container, "Period"))).toEqual(["12h", "24h", "7d", "30d", ""]);
  });

  it("does not list the launch period twice when it is the one selected", () => {
    const { container } = render(<FilterBar meta={meta("12h")} value={filters({ since: "12h" })} onChange={() => {}} />);
    expect(optionValues(selectLabelled(container, "Period"))).toEqual(["12h", "24h", "7d", "30d", ""]);
  });

  it("offers no choice of projects when the server found no project", () => {
    // Given a server started outside any project
    // When the bar is drawn
    const { container } = render(<FilterBar meta={meta("7d")} value={filters()} onChange={() => {}} />);

    // Then there is nothing to switch between
    expect(scopeButtons(container)).toEqual([]);
  });

  it("offers the project and every project, and shows which one is chosen", () => {
    // Given a server started in the app project and a person looking at it
    const { container } = render(<FilterBar meta={metaInProject("app")} value={filters({ project: "current" })} onChange={() => {}} />);

    // When the bar is drawn
    const buttons = scopeButtons(container);

    // Then both scopes are there and the project is the one pressed
    expect(buttons.map((b) => [b.textContent, b.getAttribute("aria-pressed")])).toEqual([
      ["This project", "true"],
      ["All projects", "false"],
    ]);
  });

  it("switches to every project, and back, keeping the other filters", () => {
    // Given a person on the project with the last 30 days of claude
    const onChange = vi.fn();
    const chosen = filters({ since: "30d", harness: "claude", project: "current" });
    const { container, rerender } = render(<FilterBar meta={metaInProject("app")} value={chosen} onChange={onChange} />);

    // When they choose every project
    click(scopeButtons(container)[1]!);

    // Then only the scope changes
    expect(onChange).toHaveBeenLastCalledWith({ since: "30d", harness: "claude", project: "all" });

    // And choosing the project again from there changes only the scope too
    rerender(<FilterBar meta={metaInProject("app")} value={{ ...chosen, project: "all" }} onChange={onChange} />);
    click(scopeButtons(container)[0]!);
    expect(onChange).toHaveBeenLastCalledWith(chosen);
  });
});
