package capsule

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/scottmeyer/nine-tails/internal/cli"
	"github.com/scottmeyer/nine-tails/internal/store"
)

// GuidanceLinkView identifies an explicit subscription and the exact current
// source guidance record it delivered. The source body is represented by the
// normal rendered-record/receipt row, while this view preserves provenance.
type GuidanceLinkView struct {
	ID          string     `json:"id" yaml:"id"`
	Ref         string     `json:"ref" yaml:"ref"`
	Name        string     `json:"name" yaml:"name"`
	Source      string     `json:"source" yaml:"source"`
	GuidanceID  string     `json:"guidance_id" yaml:"guidance_id"`
	GuidanceRef string     `json:"guidance_ref" yaml:"guidance_ref"`
	Meta        store.Meta `json:"meta" yaml:"meta"`
	SourceMeta  store.Meta `json:"source_meta" yaml:"source_meta"`
}

type linkedGuidance struct {
	record *store.Record
	ref    string
	links  []*store.Record
}

// sourceGuidance keeps the source owner's lifecycle semantics without exposing
// its brief items. A represented source stays raw because no source brief item
// is delivered; source superseded-by accounting still hides an obsolete entry
// when its current eligible successor will be delivered through this link.
func sourceGuidance(q store.Querier, c *Capsule, source string, linkMeta, loadMeta store.Meta) ([]*store.Record, error) {
	var inputs []store.BriefInput
	if gen, err := store.ActiveGeneration(q, source); err == nil {
		var inputErr error
		inputs, inputErr = store.GenerationInputs(q, gen.ID)
		if inputErr != nil {
			return nil, inputErr
		}
	} else if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	return guidanceWithEligibility(q, c, source, nil, inputs, func(rec *store.Record) bool {
		return store.GuidanceLinkApplies(linkMeta, rec.Meta, loadMeta)
	})
}

// writeSharedGuidance resolves only active, direct source guidance. It does
// not compile, load, or otherwise traverse the source owner, so a source's
// identity, state, tools, recall, and subscriptions cannot cross the link.
func writeSharedGuidance(q store.Querier, c *Capsule, md *strings.Builder, agent string, meta store.Meta) error {
	links, err := store.ListRecords(q, store.Filter{Agent: agent, Lane: "definition", Kind: "guidance-link"})
	if err != nil {
		return err
	}
	sort.SliceStable(links, func(i, j int) bool {
		a, b := store.Overlap(links[i].Meta, meta), store.Overlap(links[j].Meta, meta)
		if a != b {
			return a > b
		}
		return links[i].Name < links[j].Name
	})

	byGuidance := map[string]*linkedGuidance{}
	var groups []*linkedGuidance
	linkRefs := map[string]string{}
	var notices []string
	unresolved := func(link *store.Record, reason string) {
		excerpt, cut := excerptOf(reason, 240)
		if cut {
			excerpt += "…"
		}
		ref := linkRefs[link.ID]
		excerpt += "; inspect with " + cli.InlineCode(c.command("inspect "+ref))
		c.skip(link.ID, excerpt)
		notices = append(notices, "- Unresolved shared-guidance link `"+ref+"`: "+excerpt+"\n")
	}

	for _, link := range links {
		if store.Conflicts(link.Meta, meta) {
			continue
		}
		ref, err := store.Reference(q, link.ID)
		if err != nil {
			return err
		}
		linkRefs[link.ID] = ref
		source, err := store.GuidanceLinkTarget(link.Body)
		if err != nil || source == agent {
			unresolved(link, "invalid source; replace this link with `agent follow <subscriber>/<alias> <source-agent> --expect "+ref+"`")
			continue
		}
		exists, err := store.AgentExists(q, source)
		if err != nil {
			return err
		}
		if !exists {
			unresolved(link, "no records for source agent "+source+"; create its base or guidance, or replace/disable this link")
			continue
		}
		recs, err := sourceGuidance(q, c, source, link.Meta, meta)
		if err != nil {
			return err
		}
		for _, rec := range recs {
			guidanceRef, err := store.Reference(q, rec.ID)
			if err != nil {
				return err
			}
			group := byGuidance[rec.ID]
			if group == nil {
				group = &linkedGuidance{record: rec, ref: guidanceRef}
				byGuidance[rec.ID] = group
				groups = append(groups, group)
			}
			group.links = append(group.links, link)
		}
	}
	if len(groups) == 0 && len(notices) == 0 {
		return nil
	}

	md.WriteString("\n## Shared guidance\n\n")
	for _, group := range groups {
		rec := group.record
		fmt.Fprintf(md, "- Shared from `%s` (`%s`)", rec.Agent, group.ref)
		if scope := strings.TrimSpace(bracket(rec.Meta, hiddenKeys)); scope != "" {
			fmt.Fprintf(md, " %s", scope)
		}
		fmt.Fprintf(md, " (%s) %s\n", rec.Kind, indentItem(rec.Body))
		c.add(rec, "shared-guidance")
		for _, link := range group.links {
			ref := linkRefs[link.ID]
			fmt.Fprintf(md, "  - Via `%s/%s` (`%s`)", agent, link.Name, ref)
			if scope := strings.TrimSpace(bracket(link.Meta, hiddenKeys)); scope != "" {
				fmt.Fprintf(md, " %s", scope)
			}
			md.WriteString("\n")
			c.GuidanceLinks = append(c.GuidanceLinks, GuidanceLinkView{
				ID: link.ID, Ref: ref, Name: link.Name, Source: rec.Agent,
				GuidanceID: rec.ID, GuidanceRef: group.ref, Meta: link.Meta, SourceMeta: rec.Meta,
			})
			c.add(link, "guidance-links")
		}
	}
	for _, notice := range notices {
		md.WriteString(notice)
	}
	return nil
}
