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

// Activity answers like the index: what the logs say, limited to the harness and the period asked for.
func (f *fakeActivity) Activity(q domain.Filter) (domain.Activity, error) {
	f.filter = q
	var out domain.Activity
	for _, s := range f.act.Skills {
		if (q.Harness.IsZero() || s.Harness == q.Harness) && !s.Last.Before(q.Since) {
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

// twoHarnesses is an inventory with a Claude skill and a Codex skill, the Claude one reported used, and a skipped file.
func twoHarnesses(t *testing.T) (*fakeInventory, *fakeActivity) {
	t.Helper()
	inv := &fakeInventory{byHarness: map[kernel.Harness]Installed{
		kernel.Claude: {Items: []domain.Item{skill(t, kernel.Claude, "used")}, Skipped: []string{"~/.claude.json: malformed"}},
		kernel.Codex:  {Items: []domain.Item{skill(t, kernel.Codex, "other")}},
	}}
	act := &fakeActivity{act: domain.Activity{Skills: []domain.SkillActivity{{Harness: kernel.Claude, Skill: "used", Calls: 2}}}}
	return inv, act
}

func TestCatalogCoversEveryHarnessWithItsUsage(t *testing.T) {
	// Given skills installed in two harnesses and activity for one of them
	inv, act := twoHarnesses(t)

	// When the catalogue is built for all harnesses
	c, err := BuildCatalog{Inventory: inv, Activity: act}.Run(Query{})

	// Then it lists both skills, the used one first with its uses
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Entries) != 2 || c.Entries[0].Name.String() != "used" || c.Entries[0].Uses != 2 {
		t.Errorf("entries: %+v", c.Entries)
	}
}

func TestCatalogCountsOnlyTheUsageOfThePeriodAsked(t *testing.T) {
	// Given a skill last loaded in January and another last loaded in March
	inv := &fakeInventory{byHarness: map[kernel.Harness]Installed{
		kernel.Claude: {Items: []domain.Item{skill(t, kernel.Claude, "old"), skill(t, kernel.Claude, "recent")}},
	}}
	act := &fakeActivity{act: domain.Activity{Skills: []domain.SkillActivity{
		{Harness: kernel.Claude, Skill: "old", Calls: 4, Last: time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)},
		{Harness: kernel.Claude, Skill: "recent", Calls: 2, Last: time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC)},
	}}}

	// When the catalogue is built for the period starting in February
	c, err := BuildCatalog{Inventory: inv, Activity: act}.Run(Query{Harness: kernel.Claude, Since: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)})

	// Then the recent skill shows its loads and the old one counts as unused in that period
	if err != nil {
		t.Fatal(err)
	}
	uses := map[string]int64{}
	for _, e := range c.Entries {
		uses[e.Name.String()] = e.Uses
	}
	if uses["recent"] != 2 || uses["old"] != 0 || len(uses) != 2 {
		t.Errorf("uses: %v", uses)
	}
}

func TestCatalogReportsWhatItCouldNotRead(t *testing.T) {
	// Given a configuration the inventory had to skip
	inv, act := twoHarnesses(t)

	// When the catalogue is built
	c, err := BuildCatalog{Inventory: inv, Activity: act}.Run(Query{})

	// Then the skipped file is reported with it
	if err != nil || len(c.Skipped) != 1 {
		t.Errorf("skipped: %v, err %v", c.Skipped, err)
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

func TestCatalogCanListOnlyWhatIsUnusedWhileCountingEverything(t *testing.T) {
	// Given a used skill and an idle one in Claude Code
	inv := &fakeInventory{byHarness: map[kernel.Harness]Installed{
		kernel.Claude: {Items: []domain.Item{skill(t, kernel.Claude, "used"), skill(t, kernel.Claude, "idle")}},
	}}
	act := &fakeActivity{act: domain.Activity{Skills: []domain.SkillActivity{{Harness: kernel.Claude, Skill: "used", Calls: 1}}}}

	// When the catalogue is built for the unused only
	c, err := BuildCatalog{Inventory: inv, Activity: act}.Run(Query{Harness: kernel.Claude, UnusedOnly: true})

	// Then it lists the idle skill alone, while the tally still counts both as installed
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Entries) != 1 || c.Entries[0].Name.String() != "idle" {
		t.Errorf("entries: %+v", c.Entries)
	}
	if got := c.Tally[kernel.Claude]; got.Installed != 2 || got.Unused != 1 {
		t.Errorf("tally: %+v", got)
	}
}

// loggedByProject is what the logs say per project: the activity of the sessions that worked in each project, and the
// zero Project's, which is every project's.
type loggedByProject map[kernel.Project]domain.Activity

func (l loggedByProject) Activity(f domain.Filter) (domain.Activity, error) { return l[f.Project], nil }

// usedInTheApp is an index in which a skill was loaded twice in /work/app and five times in all.
func usedInTheApp(t *testing.T) (BuildCatalog, kernel.Project) {
	t.Helper()
	app, err := kernel.NewProject("/work/app")
	if err != nil {
		t.Fatal(err)
	}
	loads := func(n int64) domain.Activity {
		return domain.Activity{Skills: []domain.SkillActivity{{Harness: kernel.Claude, Skill: "used", Calls: n}}}
	}
	inv := &fakeInventory{byHarness: map[kernel.Harness]Installed{kernel.Claude: {Items: []domain.Item{skill(t, kernel.Claude, "used")}}}}
	return BuildCatalog{Inventory: inv, Activity: loggedByProject{app: loads(2), {}: loads(5)}}, app
}

func TestCatalogCountsOnlyTheUsageOfTheProjectAsked(t *testing.T) {
	// Given a skill loaded twice in a project and five times in all
	uc, app := usedInTheApp(t)

	// When the catalogue is built for that project
	c, err := uc.Run(Query{Harness: kernel.Claude, Project: app})

	// Then the skill shows the two loads of the project
	if err != nil || len(c.Entries) != 1 || c.Entries[0].Uses != 2 {
		t.Errorf("entries: %+v, %v", c.Entries, err)
	}
}

func TestCatalogCountsTheUsageOfEveryProjectWhenNoneIsAsked(t *testing.T) {
	// Given the same skill
	uc, _ := usedInTheApp(t)

	// When the catalogue is built for no project in particular
	c, err := uc.Run(Query{Harness: kernel.Claude})

	// Then the skill shows its five loads
	if err != nil || len(c.Entries) != 1 || c.Entries[0].Uses != 5 {
		t.Errorf("entries: %+v, %v", c.Entries, err)
	}
}
