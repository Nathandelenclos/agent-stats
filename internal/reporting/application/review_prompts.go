package application

import (
	"context"

	"github.com/Nathandelenclos/agent-stats/internal/reporting/domain"
)

// The prompt review's read models, as callers of ReviewPrompts see them.
type (
	FlaggedPrompt     = domain.FlaggedPrompt
	RecurringAsk      = domain.RecurringAsk
	FolderFrustration = domain.FolderFrustration
	PromptReview      = domain.PromptReview
)

// PromptQueries answers questions about the prompts the user typed. The answer describes one state of the
// index, which indexing may be changing meanwhile.
type PromptQueries interface {
	// FlaggedPrompts lists the prompts typed in the filter's range (since the filter's time, in its harness and project)
	// that show at least one frustration signal. They are the user's own: the briefs sent to subagents are not.
	FlaggedPrompts(context.Context, domain.Filter) ([]domain.FlaggedPrompt, error)
}

// ReviewPrompts shows what the user keeps correcting or repeating, so it can be written down once.
type ReviewPrompts struct {
	Queries PromptQueries
}

// Run reviews the flagged prompts q asks for: in the requested period, harness and project.
func (uc ReviewPrompts) Run(ctx context.Context, q Query) (PromptReview, error) {
	prompts, err := uc.Queries.FlaggedPrompts(ctx, domain.Filter{Since: q.Since, Harness: q.Harness, Project: q.Project})
	if err != nil {
		return PromptReview{}, err
	}
	return domain.NewPromptReview(prompts), nil
}
