package main

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// The real handoff that motivated key replacement: a framework pilot delegates
// through workshop into a game project. All tests use newHarness's temp store.
func projectHandoff(t *testing.T) (*harness, string, map[string][]string) {
	t.Helper()
	h := newHarness(t)
	h.ok("base", "pilot", "Discover roles.")
	h.ok("base", "workshop", "Coordinate the selected project.")
	ids := map[string][]string{}
	for _, scope := range []string{"nine-tails", "soccer-chess"} {
		meta := "repo-id=" + scope
		ids[scope] = append(ids[scope], h.ok("note", "workshop", "--meta", meta, scope+" handoff guidance.").id(t))
		ids[scope] = append(ids[scope], h.ok("remember", "workshop", "--meta", meta, scope+" handoff experience.").id(t))
		ids[scope] = append(ids[scope], h.ok("state", "put", "workshop/"+scope, "--expect", "none", "--meta", meta, "project: "+scope).id(t))
		ids[scope] = append(ids[scope], h.okIn("description: "+scope+" checker\nexec:\n  argv: [echo, checked]\n", "put", "shared", "--lane", "definition", "--kind", "tool", "--name", scope, "--meta", meta, "--stdin").id(t))
		ids[scope] = append(ids[scope], h.ok("signal", "--subject", scope+" handoff inbox", "--meta", meta).id(t))
	}
	parent := h.ok("load", "pilot", "--task", "Framework work", "--meta", "repo-id=nine-tails", "--meta", "harness=test", "--meta", "track=old", "--format", "json").json(t)
	return h, parent["context_id"].(string), ids
}

func assertProjectSwitch(t *testing.T, h *harness, parent string, c map[string]any, ids map[string][]string) string {
	t.Helper()
	meta := c["metadata"].(map[string]any)
	want := map[string]any{"repo-id": []any{"soccer-chess"}, "harness": []any{"test"}, "track": []any{"gameplay", "art"}}
	if !reflect.DeepEqual(meta, want) || c["parent_context"] != parent {
		t.Fatalf("switched context: meta=%v parent=%v, want %v / %s", meta, c["parent_context"], want, parent)
	}
	emitted := map[string]bool{}
	for _, id := range c["rendered_record_ids"].([]any) {
		emitted[id.(string)] = true
	}
	for _, id := range ids["nine-tails"] {
		if emitted[id] {
			t.Errorf("old project record leaked: %s", id)
		}
	}
	for _, id := range ids["soccer-chess"] {
		if !emitted[id] {
			t.Errorf("new project record missing: %s", id)
		}
	}
	ctx := c["context_id"].(string)
	receipt := h.ok("inspect", ctx).json(t)
	if !reflect.DeepEqual(receipt["metadata"], want) {
		t.Errorf("receipt did not preserve resolved values: %v", receipt)
	}
	parentReceipt := h.ok("inspect", parent).json(t)
	parentMeta := parentReceipt["metadata"].(map[string]any)
	if !reflect.DeepEqual(parentMeta["repo-id"], []any{"nine-tails"}) || !reflect.DeepEqual(parentMeta["track"], []any{"old"}) {
		t.Errorf("switch mutated parent receipt: %v", parentReceipt)
	}
	return ctx
}

func TestLoadExplicitKeysReplaceInheritedProjectScope(t *testing.T) {
	h, parent, ids := projectHandoff(t)
	c := h.ok("load", "--agent", "workshop", "--context", parent, "--task", "Game handoff", "--meta", "repo-id=soccer-chess", "--meta", "track=gameplay", "--meta", "track=art", "--meta", "track=gameplay", "--format", "json").json(t)
	ctx := assertProjectSwitch(t, h, parent, c, ids)
	if r := h.run("call", "--context", ctx, "nine-tails"); r.code != 3 || !strings.Contains(r.err, "not applicable") {
		t.Fatalf("old project tool should be inapplicable: %+v", r)
	}
	if got := h.ok("call", "--context", ctx, "soccer-chess").out; got != "checked\n" {
		t.Fatalf("new project tool did not execute: %q", got)
	}
	// Unspecified keys continue down the chain, and origin is still not scope.
	grandchild := h.ok("load", "workshop", "--context", ctx, "--format", "json").json(t)
	if !reflect.DeepEqual(grandchild["metadata"], c["metadata"]) {
		t.Fatalf("subsequent handoff lost metadata: %v", grandchild["metadata"])
	}
	lesson := h.ok("note", "--context", ctx, "Useful across projects.").id(t)
	if got := h.ok("inspect", lesson).json(t)["meta"].(map[string]any); len(got) != 0 {
		t.Fatalf("context scope copied into an unqualified lesson: %v", got)
	}
	// Deliberately selecting both projects remains possible through repeated
	// explicit values; replacement must not collapse the multimap to one value.
	both := h.ok("load", "workshop", "--context", ctx, "--meta", "repo-id=nine-tails", "--meta", "repo-id=soccer-chess", "--format", "json").json(t)
	if got := both["metadata"].(map[string]any)["repo-id"]; !reflect.DeepEqual(got, []any{"nine-tails", "soccer-chess"}) {
		t.Fatalf("explicit multi-project selection changed: %v", got)
	}
	// A historical receipt with several project values can be narrowed on
	// the next load without rewriting that immutable receipt.
	repaired := h.ok("load", "workshop", "--context", both["context_id"].(string), "--meta", "repo-id=soccer-chess", "--format", "json").json(t)
	if got := repaired["metadata"].(map[string]any)["repo-id"]; !reflect.DeepEqual(got, []any{"soccer-chess"}) {
		t.Fatalf("multi-project parent could not be narrowed: %v", got)
	}
}

func TestMCPLoadOverridesParentProjectAndFiltersTools(t *testing.T) {
	h, parent, ids := projectHandoff(t)
	request := fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"nt_load","arguments":{"agent":"workshop","context":%q,"task":"Game handoff","meta":{"repo-id":"soccer-chess","track":["gameplay","art","gameplay"]}}}}`+"\n", parent)
	r := h.okIn(mcpHello+request, "mcp")
	var c map[string]any
	if err := json.Unmarshal([]byte(mcpText(t, mcpResponses(t, r.out)[1])), &c); err != nil {
		t.Fatal(err)
	}
	ctx := assertProjectSwitch(t, h, parent, c, ids)
	requests := fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"nt_tools","arguments":{"context":%q}}}
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"nt_call","arguments":{"context":%q,"tool":"nine-tails"}}}
{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"nt_call","arguments":{"context":%q,"tool":"soccer-chess"}}}
{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"nt_load","arguments":{"agent":"workshop","context":%q,"meta":{}}}}
`, ctx, ctx, ctx, ctx)
	responses := mcpResponses(t, h.okIn(mcpHello+requests, "mcp").out)
	tools := mcpText(t, responses[1])
	if strings.Contains(tools, "nine-tails checker") || !strings.Contains(tools, "soccer-chess checker") {
		t.Fatalf("discovery ignored resolved scope: %s", tools)
	}
	if responses[2]["result"].(map[string]any)["isError"] != true {
		t.Fatalf("MCP invoked old project tool: %v", responses[2])
	}
	if got := mcpText(t, responses[3]); got != "checked\n" {
		t.Fatalf("MCP new project tool output: %q", got)
	}
	var inherited map[string]any
	if err := json.Unmarshal([]byte(mcpText(t, responses[4])), &inherited); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(inherited["metadata"], c["metadata"]) {
		t.Fatalf("empty load metadata must inherit all keys: %v", inherited)
	}
}
