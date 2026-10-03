package main

import (
	"strings"
	"testing"
)

func recallRecords(t *testing.T, h *harness, agent string) []any {
	t.Helper()
	v := h.ok("inspect", agent, "--lane", "recall", "--all", "--format", "json").json(t)
	records, _ := v["records"].([]any)
	return records
}

func TestDedupeKeyReplayReturnsTheEarlierRecordWithoutWriting(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "a", "Base.")
	ctx := contextID(t, h.ok("load", "a").out)
	first := h.ok("remember", "--context", ctx, "--dedupe-key", "job-1", "Job 1 finished.").id(t)
	replay := h.ok("remember", "--context", ctx, "--dedupe-key", "job-1", "Job 1 finished.")
	if strings.TrimSpace(replay.out) != first {
		t.Fatalf("replay printed %q, want %s", replay.out, first)
	}
	if !strings.Contains(replay.err, "deduplicated against "+first) {
		t.Fatalf("replay must say what it deduplicated against: %q", replay.err)
	}
	if n := len(recallRecords(t, h, "a")); n != 1 {
		t.Fatalf("replay wrote a record: %d recall records", n)
	}
	// The body need not match: the key, not the text, identifies the write.
	again := h.ok("remember", "--context", ctx, "--dedupe-key", "job-1", "--format", "json", "Job 1 finished (retry wording).").json(t)
	if again["id"] != first || again["deduplicated"] != true || again["dedupe_key"] != "job-1" {
		t.Fatalf("json replay envelope: %v", again)
	}
	if again["body"] != "Job 1 finished." {
		t.Fatalf("replay must return the stored body, not the retry text: %v", again["body"])
	}
}

func TestDedupeKeyFirstWriteReportsItselfAndKeysAreScopedByLane(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "a", "Base.")
	v := h.ok("note", "a", "--dedupe-key", "k", "--format", "json", "Rule.").json(t)
	if v["deduplicated"] != false || v["dedupe_key"] != "k" || v["status"] != "active" {
		t.Fatalf("first keyed write envelope: %v", v)
	}
	noteID := v["id"].(string)
	recallID := h.ok("remember", "a", "--dedupe-key", "k", "Fact.").id(t)
	if recallID == noteID {
		t.Fatal("the same key in another lane must be a separate write")
	}
	otherAgent := h.ok("base", "b", "Base.")
	_ = otherAgent
	bID := h.ok("note", "b", "--dedupe-key", "k", "Rule for b.").id(t)
	if bID == noteID {
		t.Fatal("the same key for another agent must be a separate write")
	}
	if r := h.run("note", "a", "--dedupe-key", "has space", "Rule."); r.code != 2 {
		t.Fatalf("whitespace in a key must be invalid: %+v", r)
	}
	if r := h.run("note", "a", "--dedupe-key", "k", "--supersedes", noteID, "Rule 2."); r.code != 2 {
		t.Fatalf("dedupe key with supersedes must be invalid: %+v", r)
	}
	if r := h.run("note", "a", "--dedupe-key", "fresh", ""); r.code == 0 {
		t.Fatalf("an invalid body must fail even with a fresh key: %+v", r)
	}
}

func TestDedupeKeyFollowsCorrectionsAndRefusesRetiredRecords(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "a", "Base.")
	ctx := contextID(t, h.ok("load", "a").out)
	old := h.ok("note", "--context", ctx, "--dedupe-key", "rule-7", "Old wording.").id(t)
	current := h.ok("note", "--context", ctx, "--supersedes", old, "New wording.").id(t)
	replay := h.ok("note", "--context", ctx, "--dedupe-key", "rule-7", "--format", "json", "Old wording.").json(t)
	if replay["id"] != current || replay["deduplicated"] != true {
		t.Fatalf("replay after correction must answer with the successor: %v", replay)
	}
	h.ok("disable", current, "--context", ctx, "--reason", "Retired by the user.")
	r := h.run("note", "--context", ctx, "--dedupe-key", "rule-7", "Old wording.")
	if r.code != 7 || !strings.Contains(r.err, "rule-7") {
		t.Fatalf("a retired key must conflict rather than resurrect the record: %+v", r)
	}
	if n := len(h.ok("inspect", "a", "--lane", "guidance", "--format", "json").json(t)["records"].([]any)); n != 0 {
		t.Fatalf("the conflict must not write: %d active guidance records", n)
	}
}

func TestInspectMetaFilterMatchesExactPairs(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "a", "Base.")
	x := h.ok("note", "a", "--meta", "repo-id=x", "--meta", "work-id=7", "Scoped to x.").id(t)
	h.ok("note", "a", "--meta", "repo-id=y", "Scoped to y.")
	h.ok("note", "a", "Unscoped.")
	only := func(args ...string) []any {
		t.Helper()
		v := h.ok(append([]string{"inspect", "a", "--format", "json"}, args...)...).json(t)
		records, _ := v["records"].([]any)
		return records
	}
	if got := only("--meta", "repo-id=x"); len(got) != 1 || got[0].(map[string]any)["id"] != x {
		t.Fatalf("exact meta filter: %v", got)
	}
	if got := only("--meta", "repo-id=x", "--meta", "work-id=7"); len(got) != 1 {
		t.Fatalf("every pair must match: %v", got)
	}
	if got := only("--meta", "repo-id=x", "--meta", "work-id=8"); len(got) != 0 {
		t.Fatalf("a non-matching pair must exclude the record: %v", got)
	}
	if got := only("--meta", "repo-id=", "--lane", "guidance"); len(got) != 0 {
		t.Fatalf("an empty value matches nothing rather than everything: %v", got)
	}
	if got := only("--meta", "repo-id=x", "--query", "scoped"); len(got) != 1 {
		t.Fatalf("meta and query filters combine: %v", got)
	}
	if r := h.run("inspect", "a", "--meta", "novalue"); r.code != 2 {
		t.Fatalf("malformed meta filter must be invalid: %+v", r)
	}
}
