package capsule

import "github.com/scottmeyer/nine-tails/internal/store"

// capsuleGuidance uses the representation actually delivered in this load,
// not the generation's global accounting, to decide which sources may hide.
func capsuleGuidance(q store.Querier, c *Capsule, agent string, meta store.Meta, brief []candidate, inputs []store.BriefInput) ([]*store.Record, error) {
	return guidanceWithEligibility(q, c, agent, brief, inputs, func(r *store.Record) bool {
		return !store.Conflicts(r.Meta, meta)
	})
}

// guidanceWithEligibility applies one owner's lifecycle accounting while the
// caller defines its scope eligibility. It is shared by local rendering and
// one-hop guidance links so an old record hidden by an applicable successor is
// not accidentally revived through a subscription. Callers may pass no brief
// items to retain represented raw sources without exposing foreign brief text.
func guidanceWithEligibility(q store.Querier, c *Capsule, agent string, brief []candidate, inputs []store.BriefInput, eligibleRecord func(*store.Record) bool) ([]*store.Record, error) {
	recs, err := store.ListRecords(q, store.Filter{Agent: agent, Lane: "guidance"})
	if err != nil {
		return nil, err
	}
	eligible := map[string]*store.Record{}
	for _, r := range recs {
		if r.Kind != "brief-item" && eligibleRecord(r) && c.renderableTextBody(r, "recent guidance") {
			eligible[r.ID] = r
		}
	}
	renderedItems := map[string]bool{}
	for _, it := range brief {
		renderedItems[it.rec.ID] = true
	}
	accounting := map[string]store.BriefInput{}
	for _, in := range inputs {
		accounting[in.EntryID] = in
	}
	// A successor must eventually reach an eligible source that will render
	// directly or through its brief. Cyclic/inapplicable chains cannot hide it.
	successorAvailable := func(id string) bool {
		seen := map[string]bool{}
		for {
			// Accounting keeps its historical edge even when an unchanged
			// correction replaces the successor. Judge the current endpoint;
			// unresolved or corrupt lineage cannot justify hiding source text.
			current, err := store.LatestSuccessor(q, id)
			if err != nil {
				return false
			}
			id = current
			if eligible[id] == nil || seen[id] {
				return false
			}
			seen[id] = true
			in := accounting[id]
			if in.Disposition != "superseded-by" {
				return true
			}
			id = in.Successor
		}
	}
	var out []*store.Record
	for _, r := range recs {
		if eligible[r.ID] == nil {
			continue
		}
		in := accounting[r.ID]
		covered := false
		switch in.Disposition {
		case "represented":
			// An entry can have its meaning split across several items. Require
			// every item, conservatively preferring repeated text to lost advice.
			covered = len(in.Items) > 0
			for _, id := range in.Items {
				covered = covered && renderedItems[id]
			}
		case "superseded-by":
			covered = successorAvailable(r.ID)
		}
		if !covered {
			out = append(out, r)
		}
	}
	return out, nil
}
