package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func readRecallCheck(t *testing.T, r result) recordView {
	t.Helper()
	var v recordView
	if err := json.Unmarshal([]byte(r.out), &v); err != nil {
		t.Fatal(err)
	}
	if v.RecallCheck == nil {
		t.Fatalf("missing recall check: %s", r.out)
	}
	return v
}

func TestRecallCheckSeparatesReceiptFromCurrentQueryWithoutWrites(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "a", "Base.")
	// A vocabulary miss: the memory existed, but the real load did not find it.
	body := "After rewind, restore a legal selected action."
	mem := h.ok("remember", "a", body).id(t)
	loaded := h.ok("load", "a", "--task", "undo leaves controls unresponsive", "--meta", "repo-id=game", "--format", "json")
	ctx := loaded.id(t)
	if len(loaded.json(t)["recall"].([]any)) != 0 {
		t.Fatal("fixture must actually miss recall")
	}
	before := h.ok("inspect", "a", "--include", "contexts,journal").out
	refs := h.ok("refs", "--limit", "1000", "--format", "json").out
	v := readRecallCheck(t, h.ok("inspect", localRef(t, h, mem), "--context", localRef(t, h, ctx)))
	c := v.RecallCheck
	if v.ID != mem || v.Body != body || c.Context.ID != ctx || c.Query != "undo leaves controls unresponsive" || c.QuerySource != "receipt-task" || c.RecordedInContext || !c.Current.Eligible || c.Current.Selected || c.Current.Reason != "no-word-match" || len(c.Current.MatchedTerms) != 0 {
		t.Fatalf("wrong vocabulary evidence: %+v", v)
	}
	v = readRecallCheck(t, h.ok("inspect", mem, "--context", ctx, "--query", "Rewind REWIND"))
	c = v.RecallCheck
	if c.RecordedInContext || !c.Current.Selected || c.QuerySource != "supplied" || !reflect.DeepEqual(c.Current.MatchedTerms, []string{"rewind"}) || c.Current.Excerpt != body || !strings.Contains(c.Limit, "historical query overrides") {
		t.Fatalf("current query was confused with historical delivery: %+v", c)
	}
	var y recordView
	if err := yaml.Unmarshal([]byte(h.ok("inspect", mem, "--context", ctx, "--format", "yaml").out), &y); err != nil || y.RecallCheck == nil || y.RecallCheck.Context.ID != ctx || y.Body != body {
		t.Fatalf("YAML evidence did not preserve memory and receipt: %+v, %v", y, err)
	}
	if after := h.ok("inspect", "a", "--include", "contexts,journal").out; after != before {
		t.Fatal("diagnosis changed the journal or created a new receipt")
	}
	if after := h.ok("refs", "--limit", "1000", "--format", "json").out; after != refs {
		t.Fatal("diagnosis allocated a new identity")
	}
}

func TestRecallCheckKeepsHistoricalDeliveryAfterCorrection(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "a", "Base.")
	body := "Rewind experience. " + strings.Repeat("A historical detail. ", 60)
	old := h.ok("remember", "a", body).id(t)
	loaded := h.ok("load", "a", "--task", "rewind", "--format", "json")
	ctx := loaded.id(t)
	if !loaded.json(t)["recall"].([]any)[0].(map[string]any)["truncated"].(bool) {
		t.Fatal("fixture must deliver a bounded excerpt")
	}
	next := h.ok("remember", "--context", ctx, "--supersedes", old, "Current rewind recovery.").id(t)
	v := readRecallCheck(t, h.ok("inspect", old, "--context", ctx))
	if v.Body != body || v.ID != old || v.Current == nil || v.Current.ID != next || !v.RecallCheck.RecordedInContext || v.RecallCheck.Current.Eligible || v.RecallCheck.Current.Reason != "superseded" {
		t.Fatalf("history was rewritten or silently forwarded: %+v", v)
	}
	if !strings.Contains(v.RecallCheck.Limit, "not full-text delivery") {
		t.Fatal("receipt delivery must not imply the full body was provided")
	}
	// A later memory can match now without ever having been in the old receipt.
	v = readRecallCheck(t, h.ok("inspect", next, "--context", ctx))
	if v.RecallCheck.RecordedInContext || !v.RecallCheck.Current.Selected {
		t.Fatalf("new successor was claimed as past evidence: %+v", v.RecallCheck)
	}
	h.ok("disable", next, "--context", ctx, "--reason", "No successor remains useful.")
	v = readRecallCheck(t, h.ok("inspect", next, "--context", ctx))
	if v.RecallCheck.Current.Reason != "disabled" || v.RecallCheck.Current.Eligible {
		t.Fatalf("disabled successor remains eligible: %+v", v.RecallCheck)
	}
}

func TestRecallCheckScopeExplicitSelectionsAndTypes(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "a", "Base.")
	h.ok("base", "b", "Other.")
	mem := h.ok("remember", "a", "Rewind experience.", "--meta", "repo-id=game").id(t)
	foreign := h.ok("remember", "b", "Rewind experience.").id(t)
	guide := h.ok("note", "a", "Always verify.").id(t)
	ctx := h.ok("load", "a", "--task", "rewind", "--meta", "repo-id=other", "--format", "json").id(t)
	v := readRecallCheck(t, h.ok("inspect", mem, "--context", ctx))
	if v.RecallCheck.Current.Reason != "scope-conflict" || v.RecallCheck.Current.Eligible {
		t.Fatalf("scope was ignored: %+v", v.RecallCheck)
	}
	// Explicit original delivery can disagree with today's lexical query.
	explicit := h.ok("load", "a", "--task", "unrelated", "--recall", mem, "--format", "json").id(t)
	v = readRecallCheck(t, h.ok("inspect", mem, "--context", explicit))
	if !v.RecallCheck.RecordedInContext || v.RecallCheck.Current.Selected || v.RecallCheck.Current.Reason != "no-word-match" {
		t.Fatalf("explicit delivery was conflated with word matching: %+v", v.RecallCheck)
	}
	v = readRecallCheck(t, h.ok("inspect", mem, "--context", explicit, "--query", ""))
	if v.RecallCheck.QuerySource != "supplied" || v.RecallCheck.Query != "" || v.RecallCheck.Current.Reason != "no-query-terms" {
		t.Fatalf("explicit empty query was lost: %+v", v.RecallCheck)
	}
	for _, args := range [][]string{
		{"inspect", foreign, "--context", ctx},
		{"inspect", guide, "--context", ctx},
		{"inspect", localRef(t, h, ctx), "--context", ctx},
		{"inspect", mem, "--context", localRef(t, h, mem)},
	} {
		if r := h.run(args...); r.code != 2 {
			t.Fatalf("wrong owner/type accepted: %v: %+v", args, r)
		}
	}
}

func TestRecallCheckUsesRealBudgetAndStoreBoundRendering(t *testing.T) {
	h := newHarness(t)
	h.home = filepath.Join(h.home, "long space ' $ `literal` "+strings.Repeat("x", 90))
	h.ok("base", "a", "Base.")
	var memories []string
	for i := 0; i < 18; i++ {
		memories = append(memories, h.ok("remember", "a", "Rewind recovery. "+strings.Repeat("Useful evidence. ", 30)).id(t))
	}
	loaded := h.ok("load", "a", "--task", "rewind", "--format", "json")
	selected := map[string]bool{}
	for _, v := range loaded.json(t)["recall"].([]any) {
		selected[v.(map[string]any)["id"].(string)] = true
	}
	if len(selected) == 0 || len(selected) == len(memories) {
		t.Fatal("fixture must hit the real recall budget")
	}
	for _, id := range memories {
		v := readRecallCheck(t, h.ok("inspect", id, "--context", loaded.id(t)))
		c := v.RecallCheck
		if c.RecordedInContext != selected[id] || c.Current.Selected != selected[id] || !c.Current.Eligible || !reflect.DeepEqual(c.Current.MatchedTerms, []string{"rewind"}) {
			t.Fatalf("diagnostic diverged from real load: %s: %+v", id, c)
		}
		if !selected[id] && c.Current.Reason != "outside-recall-budget" {
			t.Fatalf("budget omission called a vocabulary miss: %+v", c)
		}
	}
}

func TestRecallCheckInvalidSyntaxPrecedesStoreAndReferences(t *testing.T) {
	for _, args := range [][]string{
		{"inspect", "@999999", "--context", "@999998", "--format", "text"},
		{"inspect", "@999999", "--context", "@999998", "--all"},
		{"inspect", "@999999", "--context", "@999998", "--lane", "recall"},
		{"inspect", "@999999", "--context", "@999998", "--after", "@999997"},
		{"inspect", "a", "--context", "@999998"},
		{"inspect", "@999999", "--context", ""},
		{"inspect", "@999999", "--context", "@999998", "--query", string([]byte{0xff})},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			h := newHarness(t)
			h.home = filepath.Join(h.home, "absent")
			if r := h.run(args...); r.code != 2 {
				t.Fatalf("syntax was hidden by reference lookup: %+v", r)
			}
			if _, err := os.Stat(h.home); !os.IsNotExist(err) {
				t.Fatalf("invalid syntax created a home: %v", err)
			}
			if err := os.MkdirAll(h.home, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(h.home, "config.yaml"), []byte("invalid: ["), 0600); err != nil {
				t.Fatal(err)
			}
			if r := h.run(args...); r.code != 2 || strings.Contains(r.err, "config") {
				t.Fatalf("config masked syntax validation: %+v", r)
			}
		})
	}
}
