package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scottmeyer/nine-tails/internal/capsule"
)

func TestAgentFollowPreflightsSyntaxBeforeResolvingLocalReferences(t *testing.T) {
	h := newHarness(t)
	if err := os.WriteFile(filepath.Join(h.home, "config.yaml"), []byte("not: [valid"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"agent", "follow", "subscriber/style", "source", "--expect", "@123", "--format", "bogus"},
		{"agent", "follow", "subscriber/style", "bad/source", "--expect", "@123"},
	} {
		r := h.run(args...)
		if r.code != 2 || strings.Contains(r.err, "config") || strings.Contains(r.err, "reference") {
			t.Fatalf("syntax was masked by local reference resolution: %v => %#v", args, r)
		}
	}
	if _, err := os.Stat(filepath.Join(h.home, "nine-tails.db")); !os.IsNotExist(err) {
		t.Fatalf("syntax preflight opened the store: %v", err)
	}
}

func loadFollowedGuidance(t *testing.T, h *harness, args ...string) capsule.Capsule {
	t.Helper()
	var c capsule.Capsule
	r := h.ok(append(append([]string{"load"}, args...), "--format", "json")...)
	if err := json.Unmarshal([]byte(r.out), &c); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestAgentFollowScopesDeduplicatesAndFollowsCurrentGuidance(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "subscriber", "Local base must remain private.")
	h.ok("base", "source", "Source identity must not cross a link.")
	local := h.ok("note", "subscriber", "Local guidance stays first.").id(t)
	first := h.ok("note", "source", "Shared project rule.", "--meta", "repo-id=game").id(t)
	h.ok("note", "source", "Other project rule.", "--meta", "repo-id=other")
	firstLink := h.ok("agent", "follow", "subscriber/style", "source", "--expect", "none", "--meta", "phase=build").id(t)
	secondLink := h.ok("agent", "follow", "subscriber/duplicate", "source", "--expect", "none", "--meta", "phase=build").id(t)
	h.ok("agent", "follow", "subscriber/wrong-project", "source", "--expect", "none", "--meta", "repo-id=wrong")

	other := loadFollowedGuidance(t, h, "subscriber", "--meta", "repo-id=other", "--meta", "phase=build")
	if len(other.GuidanceLinks) != 2 || strings.Contains(other.Instructions, "Shared project rule.") || !strings.Contains(other.Instructions, "Other project rule.") {
		t.Fatalf("source scope did not select its own project: %#v\n%s", other.GuidanceLinks, other.Instructions)
	}
	// Two explicit scopes are conjunctive even when the caller omits project
	// metadata, while overlapping multivalue scopes remain eligible.
	h.ok("agent", "follow", "subscriber/multi", "source", "--expect", "none", "--meta", "repo-id=game", "--meta", "repo-id=other")
	h.ok("note", "source", "Three-way overlap must not leak.", "--meta", "repo-id=b", "--meta", "repo-id=c")
	h.ok("agent", "follow", "subscriber/three-way", "source", "--expect", "none", "--meta", "repo-id=a", "--meta", "repo-id=b")
	unscoped := loadFollowedGuidance(t, h, "subscriber", "--meta", "phase=build")
	for _, link := range unscoped.GuidanceLinks {
		if link.Name == "wrong-project" {
			t.Fatalf("incompatible explicit source/link scopes bridged: %#v", unscoped.GuidanceLinks)
		}
	}
	if !strings.Contains(unscoped.Instructions, "Shared project rule.") || !strings.Contains(unscoped.Instructions, "Other project rule.") {
		t.Fatalf("overlapping multivalue scopes stopped normal wildcard selection:\n%s", unscoped.Instructions)
	}
	threeWay := loadFollowedGuidance(t, h, "subscriber", "--meta", "repo-id=a", "--meta", "repo-id=c", "--meta", "phase=build")
	for _, link := range threeWay.GuidanceLinks {
		if link.Name == "three-way" {
			t.Fatalf("three-way empty intersection leaked: %#v", threeWay.GuidanceLinks)
		}
	}
	review := loadFollowedGuidance(t, h, "subscriber", "--meta", "repo-id=game", "--meta", "phase=review")
	for _, link := range review.GuidanceLinks {
		if link.Name == "style" || link.Name == "duplicate" {
			t.Fatalf("phase=build link scope leaked: %#v", review.GuidanceLinks)
		}
	}
	if !strings.Contains(review.Instructions, "Shared project rule.") {
		t.Fatalf("link scope leaked: %#v\n%s", review.GuidanceLinks, review.Instructions)
	}
	c := loadFollowedGuidance(t, h, "subscriber", "--meta", "repo-id=game", "--meta", "phase=build")
	if len(c.GuidanceLinks) != 3 || strings.Count(c.Instructions, "Shared project rule.") != 1 || !strings.Contains(c.Instructions, "Shared from `source`") {
		t.Fatalf("shared guidance/provenance: %#v\n%s", c.GuidanceLinks, c.Instructions)
	}
	if strings.Index(c.Instructions, "Local guidance stays first.") > strings.Index(c.Instructions, "Shared project rule.") || strings.Contains(c.Instructions, "Source identity must not cross") {
		t.Fatalf("local priority or identity leak:\n%s", c.Instructions)
	}
	if got := linkedReceipt(t, h, c.ContextID); got[local] != "recent" || got[first] != "shared-guidance" || got[firstLink] != "guidance-links" || got[secondLink] != "guidance-links" {
		t.Fatalf("receipt lacks exact shared provenance: %#v", got)
	}
	if c.UncompiledAdjustments != 1 {
		t.Fatalf("foreign guidance must not inflate local compiler work: %d", c.UncompiledAdjustments)
	}

	second := h.ok("note", "source", "Corrected shared project rule.", "--supersedes", first, "--meta", "repo-id=game").id(t)
	later := loadFollowedGuidance(t, h, "subscriber", "--meta", "repo-id=game", "--meta", "phase=build")
	if strings.Contains(later.Instructions, "Shared project rule.") || !strings.Contains(later.Instructions, "Corrected shared project rule.") || len(later.GuidanceLinks) != 3 {
		t.Fatalf("link did not read current source guidance:\n%s", later.Instructions)
	}
	h.ok("disable", second)
	if next := loadFollowedGuidance(t, h, "subscriber", "--meta", "repo-id=game", "--meta", "phase=build"); len(next.GuidanceLinks) != 0 || strings.Contains(next.Instructions, "Corrected shared") {
		t.Fatalf("disabled source guidance remained visible: %#v\n%s", next.GuidanceLinks, next.Instructions)
	}
}

func TestAgentFollowContextCASExportAndOneHop(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "subscriber", "Build.")
	h.ok("base", "source", "Keep source-only identity.")
	h.ok("base", "middle", "Middle identity.")
	ctx := loadFollowedGuidance(t, h, "subscriber").ContextID
	ctxRef := localRef(t, h, ctx)
	for _, args := range [][]string{
		{"agent", "follow", "alias", "source", "--expect", "none"},
		{"agent", "follow", "other/alias", "source", "--context", ctx, "--expect", "none"},
		{"agent", "follow", "subscriber/alias", "subscriber", "--expect", "none"},
		{"agent", "follow", "subscriber/alias", "bad/source", "--expect", "none"},
		{"put", "subscriber", "--lane", "definition", "--kind", "guidance-link", "--name", "self", "subscriber"},
	} {
		requireExit(t, h.run(args...), 2, "")
	}
	link := h.ok("agent", "follow", "style", "source", "--context", ctxRef, "--expect", "none", "--meta", "repo-id=game", "--format", "json")
	linkID := link.id(t)
	if link.json(t)["origin_context"] != ctx || link.json(t)["body"] != "source" {
		t.Fatal("follow did not retain literal source/provenance")
	}
	requireExit(t, h.run("agent", "follow", "subscriber/style", "source", "--expect", "none"), 7, "expected")
	updated := h.ok("agent", "follow", "subscriber/style", "middle", "--expect", localRef(t, h, linkID), "--format", "json")
	if updated.json(t)["supersedes"] != linkID {
		t.Fatal("follow did not use named CAS")
	}
	h.ok("note", "source", "Do not inherit through source.")
	h.ok("note", "middle", "Middle guidance only.")
	h.ok("agent", "follow", "middle/upstream", "source", "--expect", "none")
	c := loadFollowedGuidance(t, h, "subscriber")
	if len(c.GuidanceLinks) != 1 || !strings.Contains(c.Instructions, "Middle guidance only.") || strings.Contains(c.Instructions, "Do not inherit through source.") || strings.Contains(c.Instructions, "Middle identity.") {
		t.Fatalf("follow traversed more than one guidance hop:\n%s", c.Instructions)
	}

	doc := h.ok("export", "subscriber", "--include", "agents").out
	if !strings.Contains(doc, "guidance-link") || strings.Contains(doc, "Middle guidance only.") {
		t.Fatalf("export did not preserve definition only:\n%s", doc)
	}
	dest := newHarness(t)
	dest.ok("base", "subscriber", "Imported subscriber.")
	imported := dest.okIn(doc, "import", "--stdin", "--format", "json").json(t)["ids"].(map[string]any)[updated.id(t)].(string)
	loaded := loadFollowedGuidance(t, dest, "subscriber")
	if len(loaded.Skipped) != 1 || loaded.Skipped[0].ID != imported || !strings.Contains(loaded.Skipped[0].Reason, "no records for source agent middle") {
		t.Fatalf("missing source was not bounded/actionable: %#v", loaded.Skipped)
	}
	dest.ok("base", "middle", "Destination source exists but has no guidance.")
	if loaded = loadFollowedGuidance(t, dest, "subscriber"); len(loaded.Skipped) != 0 || len(loaded.GuidanceLinks) != 0 {
		t.Fatalf("source with no active guidance should be valid: %#v", loaded)
	}
	requireExit(t, dest.runIn(strings.Replace(doc, "body: middle", "body: subscriber", 1), "import", "--stdin"), 2, "source must not")
}
