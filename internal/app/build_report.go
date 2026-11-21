package app

// Report is the usage summary: totals, the main groupings and tool calls.
type Report struct {
	Total     Usage        `json:"total"`
	ByHarness []UsageGroup `json:"byHarness"`
	ByModel   []UsageGroup `json:"byModel"`
	ByFolder  []UsageGroup `json:"byFolder"`
	ByAgent   []UsageGroup `json:"byAgentType"`
	Tools     []ToolUsage  `json:"tools"`
}

// BuildReport summarises indexed usage.
type BuildReport struct {
	Index UsageIndex
}

// Run builds the report for the activity matching f.
func (uc BuildReport) Run(f Filter) (Report, error) {
	var r Report
	var err error
	if r.Total, err = uc.Index.Total(f); err != nil {
		return r, err
	}
	for _, g := range []struct {
		dst *[]UsageGroup
		dim Dimension
	}{
		{&r.ByHarness, ByHarness},
		{&r.ByModel, ByModel},
		{&r.ByFolder, ByFolder},
		{&r.ByAgent, ByAgentType},
	} {
		if *g.dst, err = uc.Index.UsageBy(f, g.dim); err != nil {
			return r, err
		}
	}
	r.Tools, err = uc.Index.Tools(f)
	return r, err
}
