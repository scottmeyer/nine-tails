package main

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// Exercise the learning handoff across transports and harness facets. This
// verifies persistence/projection, not a model's understanding or task success.
func TestLearningCycleCorrectionReviewAndCrossHarnessReuse(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "engineer", "Implement the current task and verify the result.")
	old := h.ok("remember", "engineer", "--meta", "repo-id=project", "Custom store repair commands lost their store binding.").id(t)
	rule := h.ok("note", "engineer", "--meta", "repo-id=project", "Use an explicit store path for every generated command.").id(t)
	obsolete := h.ok("note", "engineer", "--meta", "repo-id=project", "The command repair is still pending.").id(t)
	loaded := h.ok("load", "engineer", "--task", "custom store commands", "--meta", "repo-id=project", "--meta", "harness=codex", "--format", "json").json(t)
	episode := loaded["context_id"].(string)
	ref := loaded["context_ref"].(string)
	before := h.ok("inspect", episode).json(t)["rendered"]

	repaired := h.ok("remember", "--context", episode, "--supersedes", old,
		"Custom store repair commands retain the selected store after copying. Verified with an isolated store; ordinary default-store commands may stay compact.").id(t)
	corrected := h.ok("note", "--context", episode, "--supersedes", rule,
		"Bind generated commands to the selected store when default resolution would select another store.").id(t)
	h.ok("disable", obsolete, "--context", episode, "--reason", "The repair is verified; the pending status is obsolete.")

	packet := h.ok("inspect", ref, "--review", "--format", "json").json(t)
	var fromMCP map[string]any
	if err := json.Unmarshal([]byte(mcpText(t, mcpInspectResponse(t, h, map[string]any{"target": ref, "review": true}))), &fromMCP); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(packet, fromMCP) {
		t.Fatalf("CLI and MCP review disagree: CLI=%v MCP=%v", packet, fromMCP)
	}
	want := map[string]bool{"delivered:" + old: false, "write:" + repaired: false, "write:" + corrected: false, "retirement:" + obsolete: false}
	for _, raw := range packet["entries"].([]any) {
		entry := raw.(map[string]any)
		key := entry["event"].(string) + ":" + entry["id"].(string)
		if _, ok := want[key]; ok {
			want[key] = true
		}
	}
	for event, found := range want {
		if !found {
			t.Fatalf("review lost %s: %v", event, packet)
		}
	}
	if !reflect.DeepEqual(before, h.ok("inspect", episode).json(t)["rendered"]) {
		t.Fatal("review or correction rewrote historical delivery")
	}

	next := h.ok("load", "engineer", "--context", episode, "--task", "custom store commands", "--meta", "harness=claude", "--format", "json").json(t)
	ids := map[string]bool{}
	for _, id := range next["rendered_record_ids"].([]any) {
		ids[id.(string)] = true
	}
	if !ids[repaired] || !ids[corrected] || ids[old] || ids[rule] || ids[obsolete] {
		t.Fatalf("cross-harness handoff lost correction or revived obsolete knowledge: %v", ids)
	}
	meta := next["metadata"].(map[string]any)
	if !reflect.DeepEqual(meta["harness"], []any{"claude"}) || !reflect.DeepEqual(meta["repo-id"], []any{"project"}) {
		t.Fatalf("cross-harness handoff changed repository scope: %v", meta)
	}
	other := h.ok("load", "engineer", "--context", episode, "--task", "custom store commands", "--meta", "repo-id=other", "--format", "json").json(t)
	for _, id := range other["rendered_record_ids"].([]any) {
		if id == repaired || id == corrected {
			t.Fatalf("learned project lesson leaked to another repository: %v", other)
		}
	}
}

func TestLearningReviewContinuationIsCompleteAcrossTransports(t *testing.T) {
	h := newHarness(t)
	base := h.ok("base", "engineer", "Review recorded evidence.").id(t)
	episode := h.ok("load", "engineer", "--format", "json").id(t)
	want := map[string]bool{"delivered:" + base: true}
	for i := 0; i < 35; i++ {
		id := h.ok("remember", "--context", episode, fmt.Sprintf("Experience %d. %s", i, strings.Repeat("bounded preview ", 100))).id(t)
		want["write:"+id] = true
		if i%10 == 0 {
			h.ok("disable", id, "--context", episode, "--reason", "This observation no longer has a useful future role.")
			want["retirement:"+id] = true
		}
	}
	before := h.ok("refs", "--limit", "1000", "--format", "json").out
	seen := map[string]bool{}
	after := ""
	pages := 0
	for {
		args := []string{"inspect", episode, "--review"}
		mcpArgs := map[string]any{"target": episode, "review": true}
		if after != "" {
			args = append(args, "--review-after", after)
			mcpArgs["review_after"] = after
		}
		page := reviewResult(t, h.ok(args...))
		var mcpPage reviewPacket
		if err := json.Unmarshal([]byte(mcpText(t, mcpInspectResponse(t, h, mcpArgs))), &mcpPage); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(page, mcpPage) {
			t.Fatalf("page %d differs between transports", pages)
		}
		pages++
		if pages > len(want) || len(page.Entries) == 0 {
			t.Fatalf("continuation did not advance: %+v", page)
		}
		for _, entry := range page.Entries {
			key := entry.Event + ":" + entry.ID
			if !want[key] || seen[key] {
				t.Fatalf("unexpected or duplicate entry %s", key)
			}
			seen[key] = true
		}
		if page.Counts.Total != len(want) || page.Counts.Remaining != len(want)-len(seen) {
			t.Fatalf("page counts disagree with remaining evidence: %+v", page.Counts)
		}
		if page.Next == nil {
			break
		}
		after = page.Next.ReviewAfter
	}
	if pages < 2 || !reflect.DeepEqual(seen, want) {
		t.Fatalf("review lost evidence across page boundaries: pages=%d got=%v want=%v", pages, seen, want)
	}
	if after := h.ok("refs", "--limit", "1000", "--format", "json").out; after != before {
		t.Fatal("review allocated or changed references")
	}
}
