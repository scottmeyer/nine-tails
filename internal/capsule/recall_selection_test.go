package capsule

import (
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/scottmeyer/nine-tails/internal/store"
)

func TestExplicitRecallUsesModelSelectionAndTruthfulReceipt(t *testing.T) {
	s := setup(t)
	insert(t, s, store.NewRecord{Agent: "a", Lane: "definition", Kind: "agent-base", Name: "base", Body: "Base."})
	guidance := insert(t, s, store.NewRecord{Agent: "a", Lane: "guidance", Kind: "note", Body: strings.Repeat("Keep explicit corrections. ", 100)})
	lexical := insert(t, s, store.NewRecord{Agent: "a", Lane: "recall", Kind: "memory", Body: "An old semantic-search tool needed input examples."})
	first := insert(t, s, store.NewRecord{Agent: "a", Lane: "recall", Kind: "memory", Body: "Project source relationships were a proposal, not yet implemented."})
	second := insert(t, s, store.NewRecord{Agent: "a", Lane: "recall", Kind: "incident", Body: "Corrections should preserve their applicability scope."})
	firstRef, _ := store.Reference(s.DB, first.ID)
	secondRef, _ := store.Reference(s.DB, second.ID)
	selected, err := Load(s, Request{Agent: "a", Task: "semantic learning", Recall: []string{second.ID, firstRef, secondRef, first.ID}})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, memory := range selected.Recall {
		got = append(got, memory.ID)
	}
	if !reflect.DeepEqual(got, []string{second.ID, first.ID}) || strings.Contains(selected.Markdown, lexical.Body) {
		t.Fatalf("explicit choices were reordered, duplicated or padded: %+v", selected.Recall)
	}
	if !strings.Contains(selected.Instructions, guidance.Body) || strings.Contains(selected.Instructions, first.Body) {
		t.Fatal("recall selection changed guidance or data boundaries")
	}
	receipt, err := store.GetContext(s.DB, selected.ContextID)
	if err != nil {
		t.Fatal(err)
	}
	var recorded []string
	for _, entry := range receipt.Rendered {
		if entry.Section == "recall" {
			recorded = append(recorded, entry.RecordID)
		}
	}
	if !reflect.DeepEqual(recorded, got) {
		t.Fatalf("receipt differs from delivered choices: %v != %v", recorded, got)
	}
	for _, tc := range []struct {
		refs []string
		want int
	}{{nil, 1}, {[]string{}, 0}} {
		c, err := Load(s, Request{Agent: "a", Task: "semantic learning", Recall: tc.refs})
		if err != nil || len(c.Recall) != tc.want {
			t.Fatalf("omitted versus empty selection lost: %+v, %v", c, err)
		}
	}
}

func TestExplicitRecallRejectsInvalidSelectionAtomically(t *testing.T) {
	s := setup(t)
	insert(t, s, store.NewRecord{Agent: "a", Lane: "definition", Kind: "agent-base", Name: "base", Body: "Base."})
	valid := insert(t, s, store.NewRecord{Agent: "a", Lane: "recall", Kind: "memory", Body: "Relevant."})
	other := insert(t, s, store.NewRecord{Agent: "b", Lane: "recall", Kind: "memory", Body: "Other agent."})
	guidance := insert(t, s, store.NewRecord{Agent: "a", Lane: "guidance", Kind: "prefer", Body: "Instruction."})
	scoped := insert(t, s, store.NewRecord{Agent: "a", Lane: "recall", Kind: "memory", Body: "Other project.", Meta: store.Meta{"repo-id": {"other"}}})
	retired := insert(t, s, store.NewRecord{Agent: "a", Lane: "recall", Kind: "memory", Body: "Retired."})
	replaced := insert(t, s, store.NewRecord{Agent: "a", Lane: "recall", Kind: "memory", Body: "Historical wording."})
	if err := s.Tx(func(tx *sql.Tx) error {
		_, err := store.ReplaceRecord(tx, replaced.ID, store.NewRecord{Agent: "a", Lane: "recall", Kind: "memory", Body: "Current wording."})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	corrupt := insert(t, s, store.NewRecord{Agent: "a", Lane: "recall", Kind: "memory", Body: "Corrupt."})
	if _, err := s.DB.Exec("UPDATE records SET status='disabled' WHERE id=?", retired.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.SetBody(s.DB, corrupt.ID, string([]byte{0xff})); err != nil {
		t.Fatal(err)
	}
	parent, err := Load(s, Request{Agent: "a", Meta: store.Meta{"repo-id": {"current"}}})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		ref string
		err error
	}{{other.ID, store.ErrInvalid}, {guidance.ID, store.ErrInvalid}, {scoped.ID, store.ErrInvalid}, {retired.ID, store.ErrConflict}, {replaced.ID, store.ErrConflict}, {corrupt.ID, store.ErrInvalid}, {parent.ContextRef, store.ErrInvalid}, {"@01", store.ErrInvalid}, {"not-an-id", store.ErrInvalid}, {"@999999", store.ErrNotFound}, {"rec_MISSING", store.ErrNotFound}}
	for _, tc := range cases {
		t.Run(tc.ref, func(t *testing.T) {
			var before, after, refsBefore, refsAfter int
			if err := s.DB.QueryRow("SELECT COUNT(*) FROM contexts").Scan(&before); err != nil {
				t.Fatal(err)
			}
			if err := s.DB.QueryRow("SELECT COUNT(*) FROM reference_aliases").Scan(&refsBefore); err != nil {
				t.Fatal(err)
			}
			_, err := Load(s, Request{Agent: "a", Parent: parent.ContextID, Recall: []string{valid.ID, tc.ref}})
			if !errors.Is(err, tc.err) {
				t.Fatalf("selection error = %v, want %v", err, tc.err)
			}
			_ = s.DB.QueryRow("SELECT COUNT(*) FROM contexts").Scan(&after)
			_ = s.DB.QueryRow("SELECT COUNT(*) FROM reference_aliases").Scan(&refsAfter)
			if before != after || refsBefore != refsAfter {
				t.Fatal("rejected selection left a receipt or reference reservation")
			}
		})
	}
	var four []string
	for i := 0; i < 4; i++ {
		four = append(four, insert(t, s, store.NewRecord{Agent: "a", Lane: "recall", Kind: "memory", Body: "Candidate."}).ID)
	}
	selected, err := Load(s, Request{Agent: "a", Recall: four})
	if err != nil || len(selected.Recall) != 4 || selected.RecallMore != 0 || selected.RecallNext != nil {
		t.Fatalf("explicit selections must have no count cap: %+v, %v", selected, err)
	}
	var before, after int
	_ = s.DB.QueryRow("SELECT COUNT(*) FROM contexts").Scan(&before)
	_, err = Load(s, Request{Agent: "a", Recall: four, MaxBytes: len(selected.Markdown) - 1})
	var tooLarge *TooLargeError
	if !errors.As(err, &tooLarge) {
		t.Fatalf("whole-capsule transport ceiling was bypassed: %v", err)
	}
	_ = s.DB.QueryRow("SELECT COUNT(*) FROM contexts").Scan(&after)
	if before != after {
		t.Fatal("transport ceiling rejection left a receipt")
	}
}

func TestRecallExcerptKeepsWholeWordsAndContext(t *testing.T) {
	body := "Observation 34: declared tools need a useful call recipe. " + strings.Repeat("A different historical detail. ", 20) + "Semantic-search was an executable tool trial; it did not assess how an agent learns. " + strings.Repeat("Further detail. ", 30)
	excerpt, truncated := recallExcerpt(body, recallTerms("semantic"))
	if !truncated || !strings.HasPrefix(excerpt, "Observation 34: … Semantic-search") || !strings.Contains(excerpt, "did not assess how an agent learns.") || utf8.RuneCountInString(excerpt) > recallExcerptRunes {
		t.Fatalf("lost label or the meaning-bearing clause: %q", excerpt)
	}
	lateWord := strings.Repeat("n", 210)
	excerpt, _ = recallExcerpt(strings.Repeat("a", 180)+" "+lateWord+" "+strings.Repeat("tail ", 90), recallTerms(lateWord))
	if !strings.HasPrefix(excerpt, "… "+lateWord) {
		t.Fatalf("secondary crop hid omitted leading content: %q", excerpt)
	}
	for _, tc := range []struct {
		body  string
		query string
	}{
		{strings.Repeat("unmistakableword ", 100), ""},
		{"# Historical trial\n\n" + strings.Repeat("An earlier paragraph. ", 40) + "Unicode café context stays readable. " + strings.Repeat("More explanation. ", 20), "café"},
		{strings.Repeat("wideword ", 100) + "The target phrase is at the end.", "target"},
		{strings.Repeat("z", 1000), ""},
	} {
		excerpt, truncated := recallExcerpt(tc.body, recallTerms(tc.query))
		if !truncated || !utf8.ValidString(excerpt) || utf8.RuneCountInString(excerpt) > recallExcerptRunes {
			t.Fatalf("invalid bounded excerpt: %q", excerpt)
		}
		originalWords := map[string]bool{}
		for _, word := range strings.Fields(tc.body) {
			originalWords[word] = true
		}
		for _, word := range strings.Fields(excerpt) {
			if word != "…" && !originalWords[word] {
				t.Fatalf("excerpt cuts a word or invents text: %q in %q", word, excerpt)
			}
		}
	}
}

func TestAutomaticRecallUsesSoftBytesAndAdvertisesNextMatch(t *testing.T) {
	s := setup(t)
	insert(t, s, store.NewRecord{Agent: "a", Lane: "definition", Kind: "agent-base", Name: "base", Body: "Base."})
	var ids []string
	for i := 0; i < 16; i++ {
		ids = append(ids, insert(t, s, store.NewRecord{Agent: "a", Lane: "recall", Kind: "memory", Body: strings.Repeat("Semantic learning historical evidence. ", 20)}).ID)
	}
	c, err := Load(s, Request{Agent: "a", Task: "semantic learning"})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Recall) <= 3 || len(c.Recall) >= len(ids) || c.RecallMore != len(ids)-len(c.Recall) || c.RecallNext == nil || c.RecallNext.ID != ids[len(ids)-len(c.Recall)-1] {
		t.Fatalf("automatic recall retained a count cap or hid matching evidence: %+v", c)
	}
	if !strings.Contains(c.Markdown, "additional keyword matches; inspect next with `"+c.RecallNext.Inspect+"`") {
		t.Fatalf("more-matches hint is not visible: %s", c.Markdown)
	}
	for _, id := range c.RenderedIDs {
		if id == c.RecallNext.ID {
			t.Fatal("next-match hint was falsely recorded as delivered evidence")
		}
	}
	for i, recall := range c.Recall {
		if recall.ID != ids[len(ids)-i-1] {
			t.Fatal("budgeting skipped priority order")
		}
	}
	// A single highest-ranked line may itself exceed the target because its
	// metadata is large. Keep it, then stop; never select smaller lower matches.
	large := insert(t, s, store.NewRecord{Agent: "a", Lane: "recall", Kind: "memory", Body: "Semantic learning includes source relationships.", Meta: store.Meta{"label": {strings.Repeat("x", recallSoftBytes)}}})
	c, err = Load(s, Request{Agent: "a", Task: "semantic learning source relationships"})
	if err != nil || len(c.Recall) != 1 || c.Recall[0].ID != large.ID || c.RecallMore != len(ids) {
		t.Fatalf("soft target rejected the strongest large match or skipped to smaller ones: %+v, %v", c, err)
	}
}
