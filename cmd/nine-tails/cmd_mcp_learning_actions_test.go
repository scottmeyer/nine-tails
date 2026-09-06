package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func mcpLearnResponse(t *testing.T, h *harness, args map[string]any) map[string]any {
	t.Helper()
	payload, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	packet := fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"nt_learn","arguments":%s}}`+"\n", payload)
	return mcpResponses(t, h.okIn(mcpHello+packet, "mcp").out)[1]
}

func TestMCPConsolidationPreservesKindScopeAndSources(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "a", "Base.")
	ctx := contextID(t, h.ok("load", "a", "--meta", "repo-id=ambient").out)
	first := h.ok("prefer", "a", "--meta", "repo-id=letters", "Use concrete nouns.").id(t)
	second := h.ok("prefer", "a", "--meta", "repo-id=letters", "Keep sentences clear.").id(t)
	response := mcpLearnResponse(t, h, map[string]any{"context": ctx, "sources": []string{referenceFor(t, h, first), second}, "body": "Use concrete nouns in clear sentences.", "reason": "These preferences express one writing rule."})
	var result map[string]any
	if err := json.Unmarshal([]byte(mcpText(t, response)), &result); err != nil {
		t.Fatal(err)
	}
	if result["kind"] != "prefer" || result["origin_context"] != ctx || !reflect.DeepEqual(result["meta"], map[string]any{"repo-id": []any{"letters"}}) {
		t.Fatalf("MCP imposed a type or ambient scope: %+v", result)
	}
	audit := result["consolidation"].(map[string]any)
	sources := audit["sources"].([]any)
	if audit["reason"] != "These preferences express one writing rule." || len(sources) != 2 || sources[0].(map[string]any)["id"] != first || sources[1].(map[string]any)["id"] != second {
		t.Fatalf("source order or reason lost: %+v", audit)
	}
	for _, id := range []string{first, second} {
		old := h.ok("inspect", id).json(t)
		if old["status"] != "superseded" || old["current"].(map[string]any)["id"] != result["id"] {
			t.Fatalf("source no longer navigates to its consolidation: %+v", old)
		}
	}
}

func TestMCPConsolidationPreservesRecallLaneAndLibrary(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "a", "Base.")
	ctx := contextID(t, h.ok("load", "a").out)
	first := h.ok("remember", "a", "Measured retry pressure.").id(t)
	second := h.ok("remember", "a", "Observed retry backoff.").id(t)
	response := mcpLearnResponse(t, h, map[string]any{"context": ctx, "sources": []string{first, second}, "body": "Retry pressure was measured and backoff observed.", "reason": "These observations describe one retriable behavior."})
	var result map[string]any
	if err := json.Unmarshal([]byte(mcpText(t, response)), &result); err != nil {
		t.Fatal(err)
	}
	if result["lane"] != "recall" || result["kind"] != "memory" {
		t.Fatalf("recall consolidation changed lane or kind: %+v", result)
	}
	for _, id := range []string{first, second} {
		old := h.ok("inspect", id).json(t)
		if old["status"] != "superseded" || old["current"].(map[string]any)["id"] != result["id"] {
			t.Fatalf("recall source history was not retained: %+v", old)
		}
	}
	loaded := h.ok("load", "a", "--task", "retry pressure").out
	if !strings.Contains(loaded, "Retry pressure was measured and backoff observed.") {
		t.Fatalf("consolidated recall was not available to later load: %s", loaded)
	}
	page := h.ok("inspect", "a", "--page", "--query", "backoff").out
	if !strings.Contains(page, result["id"].(string)) {
		t.Fatalf("consolidated recall was not in the library page: %s", page)
	}
}

func TestMCPConsolidationRequiresDeliberateKindForMixedSources(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "a", "Base.")
	ctx := contextID(t, h.ok("load", "a").out)
	first := h.ok("avoid", "a", "Vague nouns.").id(t)
	second := h.ok("prefer", "a", "Concrete nouns.").id(t)
	args := map[string]any{"context": ctx, "sources": []string{first, second}, "body": "Use concrete nouns.", "reason": "Unify the positive and negative instructions."}
	if mcpLearnResponse(t, h, args)["result"].(map[string]any)["isError"] != true {
		t.Fatal("mixed source kinds silently became note")
	}
	if h.ok("inspect", first).json(t)["status"] != "active" || h.ok("inspect", second).json(t)["status"] != "active" {
		t.Fatal("failed consolidation changed sources")
	}
	args["kind"] = "prefer"
	var result map[string]any
	if err := json.Unmarshal([]byte(mcpText(t, mcpLearnResponse(t, h, args))), &result); err != nil || result["kind"] != "prefer" {
		t.Fatalf("explicit mixed-source kind failed: %+v, %v", result, err)
	}
}

func TestMCPForgetIsAuditedAndChecksOwnership(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "a", "Base.")
	h.ok("base", "b", "Other.")
	ctx := contextID(t, h.ok("load", "a").out)
	other := contextID(t, h.ok("load", "b").out)
	old := h.ok("note", "a", "An obsolete project-only instruction.").id(t)
	args := map[string]any{"context": other, "forget": referenceFor(t, h, old), "reason": "The project no longer uses this workflow."}
	if mcpLearnResponse(t, h, args)["result"].(map[string]any)["isError"] != true || h.ok("inspect", old).json(t)["status"] != "active" {
		t.Fatal("forget bypassed the owning agent")
	}
	args["context"] = ctx
	mcpText(t, mcpLearnResponse(t, h, args))
	stored := h.ok("inspect", old).json(t)
	audit := stored["retirement"].(map[string]any)
	if stored["status"] != "disabled" || stored["body"] != "An obsolete project-only instruction." || audit["context"] != ctx || audit["reason"] != args["reason"] {
		t.Fatalf("forget lost immutable body or its reason: %+v", stored)
	}
	if strings.Contains(h.ok("load", "a").out, "An obsolete project-only instruction.") {
		t.Fatal("forgotten record still guides future loads")
	}
	if mcpLearnResponse(t, h, args)["result"].(map[string]any)["isError"] != true {
		t.Fatal("forget accepted an already inactive target")
	}
}

func TestMCPLearningActionsRejectAmbiguityBeforeStore(t *testing.T) {
	h := newHarness(t)
	for _, args := range []map[string]any{
		{"sources": []string{}, "body": "Text", "reason": "Why"},
		{"sources": []string{"rec_ONE"}, "body": "Text", "reason": "Why"},
		{"sources": []any{"rec_ONE", false}, "body": "Text", "reason": "Why"},
		{"sources": []string{"rec_ONE", "rec_TWO"}, "reason": "Why"},
		{"sources": []string{"rec_ONE", "rec_TWO"}, "body": "Text"},
		{"sources": []string{"rec_ONE", "rec_TWO"}, "body": "Text", "reason": "Why", "kind": "remember"},
		{"sources": []string{"rec_ONE", "rec_TWO"}, "body": "Text", "reason": "Why", "meta": map[string]any{}},
		{"sources": []string{"rec_ONE", "rec_TWO"}, "body": "Text", "reason": "Why", "clear_meta": false},
		{"sources": []string{"rec_ONE", "rec_TWO"}, "body": "Text", "reason": "Why", "supersedes": "rec_ONE"},
		{"forget": "rec_ONE"},
		{"forget": "rec_ONE", "reason": " "},
		{"forget": "rec_ONE", "reason": "Why", "body": "Text"},
		{"forget": "rec_ONE", "reason": "Why", "kind": "note"},
		{"forget": "rec_ONE", "reason": "Why", "sources": []string{"rec_ONE", "rec_TWO"}},
		{"forget": "rec_ONE", "reason": "Why", "supersedes": "rec_TWO"},
		{"forget": "rec_ONE", "reason": "Why", "meta": map[string]any{}},
		{"forget": "rec_ONE", "reason": "Why", "clear_meta": false},
		{"body": "Text", "reason": "Unused reason"},
	} {
		args["context"] = "ctx_MISSING"
		response := mcpLearnResponse(t, h, args)
		if failure, ok := response["error"].(map[string]any); !ok || failure["code"] != float64(-32602) {
			t.Fatalf("ambiguous action did not fail protocol validation: %+v => %+v", args, response)
		}
	}
	if _, err := os.Stat(filepath.Join(h.home, "nine-tails.db")); !os.IsNotExist(err) {
		t.Fatalf("invalid learning actions opened store: %v", err)
	}
}
