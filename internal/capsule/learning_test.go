package capsule

import (
	"database/sql"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/scottmeyer/nine-tails/internal/store"
)

func TestRecallIsRelevantBoundedDataAndReceiptMatches(t *testing.T) {
	s := setup(t)
	insert(t, s, store.NewRecord{Agent: "coach", Lane: "definition", Kind: "agent-base", Name: "base", Body: "Coach."})
	guidance := insert(t, s, store.NewRecord{Agent: "coach", Lane: "guidance", Kind: "prefer", Body: strings.Repeat("Keep every explicit correction. ", 200)})
	remember := func(agent, body string, meta store.Meta) *store.Record {
		return insert(t, s, store.NewRecord{Agent: agent, Lane: "recall", Kind: "memory", Body: body, Meta: meta})
	}
	best := remember("coach", strings.Repeat("前置き unrelated introduction. ", 50)+"Intercept passing taught anticipation.", nil)
	scoped := remember("coach", "Passing needs support.", store.Meta{"repo-id": {"soccer"}})
	remember("coach", "Passing an older trial.", nil)
	newest := remember("coach", "Passing a newer trial.", nil)
	remember("coach", "The task was to help create a new project.", nil)
	remember("coach", "Passing intercept wrong project.", store.Meta{"repo-id": {"other"}})
	remember("other", "Passing intercept private other agent.", nil)
	retired := remember("coach", "Passing intercept retired wording.", nil)
	if _, err := s.DB.Exec("UPDATE records SET status = 'disabled' WHERE id = ?", retired.ID); err != nil {
		t.Fatal(err)
	}
	req := Request{Agent: "coach", Task: "Help build a new project about passing and intercept", Meta: store.Meta{"repo-id": {"soccer"}}}
	c, err := Load(s, req)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{best.ID, scoped.ID, newest.ID}
	var got []string
	for _, r := range c.Recall {
		got = append(got, r.ID)
		if utf8.RuneCountInString(r.Excerpt) > 360 || !utf8.ValidString(r.Excerpt) || r.Inspect != "nine-tails inspect "+r.Ref {
			t.Errorf("invalid recall view: %+v", r)
		}
		if strings.Contains(c.Instructions, r.Excerpt) {
			t.Errorf("recall evidence leaked into instructions: %s", r.ID)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("recall order %v, want %v", got, want)
	}
	if !c.Recall[0].Truncated || !strings.Contains(c.Recall[0].Excerpt, "Intercept passing") || !strings.Contains(c.Markdown, "(truncated)") {
		t.Errorf("long recall must deliver relevant excerpt with truncation: %+v", c.Recall[0])
	}
	if !strings.Contains(c.Instructions, guidance.Body) {
		t.Fatal("retrieval must not evict or shorten guidance")
	}
	receipt, err := store.GetContext(s.DB, c.ContextID)
	if err != nil {
		t.Fatal(err)
	}
	var seen []string
	for _, r := range receipt.Rendered {
		if r.Section == "recall" {
			seen = append(seen, r.RecordID)
		}
	}
	if !reflect.DeepEqual(seen, got) || !reflect.DeepEqual(receipt.RenderedIDs(), c.RenderedIDs) {
		t.Fatalf("receipt does not account for exact rendered ids: %+v", receipt)
	}
	again, err := Load(s, req)
	if err != nil || !reflect.DeepEqual(c.Recall, again.Recall) {
		t.Fatalf("recall ranking must be deterministic: %+v (%v)", again, err)
	}
	b, err := json.Marshal(c)
	if err != nil || !strings.Contains(string(b), `"recall":[{"id":"`+best.ID) {
		t.Fatalf("structured recall missing: %s (%v)", b, err)
	}
}

func TestRecallQueryOverrideAndMeaningfulTerms(t *testing.T) {
	s := setup(t)
	insert(t, s, store.NewRecord{Agent: "a", Lane: "definition", Kind: "agent-base", Name: "base", Body: "Base."})
	passing := insert(t, s, store.NewRecord{Agent: "a", Lane: "recall", Kind: "memory", Body: "Passing into space helped."})
	writing := insert(t, s, store.NewRecord{Agent: "a", Lane: "recall", Kind: "memory", Body: "Writing short paragraphs worked."})
	for _, tc := range []struct {
		name, task string
		query      *string
		want       string
	}{
		{name: "task", task: "passing", want: passing.ID},
		{name: "override", task: "passing", query: strptr("writing"), want: writing.ID},
		{name: "disabled", task: "passing", query: strptr("")},
		{name: "stopwords", task: "help me create the new project"},
		{name: "no substring", task: "pass"},
		{name: "empty"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := Load(s, Request{Agent: "a", Task: tc.task, Query: tc.query})
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == "" {
				if len(c.Recall) != 0 || strings.Contains(c.Markdown, "## Relevant recall") {
					t.Fatalf("unexpected recall: %+v", c.Recall)
				}
			} else if len(c.Recall) != 1 || c.Recall[0].ID != tc.want {
				t.Fatalf("recall = %+v, want %s", c.Recall, tc.want)
			}
		})
	}
}

func strptr(s string) *string { return &s }

func TestGuidanceFallsBackWhenRepresentationCannotRender(t *testing.T) {
	for _, reason := range []string{"wrong-scope", "corrupt", "partial", "healthy"} {
		t.Run(reason, func(t *testing.T) {
			s := setup(t)
			insert(t, s, store.NewRecord{Agent: "a", Lane: "definition", Kind: "agent-base", Name: "base", Body: "Base."})
			source := insert(t, s, store.NewRecord{Agent: "a", Lane: "guidance", Kind: "prefer", Body: "Keep the original correction.", Meta: store.Meta{"repo-id": {"soccer"}}})
			var itemIDs []string
			if err := s.Tx(func(tx *sql.Tx) error {
				meta := store.Meta{"repo-id": {"soccer"}}
				if reason == "wrong-scope" || reason == "partial" {
					meta = store.Meta{"repo-id": {"other"}}
				}
				items := []store.NewItem{{Key: "one", Body: "Condensed correction.", Meta: meta, Sources: []string{source.ID}}}
				if reason == "partial" {
					items = append(items, store.NewItem{Key: "two", Body: "Other half.", Sources: []string{source.ID}})
				}
				_, recs, err := store.InstallGeneration(tx, "a", "", items, []store.BriefInput{{EntryID: source.ID, Disposition: "represented"}})
				if err != nil {
					return err
				}
				for _, r := range recs {
					itemIDs = append(itemIDs, r.ID)
				}
				if reason == "corrupt" {
					return store.SetBody(tx, recs[0].ID, string([]byte{0xff}))
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			c, err := Load(s, Request{Agent: "a", Meta: store.Meta{"repo-id": {"soccer"}}})
			if err != nil {
				t.Fatal(err)
			}
			wantSource := reason != "healthy"
			if strings.Contains(c.Instructions, source.Body) != wantSource {
				t.Fatalf("source visibility wrong: %s", c.Instructions)
			}
			seen := false
			for _, id := range c.RenderedIDs {
				seen = seen || id == source.ID
			}
			if seen != wantSource {
				t.Fatalf("receipt lost fallback source: %v (items %v)", c.RenderedIDs, itemIDs)
			}
		})
	}
}

func TestCompilerSupersessionCannotHideInapplicableSuccessorOrCycle(t *testing.T) {
	for _, mode := range []string{"visible", "scoped-out", "cycle"} {
		t.Run(mode, func(t *testing.T) {
			s := setup(t)
			insert(t, s, store.NewRecord{Agent: "a", Lane: "definition", Kind: "agent-base", Name: "base", Body: "Base."})
			old := insert(t, s, store.NewRecord{Agent: "a", Lane: "guidance", Kind: "note", Body: "Original."})
			meta := store.Meta{}
			if mode == "scoped-out" {
				meta["repo-id"] = []string{"other"}
			}
			newer := insert(t, s, store.NewRecord{Agent: "a", Lane: "guidance", Kind: "note", Body: "Successor.", Meta: meta})
			inputs := []store.BriefInput{{EntryID: old.ID, Disposition: "superseded-by", Successor: newer.ID}}
			if mode == "cycle" {
				inputs = append(inputs, store.BriefInput{EntryID: newer.ID, Disposition: "superseded-by", Successor: old.ID})
			}
			if err := s.Tx(func(tx *sql.Tx) error {
				_, _, err := store.InstallGeneration(tx, "a", "", nil, inputs)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			c, err := Load(s, Request{Agent: "a", Meta: store.Meta{"repo-id": {"soccer"}}})
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(c.Instructions, old.Body) != (mode != "visible") {
				t.Fatalf("unsafe supersession in mode %s: %s", mode, c.Instructions)
			}
		})
	}
}

func TestRecallCarriesHistoricalDateAndItsOwnOrigin(t *testing.T) {
	s := setup(t)
	insert(t, s, store.NewRecord{Agent: "coach", Lane: "definition", Kind: "agent-base", Name: "base", Body: "Coach."})
	earlier, err := Load(s, Request{Agent: "coach", Task: "Observe passing"})
	if err != nil {
		t.Fatal(err)
	}
	memory := insert(t, s, store.NewRecord{Agent: "coach", Lane: "recall", Kind: "memory", Body: "Passing routes were unclear in the early interface.", OriginContext: earlier.ContextID})
	c, err := Load(s, Request{Agent: "coach", Task: "Revise passing"})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Recall) != 1 {
		t.Fatal(c.Recall)
	}
	r := c.Recall[0]
	ref, err := store.Reference(s.DB, memory.ID)
	if err != nil {
		t.Fatal(err)
	}
	if r.Ref != ref || r.CreatedAt != memory.CreatedAt || r.OriginContext != earlier.ContextID || r.OriginContextRef != earlier.ContextRef || r.OriginContext == c.ContextID {
		t.Fatalf("lost historical provenance: %+v", r)
	}
	date, _, _ := strings.Cut(memory.CreatedAt, "T")
	if !strings.Contains(c.Markdown, "recorded "+date) || !strings.Contains(c.Markdown, "recall="+ref) || strings.Contains(c.Instructions, memory.Body) {
		t.Fatal(c.Markdown)
	}
	inspected, err := store.ResolveReference(s.DB, r.Ref)
	if err != nil || inspected != memory.ID {
		t.Fatal(inspected, err)
	}
}
