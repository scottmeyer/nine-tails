package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/scottmeyer/nine-tails/internal/store"
)

func decodeLibrary(t *testing.T, r result) libraryPage {
	t.Helper()
	var page libraryPage
	if err := json.Unmarshal([]byte(r.out), &page); err != nil {
		t.Fatalf("library output: %s: %v", r.out, err)
	}
	return page
}

func TestLibraryPagesAllMemoriesWithoutReloadingPersona(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "a", "Base.")
	ctx := contextID(t, h.ok("load", "a", "--meta", "repo-id=game").out)
	var ids []string
	for i := 0; i < 42; i++ {
		id := h.ok("remember", "a", "--meta", "repo-id=game", fmt.Sprintf("Lesson %02d: %s full evidence is available here.", i, strings.Repeat("space and timing ", 16))).id(t)
		ids = append(ids, id)
	}
	h.ok("remember", "a", "--meta", "repo-id=other", "Other project should not appear.")
	h.ok("note", "a", "Standing instructions are not paged away.")
	first := decodeLibrary(t, h.ok("inspect", "--page", "--context", referenceFor(t, h, ctx)))
	if len(first.Entries) <= 3 || first.Next == nil || first.Context != ctx || first.Order != "newest-first" {
		t.Fatalf("expected a useful first page and continuation: %+v", first)
	}
	var seen []string
	page := first
	for turns := 0; ; turns++ {
		if turns > len(ids) {
			t.Fatal("cursor did not advance")
		}
		for _, entry := range page.Entries {
			if entry.Ref == "" || entry.Inspect != "nine-tails inspect "+entry.Ref || !entry.Truncated || len([]rune(entry.Excerpt)) > 160 {
				t.Fatalf("unbounded or uninspectable preview: %+v", entry)
			}
			seen = append(seen, entry.ID)
		}
		if page.Next == nil {
			break
		}
		if page.Next.After != page.Entries[len(page.Entries)-1].Ref {
			t.Fatal("continuation must follow the last returned entry")
		}
		page = decodeLibrary(t, h.ok("inspect", "a", "--page", "--context", ctx, "--after", page.Next.After))
	}
	if len(seen) != len(ids) {
		t.Fatalf("lost or duplicated entries: got %d want %d", len(seen), len(ids))
	}
	for i, id := range seen {
		if id != ids[len(ids)-1-i] {
			t.Fatalf("wrong chronology at %d: %s", i, id)
		}
	}
	st, err := store.Open(h.home)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var receipts int
	if err := st.DB.QueryRow("SELECT COUNT(*) FROM contexts").Scan(&receipts); err != nil || receipts != 1 {
		t.Fatalf("library created persona receipts: %d %v", receipts, err)
	}
}

func TestLibraryLiveCursorSurvivesRetirementAndFiltersBeforePaging(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "a", "Base.")
	ctx := contextID(t, h.ok("load", "a", "--meta", "repo-id=game").out)
	query := "ÉCOLE's $HOME `echo literal`"
	var ids []string
	for i := 0; i < 24; i++ {
		ids = append(ids, h.ok("remember", "a", "--meta", "repo-id=game", query+" "+strings.Repeat("an observation ", 20)).id(t))
	}
	for i := 0; i < 25; i++ {
		h.ok("remember", "a", "--meta", "repo-id=other", query+" in the wrong project")
		h.ok("remember", "a", "unrelated observation")
	}
	first := decodeLibrary(t, h.ok("inspect", "--page", "--context", ctx, "--query", strings.ToLower(query)))
	if first.Next == nil || len(first.Entries) == 0 {
		t.Fatal("query/scope matches should fill a page")
	}
	if !strings.Contains(first.Next.Inspect, "--query "+quoteLibraryArgument(strings.ToLower(query))) {
		t.Fatal("continuation lost literal query or safe quoting")
	}
	h.ok("disable", first.Next.After, "--context", ctx, "--reason", "User retired this exact entry.")
	newest := h.ok("remember", "a", "--meta", "repo-id=game", query+" newly learned").id(t)
	second := decodeLibrary(t, h.ok("inspect", "--page", "--context", ctx, "--query", strings.ToLower(query), "--after", first.Next.After))
	if len(second.Entries) == 0 || second.Entries[0].ID != ids[len(ids)-len(first.Entries)-1] {
		t.Fatalf("cursor skipped the first omitted entry: %+v", second)
	}
	for _, entry := range second.Entries {
		if entry.ID == newest {
			t.Fatal("new entries before cursor require restart")
		}
	}
}

func TestLibraryRejectsAmbiguousInputs(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "a", "Base.")
	h.ok("base", "b", "Base.")
	ctx := contextID(t, h.ok("load", "a").out)
	other := h.ok("remember", "b", "Other owner.").id(t)
	for _, args := range [][]string{
		{"inspect"},
		{"inspect", "--page"},
		{"inspect", "b", "--page", "--context", ctx},
		{"inspect", "--page", "--context", ctx, "--after", other},
		{"inspect", "a", "--page", "--lane", "guidance"},
		{"inspect", "a", "--page", "--all"},
		{"inspect", "a", "--page", "--include", ""},
		{"inspect", "a", "--after", other},
		{"inspect", "a", "--context", ctx},
		{"inspect", "a", "--page", "--context", ""},
		{"inspect", "a", "--page", "--context", "a"},
		{"inspect", "a", "--page", "--context", other},
		{"inspect", "a", "--page", "--context", referenceFor(t, h, other)},
	} {
		if r := h.run(args...); r.code != 2 {
			t.Fatalf("expected invalid page input for %v: %+v", args, r)
		}
	}
	if r := h.run("inspect", "a", "--page", "--after", "rec_999"); r.code != 3 {
		t.Fatalf("unknown cursor should be not found: %+v", r)
	}
	if r := h.run("inspect", "a", "--page", "--context", "ctx_999"); r.code != 3 {
		t.Fatalf("unknown receipt should be not found: %+v", r)
	}
	if page := decodeLibrary(t, h.ok("inspect", "--page", "--context", ctx)); len(page.Entries) != 0 || page.Next != nil {
		t.Fatalf("empty library: %+v", page)
	}
}
