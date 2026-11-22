import { describe, expect, it } from "vitest";
import { optionValues, render, selectLabelled } from "../../test/render";
import { FilterBar } from "./Filters";
import type { Meta } from "./types";

const meta = (since: string): Meta => ({ harnesses: ["claude", "codex"], defaults: { since, harness: "" } });

describe("FilterBar", () => {
  it("offers the presets and the harnesses the server knows", () => {
    // Given a server that opens on the last 7 days
    // When the bar is drawn
    const { container } = render(<FilterBar meta={meta("7d")} value={{ since: "7d", harness: "" }} onChange={() => {}} />);

    // Then the periods are the presets and the harnesses are the server's
    expect(optionValues(selectLabelled(container, "Period"))).toEqual(["24h", "7d", "30d", ""]);
    expect(optionValues(selectLabelled(container, "Harness"))).toEqual(["", "claude", "codex"]);
  });

  it("keeps the period the server was launched on, whichever period is chosen", () => {
    // Given a server launched with --since 12h, which is not a preset, and a person who chose Last 7 days
    const launched = meta("12h");

    // When the bar is drawn
    const { container } = render(<FilterBar meta={launched} value={{ since: "7d", harness: "" }} onChange={() => {}} />);

    // Then 12h is still there to go back to
    expect(optionValues(selectLabelled(container, "Period"))).toEqual(["12h", "24h", "7d", "30d", ""]);
  });

  it("does not list the launch period twice when it is the one selected", () => {
    const { container } = render(<FilterBar meta={meta("12h")} value={{ since: "12h", harness: "" }} onChange={() => {}} />);
    expect(optionValues(selectLabelled(container, "Period"))).toEqual(["12h", "24h", "7d", "30d", ""]);
  });
});
