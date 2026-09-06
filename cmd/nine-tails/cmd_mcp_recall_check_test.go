package main

import (
	"encoding/json"
	"reflect"
	"testing"
)

func mcpRecallCheck(t *testing.T, h *harness, args map[string]any) recordView {
	t.Helper()
	var view recordView
	if err := json.Unmarshal([]byte(mcpText(t, mcpInspectResponse(t, h, args))), &view); err != nil {
		t.Fatal(err)
	}
	if view.RecallCheck == nil {
		t.Fatalf("MCP inspection omitted recall_check: %+v", view)
	}
	return view
}

func TestMCPRecallCheckMatchesCLISelectionAndHistoricalDelivery(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "a", "Base.")
	mem := h.ok("remember", "a", "After rewind, restore a legal selected action.").id(t)
	missed := h.ok("load", "a", "--task", "undo leaves controls unresponsive", "--format", "json")
	ctx := missed.id(t)
	ctxRef := missed.json(t)["context_ref"].(string)
	view := mcpRecallCheck(t, h, map[string]any{"target": localRef(t, h, mem), "context": ctxRef})
	if view.ID != mem || view.RecallCheck.RecordedInContext || view.RecallCheck.Current.Selected || view.RecallCheck.Current.Reason != "no-word-match" {
		t.Fatalf("MCP miss diverged from CLI semantics: %+v", view)
	}
	view = mcpRecallCheck(t, h, map[string]any{"target": mem, "context": ctx, "query": "Rewind REWIND"})
	if !view.RecallCheck.Current.Selected || view.RecallCheck.QuerySource != "supplied" || !reflect.DeepEqual(view.RecallCheck.Current.MatchedTerms, []string{"rewind"}) {
		t.Fatalf("MCP supplied query did not use lexical selection: %+v", view.RecallCheck)
	}
	view = mcpRecallCheck(t, h, map[string]any{"target": mem, "context": ctx, "page": false, "query": ""})
	if view.RecallCheck.QuerySource != "supplied" || view.RecallCheck.Query != "" || view.RecallCheck.Current.Reason != "no-query-terms" {
		t.Fatalf("MCP lost an explicit empty query: %+v", view.RecallCheck)
	}

	delivered := h.ok("load", "a", "--task", "rewind", "--format", "json").id(t)
	next := h.ok("remember", "--context", delivered, "--supersedes", mem, "Current rewind recovery.").id(t)
	view = mcpRecallCheck(t, h, map[string]any{"target": mem, "context": delivered})
	if !view.RecallCheck.RecordedInContext || view.Current == nil || view.Current.ID != next || view.RecallCheck.Current.Eligible || view.RecallCheck.Current.Reason != "superseded" {
		t.Fatalf("MCP confused historical delivery with the current successor: %+v", view)
	}
	stable := h.ok("refs", "--limit", "1000", "--format", "json").out
	mcpRecallCheck(t, h, map[string]any{"target": next, "context": delivered})
	if got := h.ok("refs", "--limit", "1000", "--format", "json").out; got != stable {
		t.Fatal("MCP recall check allocated a receipt or record")
	}
}
