package capsule

import (
	"fmt"
	"unicode/utf8"

	"github.com/scottmeyer/nine-tails/internal/store"
)

// RecallCheck separates immutable delivery evidence from a current lookup.
// Query overrides, explicit selections and rendered excerpt bytes are not
// retained on old receipts, so this must never claim to replay an old load.
type RecallCheck struct {
	Version           int            `json:"version" yaml:"version"`
	CheckedAt         string         `json:"checked_at" yaml:"checked_at"`
	Context           *store.Context `json:"context" yaml:"context"`
	Query             string         `json:"query" yaml:"query"`
	QuerySource       string         `json:"query_source" yaml:"query_source"`
	RecordedInContext bool           `json:"recorded_in_context" yaml:"recorded_in_context"`
	Current           RecallMatch    `json:"current" yaml:"current"`
	Limit             string         `json:"limit" yaml:"limit"`
}

type RecallMatch struct {
	Eligible     bool     `json:"eligible" yaml:"eligible"`
	Selected     bool     `json:"selected" yaml:"selected"`
	Reason       string   `json:"reason" yaml:"reason"`
	MatchedTerms []string `json:"matched_terms" yaml:"matched_terms"`
	Excerpt      string   `json:"excerpt,omitempty" yaml:"excerpt,omitempty"`
	Truncated    bool     `json:"truncated" yaml:"truncated"`
}

// CheckRecall uses the real selection path against today's records and the
// receipt's scope, without creating a capsule or writing any evidence back.
// The caller supplies a consistent database snapshot and resolves references.
func CheckRecall(q store.Querier, record *store.Record, ctx *store.Context, query *string, commandHome string) (*RecallCheck, error) {
	if record.Agent != ctx.Agent || record.Lane != "recall" {
		return nil, fmt.Errorf("%w: recall check requires a recall record owned by context agent %s", store.ErrInvalid, ctx.Agent)
	}
	v := &RecallCheck{
		Version: 1, CheckedAt: store.Now(), Context: ctx,
		Query: ctx.Task, QuerySource: "receipt-task",
		Current: RecallMatch{MatchedTerms: []string{}},
		Limit:   "Receipt proves exact record delivery, not full-text delivery, relevance or use. Current selection is a fresh lexical check; historical query overrides, explicit selections and excerpt bytes were not retained.",
	}
	if query != nil {
		v.Query, v.QuerySource = *query, "supplied"
	}
	for _, delivered := range ctx.Rendered {
		if delivered.RecordID == record.ID && delivered.Section == "recall" {
			v.RecordedInContext = true
			break
		}
	}
	switch {
	case record.Status != "active":
		v.Current.Reason = record.Status
	case store.Conflicts(record.Meta, ctx.Meta):
		v.Current.Reason = "scope-conflict"
	case !utf8.ValidString(record.Body):
		v.Current.Reason = "invalid-text"
	default:
		v.Current.Eligible = true
	}
	if !v.Current.Eligible {
		return v, nil
	}
	terms := recallTerms(v.Query)
	v.Current.MatchedTerms = recallMatches(record, terms)
	if len(terms) == 0 {
		v.Current.Reason = "no-query-terms"
		return v, nil
	}
	if len(v.Current.MatchedTerms) == 0 {
		v.Current.Reason = "no-word-match"
		return v, nil
	}
	c := &Capsule{Agent: ctx.Agent, commandHome: commandHome}
	selected, views, err := recallCandidates(q, c, Request{Agent: ctx.Agent, Task: v.Query}, ctx.Meta)
	if err != nil {
		return nil, err
	}
	v.Current.Reason = "outside-recall-budget"
	for _, candidate := range selected {
		if candidate.rec.ID == record.ID {
			view := views[record.ID]
			v.Current.Selected, v.Current.Reason = true, "selected"
			v.Current.Excerpt, v.Current.Truncated = view.Excerpt, view.Truncated
			break
		}
	}
	return v, nil
}
