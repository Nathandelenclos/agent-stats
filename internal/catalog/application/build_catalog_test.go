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

func (f *fakeActivity) Activity(q domain.Filter) (domain.Activity, error) {
	f.filter = q
	return f.act, f.err
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

func TestCatalogOfOneHarnessLooksAtThatHarnessOnly(t *testing.T) {
	// Given an inventory and a harness filter
	inv := &fakeInventory{}
	act := &fakeActivity{}

	// When the catalogue is built for Codex
	_, err := BuildCatalog{Inventory: inv, Activity: act}.Run(Query{Harness: kernel.Codex})

	// Then only Codex is inventoried and the activity is limited to it
	if err != nil || len(inv.asked) != 1 || inv.asked[0] != kernel.Codex || act.filter.Harness != kernel.Codex {
		t.Errorf("asked %v, filter %+v, err %v", inv.asked, act.filter, err)
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
