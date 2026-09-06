package compile

import "github.com/scottmeyer/nine-tails/internal/store"

// Preview is the reviewable, dry-run view of the generation assembled in the
// transaction. Its item IDs are provisional until the transaction commits.
type Preview struct {
	ProposedItems     []ItemView   `json:"proposed_items" yaml:"proposed_items"`
	RemainingGuidance []SourceView `json:"remaining_guidance" yaml:"remaining_guidance"`
}

// BuildPreview reads the generation and current source accounting while the
// install transaction is still open. It therefore shows inherited source
// relationships and the raw guidance that would remain unrepresented after
// this generation, without performing any reads after a dry-run rollback.
func BuildPreview(q store.Querier, agent, generationID string) (*Preview, error) {
	view, err := readGenerationView(q, generationID)
	if err != nil {
		return nil, err
	}
	preview := &Preview{ProposedItems: view.Items, RemainingGuidance: []SourceView{}}
	remaining, err := store.RecentGuidance(q, agent)
	if err != nil {
		return nil, err
	}
	for _, source := range remaining {
		preview.RemainingGuidance = append(preview.RemainingGuidance, SourceView{ID: source.ID, Kind: source.Kind, Body: source.Body, Meta: source.Meta})
	}
	return preview, nil
}

// Compiler input and installation review share the exact source view used by
// lint: immutable item text with current successor bodies and source scopes.
func readGenerationView(q store.Querier, generationID string) (*GenerationView, error) {
	items, err := store.GenerationItems(q, generationID)
	if err != nil {
		return nil, err
	}
	view := &GenerationView{ID: generationID, Items: []ItemView{}}
	for _, item := range items {
		ids, err := currentSources(q, generationID, item.ID)
		if err != nil {
			return nil, err
		}
		sources := make([]SourceView, 0, len(ids))
		for _, id := range ids {
			source, err := store.GetRecord(q, id)
			if err != nil {
				return nil, err
			}
			sources = append(sources, SourceView{ID: source.ID, Kind: source.Kind, Body: source.Body, Meta: source.Meta})
		}
		view.Items = append(view.Items, ItemView{ID: item.ID, Key: item.Name, Body: item.Body, Meta: item.Meta, Sources: sources})
	}
	return view, nil
}
