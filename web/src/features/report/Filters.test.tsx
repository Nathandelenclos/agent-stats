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
});
