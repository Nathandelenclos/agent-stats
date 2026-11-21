package application

import (
	"fmt"

	"github.com/Nathandelenclos/agent-stats/internal/indexing/domain"
)

// IndexSessions brings the index up to date with the session logs on disk.
// A changed log is read again from the start and replaces what was indexed from it, which also covers truncation.
// What was indexed from logs a harness deleted is kept, so history survives log rotation.
type IndexSessions struct {
	Logs   SessionLogs
	Store  TranscriptStore
	Prices PriceList
}

// IndexReport says what an IndexSessions run did.
type IndexReport struct {
	Scanned, Indexed int
	SkippedLines     int // unusable lines in the logs indexed by this run
	Errors           []string
}

// Run indexes every new or changed log. progress, when set, is called every 50 indexed files.
func (uc IndexSessions) Run(progress func(IndexReport)) (IndexReport, error) {
	var rep IndexReport
	known, err := uc.Store.IndexedFiles()
	if err != nil {
		return rep, err
	}
	files, problems, err := uc.Logs.List()
	for _, p := range problems {
		rep.Errors = append(rep.Errors, p.Error())
	}
	if err != nil {
		return rep, err
	}
	for _, f := range files {
		rep.Scanned++
		if k, ok := known[f.Path]; ok && k.Size == f.Size && k.ModTime.Equal(f.ModTime) {
			continue
		}
		t, err := uc.Logs.Read(f)
		if err == nil {
			err = uc.Store.Save(f, uc.indexed(t))
		}
		if err != nil {
			rep.Errors = append(rep.Errors, fmt.Sprintf("%s: %v", f.Path, err))
			continue
		}
		rep.Indexed++
		rep.SkippedLines += t.SkippedLines
		if progress != nil && rep.Indexed%50 == 0 {
			progress(rep)
		}
	}
	return rep, nil
}

func (uc IndexSessions) indexed(t domain.Transcript) IndexedTranscript {
	costs := make([]PricedCost, len(t.Requests))
	for i, r := range t.Requests {
		listed, known := uc.Prices.Cost(r.Model, r.Tokens)
		costs[i].Cost, costs[i].Priced = r.Cost(listed, known)
	}
	return IndexedTranscript{Transcript: t, Costs: costs, Signals: domain.AnalyzeSession(t.Prompts)}
}
