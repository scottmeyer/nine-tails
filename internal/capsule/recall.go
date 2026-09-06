package capsule

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/scottmeyer/nine-tails/internal/store"
)

const recallSoftBytes, recallExcerptRunes = 4096, 360

// RecallNextView points to the next automatic match without claiming its body
// was delivered. It is deliberately absent from the receipt's recall entries.
type RecallNextView struct {
	ID      string `json:"id" yaml:"id"`
	Ref     string `json:"ref" yaml:"ref"`
	Inspect string `json:"inspect" yaml:"inspect"`
}

// RecallView is bounded evidence, deliberately separate from instructions.
type RecallView struct {
	ID               string     `json:"id" yaml:"id"`
	Ref              string     `json:"ref" yaml:"ref"`
	CreatedAt        string     `json:"created_at" yaml:"created_at"`
	OriginContext    string     `json:"origin_context,omitempty" yaml:"origin_context,omitempty"`
	OriginContextRef string     `json:"origin_context_ref,omitempty" yaml:"origin_context_ref,omitempty"`
	Kind             string     `json:"kind" yaml:"kind"`
	Excerpt          string     `json:"excerpt" yaml:"excerpt"`
	Truncated        bool       `json:"truncated" yaml:"truncated"`
	Meta             store.Meta `json:"meta" yaml:"meta"`
	Inspect          string     `json:"inspect" yaml:"inspect"`
}

// Whole-word matching avoids substring accidents and repetition cannot inflate
// relevance. Stopwords deliberately include generic task verbs. No model call,
// embeddings, external backend, or corpus-dependent index is needed.
var recallStopwords = func() map[string]bool {
	words := "a an and are as at be been being but by can could did do does doing for from had has have help how i if in into is it its just may me might my of on or our please should so some than that the their them then there these they this those through to use using want was we were what when where which while who will with work would you your build create make task project review improve new"
	out := map[string]bool{}
	for _, word := range strings.Fields(words) {
		out[word] = true
	}
	return out
}()

func recallTerms(text string) map[string]bool {
	out := map[string]bool{}
	for _, word := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if utf8.RuneCountInString(word) >= 2 && !recallStopwords[word] {
			out[word] = true
		}
	}
	return out
}

func recallCandidates(q store.Querier, c *Capsule, req Request, meta store.Meta) ([]candidate, map[string]RecallView, error) {
	query := req.Task
	if req.Query != nil {
		query = *req.Query
	}
	terms := recallTerms(query)
	views := map[string]RecallView{}
	if req.Recall == nil && len(terms) == 0 {
		return nil, views, nil
	}
	recs, err := recallRecords(q, req, meta)
	if err != nil {
		return nil, nil, err
	}
	var out []candidate
	for i, r := range recs {
		if store.Conflicts(r.Meta, meta) || !c.renderableTextBody(r, "recall") {
			continue
		}
		// Match content and explicit labels, but never ambient context keys
		// such as repo-id that would make every local memory a search hit.
		doc := recallTerms(r.Name + " " + r.Meta.First("subject") + " " + r.Meta.First("title") + " " + r.Body)
		score := 0
		for word := range terms {
			if doc[word] {
				score++
			}
		}
		if req.Recall == nil && score == 0 {
			continue
		}
		excerpt, truncated := recallExcerpt(r.Body, terms)
		ref, err := store.Reference(q, r.ID)
		if err != nil {
			return nil, nil, err
		}
		originRef := ""
		if r.OriginContext != "" {
			// An imported record may retain provenance from another store;
			// only advertise a local reference when one actually exists.
			originRef, err = store.Reference(q, r.OriginContext)
			if err != nil && !errors.Is(err, store.ErrNotFound) {
				return nil, nil, err
			}
		}
		inspect := "nine-tails inspect " + ref
		date, _, _ := strings.Cut(r.CreatedAt, "T")
		line := "- " + bracket(r.Meta, hiddenKeys, "recall="+ref) + "(" + r.Kind + ", recorded " + date + ") " + excerpt
		if truncated {
			line += " (truncated)"
		}
		line += " — inspect with `" + inspect + "`\n"
		views[r.ID] = RecallView{ID: r.ID, Ref: ref, CreatedAt: r.CreatedAt, OriginContext: r.OriginContext, OriginContextRef: originRef, Kind: r.Kind, Excerpt: excerpt, Truncated: truncated, Meta: r.Meta, Inspect: inspect}
		out = append(out, candidate{rec: r, score: score, text: line, ordinal: i})
	}
	if req.Recall != nil {
		return out, views, nil
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].score != out[j].score {
			return out[i].score > out[j].score
		}
		a, b := store.Overlap(out[i].rec.Meta, meta), store.Overlap(out[j].rec.Meta, meta)
		if a != b {
			return a > b
		}
		return out[i].ordinal > out[j].ordinal // created_at desc, rowid desc
	})
	used, selected := 0, 0
	for _, cd := range out {
		if selected > 0 && used+len(cd.text) > recallSoftBytes {
			break
		}
		used += len(cd.text)
		selected++
	}
	if selected < len(out) {
		next := views[out[selected].rec.ID]
		c.RecallMore = len(out) - selected
		c.RecallNext = &RecallNextView{ID: next.ID, Ref: next.Ref, Inspect: next.Inspect}
	}
	return out[:selected], views, nil
}

// Explicit selection is a caller's relevance judgment, never permission to
// cross an ownership/scope boundary or silently follow a replacement chain.
// This runs inside Load's transaction, so any failure rolls back its receipt.
func recallRecords(q store.Querier, req Request, meta store.Meta) ([]*store.Record, error) {
	if req.Recall == nil {
		return store.ListRecords(q, store.Filter{Agent: req.Agent, Lane: "recall"})
	}
	seen := map[string]bool{}
	var out []*store.Record
	for _, requested := range req.Recall {
		id := requested
		if strings.HasPrefix(id, "@") {
			var err error
			id, err = store.ResolveReference(q, id)
			if err != nil {
				return nil, err
			}
		}
		if !store.IsID(id) || strings.HasPrefix(id, "ctx_") || strings.HasPrefix(id, "gen_") {
			return nil, fmt.Errorf("%w: recall selection %q must identify a recall record", store.ErrInvalid, requested)
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		r, err := store.GetRecord(q, id)
		if err != nil {
			return nil, err
		}
		if r.Agent != req.Agent || r.Lane != "recall" {
			return nil, fmt.Errorf("%w: recall selection %s belongs to %s/%s, not %s/recall", store.ErrInvalid, requested, r.Agent, r.Lane, req.Agent)
		}
		if r.Status != "active" {
			return nil, fmt.Errorf("%w: recall selection %s is %s; inspect it before choosing a current record", store.ErrConflict, requested, r.Status)
		}
		if store.Conflicts(r.Meta, meta) {
			return nil, fmt.Errorf("%w: recall selection %s conflicts with this load's metadata", store.ErrInvalid, requested)
		}
		if !utf8.ValidString(r.Body) {
			return nil, fmt.Errorf("%w: recall selection %s has an invalid UTF-8 body", store.ErrInvalid, requested)
		}
		out = append(out, r)
	}
	return out, nil
}

// Excerpts retain whole whitespace-delimited words. Prefer the sentence or
// clause around a body match, and keep a short leading heading/label when the
// relevant passage occurs later. Ellipses identify omitted spans within the cap.
func recallExcerpt(body string, terms map[string]bool) (string, bool) {
	runes := []rune(strings.Join(strings.Fields(body), " "))
	if len(runes) <= recallExcerptRunes {
		return string(runes), false
	}
	focus, focusEnd := 0, 0
	for i := 0; i < len(runes); {
		if !unicode.IsLetter(runes[i]) && !unicode.IsDigit(runes[i]) {
			i++
			continue
		}
		end := i + 1
		for end < len(runes) && (unicode.IsLetter(runes[end]) || unicode.IsDigit(runes[end])) {
			end++
		}
		if terms[strings.ToLower(string(runes[i:end]))] {
			focus, focusEnd = i, end
			break
		}
		i = end
	}
	start := 0
	if focusEnd > recallExcerptRunes-80 {
		start = recallWordStart(runes, max(0, focus-80))
		for i := focus - 1; i >= max(0, focus-240); i-- {
			if strings.ContainsRune(".!?;:", runes[i]) && i+1 < len(runes) && unicode.IsSpace(runes[i+1]) {
				start = i + 2
				break
			}
		}
	}
	heading := recallHeading(body)
	prefixFor := func(start int) string {
		if start == 0 {
			return ""
		}
		if heading != "" {
			return heading + " … "
		}
		return "… "
	}
	prefix := prefixFor(start)
	budget := recallExcerptRunes - utf8.RuneCountInString(prefix) - 2
	if focusEnd-start > budget {
		start = recallWordStart(runes, focus)
		prefix = prefixFor(start)
		budget = recallExcerptRunes - utf8.RuneCountInString(prefix) - 2
		if focusEnd-start > budget {
			prefix = ""
			if start > 0 {
				prefix = "… "
			}
			budget = recallExcerptRunes - utf8.RuneCountInString(prefix) - 2
		}
	}
	end := min(len(runes), start+budget)
	if end < len(runes) && !unicode.IsSpace(runes[end]) {
		for end > start && !unicode.IsSpace(runes[end-1]) {
			end--
		}
	}
	// Prefer a complete ending when it keeps most of the available context.
	for i := end - 1; i >= max(focusEnd, start+budget*2/3); i-- {
		if strings.ContainsRune(".!?;", runes[i]) && (i+1 == len(runes) || unicode.IsSpace(runes[i+1])) {
			end = i + 1
			break
		}
	}
	text := strings.TrimSpace(string(runes[start:end]))
	if text == "" {
		// An individual unbroken word may exceed the entire evidence budget;
		// do not manufacture a partial word. The inspect path has the full text.
		return "…", true
	}
	text = prefix + text
	if end < len(runes) {
		text += " …"
	}
	return text, true
}

func recallWordStart(runes []rune, start int) int {
	for start > 0 && !unicode.IsSpace(runes[start-1]) {
		start--
	}
	return start
}

func recallHeading(body string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(body), "\n")
	line = strings.Join(strings.Fields(line), " ")
	if strings.HasPrefix(line, "#") && utf8.RuneCountInString(line) <= 80 {
		return line
	}
	if at := strings.Index(line, ": "); at > 0 && utf8.RuneCountInString(line[:at]) <= 80 && !strings.ContainsAny(line[:at], ".!?;") {
		return line[:at+1]
	}
	return ""
}
