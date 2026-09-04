// Package compile owns the compiler contract (spec §12): building compiler
// input, validating compiler output, computing coverage classification,
// installing a generation through compare-and-swap, and the condition-loss
// lint.
package compile

import (
	"errors"
	"fmt"
	"sort"

	"github.com/scottmeyer/nine-tails/internal/store"
)

// Warning is one condition-loss finding (spec §12.6).
type Warning struct {
	Item     string   `json:"item" yaml:"item"`         // brief item record id
	Key      string   `json:"key" yaml:"key"`           // the metadata key that was dropped
	Strength string   `json:"strength" yaml:"strength"` // strong | weak
	Values   []string `json:"values" yaml:"values"`     // the value(s) every source shared
	Sources  []string `json:"sources" yaml:"sources"`   // source entry record ids
	Message  string   `json:"message" yaml:"message"`
}

// LintConditionLoss checks the active generation for agent.
func LintConditionLoss(q store.Querier, agent string) ([]Warning, error) {
	gen, err := store.ActiveGeneration(q, agent)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return LintGeneration(q, gen.ID)
}

// LintGeneration runs the condition-loss lint on one generation. For each
// item with at least one source: if any source has an empty meta multimap,
// no warning. Otherwise, for each key K present on every source whose value
// sets intersect, K missing from the item is a STRONG warning. If every
// source has an origin context, for each key K present on every origin
// context's metadata (and on no source) whose value sets intersect, K missing
// from the item is a WEAK warning.
func LintGeneration(q store.Querier, genID string) ([]Warning, error) {
	items, err := store.GenerationItems(q, genID)
	if err != nil {
		return nil, err
	}
	var out []Warning
	for _, it := range items {
		srcIDs, err := store.ItemSources(q, genID, it.ID)
		if err != nil {
			return nil, err
		}
		if len(srcIDs) == 0 {
			continue
		}
		srcs, err := store.ListRecords(q, store.Filter{IDs: srcIDs, Status: "*"})
		if err != nil {
			return nil, err
		}
		if len(srcs) == 0 {
			continue
		}
		metas := make([]store.Meta, len(srcs))
		unqualified := false
		for i, s := range srcs {
			metas[i] = s.Meta
			if len(s.Meta) == 0 {
				unqualified = true
			}
		}
		if unqualified {
			continue
		}
		for _, k := range commonKeys(metas) {
			if it.Meta.Has(k) {
				continue
			}
			if vals := intersectValues(metas, k); len(vals) > 0 {
				out = append(out, Warning{Item: it.ID, Key: k, Strength: "strong", Values: vals, Sources: srcIDs,
					Message: fmt.Sprintf("item %s drops %s=%s, which every source entry carried explicitly", it.ID, k, vals[0])})
			}
		}
		ctxMetas := make([]store.Meta, 0, len(srcs))
		for _, s := range srcs {
			if s.OriginContext == "" {
				ctxMetas = nil
				break
			}
			c, err := store.GetContext(q, s.OriginContext)
			if err != nil {
				ctxMetas = nil
				break
			}
			ctxMetas = append(ctxMetas, c.Meta)
		}
		if len(ctxMetas) != len(srcs) {
			continue
		}
		onSources := map[string]bool{}
		for _, m := range metas {
			for k := range m {
				onSources[k] = true
			}
		}
		for _, k := range commonKeys(ctxMetas) {
			if onSources[k] || it.Meta.Has(k) {
				continue
			}
			if vals := intersectValues(ctxMetas, k); len(vals) > 0 {
				out = append(out, Warning{Item: it.ID, Key: k, Strength: "weak", Values: vals, Sources: srcIDs,
					Message: fmt.Sprintf("item %s omits %s=%s, which every source's origin context shared (may be a genuine generalization)", it.ID, k, vals[0])})
			}
		}
	}
	return out, nil
}

func commonKeys(metas []store.Meta) []string {
	if len(metas) == 0 {
		return nil
	}
	count := map[string]int{}
	for _, m := range metas {
		for k := range m {
			count[k]++
		}
	}
	var out []string
	for k, n := range count {
		if n == len(metas) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// intersectValues returns the values of key present on every meta, in the
// first meta's order.
func intersectValues(metas []store.Meta, key string) []string {
	if len(metas) == 0 {
		return nil
	}
	var out []string
	for _, v := range metas[0][key] {
		all := true
		for _, m := range metas[1:] {
			if !m.Contains(key, v) {
				all = false
				break
			}
		}
		if all {
			out = append(out, v)
		}
	}
	return out
}
