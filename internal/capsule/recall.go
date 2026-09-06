package capsule

import (
	"errors"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/scottmeyer/nine-tails/internal/store"
)

const recallLimit, recallExcerptRunes = 3, 360

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
	if len(terms) == 0 {
		return nil, views, nil
	}
	recs, err := store.ListRecords(q, store.Filter{Agent: req.Agent, Lane: "recall"})
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
		if score == 0 {
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
			line += "… (truncated)"
		}
		line += " — inspect with `" + inspect + "`\n"
		views[r.ID] = RecallView{ID: r.ID, Ref: ref, CreatedAt: r.CreatedAt, OriginContext: r.OriginContext, OriginContextRef: originRef, Kind: r.Kind, Excerpt: excerpt, Truncated: truncated, Meta: r.Meta, Inspect: inspect}
		out = append(out, candidate{rec: r, score: score, text: line, ordinal: i})
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
	if len(out) > recallLimit {
		out = out[:recallLimit]
	}
	return out, views, nil
}

// Center long evidence near its first matching word so a hit near the end
// does not deliver an unrelated opening. Truncated means either end was cut.
func recallExcerpt(body string, terms map[string]bool) (string, bool) {
	runes := []rune(strings.Join(strings.Fields(body), " "))
	if len(runes) <= recallExcerptRunes {
		return string(runes), false
	}
	start := 0
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
			start = max(0, i-80)
			break
		}
		i = end
	}
	start = min(start, len(runes)-recallExcerptRunes)
	return string(runes[start : start+recallExcerptRunes]), true
}
