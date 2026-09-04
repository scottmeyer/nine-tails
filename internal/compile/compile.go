package compile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"

	"github.com/scottmeyer/nine-tails/internal/cli"
	"github.com/scottmeyer/nine-tails/internal/store"
)

// ---- compile-input (DESIGN §10) ----

// Input is the compile-input document handed to the compiler.
type Input struct {
	Agent            string          `json:"agent" yaml:"agent"`
	Instructions     string          `json:"instructions" yaml:"instructions"`
	ExpectGeneration string          `json:"expect_generation" yaml:"expect_generation"` // gen_N or "none"
	ExpectBase       string          `json:"expect_base" yaml:"expect_base"`
	Base             BaseView        `json:"base" yaml:"base"`
	ActiveGeneration *GenerationView `json:"active_generation" yaml:"active_generation"` // null when none
	InputEntries     []string        `json:"input_entries" yaml:"input_entries"`         // exactly the ids in Entries
	Entries          []Entry         `json:"entries" yaml:"entries"`                     // RecentGuidance, oldest first
}

// BaseView is the active base as the compiler sees it.
type BaseView struct {
	ID   string `json:"id" yaml:"id"`
	Body string `json:"body" yaml:"body"`
}

// GenerationView is the active generation as the compiler sees it.
type GenerationView struct {
	ID    string     `json:"id" yaml:"id"`
	Items []ItemView `json:"items" yaml:"items"`
}

// ItemView is one active brief item with the guidance entries it stands for,
// so a compiler can re-derive the item's scope from evidence instead of
// inheriting whatever an earlier compile wrote.
type ItemView struct {
	ID      string       `json:"id" yaml:"id"`
	Key     string       `json:"key" yaml:"key"`
	Body    string       `json:"body" yaml:"body"`
	Meta    store.Meta   `json:"meta" yaml:"meta"`
	Sources []SourceView `json:"sources" yaml:"sources"`
}

// SourceView is one guidance entry an active item represents: its id and the
// metadata that entry carried explicitly.
type SourceView struct {
	ID   string     `json:"id" yaml:"id"`
	Meta store.Meta `json:"meta" yaml:"meta"`
}

// Entry is one recent guidance record (the ordinary envelope) plus what its
// origin context knew, so the compiler can judge coverage. Both origin_context_*
// fields are present exactly when the origin receipt exists: a context that
// carried no metadata shows {} and one that rendered nothing shows [], so a
// compiler never has to cross-check origin_context to tell "no origin" from
// "origin knew nothing".
type Entry struct {
	*store.Record         `yaml:",inline"`
	OriginContextMetadata *store.Meta `json:"origin_context_metadata,omitempty" yaml:"origin_context_metadata,omitempty"`
	OriginContextRendered *[]string   `json:"origin_context_rendered,omitempty" yaml:"origin_context_rendered,omitempty"`
}

type entryEnvelope struct {
	store.RecordEnvelope  `yaml:",inline"`
	OriginContextMetadata *store.Meta `json:"origin_context_metadata,omitempty" yaml:"origin_context_metadata,omitempty"`
	OriginContextRendered *[]string   `json:"origin_context_rendered,omitempty" yaml:"origin_context_rendered,omitempty"`
}

func (e Entry) envelope() entryEnvelope {
	var record store.RecordEnvelope
	if e.Record != nil {
		record = e.Record.Envelope()
	}
	return entryEnvelope{
		RecordEnvelope:        record,
		OriginContextMetadata: e.OriginContextMetadata,
		OriginContextRendered: e.OriginContextRendered,
	}
}

// MarshalJSON prevents the embedded Record marshaler from swallowing the
// origin receipt fields while retaining the stable record envelope.
func (e Entry) MarshalJSON() ([]byte, error) {
	return json.Marshal(e.envelope())
}

func (e Entry) MarshalYAML() (any, error) {
	return e.envelope(), nil
}

// Instructions returns the active base of the `brief-compiler` agent when
// there is one, else the built-in default.
func Instructions(q store.Querier) string {
	if r, err := store.ActiveNamed(q, "brief-compiler", "definition", "agent-base", "base"); err == nil {
		return r.Body
	}
	return DefaultInstructions
}

// BuildInput assembles the compile-input document for agent.
func BuildInput(q store.Querier, agent string) (*Input, error) {
	ok, err := store.AgentExists(q, agent)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, cli.NotFound("no records for agent %q", agent)
	}
	base, err := store.ActiveNamed(q, agent, "definition", "agent-base", "base")
	if errors.Is(err, store.ErrNotFound) {
		return nil, cli.NotFound("agent %s has no active base (create one with `nine-tails base %s ...`)", agent, agent)
	}
	if err != nil {
		return nil, err
	}
	in := &Input{
		Agent: agent, Instructions: Instructions(q),
		ExpectGeneration: "none", ExpectBase: base.ID,
		Base:         BaseView{ID: base.ID, Body: base.Body},
		InputEntries: []string{}, Entries: []Entry{},
	}
	gen, err := store.ActiveGeneration(q, agent)
	switch {
	case err == nil:
		items, err := store.GenerationItems(q, gen.ID)
		if err != nil {
			return nil, err
		}
		gv := &GenerationView{ID: gen.ID, Items: []ItemView{}}
		for _, it := range items {
			srcIDs, err := store.ItemSources(q, gen.ID, it.ID)
			if err != nil {
				return nil, err
			}
			sources := []SourceView{}
			for _, id := range srcIDs {
				src, err := store.GetRecord(q, id)
				if err != nil {
					return nil, err
				}
				sources = append(sources, SourceView{ID: id, Meta: src.Meta})
			}
			gv.Items = append(gv.Items, ItemView{ID: it.ID, Key: it.Name, Body: it.Body, Meta: it.Meta, Sources: sources})
		}
		in.ActiveGeneration = gv
		in.ExpectGeneration = gen.ID
	case !errors.Is(err, store.ErrNotFound):
		return nil, err
	}
	recs, err := store.RecentGuidance(q, agent)
	if err != nil {
		return nil, err
	}
	for _, r := range recs {
		e := Entry{Record: r}
		if r.OriginContext != "" {
			c, err := store.GetContext(q, r.OriginContext)
			switch {
			case err == nil:
				meta := c.Meta
				if meta == nil {
					meta = store.Meta{}
				}
				rendered := c.RenderedIDs()
				if rendered == nil {
					rendered = []string{}
				}
				e.OriginContextMetadata = &meta
				e.OriginContextRendered = &rendered
			case !errors.Is(err, store.ErrNotFound):
				return nil, err
			}
		}
		in.InputEntries = append(in.InputEntries, r.ID)
		in.Entries = append(in.Entries, e)
	}
	return in, nil
}

// ---- compiler output ----

// Output is the parsed compiler response. Problems holds the shape problems
// Parse found (a list where a mapping was expected, an unusable metadata
// key, ...); Validate reports them together with the semantic problems so a
// compiler sees the complete list at once.
type Output struct {
	InputEntries    []string
	HasInputEntries bool // the key was present (even if empty)
	Items           []OutItem
	Entries         []OutEntry
	Problems        []string
}

// OutItem is one emitted brief item.
type OutItem struct {
	Key  string
	Body string
	Meta store.Meta
}

// OutEntry is the accounting row for one input entry.
type OutEntry struct {
	ID           string
	Disposition  string
	Items        []string // item keys; required iff represented
	HasItems     bool     // distinguishes an omitted field from items: [] / null
	Successor    string   // required iff superseded-by
	HasSuccessor bool     // distinguishes an omitted field from successor: "" / null
	Refinement   bool
	Equivalents  []string
}

// invalid builds the exit-2 error: a summary line followed by one indented
// line per problem.
func invalid(problems []string) error {
	return cli.Invalid("compiler output is invalid\n  %s", strings.Join(problems, "\n  "))
}

// Parse decodes the compiler output from YAML or JSON (JSON is YAML).
// Structural keys are accepted in snake_case or kebab-case; metadata keys are
// taken verbatim. Scalars keep their source text. Only a document that cannot
// be read at all (not YAML, empty, not a mapping) is an error; every other
// shape problem is collected into Output.Problems, in document order, so
// Validate can report it alongside the semantic problems.
func Parse(b []byte) (*Output, error) {
	dec := yaml.NewDecoder(bytes.NewReader(b))
	var root yaml.Node
	if err := dec.Decode(&root); errors.Is(err, io.EOF) {
		return nil, invalid([]string{"the document is empty"})
	} else if err != nil {
		return nil, invalid([]string{fmt.Sprintf("not valid YAML or JSON: %v", err)})
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); err == nil {
		return nil, invalid([]string{"compiler output must contain exactly one YAML or JSON document"})
	} else if !errors.Is(err, io.EOF) {
		return nil, invalid([]string{fmt.Sprintf("not valid YAML or JSON: %v", err)})
	}
	v, err := fromYAML(&root)
	if err != nil {
		return nil, invalid([]string{err.Error()})
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, invalid([]string{"the document must be a mapping with input_entries, items and entries"})
	}
	d := &decoder{}
	out := &Output{}
	top := d.normKeys(m, "the document")
	if ie, ok := top["input_entries"]; ok {
		out.HasInputEntries = true
		out.InputEntries = d.stringList(ie, "input_entries")
	}
	for i, raw := range d.list(top["items"], "items") {
		im, ok := raw.(map[string]any)
		if !ok {
			d.problem("item #%d must be a mapping with key, body and meta", i+1)
			continue
		}
		im = d.normKeys(im, fmt.Sprintf("item #%d", i+1))
		it := OutItem{Key: d.str(im["key"], fmt.Sprintf("item #%d key", i+1))}
		label := it.Key
		if label == "" {
			label = fmt.Sprintf("#%d", i+1)
		}
		it.Body = store.NormalizeBody(d.str(im["body"], "item "+label+" body"))
		it.Meta = d.meta(im["meta"], "item "+label)
		out.Items = append(out.Items, it)
	}
	for i, raw := range d.list(top["entries"], "entries") {
		em, ok := raw.(map[string]any)
		if !ok {
			d.problem("entry #%d must be a mapping with id and disposition", i+1)
			continue
		}
		em = d.normKeys(em, fmt.Sprintf("entry #%d", i+1))
		e := OutEntry{ID: d.str(em["id"], fmt.Sprintf("entry #%d id", i+1))}
		label := e.ID
		if label == "" {
			label = fmt.Sprintf("#%d", i+1)
		}
		e.Disposition = d.str(em["disposition"], "entry "+label+" disposition")
		if raw, ok := em["items"]; ok {
			e.HasItems = true
			e.Items = d.stringList(raw, "entry "+label+" items")
		}
		if raw, ok := em["successor"]; ok {
			e.HasSuccessor = true
			e.Successor = d.str(raw, "entry "+label+" successor")
		}
		e.Equivalents = d.stringList(em["equivalent_records"], "entry "+label+" equivalent_records")
		if r, ok := em["refinement"]; ok && r != nil {
			switch strings.ToLower(d.str(r, "entry "+label+" refinement")) {
			case "true":
				e.Refinement = true
			case "false", "":
			default:
				d.problem("entry %s refinement must be true or false", label)
			}
		}
		out.Entries = append(out.Entries, e)
	}
	out.Problems = d.problems
	return out, nil
}

// fromYAML converts a node tree into map[string]any / []any / string / nil.
// Scalars keep their source text so numbers, booleans and timestamps in
// metadata are preserved exactly as the compiler wrote them.
func fromYAML(n *yaml.Node) (any, error) {
	switch n.Kind {
	case 0:
		return nil, errors.New("the document is empty")
	case yaml.DocumentNode:
		if len(n.Content) == 0 {
			return nil, errors.New("the document is empty")
		}
		return fromYAML(n.Content[0])
	case yaml.AliasNode:
		return fromYAML(n.Alias)
	case yaml.ScalarNode:
		if n.Tag == "!!null" {
			return nil, nil
		}
		return n.Value, nil
	case yaml.SequenceNode:
		out := make([]any, 0, len(n.Content))
		for _, c := range n.Content {
			v, err := fromYAML(c)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	case yaml.MappingNode:
		out := make(map[string]any, len(n.Content)/2)
		seen := make(map[string]int, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i]
			line := k.Line
			if k.Kind == yaml.AliasNode {
				k = k.Alias
			}
			if k.Kind != yaml.ScalarNode {
				return nil, fmt.Errorf("line %d: mapping keys must be scalars", k.Line)
			}
			if firstLine, duplicate := seen[k.Value]; duplicate {
				return nil, fmt.Errorf("line %d: mapping key %q appears more than once (first defined on line %d)", line, k.Value, firstLine)
			}
			seen[k.Value] = line
			v, err := fromYAML(n.Content[i+1])
			if err != nil {
				return nil, err
			}
			out[k.Value] = v
		}
		return out, nil
	}
	return nil, fmt.Errorf("line %d: unsupported node", n.Line)
}

// sortedKeys returns the keys of m in sorted order, so every walk over a
// mapping (and every problem it reports) is deterministic.
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

type decoder struct{ problems []string }

func (d *decoder) problem(format string, args ...any) {
	d.problems = append(d.problems, fmt.Sprintf(format, args...))
}

// normKeys returns m with structural keys normalized from kebab-case to
// snake_case. It is applied to the document, item and entry mappings only,
// never to metadata, whose keys are the compiler's to choose. Two spellings
// of the same key in one mapping are a problem, not a coin toss.
func (d *decoder) normKeys(m map[string]any, what string) map[string]any {
	out := make(map[string]any, len(m))
	seen := map[string]string{}
	for _, k := range sortedKeys(m) {
		nk := strings.ReplaceAll(strings.TrimSpace(k), "-", "_")
		if prev, dup := seen[nk]; dup {
			d.problem("%s has both %q and %q, which are the same key", what, prev, k)
			continue
		}
		seen[nk] = k
		out[nk] = m[k]
	}
	return out
}

// str reads a scalar; nil is "", anything structured is a problem.
func (d *decoder) str(v any, what string) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	}
	d.problem("%s must be a scalar", what)
	return ""
}

// list reads a sequence; nil is empty, anything else is a problem.
func (d *decoder) list(v any, what string) []any {
	switch x := v.(type) {
	case nil:
		return nil
	case []any:
		return x
	}
	d.problem("%s must be a list", what)
	return nil
}

// stringList reads a sequence of scalars; a bare scalar is a one-item list.
func (d *decoder) stringList(v any, what string) []string {
	switch x := v.(type) {
	case nil:
		return nil
	case string:
		return []string{strings.TrimSpace(x)}
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			s, ok := e.(string)
			if !ok {
				d.problem("%s must be a list of scalars", what)
				return out
			}
			out = append(out, strings.TrimSpace(s))
		}
		return out
	}
	d.problem("%s must be a list of scalars", what)
	return nil
}

// meta reads a metadata mapping whose values are scalars or lists of scalars.
// Keys are visited in sorted order so problems come out the same every run.
func (d *decoder) meta(v any, what string) store.Meta {
	m := store.Meta{}
	switch x := v.(type) {
	case nil:
		return m
	case map[string]any:
		for _, k := range sortedKeys(x) {
			raw := x[k]
			key := strings.TrimSpace(k)
			if key == "" || strings.IndexFunc(key, unicode.IsSpace) >= 0 || strings.ContainsAny(key, "[]=") {
				d.problem("%s metadata key %q may not be empty or contain whitespace, '=', '[' or ']'", what, k)
				continue
			}
			switch val := raw.(type) {
			case nil:
			case string:
				m.Add(key, val)
			case []any:
				for _, e := range val {
					s, ok := e.(string)
					if !ok {
						d.problem("%s metadata %s must be a scalar or a list of scalars", what, key)
						break
					}
					m.Add(key, s)
				}
			default:
				d.problem("%s metadata %s must be a scalar or a list of scalars", what, key)
			}
		}
		return m
	}
	d.problem("%s meta must be a mapping", what)
	return m
}

// CheckEcho records a problem on out when its input_entries is not the
// compile input's list verbatim (spec §12.3: "echoed unchanged"). Only
// `compile` knows the input; `brief put` can check self-consistency alone.
func CheckEcho(out *Output, want []string) {
	if strings.Join(out.InputEntries, "\x00") == strings.Join(want, "\x00") && len(out.InputEntries) == len(want) {
		return
	}
	out.Problems = append(out.Problems, fmt.Sprintf("input_entries must echo the compile input's input_entries [%s] unchanged, got [%s]",
		strings.Join(want, ", "), strings.Join(out.InputEntries, ", ")))
}

// ---- validation, coverage, install (spec §12.4, §12.5) ----

// Plan is validated compiler output ready for store.InstallGeneration.
type Plan struct {
	Agent  string
	Items  []store.NewItem
	Inputs []store.BriefInput // Items hold item keys until installed
}

// Validate checks every rule in DESIGN §10 against the store, collecting all
// problems (after the shape problems Parse already found) into one exit-2
// error, and computes coverage for each entry.
func Validate(q store.Querier, agent string, out *Output) (*Plan, error) {
	problems := append([]string(nil), out.Problems...)
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }

	if !out.HasInputEntries {
		add("input_entries is missing (echo the input's input_entries unchanged)")
	}
	keys := map[string]bool{}
	for i, it := range out.Items {
		label := it.Key
		if label == "" {
			label = fmt.Sprintf("#%d", i+1)
		}
		switch {
		case it.Key == "":
			add("item %s has no key", label)
		case keys[it.Key]:
			add("item key %s is duplicated", it.Key)
		default:
			if err := store.ValidRecordName("brief item", it.Key); err != nil {
				add("%s", strings.TrimPrefix(err.Error(), store.ErrInvalid.Error()+": "))
			}
			keys[it.Key] = true
		}
		if it.Body == "" {
			add("item %s has an empty body", label)
		}
	}

	want := map[string]bool{}
	for _, id := range out.InputEntries {
		if want[id] {
			add("input_entries lists %s more than once", id)
			continue
		}
		want[id] = true
	}
	seen := map[string]bool{}
	var entries []OutEntry
	for i, e := range out.Entries {
		switch {
		case e.ID == "":
			add("entry #%d has no id", i+1)
		case seen[e.ID]:
			add("entry %s appears more than once", e.ID)
		case !want[e.ID]:
			seen[e.ID] = true
			add("entry %s is not in input_entries", e.ID)
		default:
			seen[e.ID] = true
			entries = append(entries, e)
		}
	}
	for _, id := range out.InputEntries {
		if !seen[id] {
			seen[id] = true
			add("entry %s is missing from entries", id)
		}
	}

	activeGuidance := func(id string) (*store.Record, bool, error) {
		rec, err := store.GetRecord(q, id)
		if errors.Is(err, store.ErrNotFound) {
			return nil, false, nil
		}
		if err != nil {
			return nil, false, err
		}
		ok := rec.Agent == agent && rec.Lane == "guidance" && rec.Kind != "brief-item" && rec.Status == "active"
		return rec, ok, nil
	}

	sources := map[string][]string{} // item key → entry ids
	var inputs []store.BriefInput
	for _, e := range entries {
		rec, ok, err := activeGuidance(e.ID)
		if err != nil {
			return nil, err
		}
		if !ok {
			add("entry %s is not an active guidance entry of %s", e.ID, agent)
			continue
		}
		switch e.Disposition {
		case "represented":
			if len(e.Items) == 0 {
				add("entry %s is represented but lists no items", e.ID)
			}
			if e.HasSuccessor {
				add("entry %s is represented but names a successor", e.ID)
			}
		case "deferred":
			if e.HasItems {
				add("entry %s is deferred but lists items", e.ID)
			}
			if e.HasSuccessor {
				add("entry %s is deferred but names a successor", e.ID)
			}
		case "superseded-by":
			if e.HasItems {
				add("entry %s is superseded-by but lists items", e.ID)
			}
			switch {
			case !e.HasSuccessor || e.Successor == "":
				add("entry %s is superseded-by but names no successor", e.ID)
			case e.Successor == e.ID:
				add("entry %s cannot supersede itself", e.ID)
			default:
				_, ok, err := activeGuidance(e.Successor)
				if err != nil {
					return nil, err
				}
				if !ok {
					add("entry %s successor %s is not an active guidance entry of %s", e.ID, e.Successor, agent)
				}
			}
		case "":
			add("entry %s has no disposition (represented|deferred|superseded-by)", e.ID)
		default:
			add("entry %s has unknown disposition %q (represented|deferred|superseded-by)", e.ID, e.Disposition)
		}
		for _, k := range e.Items {
			if !keys[k] {
				add("entry %s references unknown item key %s", e.ID, k)
				continue
			}
			sources[k] = append(sources[k], e.ID)
		}
		for _, eq := range e.Equivalents {
			exists, err := store.RecordExists(q, eq)
			if err != nil {
				return nil, err
			}
			if !exists {
				add("entry %s equivalent record %s does not exist", e.ID, eq)
			}
		}
		cov, err := Coverage(q, rec, e.Refinement, e.Equivalents)
		if err != nil {
			return nil, err
		}
		inputs = append(inputs, store.BriefInput{EntryID: e.ID, Disposition: e.Disposition, Coverage: cov,
			Successor: e.Successor, Items: e.Items, Equivalents: e.Equivalents})
	}
	if len(problems) > 0 {
		return nil, invalid(problems)
	}
	plan := &Plan{Agent: agent, Inputs: inputs}
	for _, it := range out.Items {
		plan.Items = append(plan.Items, store.NewItem{Key: it.Key, Body: it.Body, Meta: it.Meta, Sources: sources[it.Key]})
	}
	return plan, nil
}

// Coverage classifies one entry (spec §12.5, DESIGN §10): the compiler
// proposes equivalents, nine-tails checks whether the origin context rendered
// any of them.
func Coverage(q store.Querier, entry *store.Record, refinement bool, equivalents []string) (string, error) {
	switch {
	case refinement:
		return "refinement", nil
	case len(equivalents) > 0:
		if entry.OriginContext == "" {
			return "unknown", nil
		}
		rendered, err := store.ContextRenderedSet(q, entry.OriginContext)
		if err != nil {
			return "", err
		}
		for _, id := range equivalents {
			if rendered[id] {
				return "covered-rendered", nil
			}
		}
		return "covered-unrendered", nil
	case entry.OriginContext == "":
		return "unknown", nil
	}
	return "novel", nil
}

// expectID normalizes one --expect-* value: surrounding whitespace is
// tolerated, anything that is not an id of the right kind (or "none" where
// allowed) is exit 2 rather than a misleading exit-7 conflict.
func expectID(flag, value, prefix, hint string, allowNone bool) (string, error) {
	v := strings.TrimSpace(value)
	switch {
	case v == "":
		return "", cli.Invalid("%s is required: %s", flag, hint)
	case allowNone && v == "none":
		return v, nil
	case strings.HasPrefix(v, prefix+"_") && store.IsID(v):
		return v, nil
	}
	return "", cli.Invalid("%s must be %s, got %q", flag, hint, value)
}

// CheckExpect verifies the compare-and-swap preconditions: expectGen is the
// active generation id or "none", expectBase the active base id. It returns
// the active generation id ("" when none) for store.InstallGeneration.
func CheckExpect(q store.Querier, agent, expectGen, expectBase string) (string, error) {
	wantGen, err := expectID("--expect-generation", expectGen, "gen",
		"'none' or the active generation id like gen_11 (compile-input shows it as expect_generation)", true)
	if err != nil {
		return "", err
	}
	wantBase, err := expectID("--expect-base", expectBase, "base",
		"the active base id like base_4 (compile-input shows it as expect_base)", false)
	if err != nil {
		return "", err
	}
	active := ""
	gen, err := store.ActiveGeneration(q, agent)
	switch {
	case err == nil:
		active = gen.ID
	case !errors.Is(err, store.ErrNotFound):
		return "", err
	}
	if wantGen != active && !(wantGen == "none" && active == "") {
		have := active
		if have == "" {
			have = "no generation"
		}
		return "", cli.Conflict("expected %s but %s is active", wantGen, have)
	}
	base, err := store.ActiveNamed(q, agent, "definition", "agent-base", "base")
	switch {
	case errors.Is(err, store.ErrNotFound):
		return "", cli.Conflict("expected %s but no base is active", wantBase)
	case err != nil:
		return "", err
	case base.ID != wantBase:
		return "", cli.Conflict("expected %s but %s is active", wantBase, base.ID)
	}
	return active, nil
}

// Result is what brief put and compile print.
type Result struct {
	Generation string             `json:"generation" yaml:"generation"`
	Items      []string           `json:"items" yaml:"items"`
	Warnings   []Warning          `json:"warnings" yaml:"warnings"`
	Inputs     []store.BriefInput `json:"inputs,omitempty" yaml:"inputs,omitempty"`
	DryRun     bool               `json:"dry_run,omitempty" yaml:"dry_run,omitempty"`
}

// Install runs the whole install path inside the caller's transaction:
// compare-and-swap check, validation with coverage, store.InstallGeneration,
// then the condition-loss lint on the new generation. The caller commits, or
// rolls back for a dry run.
func Install(tx store.Querier, agent, expectGen, expectBase string, out *Output) (*Result, error) {
	active, err := CheckExpect(tx, agent, expectGen, expectBase)
	if err != nil {
		return nil, err
	}
	plan, err := Validate(tx, agent, out)
	if err != nil {
		return nil, err
	}
	gen, recs, err := store.InstallGeneration(tx, agent, active, plan.Items, plan.Inputs)
	if err != nil {
		return nil, err
	}
	warnings, err := LintGeneration(tx, gen.ID)
	if err != nil {
		return nil, err
	}
	if warnings == nil {
		warnings = []Warning{}
	}
	res := &Result{Generation: gen.ID, Items: []string{}, Warnings: warnings, Inputs: plan.Inputs}
	byKey := map[string]string{}
	for _, r := range recs {
		res.Items = append(res.Items, r.ID)
		byKey[r.Name] = r.ID
	}
	for i := range res.Inputs {
		for j, k := range res.Inputs[i].Items {
			res.Inputs[i].Items[j] = byKey[k]
		}
	}
	return res, nil
}

// ---- coverage rows for inspect (DESIGN §12) ----

// CoverageRow is one row of `inspect <agent> --coverage C`: the entry, its
// latest accounting, and the item ids and equivalent records that
// classification rests on. Lists are never null.
type CoverageRow struct {
	Entry       *store.Record `json:"entry" yaml:"entry"`
	Disposition string        `json:"disposition" yaml:"disposition"`
	Coverage    string        `json:"coverage" yaml:"coverage"`
	Items       []string      `json:"items" yaml:"items"`
	Equivalents []string      `json:"equivalent_records" yaml:"equivalent_records"`
}

// Coverages are the classifications --coverage accepts.
var Coverages = []string{"novel", "covered-unrendered", "covered-rendered", "refinement", "unknown"}

// CoverageRows returns the entries of agent whose latest brief_inputs row
// (from the newest generation that accounted for them, any status) carries
// coverage want, ordered by entry id. It walks store.GenerationInputs because
// store.LatestCoverage does not carry items or equivalents.
func CoverageRows(q store.Querier, agent, want string) ([]CoverageRow, error) {
	known := false
	for _, c := range Coverages {
		known = known || c == want
	}
	if !known {
		return nil, cli.Invalid("unknown coverage %q (%s)", want, strings.Join(Coverages, "|"))
	}
	gens, err := store.ListGenerations(q, agent)
	if err != nil {
		return nil, err
	}
	latest := map[string]store.BriefInput{}
	for _, g := range gens {
		inputs, err := store.GenerationInputs(q, g.ID)
		if err != nil {
			return nil, err
		}
		for _, bi := range inputs {
			latest[bi.EntryID] = bi // later generations overwrite earlier
		}
	}
	rows := []CoverageRow{}
	for id, bi := range latest {
		if bi.Coverage != want {
			continue
		}
		rec, err := store.GetRecord(q, id)
		if err != nil {
			return nil, err
		}
		row := CoverageRow{Entry: rec, Disposition: bi.Disposition, Coverage: bi.Coverage, Items: bi.Items, Equivalents: bi.Equivalents}
		if row.Items == nil {
			row.Items = []string{}
		}
		if row.Equivalents == nil {
			row.Equivalents = []string{}
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return idNum(rows[i].Entry.ID) < idNum(rows[j].Entry.ID) })
	return rows, nil
}

// idNum is the numeric suffix of an id (rec_41 → 41), for stable ordering.
func idNum(id string) int {
	n, _ := strconv.Atoi(id[strings.LastIndex(id, "_")+1:])
	return n
}
