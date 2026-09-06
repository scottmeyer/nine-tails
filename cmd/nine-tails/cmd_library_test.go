package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

func TestLibrarySyntaxPreflightBeforeReferencesAndStore(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"missing target", nil, "inspect requires a target"},
		{"missing page selector", []string{"--page"}, "--page requires an agent or --context"},
		{"extra target", []string{"a", "b", "--page", "--context", "@123"}, "accepts at most 1 arg(s)"},
		{"record target", []string{"rec_123", "--page"}, "--page wants an agent or --context, not a record ID"},
		{"context target", []string{"ctx_123", "--page"}, "--page wants an agent or --context, not a record ID"},
		{"reference target", []string{"@123", "--page"}, "--page wants an agent or --context, not a record ID"},
		{"invalid agent", []string{"bad name", "--page"}, "agent name \"bad name\" must match"},
		{"reserved agent", []string{"none", "--page"}, "reserved name"},
		{"context without page", []string{"@123", "--context", "@456"}, "--context and --after require --page"},
		{"cursor without page", []string{"@123", "--after", "@456"}, "--context and --after require --page"},
		{"page false", []string{"@123", "--page=false", "--context", "@456"}, "--context and --after require --page"},
		{"empty context", []string{"a", "--page", "--context="}, "--context and --after must be nonempty"},
		{"plain context", []string{"a", "--page", "--context", "a"}, "--context must identify a context receipt"},
		{"record context", []string{"a", "--page", "--context", "rec_123"}, "--context must identify a context receipt"},
		{"generation context", []string{"a", "--page", "--context", "gen_123"}, "--context must identify a context receipt"},
		{"malformed context", []string{"a", "--page", "--context", "ctx_bad"}, "--context must identify a context receipt"},
		{"malformed context ref", []string{"a", "--page", "--context", "@0"}, "reference \"@0\" must be @ followed by a positive integer"},
		{"empty cursor", []string{"--page", "--context", "@123", "--after="}, "--context and --after must be nonempty"},
		{"plain cursor", []string{"--page", "--context", "@123", "--after", "memory"}, "must identify a recall record"},
		{"context cursor", []string{"--page", "--context", "@123", "--after", "ctx_123"}, "must identify a recall record"},
		{"generation cursor", []string{"--page", "--context", "@123", "--after", "gen_123"}, "must identify a recall record"},
		{"malformed cursor ref", []string{"--page", "--context", "@123", "--after", "@0"}, "reference \"@0\" must be @ followed by a positive integer"},
		{"non-recall lane", []string{"--page", "--context", "@123", "--lane", "guidance"}, "--lane must be recall"},
		{"empty lane", []string{"--page", "--context", "@123", "--lane="}, "--lane must be recall"},
		{"invalid query text", []string{"--page", "--context", "@123", "--query", string([]byte{0xff})}, "recall query must be valid UTF-8"},
	}
	for _, flag := range []string{"include", "kind", "name", "coverage", "lint", "all"} {
		value := ""
		if flag == "all" {
			value = "false"
		}
		tests = append(tests, struct {
			name string
			args []string
			want string
		}{"incompatible " + flag, []string{"--page", "--context", "@123", "--" + flag + "=" + value}, "--page cannot be combined with --" + flag})
	}
	for _, tc := range tests {
		for _, format := range []string{"", "json"} {
			t.Run(tc.name+"/"+format, func(t *testing.T) {
				h := newHarness(t)
				h.home = filepath.Join(h.home, "unopened")
				args := append([]string{"inspect"}, tc.args...)
				if format != "" {
					args = append(args, "--format", format)
				}
				r := h.run(args...)
				if r.code != 2 || !strings.HasPrefix(r.err, "nine-tails: ") || !strings.Contains(r.err, tc.want) {
					t.Fatalf("page syntax must win before resolution: %+v", r)
				}
				if format == "json" {
					v := r.json(t)
					if v["code"] != float64(2) || r.err != "nine-tails: "+v["error"].(string)+"\n" {
						t.Fatalf("page error envelope disagrees with stderr: %+v", r)
					}
				} else if r.out != "" {
					t.Fatalf("default error wrote stdout: %+v", r)
				}
				if _, err := os.Stat(h.home); !os.IsNotExist(err) {
					t.Fatalf("page syntax opened home before rejecting input: %v", err)
				}
			})
		}
	}
}

func TestLibrarySyntaxPreflightBeforeConfig(t *testing.T) {
	h := newHarness(t)
	if err := os.WriteFile(filepath.Join(h.home, "config.yaml"), []byte("[invalid config"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"inspect", "rec_123", "--page", "--format", "json"},
		{"inspect", "--page", "--context", "@123", "--all", "--format", "json"},
	} {
		r := h.run(args...)
		if r.code != 2 || !strings.Contains(r.err, "--page ") || strings.Contains(r.err, "config") || r.json(t)["code"] != float64(2) {
			t.Fatalf("config masked page syntax: %+v", r)
		}
	}
	if files, err := os.ReadDir(h.home); err != nil || len(files) != 1 || files[0].Name() != "config.yaml" {
		t.Fatalf("page preflight changed the store home: %+v %v", files, err)
	}
}

func TestLibraryInvalidFormatBeforeReferenceResolution(t *testing.T) {
	h := newHarness(t)
	h.home = filepath.Join(h.home, "unopened")
	r := h.run("inspect", "--page", "--context", "@123", "--format", "bogus")
	if r.code != 2 || r.out != "" || r.err != "nine-tails: unknown format \"bogus\" (json|yaml)\n" {
		t.Fatalf("reference resolution masked page format: %+v", r)
	}
	if _, err := os.Stat(h.home); !os.IsNotExist(err) {
		t.Fatalf("invalid page format created home: %v", err)
	}
}
