// Package capsule assembles a named agent into a token-bounded context
// capsule (spec §10): base + state + brief items + recent guidance + tools +
// related agents + due signals, ranked by metadata overlap, cut at the budget,
// and recorded as an immutable context receipt. The whole load runs in one
// write transaction so the context ID is known before rendering.
package capsule

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	"github.com/scottmeyer/nine-tails/internal/store"
	"github.com/scottmeyer/nine-tails/internal/tokens"
	"github.com/scottmeyer/nine-tails/internal/tool"
)

// Policy is the budget policy (DESIGN.md §7). Fractions apply to the
// post-mandatory budget.
type Policy struct {
	BriefFloor         float64
	RecentCap          float64
	ToolsCap           float64
	SignalsCap         float64
	SignalExcerptChars int
}

// DefaultPolicy matches DESIGN.md defaults.
var DefaultPolicy = Policy{BriefFloor: 0.40, RecentCap: 0.30, ToolsCap: 0.15, SignalsCap: 0.15, SignalExcerptChars: 300}

// validate checks that the configurable section allocations are meaningful.
// The four fractions share the post-mandatory budget, so their sum may not
// exceed one. Load also clamps every fill to the global remainder as a second
// line of defense against ever exceeding the caller's maximum.
func (p Policy) validate() error {
	allocations := []struct {
		name  string
		value float64
	}{
		{"brief_floor", p.BriefFloor},
		{"recent_cap", p.RecentCap},
		{"tools_cap", p.ToolsCap},
		{"signals_cap", p.SignalsCap},
	}
	total := 0.0
	for _, a := range allocations {
		if math.IsNaN(a.value) || math.IsInf(a.value, 0) || a.value < 0 || a.value > 1 {
			return fmt.Errorf("%s must be a finite fraction from 0 to 1 (got %v)", a.name, a.value)
		}
		total += a.value
	}
	if total > 1+1e-9 {
		return fmt.Errorf("brief_floor + recent_cap + tools_cap + signals_cap must be at most 1 (got %g)", total)
	}
	if p.SignalExcerptChars <= 0 {
		return fmt.Errorf("signal_excerpt_chars must be positive (got %d)", p.SignalExcerptChars)
	}
	return nil
}

// Request describes one load.
type Request struct {
	Agent  string
	Task   string
	Parent string     // parent context ID, "" for none
	Meta   store.Meta // explicit --meta
	Budget int
	Policy Policy
	Now    time.Time
}

// SignalView is the capped view of a due signal in a capsule.
type SignalView struct {
	ID          string     `json:"id" yaml:"id"`
	Subject     string     `json:"subject" yaml:"subject"`
	Excerpt     string     `json:"excerpt" yaml:"excerpt"`
	Truncated   bool       `json:"truncated" yaml:"truncated"`
	State       string     `json:"state" yaml:"state"`
	LeasedUntil string     `json:"leased_until,omitempty" yaml:"leased_until,omitempty"`
	Meta        store.Meta `json:"meta" yaml:"meta"`
	Inspect     string     `json:"inspect" yaml:"inspect"`
}

// StateView is one state document in a capsule.
type StateView struct {
	ID     string `json:"id" yaml:"id"`
	Name   string `json:"name" yaml:"name"`
	Format string `json:"format" yaml:"format"`
	Body   string `json:"body" yaml:"body"`
}

// Truncation reports how many candidates a section dropped for budget.
type Truncation struct {
	Section string `json:"section" yaml:"section"`
	Omitted int    `json:"omitted" yaml:"omitted"`
}

// Skipped reports an optional record that could not be rendered.
type Skipped struct {
	ID     string `json:"id" yaml:"id"`
	Reason string `json:"reason" yaml:"reason"`
}

// Capsule is the assembled result.
type Capsule struct {
	ContextID       string       `json:"context_id" yaml:"context_id"`
	Agent           string       `json:"agent" yaml:"agent"`
	Task            string       `json:"task" yaml:"task"`
	Parent          string       `json:"parent_context" yaml:"parent_context"`
	Metadata        store.Meta   `json:"metadata" yaml:"metadata"`
	Instructions    string       `json:"instructions" yaml:"instructions"`
	State           []StateView  `json:"state" yaml:"state"`
	Tools           []string     `json:"tools" yaml:"tools"`
	Agents          []string     `json:"agents" yaml:"agents"`
	Signals         []SignalView `json:"signals" yaml:"signals"`
	RenderedIDs     []string     `json:"rendered_record_ids" yaml:"rendered_record_ids"`
	Budget          int          `json:"budget" yaml:"budget"`
	EstimatedTokens int          `json:"estimated_tokens" yaml:"estimated_tokens"`
	Truncated       []Truncation `json:"truncated" yaml:"truncated"`
	Skipped         []Skipped    `json:"skipped" yaml:"skipped"`

	// Markdown is the full context-ready document (instructions + signals).
	Markdown string `json:"-" yaml:"-"`
	rendered []store.ContextRecord
}

// TruncationSummary renders the stderr line for md mode, or "" when nothing
// was omitted.
func (c *Capsule) TruncationSummary() string {
	if len(c.Truncated) == 0 {
		return ""
	}
	parts := make([]string, 0, len(c.Truncated))
	for _, t := range c.Truncated {
		parts = append(parts, fmt.Sprintf("%d %s", t.Omitted, t.Section))
	}
	return fmt.Sprintf("budget %d: omitted %s", c.Budget, strings.Join(parts, ", "))
}

type candidate struct {
	rec     *store.Record
	score   int
	text    string // rendered fragment
	cost    int
	ordinal int
}

var errBudget = errors.New("budget")

// IsBudgetError reports whether err is a "cannot fit budget" error.
func IsBudgetError(err error) bool { return errors.Is(err, errBudget) }

// Load assembles the capsule and persists its receipt in one transaction.
func Load(s *store.Store, req Request) (*Capsule, error) {
	if req.Now.IsZero() {
		req.Now = store.Clock()
	}
	if req.Policy == (Policy{}) {
		req.Policy = DefaultPolicy
	}
	if err := req.Policy.validate(); err != nil {
		return nil, fmt.Errorf("%w: policy: %v", store.ErrInvalid, err)
	}
	if req.Budget <= 0 {
		return nil, fmt.Errorf("%w: budget must be positive", store.ErrInvalid)
	}
	var out *Capsule
	err := s.Tx(func(tx *sql.Tx) error {
		c, err := load(tx, req)
		if err != nil {
			return err
		}
		out = c
		return nil
	})
	return out, err
}

func load(tx *sql.Tx, req Request) (*Capsule, error) {
	pol := req.Policy

	// Resolved metadata: parent's ∪ explicit.
	meta := store.Meta{}
	if req.Parent != "" {
		pc, err := store.GetContext(tx, req.Parent)
		if err != nil {
			return nil, err
		}
		meta.Merge(pc.Meta)
	}
	meta.Merge(req.Meta)

	base, err := store.ActiveNamed(tx, req.Agent, "definition", "agent-base", "base")
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, fmt.Errorf("%w: agent %q has no base definition (create one with `nine-tails base %s ...`)", store.ErrNotFound, req.Agent, req.Agent)
		}
		return nil, err
	}

	ctxID, err := store.NextID(tx, "ctx")
	if err != nil {
		return nil, err
	}
	c := &Capsule{ContextID: ctxID, Agent: req.Agent, Task: req.Task, Parent: req.Parent, Metadata: meta, Budget: req.Budget,
		State: []StateView{}, Tools: []string{}, Agents: []string{}, Signals: []SignalView{}, RenderedIDs: []string{},
		Truncated: []Truncation{}, Skipped: []Skipped{}}

	// ---- mandatory: header + base + state ----
	var md strings.Builder
	md.WriteString("# " + titleOf(base, req.Agent) + "\n\n")
	md.WriteString("[nine-tails-context=" + ctxID + "]\n\n")
	md.WriteString(base.Body + "\n")
	c.add(base, "base")

	states, err := store.ListRecords(tx, store.Filter{Agent: req.Agent, Lane: "state"})
	if err != nil {
		return nil, err
	}
	var stateCands []candidate
	for _, st := range states {
		if store.Conflicts(st.Meta, meta) {
			continue
		}
		var v any
		if err := yaml.Unmarshal([]byte(st.Body), &v); err != nil {
			c.Skipped = append(c.Skipped, Skipped{ID: st.ID, Reason: "state body is not valid YAML"})
			continue
		}
		stateCands = append(stateCands, candidate{rec: st, score: store.Overlap(st.Meta, meta)})
	}
	sort.SliceStable(stateCands, func(a, b int) bool {
		if stateCands[a].score != stateCands[b].score {
			return stateCands[a].score > stateCands[b].score
		}
		return stateCands[a].rec.Name < stateCands[b].rec.Name
	})
	for _, sc := range stateCands {
		st := sc.rec
		md.WriteString("\n## Current state (" + st.Name + ", " + st.ID + ")\n\n```yaml\n" + st.Body + "\n```\n")
		c.add(st, "state")
		c.State = append(c.State, StateView{ID: st.ID, Name: st.Name, Format: "yaml", Body: st.Body})
	}
	mandatory := tokens.Estimate(md.String())
	if mandatory > req.Budget {
		return nil, fmt.Errorf("%w: mandatory content needs %d tokens, budget is %d", errBudget, mandatory, req.Budget)
	}
	R := req.Budget - mandatory
	briefFloor := int(pol.BriefFloor * float64(R))
	recentCap := int(pol.RecentCap * float64(R))
	toolsCap := int(pol.ToolsCap * float64(R))
	signalsCap := int(pol.SignalsCap * float64(R))
	used := 0

	// ---- brief items ----
	var briefCands []candidate
	if gen, err := store.ActiveGeneration(tx, req.Agent); err == nil {
		items, err := store.GenerationItems(tx, gen.ID)
		if err != nil {
			return nil, err
		}
		for i, it := range items {
			if it.Status != "active" || store.Conflicts(it.Meta, meta) {
				continue
			}
			text := "- " + bracket(it.Meta, hiddenKeys) + escapeLead(it.Meta, indentItem(it.Body)) + "\n"
			briefCands = append(briefCands, candidate{rec: it, score: store.Overlap(it.Meta, meta), text: text, cost: tokens.Estimate(text), ordinal: i})
		}
		sort.SliceStable(briefCands, func(a, b int) bool {
			if briefCands[a].score != briefCands[b].score {
				return briefCands[a].score > briefCands[b].score
			}
			return briefCands[a].ordinal < briefCands[b].ordinal
		})
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}

	// ---- recent guidance (unrepresented) ----
	guidance, err := store.RecentGuidance(tx, req.Agent)
	if err != nil {
		return nil, err
	}
	var recentCands []candidate
	for i := len(guidance) - 1; i >= 0; i-- { // newest first
		g := guidance[i]
		if store.Conflicts(g.Meta, meta) {
			continue
		}
		text := "- " + bracket(g.Meta, hiddenKeys) + "(" + g.Kind + ") " + indentItem(g.Body) + "\n"
		recentCands = append(recentCands, candidate{rec: g, score: store.Overlap(g.Meta, meta), text: text, cost: tokens.Estimate(text), ordinal: len(recentCands)})
	}
	sort.SliceStable(recentCands, func(a, b int) bool { return recentCands[a].score > recentCands[b].score })

	// ---- tools + related agents ----
	toolCands, err := toolCandidates(tx, c, req.Agent, meta)
	if err != nil {
		return nil, err
	}
	agentRecs, err := store.ListRecords(tx, store.Filter{Agent: req.Agent, Lane: "definition", Kind: "related-agent"})
	if err != nil {
		return nil, err
	}
	var agentCands []candidate
	for _, a := range agentRecs {
		if store.Conflicts(a.Meta, meta) {
			continue
		}
		text := "- `" + a.Name + "`: " + oneLine(a.Body) + "\n"
		agentCands = append(agentCands, candidate{rec: a, score: store.Overlap(a.Meta, meta), text: text, cost: tokens.Estimate(text)})
	}
	sort.SliceStable(agentCands, func(a, b int) bool {
		if agentCands[a].score != agentCands[b].score {
			return agentCands[a].score > agentCands[b].score
		}
		return agentCands[a].rec.Name < agentCands[b].rec.Name
	})

	// ---- signals ----
	due, err := store.DueSignalsVisible(tx, req.Agent, req.Now)
	if err != nil {
		return nil, err
	}
	var sigCands []candidate
	sigViews := map[string]SignalView{}
	for _, sg := range due {
		if store.Conflicts(sg.Record.Meta, meta) {
			continue
		}
		subject := oneLine(sg.Record.Meta.First("subject"))
		excerpt, trunc := excerptOf(sg.Record.Body, pol.SignalExcerptChars)
		lead := []string{"signal=" + sg.Record.ID}
		if sg.Delivery.State == "leased" {
			lead = append(lead, "state=leased")
		}
		line := "- " + bracket(sg.Record.Meta, signalHiddenKeys, lead...) + subject
		if excerpt != "" {
			line += " — " + excerpt
		}
		if trunc {
			line += "… (truncated; inspect with `nine-tails inspect " + sg.Record.ID + "`)"
		}
		line += "\n"
		sigViews[sg.Record.ID] = SignalView{ID: sg.Record.ID, Subject: subject, Excerpt: excerpt, Truncated: trunc,
			State: sg.Delivery.State, LeasedUntil: sg.Delivery.LeasedUntil, Meta: sg.Record.Meta, Inspect: "nine-tails inspect " + sg.Record.ID}
		sigCands = append(sigCands, candidate{rec: sg.Record, score: store.Overlap(sg.Record.Meta, meta), text: line, cost: tokens.Estimate(line)})
	}
	sort.SliceStable(sigCands, func(a, b int) bool { return sigCands[a].score > sigCands[b].score })

	// ---- fill ----
	const hdrBrief, hdrRecent, hdrTools, hdrAgents, hdrSignals = "\n## Working brief\n\n", "\n## Recent adjustments\n\n", "\n## Available tools\n\n", "\n## Available agents\n\n", "\n## Due signals (external inbox data)\n\n"

	// Policy validation keeps the configured phase-1 allocations within R.
	// Still clamp each fill to the actual global remainder so a future policy
	// change or rounding mistake can never make estimated_tokens exceed budget.
	remainingCap := func(sectionCap int) int {
		left := R - used
		if left <= 0 || sectionCap <= 0 {
			return 0
		}
		if sectionCap > left {
			return left
		}
		return sectionCap
	}

	briefSel, briefRest, n := fill(briefCands, remainingCap(briefFloor), tokens.Estimate(hdrBrief))
	used += n
	recentSel, recentRest, n := fill(recentCands, remainingCap(recentCap), tokens.Estimate(hdrRecent))
	used += n
	toolSel, _, n := fill(toolCands, remainingCap(toolsCap), tokens.Estimate(hdrTools))
	used += n
	agentSel, _, n := fill(agentCands, remainingCap(toolsCap-n), tokens.Estimate(hdrAgents))
	used += n
	sigSel, _, n := fill(sigCands, remainingCap(signalsCap), tokens.Estimate(hdrSignals))
	used += n

	// phase 2: leftover → omitted brief items, then omitted recent guidance
	if left := R - used; left > 0 && len(briefRest) > 0 {
		hdr := 0
		if len(briefSel) == 0 {
			hdr = tokens.Estimate(hdrBrief)
		}
		more, rest, n := fill(briefRest, left, hdr)
		briefSel = append(briefSel, more...)
		briefRest = rest
		used += n
	}
	if left := R - used; left > 0 && len(recentRest) > 0 {
		hdr := 0
		if len(recentSel) == 0 {
			hdr = tokens.Estimate(hdrRecent)
		}
		more, rest, n := fill(recentRest, left, hdr)
		recentSel = append(recentSel, more...)
		recentRest = rest
		used += n
	}
	// Render order is the sort order: re-sort the union of phase 1 and 2.
	sort.SliceStable(briefSel, func(a, b int) bool {
		if briefSel[a].score != briefSel[b].score {
			return briefSel[a].score > briefSel[b].score
		}
		return briefSel[a].ordinal < briefSel[b].ordinal
	})
	sort.SliceStable(recentSel, func(a, b int) bool {
		if recentSel[a].score != recentSel[b].score {
			return recentSel[a].score > recentSel[b].score
		}
		return recentSel[a].ordinal < recentSel[b].ordinal // created_at desc, rowid desc
	})

	writeSection := func(hdr, section string, sel []candidate) {
		if len(sel) == 0 {
			return
		}
		md.WriteString(hdr)
		for _, cd := range sel {
			md.WriteString(cd.text)
			c.add(cd.rec, section)
		}
	}
	writeSection(hdrBrief, "brief", briefSel)
	writeSection(hdrRecent, "recent", recentSel)
	writeSection(hdrTools, "tools", toolSel)
	for _, cd := range toolSel {
		c.Tools = append(c.Tools, cd.rec.Name)
	}
	writeSection(hdrAgents, "agents", agentSel)
	for _, cd := range agentSel {
		c.Agents = append(c.Agents, cd.rec.Name)
	}
	c.Instructions = md.String()
	writeSection(hdrSignals, "signals", sigSel)
	for _, cd := range sigSel {
		c.Signals = append(c.Signals, sigViews[cd.rec.ID])
	}
	c.Markdown = md.String()
	c.EstimatedTokens = mandatory + used

	c.truncation("brief", len(briefRest))
	c.truncation("recent", len(recentRest))
	c.truncation("tools", len(toolCands)-len(toolSel))
	c.truncation("agents", len(agentCands)-len(agentSel))
	c.truncation("signals", len(sigCands)-len(sigSel))

	// ---- receipt ----
	if err := store.CreateContextWithID(tx, ctxID, req.Agent, req.Parent, req.Task, req.Budget, meta, c.rendered); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Capsule) add(r *store.Record, section string) {
	c.rendered = append(c.rendered, store.ContextRecord{RecordID: r.ID, Section: section, Ordinal: len(c.rendered)})
	c.RenderedIDs = append(c.RenderedIDs, r.ID)
}

func (c *Capsule) truncation(section string, omitted int) {
	if omitted > 0 {
		c.Truncated = append(c.Truncated, Truncation{Section: section, Omitted: omitted})
	}
}

// fill selects candidates in order while their cumulative cost (plus the
// section header, charged once when the first item is selected) stays within
// cap. Each candidate is atomic; a candidate that does not fit is skipped and
// iteration continues. Returns selected, rejected, and tokens used.
func fill(cands []candidate, cap_ int, header int) (sel, rest []candidate, used int) {
	for _, cd := range cands {
		extra := cd.cost
		if len(sel) == 0 {
			extra += header
		}
		if used+extra <= cap_ {
			sel = append(sel, cd)
			used += extra
		} else {
			rest = append(rest, cd)
		}
	}
	return sel, rest, used
}

func toolCandidates(q store.Querier, c *Capsule, agent string, meta store.Meta) ([]candidate, error) {
	own, err := store.ListRecords(q, store.Filter{Agent: agent, Lane: "definition", Kind: "tool"})
	if err != nil {
		return nil, err
	}
	// Agent-owned names shadow shared names unconditionally. In particular, an
	// owned definition that conflicts with this invocation's metadata is omitted
	// rather than allowing a different shared implementation to appear under the
	// same semantic name (and disagree with call's owned-first resolution).
	seen := map[string]bool{}
	var out []candidate
	push := func(r *store.Record) {
		if store.Conflicts(r.Meta, meta) {
			return
		}
		def, err := tool.Parse(r.Body)
		if err != nil {
			c.Skipped = append(c.Skipped, Skipped{ID: r.ID, Reason: "tool body: " + err.Error()})
			return
		}
		text := "- `" + r.Name + "`: " + oneLine(def.Description) + bracketSuffix(r.Meta, hiddenKeys) + "\n"
		out = append(out, candidate{rec: r, score: store.Overlap(r.Meta, meta), text: text, cost: tokens.Estimate(text)})
	}
	for _, r := range own {
		seen[r.Name] = true
		push(r)
	}
	if agent != "shared" {
		shared, err := store.ListRecords(q, store.Filter{Agent: "shared", Lane: "definition", Kind: "tool"})
		if err != nil {
			return nil, err
		}
		for _, r := range shared {
			if r.Meta.Has("available-to") && !r.Meta.Contains("available-to", agent) {
				continue
			}
			if seen[r.Name] {
				continue
			}
			seen[r.Name] = true
			push(r)
		}
	}
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].score != out[b].score {
			return out[a].score > out[b].score
		}
		return out[a].rec.Name < out[b].rec.Name
	})
	return out, nil
}

// hiddenKeys are never shown in meta brackets.
var hiddenKeys = map[string]bool{"subject": true, "available-to": true, "title": true}

// signalHiddenKeys additionally hides the keys the signal bracket reserves
// for its own signal=<id> and state=leased pairs.
var signalHiddenKeys = map[string]bool{"subject": true, "available-to": true, "title": true, "signal": true, "state": true}

func titleOf(base *store.Record, agent string) string {
	if t := base.Meta.First("title"); t != "" {
		return t
	}
	words := strings.FieldsFunc(agent, func(r rune) bool { return r == '-' || r == '_' || r == '.' })
	for i, w := range words {
		if w != "" {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}

// bracket renders "[k=v k2=v2] " for meta (sorted keys, insertion-order
// values), skipping hidden keys, with optional leading literal pairs. Empty
// when nothing to show. Values containing Unicode whitespace, ']' or '"' are
// quoted.
func bracket(m store.Meta, hide map[string]bool, lead ...string) string {
	var parts []string
	parts = append(parts, lead...)
	for _, k := range store.SortedKeys(m) {
		if hide[k] {
			continue
		}
		for _, v := range m[k] {
			parts = append(parts, k+"="+quoteValue(v))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "[" + strings.Join(parts, " ") + "] "
}

func quoteValue(v string) string {
	if v != "" && !strings.ContainsAny(v, "]\"") && !strings.ContainsFunc(v, unicode.IsSpace) {
		return v
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(v) + `"`
}

func bracketSuffix(m store.Meta, hide map[string]bool) string {
	b := bracket(m, hide)
	if b == "" {
		return ""
	}
	return " " + strings.TrimSpace(b)
}

// escapeLead protects a body that begins with '[' when there is no bracket
// in front of it, so a reader cannot mistake it for metadata.
func escapeLead(m store.Meta, body string) string {
	if strings.HasPrefix(body, "[") && bracket(m, hiddenKeys) == "" {
		return `\` + body
	}
	return body
}

// indentItem keeps a multi-line body inside one markdown list item.
func indentItem(body string) string {
	lines := strings.Split(body, "\n")
	for i := 1; i < len(lines); i++ {
		lines[i] = "  " + lines[i]
	}
	return strings.Join(lines, "\n")
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func excerptOf(body string, maxRunes int) (string, bool) {
	body = oneLine(body)
	if maxRunes <= 0 || utf8.RuneCountInString(body) <= maxRunes {
		return body, false
	}
	n := 0
	for i := range body {
		if n == maxRunes {
			return body[:i], true
		}
		n++
	}
	return body, false
}
