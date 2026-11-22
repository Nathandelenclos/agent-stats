package domain

import (
	"cmp"
	"slices"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
)

func sortEntries(entries []Entry) {
	rank := func(h kernel.Harness) int { return slices.Index(kernel.Harnesses, h) }
	kindRank := func(k Kind) int { return slices.Index(Kinds, k) }
	slices.SortStableFunc(entries, func(a, b Entry) int {
		return cmp.Or(
			cmp.Compare(rank(a.Harness), rank(b.Harness)),
			cmp.Compare(kindRank(a.Kind), kindRank(b.Kind)),
			cmp.Compare(b.Uses, a.Uses),
			cmp.Compare(a.Name.s, b.Name.s),
			cmp.Compare(a.Scope.name, b.Scope.name),
			cmp.Compare(a.Source, b.Source),
		)
	})
}
