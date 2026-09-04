package main

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/scottmeyer/nine-tails/internal/cli"
	"github.com/scottmeyer/nine-tails/internal/compile"
	"github.com/scottmeyer/nine-tails/internal/store"
)

// inspectView is the full machine-readable dump of one agent.
type inspectView struct {
	Agent        string
	Base         *store.Record
	BaseHistory  []*store.Record
	State        []*store.Record
	Brief        *briefView
	BriefHistory []*briefView
	Journal      []*store.Record
	Tools        []*store.Record
	Agents       []*store.Record
	Signals      []signalRecordView
	Contexts     []*store.Context
}

// inspectOutput uses interface fields so a selected empty section is emitted
// as [] (or null for base/brief), while an unselected --include section stays
// omitted. A non-nil interface is not removed by either JSON or YAML's
// omitempty handling, even when it contains an empty slice or typed nil.
type inspectOutput struct {
	Agent        string `json:"agent" yaml:"agent"`
	Base         any    `json:"base,omitempty" yaml:"base,omitempty"`
	BaseHistory  any    `json:"base_history,omitempty" yaml:"base_history,omitempty"`
	State        any    `json:"state,omitempty" yaml:"state,omitempty"`
	Brief        any    `json:"brief,omitempty" yaml:"brief,omitempty"`
	BriefHistory any    `json:"brief_history,omitempty" yaml:"brief_history,omitempty"`
	Journal      any    `json:"journal,omitempty" yaml:"journal,omitempty"`
	Tools        any    `json:"tools,omitempty" yaml:"tools,omitempty"`
	Agents       any    `json:"agents,omitempty" yaml:"agents,omitempty"`
	Signals      any    `json:"signals,omitempty" yaml:"signals,omitempty"`
	Contexts     any    `json:"contexts,omitempty" yaml:"contexts,omitempty"`
	Records      any    `json:"records,omitempty" yaml:"records,omitempty"`
}

type briefView struct {
	Generation *store.Generation  `json:"generation" yaml:"generation"`
	Items      []*store.Record    `json:"items" yaml:"items"`
	Inputs     []store.BriefInput `json:"inputs" yaml:"inputs"`
}

// recordView is one record plus the receipts that rendered it.
type recordView struct {
	store.RecordEnvelope `yaml:",inline"`
	Delivery             *deliveryView `json:"delivery,omitempty" yaml:"delivery,omitempty"`
	RenderedIn           []string      `json:"rendered_in" yaml:"rendered_in"`
}

// signalRecordView is the record envelope plus its mechanical delivery state.
// It deliberately flattens Record instead of exposing {record, delivery}.
type signalRecordView struct {
	store.RecordEnvelope `yaml:",inline"`
	Delivery             deliveryView `json:"delivery" yaml:"delivery"`
}

func newInspectCmd(a *app) *cobra.Command {
	var include, lane, kind, name, query, coverage, lint, format string
	var all bool
	c := &cobra.Command{
		Use:   "inspect <agent | record-id | ctx-id | gen-id>",
		Short: "Return raw or filtered agent state (the repair surface)",
		Long: `Everything an agent needs to explain or repair another agent.
  inspect pr-review                              full dump (active records)
  inspect pr-review --include base,brief,journal  chosen sections
  inspect pr-review --query "truncated"           substring search across lanes
  inspect pr-review --lane guidance --kind avoid  flat filtered record list
  inspect pr-review --coverage covered-unrendered entries with that classification
  inspect pr-review --lint condition-loss         brief items that dropped source scope
  inspect rec_41 | ctx_72 | gen_12                one thing by id
Add --all to include superseded and disabled records.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.open(); err != nil {
				return err
			}
			target := args[0]
			if store.IsID(target) {
				v, ok, err := a.inspectByID(target)
				if err != nil {
					return err
				}
				if !ok {
					return cli.NotFound("no record, context or generation %s", target)
				}
				return cli.Write(a.stdout, format, v)
			}
			agent := target
			if err := store.ValidAgentName(agent); err != nil {
				return err
			}
			exists, err := store.AgentExists(a.st.DB, agent)
			if err != nil {
				return err
			}
			if !exists {
				return cli.NotFound("no records for agent %q", agent)
			}
			status := ""
			if all {
				status = "*"
			}
			v := &inspectView{Agent: agent}
			// Flag presence, rather than a non-empty value, selects the flat
			// repair view. An explicitly empty filter is still an intentional
			// request for records (and simply leaves that dimension unfiltered).
			flat := cmd.Flags().Changed("lane") || cmd.Flags().Changed("kind") ||
				cmd.Flags().Changed("name") || cmd.Flags().Changed("query")
			switch {
			case coverage != "":
				rows, err := compile.CoverageRows(a.st.DB, agent, coverage)
				if err != nil {
					return err
				}
				return cli.Write(a.stdout, format, map[string]any{"agent": agent, "coverage": rows})
			case lint != "":
				if lint != "condition-loss" {
					return cli.Invalid("unknown lint %q (condition-loss)", lint)
				}
				ws, err := compile.LintConditionLoss(a.st.DB, agent)
				if err != nil {
					return err
				}
				if ws == nil {
					ws = []compile.Warning{}
				}
				gen := ""
				if g, err := store.ActiveGeneration(a.st.DB, agent); err == nil {
					gen = g.ID
				}
				return cli.Write(a.stdout, format, map[string]any{"agent": agent, "generation": gen, "lint": ws})
			case flat:
				recs, err := store.ListRecords(a.st.DB, store.Filter{Agent: agent, Lane: lane, Kind: kind, Name: name, Status: status})
				if err != nil {
					return err
				}
				if query != "" {
					recs = filterQuery(recs, query)
				}
				if recs == nil {
					recs = []*store.Record{}
				}
				rows, err := a.flattenInspectRecords(recs)
				if err != nil {
					return err
				}
				return cli.Write(a.stdout, format, inspectOutput{Agent: agent, Records: rows})
			default:
				want, err := inspectSections(include)
				if err != nil {
					return err
				}
				if err := a.fillInspect(v, agent, want, status); err != nil {
					return err
				}
				return cli.Write(a.stdout, format, v.output(want, all))
			}
		},
	}
	c.Flags().StringVar(&include, "include", "", "comma list of base,state,brief,journal,tools,agents,signals,contexts (default: all but contexts)")
	c.Flags().StringVar(&lane, "lane", "", "filter: lane")
	c.Flags().StringVar(&kind, "kind", "", "filter: kind")
	c.Flags().StringVar(&name, "name", "", "filter: name")
	c.Flags().StringVar(&query, "query", "", "case-insensitive substring over body, name and metadata values")
	c.Flags().StringVar(&coverage, "coverage", "", "list entries with this coverage: novel|covered-unrendered|covered-rendered|refinement|unknown")
	c.Flags().StringVar(&lint, "lint", "", "run a lint: condition-loss")
	c.Flags().BoolVar(&all, "all", false, "include superseded and disabled records")
	c.Flags().StringVar(&format, "format", "json", "json|yaml")
	return c
}

func filterQuery(recs []*store.Record, q string) []*store.Record {
	q = strings.ToLower(q)
	var out []*store.Record
	for _, r := range recs {
		if strings.Contains(strings.ToLower(r.Body), q) || strings.Contains(strings.ToLower(r.Name), q) {
			out = append(out, r)
			continue
		}
		hit := false
		for _, vs := range r.Meta {
			for _, v := range vs {
				if strings.Contains(strings.ToLower(v), q) {
					hit = true
				}
			}
		}
		if hit {
			out = append(out, r)
		}
	}
	return out
}

func inspectSections(include string) (map[string]bool, error) {
	want := map[string]bool{}
	if include == "" {
		for _, s := range []string{"base", "state", "brief", "journal", "tools", "agents", "signals"} {
			want[s] = true
		}
		return want, nil
	}
	for _, s := range strings.Split(include, ",") {
		s = strings.TrimSpace(s)
		switch s {
		case "base", "state", "brief", "journal", "tools", "agents", "signals", "contexts":
			want[s] = true
		case "":
		default:
			return nil, cli.Invalid("unknown section %q in --include", s)
		}
	}
	return want, nil
}

func (v *inspectView) output(want map[string]bool, all bool) inspectOutput {
	out := inspectOutput{Agent: v.Agent}
	if want["base"] {
		if v.Base == nil {
			// A typed nil *store.Record would invoke Record.MarshalYAML in
			// yaml.v3 and panic before the value-receiver method can run. A nil
			// pointer without custom marshaling preserves the required explicit
			// null in both formats.
			out.Base = (*struct{})(nil)
		} else {
			out.Base = v.Base
		}
		if all {
			if v.BaseHistory == nil {
				v.BaseHistory = []*store.Record{}
			}
			out.BaseHistory = v.BaseHistory
		}
	}
	if want["state"] {
		if v.State == nil {
			v.State = []*store.Record{}
		}
		out.State = v.State
	}
	if want["brief"] {
		out.Brief = v.Brief
		if all {
			if v.BriefHistory == nil {
				v.BriefHistory = []*briefView{}
			}
			out.BriefHistory = v.BriefHistory
		}
	}
	if want["journal"] {
		if v.Journal == nil {
			v.Journal = []*store.Record{}
		}
		out.Journal = v.Journal
	}
	if want["tools"] {
		if v.Tools == nil {
			v.Tools = []*store.Record{}
		}
		out.Tools = v.Tools
	}
	if want["agents"] {
		if v.Agents == nil {
			v.Agents = []*store.Record{}
		}
		out.Agents = v.Agents
	}
	if want["signals"] {
		if v.Signals == nil {
			v.Signals = []signalRecordView{}
		}
		out.Signals = v.Signals
	}
	if want["contexts"] {
		if v.Contexts == nil {
			v.Contexts = []*store.Context{}
		}
		out.Contexts = v.Contexts
	}
	return out
}

func (a *app) flattenInspectRecords(recs []*store.Record) ([]any, error) {
	out := make([]any, 0, len(recs))
	for _, rec := range recs {
		if rec.Lane != "signal" {
			out = append(out, rec)
			continue
		}
		delivery, err := store.GetDelivery(a.st.DB, rec.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, signalRecordView{RecordEnvelope: rec.Envelope(), Delivery: deliveryViewAsOf(*delivery, a.now())})
	}
	return out, nil
}

func (a *app) inspectByID(id string) (any, bool, error) {
	db := a.st.DB
	switch {
	case strings.HasPrefix(id, "ctx_"):
		c, err := store.GetContext(db, id)
		if err != nil {
			return nil, false, err
		}
		return c, true, nil
	case strings.HasPrefix(id, "gen_"):
		g, err := store.GetGeneration(db, id)
		if err != nil {
			return nil, false, err
		}
		items, err := store.GenerationItems(db, id)
		if err != nil {
			return nil, false, err
		}
		inputs, err := store.GenerationInputs(db, id)
		if err != nil {
			return nil, false, err
		}
		if items == nil {
			items = []*store.Record{}
		}
		if inputs == nil {
			inputs = []store.BriefInput{}
		}
		return briefView{Generation: g, Items: items, Inputs: inputs}, true, nil
	case strings.HasPrefix(id, "lease_"):
		return nil, false, cli.NotFound("%s is a lease token, not a record", id)
	}
	ok, err := store.RecordExists(db, id)
	if err != nil {
		return nil, false, err
	}
	if !ok {
		// Not an id after all (e.g. an agent literally named like one).
		return nil, false, nil
	}
	rec, err := store.GetRecord(db, id)
	if err != nil {
		return nil, false, err
	}
	v := recordView{RecordEnvelope: rec.Envelope(), RenderedIn: []string{}}
	if rec.Lane == "signal" {
		if d, err := store.GetDelivery(db, id); err == nil {
			effective := deliveryViewAsOf(*d, a.now())
			v.Delivery = &effective
		}
	}
	ctxs, err := store.ContextsRendering(db, id)
	if err != nil {
		return nil, false, err
	}
	if ctxs != nil {
		v.RenderedIn = ctxs
	}
	return v, true, nil
}

func (a *app) fillInspect(v *inspectView, agent string, want map[string]bool, status string) error {
	db := a.st.DB
	recs, err := store.ListRecords(db, store.Filter{Agent: agent, Status: status})
	if err != nil {
		return err
	}
	for _, r := range recs {
		switch {
		case r.Lane == "definition" && r.Kind == "agent-base":
			if want["base"] {
				if status == "*" {
					v.BaseHistory = append(v.BaseHistory, r)
				}
				if r.Status == "active" {
					v.Base = r
				}
			}
		case r.Lane == "state":
			if want["state"] {
				v.State = append(v.State, r)
			}
		case r.Lane == "definition" && r.Kind == "tool":
			if want["tools"] {
				v.Tools = append(v.Tools, r)
			}
		case r.Lane == "definition" && r.Kind == "related-agent":
			if want["agents"] {
				v.Agents = append(v.Agents, r)
			}
		case r.Kind == "brief-item":
			// shown under brief
		case r.Lane == "signal":
			if want["signals"] && status == "*" {
				delivery, err := store.GetDelivery(db, r.ID)
				if err != nil {
					return err
				}
				v.Signals = append(v.Signals, signalRecordView{RecordEnvelope: r.Envelope(), Delivery: deliveryViewAsOf(*delivery, a.now())})
			}
		case r.Lane == "guidance" || r.Lane == "recall":
			if want["journal"] {
				v.Journal = append(v.Journal, r)
			}
		default:
			if want["journal"] {
				v.Journal = append(v.Journal, r)
			}
		}
	}
	if want["tools"] && agent != "shared" {
		shared, err := store.ListRecords(db, store.Filter{Agent: "shared", Lane: "definition", Kind: "tool", Status: status})
		if err != nil {
			return err
		}
		for _, r := range shared {
			if !r.Meta.Has("available-to") || r.Meta.Contains("available-to", agent) {
				v.Tools = append(v.Tools, r)
			}
		}
	}
	if want["brief"] {
		if status == "*" {
			generations, err := store.ListGenerations(db, agent)
			if err != nil {
				return err
			}
			for _, generation := range generations {
				view, err := loadBriefView(db, generation)
				if err != nil {
					return err
				}
				v.BriefHistory = append(v.BriefHistory, view)
				if generation.Status == "active" {
					v.Brief = view
				}
			}
		} else if generation, err := store.ActiveGeneration(db, agent); err == nil {
			view, err := loadBriefView(db, generation)
			if err != nil {
				return err
			}
			v.Brief = view
		}
	}
	if want["signals"] && status != "*" {
		sigs, err := store.PendingSignals(db, agent, a.now())
		if err != nil {
			return err
		}
		for _, sig := range sigs {
			v.Signals = append(v.Signals, signalRecordView{RecordEnvelope: sig.Record.Envelope(), Delivery: deliveryViewAsOf(sig.Delivery, a.now())})
		}
	}
	if want["contexts"] {
		ctxs, err := store.ListContexts(db, agent, 20)
		if err != nil {
			return err
		}
		v.Contexts = ctxs
	}
	return nil
}

func loadBriefView(q store.Querier, generation *store.Generation) (*briefView, error) {
	items, err := store.GenerationItems(q, generation.ID)
	if err != nil {
		return nil, err
	}
	inputs, err := store.GenerationInputs(q, generation.ID)
	if err != nil {
		return nil, err
	}
	if items == nil {
		items = []*store.Record{}
	}
	if inputs == nil {
		inputs = []store.BriefInput{}
	}
	return &briefView{Generation: generation, Items: items, Inputs: inputs}, nil
}
