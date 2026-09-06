package main

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/scottmeyer/nine-tails/internal/store"
)

func TestCloseIsOptionalBookkeepingWithoutMarks(t *testing.T) {
	h := newHarness(t)
	base := h.ok("base", "a", "Base.").id(t)
	h.ok("prefer", "a", "Lead with evidence.")
	loaded := h.ok("load", "a", "--format", "json").json(t)
	ctx := loaded["context_id"].(string)
	ref := loaded["context_ref"].(string)
	before := h.ok("inspect", ctx).json(t)
	rendered := before["rendered"].([]any)
	if first := rendered[0].(map[string]any); len(rendered) != 2 || first["id"] != base || first["ordinal"] != float64(0) || first["excerpt"] != "Base." {
		t.Fatalf("receipt evidence: %v", rendered)
	}
	for _, extra := range []string{"0=+", base + "=X", "@1=?"} {
		if r := h.run("close", ref, extra); r.code != 2 {
			t.Fatalf("obsolete mark argument accepted: %d %q", r.code, r.err)
		}
	}
	if got := h.ok("inspect", ctx).json(t)["closed_at"]; got != nil && got != "" {
		t.Fatalf("invalid close mutated receipt: %v", got)
	}
	// Saving and loading work while the preceding receipt remains open.
	h.ok("note", "--context", ctx, "State the concrete finding first.")
	if r := h.ok("load", "a"); !strings.Contains(r.out, "State the concrete finding first.") {
		t.Fatalf("learning waited for receipt closure: %s", r.out)
	}
	closed := h.ok("close", ref, "--format", "json").json(t)
	if closed["context_id"] != ctx || closed["closed_at"] == nil || closed["closed_at"] == "" || closed["marks"] != nil {
		t.Fatalf("close result: %v", closed)
	}
	for _, r := range h.ok("inspect", ctx).json(t)["rendered"].([]any) {
		if r.(map[string]any)["mark"] != nil {
			t.Fatalf("new close manufactured a mark: %v", r)
		}
	}
	if count := countLegacyMarks(t, h); count != 0 {
		t.Fatalf("close wrote %d legacy marks", count)
	}
	if r := h.run("close", ctx); r.code != 7 {
		t.Fatalf("a receipt closes once: %d %q", r.code, r.err)
	}
	if r := h.run("close", base); r.code != 2 {
		t.Fatalf("record is not a receipt: %d %q", r.code, r.err)
	}
	if r := h.run("close", "ctx_999"); r.code != 3 {
		t.Fatalf("unknown receipt: %d %q", r.code, r.err)
	}
	next := h.ok("load", "a", "--format", "json").id(t)
	if got := h.ok("close", next).id(t); got != next {
		t.Fatalf("default output must remain canonical ID: %q", got)
	}
}

func TestCloseInvalidArgumentsAndHelpDoNotOpenStore(t *testing.T) {
	h := newHarness(t)
	for _, args := range [][]string{{"close"}, {"close", "ctx_999", "0=+"}, {"close", "ctx_999", "--format", "bad"}} {
		if r := h.run(args...); r.code != 2 {
			t.Fatalf("invalid close %v: %d %q", args, r.code, r.err)
		}
	}
	help := h.ok("close", "--help").out
	if !strings.Contains(help, "closure is optional") || strings.Contains(help, "mark") {
		t.Fatalf("close help: %s", help)
	}
	entries, err := os.ReadDir(h.home)
	if err != nil || len(entries) != 0 {
		t.Fatalf("invalid/help calls touched store: %v %v", entries, err)
	}
}

func countLegacyMarks(t *testing.T, h *harness) int {
	t.Helper()
	st, err := store.Open(h.home)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var n int
	if err := st.DB.QueryRow(`SELECT COUNT(*) FROM context_marks`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestHistoricalMarksRemainInspectableWithoutAffectingLearning(t *testing.T) {
	h := newHarness(t)
	base := h.ok("base", "a", "Base.").id(t)
	source := h.ok("prefer", "a", "Lead with evidence.").id(t)
	doc := fmt.Sprintf("input_entries: [%s]\nitems:\n  - {key: evidence, body: Lead with evidence.}\nentries:\n  - {id: %s, disposition: represented, items: [evidence]}\n", source, source)
	installed := h.okIn(doc, "brief", "put", "a", "--expect-generation", "none", "--expect-base", base, "--stdin", "--format", "json").json(t)
	item := strs(t, installed["items"])[0]
	var first string
	for _, mark := range []string{"X", "X", "-", "---", "+"} {
		ctx := h.ok("load", "a", "--format", "json").id(t)
		if first == "" {
			first = ctx
		}
		// Simulate rows written by an older version. The supported command
		// tree has no score writer and must not reinterpret this history.
		st, err := store.Open(h.home)
		if err != nil {
			t.Fatal(err)
		}
		err = st.Tx(func(tx *sql.Tx) error {
			if _, err := tx.Exec(`INSERT INTO context_marks(context_id, record_id, mark, created_at) VALUES (?, ?, ?, ?)`, ctx, item, mark, store.Now()); err != nil {
				return err
			}
			_, err := tx.Exec(`UPDATE contexts SET closed_at = ? WHERE id = ?`, store.Now(), ctx)
			return err
		})
		st.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	found := false
	for _, r := range h.ok("inspect", first).json(t)["rendered"].([]any) {
		record := r.(map[string]any)
		if record["id"] == item {
			found = record["mark"] == "X"
		}
	}
	if !found {
		t.Fatal("historical receipt lost its mark")
	}
	brief := h.ok("inspect", "a", "--include", "brief").json(t)["brief"].(map[string]any)
	tally := brief["tallies"].(map[string]any)[item].(map[string]any)
	if tally["wrong"] != float64(2) || tally["minus"] != float64(2) || tally["plus"] != float64(1) {
		t.Fatalf("historical tally changed: %v", tally)
	}
	input := h.ok("compile-input", "a").json(t)
	active := input["active_generation"].(map[string]any)["items"].([]any)[0].(map[string]any)
	if _, exists := active["tally"]; exists {
		t.Fatalf("compiler consumes historical scores: %v", active)
	}
	instructions := input["instructions"].(string)
	for _, word := range []string{"tally", "mark", "hindered", "usefulness"} {
		if strings.Contains(instructions, word) {
			t.Fatalf("compiler teaches scores (%s): %s", word, instructions)
		}
	}
	lint := h.ok("inspect", "a", "--lint", "condition-loss").json(t)["lint"]
	if lint != nil && len(lint.([]any)) != 0 {
		t.Fatalf("history became score-based lint: %v", lint)
	}
	next := h.ok("load", "a", "--format", "json").id(t)
	h.ok("close", next)
	if count := countLegacyMarks(t, h); count != 5 {
		t.Fatalf("new close changed legacy marks: %d", count)
	}
	if r := h.run("close", first); r.code != 7 {
		t.Fatalf("historical receipt reclosed: %d %q", r.code, r.err)
	}
}
