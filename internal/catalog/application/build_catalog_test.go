package application

import (
	"errors"
	"testing"
	"time"

	"github.com/Nathandelenclos/agent-stats/internal/catalog/domain"
	"github.com/Nathandelenclos/agent-stats/internal/kernel"
)

type fakeInventory struct {
	byHarness map[kernel.Harness]Installed
	asked     []kernel.Harness
}

func (f *fakeInventory) Installed(h kernel.Harness) (Installed, error) {
	f.asked = append(f.asked, h)
	return f.byHarness[h], nil
}

type fakeActivity struct {
	act    domain.Activity
	filter domain.Filter
	err    error
}

// Activity answers like the index: what the logs say, limited to the harness asked for.
func (f *fakeActivity) Activity(q domain.Filter) (domain.Activity, error) {
	f.filter = q
	var out domain.Activity
	for _, s := range f.act.Skills {
		if q.Harness.IsZero() || s.Harness == q.Harness {
			out.Skills = append(out.Skills, s)
		}
	}
	return out, f.err
}

func skill(t *testing.T, h kernel.Harness, n string) domain.Item {
	t.Helper()
	name, err := domain.NewName(n)
	if err != nil {
		t.Fatal(err)
	}
	return domain.Item{Harness: h, Kind: domain.Skill, Name: name, Scope: domain.ScopeUser}
}

func TestCatalogCoversEveryHarnessAndPassesTheFilterToTheActivity(t *testing.T) {
	// Given an inventory with a skill per harness and activity for one skill
	inv := &fakeInventory{byHarness: map[kernel.Harness]Installed{
		kernel.Claude: {Items: []domain.Item{skill(t, kernel.Claude, "used")}, Skipped: []string{"~/.claude.json: malformed"}},
		kernel.Codex:  {Items: []domain.Item{skill(t, kernel.Codex, "other")}},
	}}
	act := &fakeActivity{act: domain.Activity{Skills: []domain.SkillActivity{{Harness: kernel.Claude, Skill: "used", Calls: 2}}}}
	since := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// When the catalogue is built for all harnesses since a date
	c, err := BuildCatalog{Inventory: inv, Activity: act}.Run(Query{Since: since})

	// Then every harness was inventoried, the date reached the activity query, and skipped files are reported
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.asked) != len(kernel.Harnesses) {
		t.Errorf("harnesses asked: %v", inv.asked)
	}
	if !act.filter.Since.Equal(since) || !act.filter.Harness.IsZero() {
		t.Errorf("filter: %+v", act.filter)
	}
	if len(c.Entries) != 2 || c.Entries[0].Uses != 2 || len(c.Skipped) != 1 {
		t.Errorf("catalog: %+v", c)
	}
}

func TestCatalogOfOneHarnessShowsThatHarnessOnly(t *testing.T) {
	// Given skills installed and used in two harnesses
	inv := &fakeInventory{byHarness: map[kernel.Harness]Installed{
		kernel.Claude: {Items: []domain.Item{skill(t, kernel.Claude, "review")}},
		kernel.Codex:  {Items: []domain.Item{skill(t, kernel.Codex, "review")}},
	}}
	act := &fakeActivity{act: domain.Activity{Skills: []domain.SkillActivity{
		{Harness: kernel.Claude, Skill: "review", Calls: 5},
		{Harness: kernel.Codex, Skill: "review", Calls: 3},
	}}}

	// When the catalogue is built for Claude Code
	c, err := BuildCatalog{Inventory: inv, Activity: act}.Run(Query{Harness: kernel.Claude})

	// Then it lists the Claude skill alone, with the Claude uses
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Entries) != 1 || c.Entries[0].Harness != kernel.Claude || c.Entries[0].Uses != 5 {
		t.Errorf("entries: %+v", c.Entries)
	}
}

func TestCatalogFailsWhenTheLogsCannotBeRead(t *testing.T) {
	// Given an activity reader that fails
	boom := errors.New("boom")

	// When the catalogue is built, then the failure is returned rather than a catalogue that looks all unused
	if _, err := (BuildCatalog{Inventory: &fakeInventory{}, Activity: &fakeActivity{err: boom}}).Run(Query{}); !errors.Is(err, boom) {
		t.Errorf("err = %v", err)
	}
}
