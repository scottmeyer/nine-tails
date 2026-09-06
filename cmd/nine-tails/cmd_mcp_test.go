package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const mcpHello = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}
{"jsonrpc":"2.0","method":"notifications/initialized"}
`

func mcpResponses(t *testing.T, body string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(body), "\n") {
		var r map[string]any
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("invalid MCP stdout: %v\n%s", err, body)
		}
		out = append(out, r)
	}
	return out
}

func mcpText(t *testing.T, r map[string]any) string {
	t.Helper()
	result, ok := r["result"].(map[string]any)
	if !ok {
		t.Fatalf("not a tool result: %#v", r)
	}
	if result["isError"] == true {
		t.Fatalf("tool failed: %#v", result)
	}
	return result["content"].([]any)[0].(map[string]any)["text"].(string)
}

func TestMCPStableCatalogAndProtocolErrors(t *testing.T) {
	h := newHarness(t)
	r := h.okIn(mcpHello+`{"jsonrpc":"2.0","id":"catalog","method":"tools/list"}
{"jsonrpc":"2.0","method":"notifications/future"}
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"nt_load","arguments":{"agent":"a","unexpected":true}}}
{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"missing","arguments":{}}}
{"jsonrpc":"2.0","id":5,"method":"ping"}
`, "mcp")
	responses := mcpResponses(t, r.out)
	if len(responses) != 5 {
		t.Fatalf("notifications received responses: %s", r.out)
	}
	if responses[0]["result"].(map[string]any)["protocolVersion"] != "2025-11-25" {
		t.Fatal(responses[0])
	}
	if got := len(responses[1]["result"].(map[string]any)["tools"].([]any)); got != 7 {
		t.Fatalf("tools=%d", got)
	}
	for _, n := range []int{2, 3} {
		if responses[n]["error"].(map[string]any)["code"] != float64(-32602) {
			t.Fatal(responses[n])
		}
	}
	if _, err := os.Stat(filepath.Join(h.home, "nine-tails.db")); !os.IsNotExist(err) {
		t.Fatalf("protocol-only session touched store: %v", err)
	}
}

func TestMCPLearningUsesReceiptsAndExistingCLI(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "writer", "Write useful prose.")
	h.ok("base", "designer", "Design useful games.")
	loaded := h.okIn(mcpHello+`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"nt_load","arguments":{"agent":"writer","task":"Draft a story","meta":{"repo-id":"story"}}}}
`, "mcp")
	var capsule map[string]any
	if err := json.Unmarshal([]byte(mcpText(t, mcpResponses(t, loaded.out)[1])), &capsule); err != nil {
		t.Fatal(err)
	}
	ctx := capsule["context_id"].(string)
	call := fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"nt_learn","arguments":{"context":%q,"kind":"prefer","body":"Use concrete images."}}}
`, ctx)
	learnings := h.okIn(mcpHello+call, "mcp")
	mcpText(t, mcpResponses(t, learnings.out)[1])
	if !strings.Contains(h.ok("load", "writer").out, "Use concrete images.") {
		t.Fatal("lesson did not surface immediately")
	}
	if strings.Contains(h.ok("load", "designer").out, "Use concrete images.") {
		t.Fatal("lesson leaked into another agent")
	}
	// A missing context is a tool-level failure, not unframed CLI stderr/stdout.
	r := h.okIn(mcpHello+`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"nt_learn","arguments":{"context":"ctx_MISSING","body":"x"}}}
`, "mcp")
	if !mcpResponses(t, r.out)[1]["result"].(map[string]any)["isError"].(bool) {
		t.Fatal(r.out)
	}
}

func TestMCPToolDiscoveryMatchesAgentScope(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "writer", "Write.")
	h.ok("base", "designer", "Design.")
	body := "description: Check a game\nexec:\n  argv: [echo, checked]\n"
	h.okIn(body, "put", "shared", "--lane", "definition", "--kind", "tool", "--name", "check", "--meta", "available-to=designer", "--stdin")
	designer := contextID(t, h.ok("load", "designer", "--meta", "repo-id=game").out)
	writer := contextID(t, h.ok("load", "writer", "--meta", "repo-id=story").out)
	for _, tc := range []struct {
		ctx   string
		count int
	}{{designer, 1}, {writer, 0}} {
		r := h.okIn(mcpHello+fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"nt_tools","arguments":{"context":%q}}}
`, tc.ctx), "mcp")
		var result struct {
			Tools []any `json:"tools"`
		}
		if err := json.Unmarshal([]byte(mcpText(t, mcpResponses(t, r.out)[1])), &result); err != nil {
			t.Fatal(err)
		}
		if len(result.Tools) != tc.count {
			t.Fatal(r.out)
		}
	}
	// An inapplicable owned tool still shadows the shared implementation.
	h.okIn(body, "put", "designer", "--lane", "definition", "--kind", "tool", "--name", "check", "--meta", "repo-id=other", "--stdin")
	r := h.okIn(mcpHello+fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"nt_tools","arguments":{"context":%q}}}
`, designer), "mcp")
	if strings.Contains(mcpText(t, mcpResponses(t, r.out)[1]), "Check a game") {
		t.Fatal("owned scope conflict exposed shadowed shared tool")
	}
}

func TestMCPStateAndMalformedInput(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "a", "A.")
	ctx := contextID(t, h.ok("load", "a").out)
	packet := fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"nt_state","arguments":{"name":"working","context":%q,"body":"phase: building","expect":"none"}}}
`, ctx)
	r := h.okIn(mcpHello+packet, "mcp")
	mcpText(t, mcpResponses(t, r.out)[1])
	if !strings.Contains(h.ok("state", "get", "a/working").out, "building") {
		t.Fatal("state update missing")
	}
	r = h.okIn("not-json\n"+mcpHello+`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"nt_load","arguments":{"agent":[]}}}
`, "mcp")
	responses := mcpResponses(t, r.out)
	if responses[0]["error"].(map[string]any)["code"] != float64(-32700) {
		t.Fatal(r.out)
	}
	if responses[2]["error"].(map[string]any)["code"] != float64(-32602) {
		t.Fatal(r.out)
	}
}

func TestMCPPreservesToolNumbersAndRejectsRoutingAmbiguity(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "a", "A.")
	ctx := contextID(t, h.ok("load", "a").out)
	h.okIn("description: Echo exact value\nexec:\n  argv: [printf, '%s', '{{value}}']\ninput:\n  value:\n    type: number\n    required: true\n", "put", "a", "--lane", "definition", "--kind", "tool", "--name", "exact", "--stdin")
	r := h.okIn(mcpHello+fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"nt_call","arguments":{"context":%q,"tool":"exact","input":{"value":9007199254740993}}}}
`, ctx), "mcp")
	if got := mcpText(t, mcpResponses(t, r.out)[1]); got != "9007199254740993" {
		t.Fatalf("numeric input changed: %s", got)
	}
	for _, value := range []string{"  indented line\n\n", ""} {
		args, _ := json.Marshal(map[string]any{"context": ctx, "tool": "exact", "input": map[string]any{"value": value}})
		packet := mcpHello + fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"nt_call","arguments":%s}}`+"\n", args)
		result := h.okIn(packet, "mcp")
		if got := mcpText(t, mcpResponses(t, result.out)[1]); got != value {
			t.Fatalf("stdout changed: %q != %q", got, value)
		}
	}
	for _, packet := range []string{
		`{"name":"nt_load","arguments":{"agent":"a","meta":{"repo-id=alpha":"beta"}}}`,
		`{"name":"nt_close","arguments":{"context":"--help"}}`,
		`{"name":"nt_inspect","arguments":{"target":"--help"}}`,
	} {
		r := h.okIn(mcpHello+`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":`+packet+"}\n", "mcp")
		if mcpResponses(t, r.out)[1]["result"].(map[string]any)["isError"] != true {
			t.Fatal(r.out)
		}
	}
}

func TestMCPExplicitEmptyStateMetadataClearsScope(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "a", "A.")
	ctx := contextID(t, h.ok("load", "a").out)
	id := h.ok("state", "put", "a/working", "--expect", "none", "--meta", "repo-id=alpha", "phase: building").id(t)
	r := h.okIn(mcpHello+fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"nt_state","arguments":{"name":"working","context":%q,"body":"phase: done","expect":%q,"meta":{}}}}
`, ctx, id), "mcp")
	mcpText(t, mcpResponses(t, r.out)[1])
	if strings.Contains(h.ok("state", "get", "a/working").out, "alpha") {
		t.Fatal("explicit empty metadata did not clear scope")
	}
}
