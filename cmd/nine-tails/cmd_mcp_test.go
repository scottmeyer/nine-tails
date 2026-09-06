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

func TestMCPRejectsFractionalIDsButPreservesExactIntegerLiterals(t *testing.T) {
	h := newHarness(t)
	r := h.okIn(`{"jsonrpc":"2.0","id":1.5,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}
{"jsonrpc":"2.0","id":900719925474099312345678901234567890,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}
`, "mcp")
	responses := mcpResponses(t, r.out)
	if responses[0]["error"].(map[string]any)["code"] != float64(-32600) {
		t.Fatalf("fractional id accepted: %s", r.out)
	}
	if !strings.Contains(r.out, `"id":900719925474099312345678901234567890`) {
		t.Fatalf("large integer id changed: %s", r.out)
	}
	for _, literal := range []string{"1.0", "1e2", "1.20e1", "100e-2", "1200e-2", "1e999999999999999999999", "-0", "0e-999999999999999999999"} {
		if !validIntegerJSONNumber(literal) {
			t.Fatalf("integer literal rejected: %s", literal)
		}
	}
	for _, literal := range []string{"1e-2", "1.5", "1.01e1", "1e-999999999999999999999"} {
		if validIntegerJSONNumber(literal) {
			t.Fatalf("fractional literal accepted: %s", literal)
		}
	}
}

func TestMCPPreflightRejectsSyntaxBeforeResolvingLocalReferences(t *testing.T) {
	for _, tc := range []struct{ name, arguments string }{
		{"nt_load", `{"agent":"-bad","context":"@1"}`},
		{"nt_load", `{"agent":"a","context":"@1","meta":{"bad key":"x"}}`},
		{"nt_load", `{"agent":"a","context":"@1","recall":["bad"]}`},
		{"nt_call", `{"tool":"-bad","context":"@1"}`},
		{"nt_call", `{"tool":"bad tool","context":"@1"}`},
		{"nt_learn", `{"body":"x","supersedes":"bad","context":"@1"}`},
		{"nt_state", `{"name":"state","body":"x","context":"@1"}`},
		{"nt_state", `{"name":"bad name","target":"owner/state","expect":"none","context":"@1"}`},
		{"nt_state", `{"name":"state","expect":"none","context":"@1"}`},
		{"nt_state", `{"name":"state","meta":{},"context":"@1"}`},
	} {
		h := newHarness(t)
		r := h.okIn(mcpHello+fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":%q,"arguments":%s}}
`, tc.name, tc.arguments), "mcp")
		result := mcpResponses(t, r.out)[1]["result"].(map[string]any)
		if result["isError"] != true || strings.Contains(result["content"].([]any)[0].(map[string]any)["text"].(string), "@1") {
			t.Fatalf("syntax was masked by reference resolution: %s", r.out)
		}
		if _, err := os.Stat(filepath.Join(h.home, "nine-tails.db")); !os.IsNotExist(err) {
			t.Fatalf("preflight opened store: %v", err)
		}
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

func TestMCPLearningCorrectionScope(t *testing.T) {
	for _, tc := range []struct {
		name string
		args map[string]any
		want map[string]any
	}{
		{"omitted", nil, map[string]any{"repo-id": []any{"original"}, "language": []any{"go", "typescript"}}},
		{"false-preserves", map[string]any{"clear_meta": false}, map[string]any{"repo-id": []any{"original"}, "language": []any{"go", "typescript"}}},
		{"replacement", map[string]any{"meta": map[string]any{"repo-id": "new"}}, map[string]any{"repo-id": []any{"new"}}},
		{"false-and-replacement", map[string]any{"clear_meta": false, "meta": map[string]any{"repo-id": "new"}}, map[string]any{"repo-id": []any{"new"}}},
		{"empty", map[string]any{"meta": map[string]any{}}, map[string]any{}},
		{"false-and-empty", map[string]any{"clear_meta": false, "meta": map[string]any{}}, map[string]any{}},
		{"clear", map[string]any{"clear_meta": true}, map[string]any{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.ok("base", "a", "A.")
			ctx := contextID(t, h.ok("load", "a", "--meta", "repo-id=ambient-other").out)
			old := h.ok("note", "a", "--meta", "repo-id=original", "--meta", "language=go", "--meta", "language=typescript", "Original lesson").id(t)
			args := map[string]any{"context": ctx, "body": "Corrected lesson", "supersedes": old}
			for k, v := range tc.args {
				args[k] = v
			}
			packet, err := json.Marshal(args)
			if err != nil {
				t.Fatal(err)
			}
			r := h.okIn(mcpHello+fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"nt_learn","arguments":%s}}`+"\n", packet), "mcp")
			var record map[string]any
			if err := json.Unmarshal([]byte(mcpText(t, mcpResponses(t, r.out)[1])), &record); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(record["meta"], tc.want) || record["body"] != "Corrected lesson" || record["supersedes"] != old || record["origin_context"] != ctx {
				t.Fatalf("wrong replacement envelope: %+v", record)
			}
			if h.ok("inspect", old).json(t)["status"] != "superseded" {
				t.Fatal("predecessor was not replaced")
			}
		})
	}
}

func TestMCPLearningNewScopeAndInvalidClear(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "a", "A.")
	ctx := contextID(t, h.ok("load", "a", "--meta", "repo-id=ambient-other").out)
	old := h.ok("note", "a", "--meta", "repo-id=original", "Original lesson").id(t)
	for _, fields := range []string{
		`"clear_meta":"true"`, `"clear_meta":1`, `"clear_meta":null`, `"clear_meta":{}`, `"clear_meta":[]`,
		`"clear_meta":true,"meta":{}`, `"clear_meta":true,"meta":{"repo-id":"new"}`,
	} {
		packet := fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"nt_learn","arguments":{"context":%q,"body":"Changed","supersedes":%q,%s}}}`+"\n", ctx, old, fields)
		r := h.okIn(mcpHello+packet, "mcp")
		response := mcpResponses(t, r.out)[1]
		if failure, ok := response["error"].(map[string]any); !ok || failure["code"] != float64(-32602) {
			t.Fatalf("invalid clear arguments were not rejected by protocol validation: %s\n%s", fields, r.out)
		}
		if record := h.ok("inspect", old).json(t); record["status"] != "active" || record["body"] != "Original lesson" || !reflect.DeepEqual(record["meta"], map[string]any{"repo-id": []any{"original"}}) {
			t.Fatalf("invalid correction changed predecessor: %+v", record)
		}
	}
	for _, fields := range []string{"", `,"clear_meta":false`, `,"clear_meta":true`, `,"meta":{}`} {
		packet := fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"nt_learn","arguments":{"context":%q,"body":"New lesson"%s}}}`+"\n", ctx, fields)
		r := h.okIn(mcpHello+packet, "mcp")
		var record map[string]any
		if err := json.Unmarshal([]byte(mcpText(t, mcpResponses(t, r.out)[1])), &record); err != nil {
			t.Fatal(err)
		}
		if len(record["meta"].(map[string]any)) != 0 {
			t.Fatalf("new learning inherited ambient scope: %+v", record)
		}
	}
}

func TestMCPLearningMetadataOnlyCorrectionKeepsBody(t *testing.T) {
	for _, tc := range []struct {
		name string
		args map[string]any
		want map[string]any
	}{
		{"preserve", nil, map[string]any{"repo-id": []any{"original"}, "language": []any{"go"}}},
		{"exact", map[string]any{"meta": map[string]any{"repo-id": "new"}}, map[string]any{"repo-id": []any{"new"}}},
		{"clear", map[string]any{"clear_meta": true}, map[string]any{}},
		{"empty", map[string]any{"meta": map[string]any{}}, map[string]any{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.ok("base", "a", "A.")
			ctx := contextID(t, h.ok("load", "a", "--meta", "repo-id=ambient-other").out)
			const body = "Original line.\n\nKeep this exact paragraph."
			old := h.ok("prefer", "a", "--meta", "repo-id=original", "--meta", "language=go", body).id(t)
			args := map[string]any{"context": ctx, "supersedes": old}
			for k, v := range tc.args {
				args[k] = v
			}
			packet, err := json.Marshal(args)
			if err != nil {
				t.Fatal(err)
			}
			r := h.okIn(mcpHello+fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"nt_learn","arguments":%s}}`+"\n", packet), "mcp")
			var record map[string]any
			if err := json.Unmarshal([]byte(mcpText(t, mcpResponses(t, r.out)[1])), &record); err != nil {
				t.Fatal(err)
			}
			if record["body"] != body || record["kind"] != "prefer" || !reflect.DeepEqual(record["meta"], tc.want) || record["supersedes"] != old || record["origin_context"] != ctx {
				t.Fatalf("metadata-only correction lost text, scope, kind or provenance: %+v", record)
			}
			if historical := h.ok("inspect", old).json(t); historical["body"] != body || historical["status"] != "superseded" {
				t.Fatalf("metadata-only correction rewrote history: %+v", historical)
			}
		})
	}
}

func TestMCPLearningRejectsMissingBodyOrPredecessorBeforeStore(t *testing.T) {
	h := newHarness(t)
	for _, fields := range []string{"", `,"meta":{}`, `,"clear_meta":true`, `,"supersedes":""`, `,"body":""`, `,"body":null`, `,"body":"","supersedes":"rec_MISSING"`} {
		packet := fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"nt_learn","arguments":{"context":"ctx_MISSING"%s}}}`+"\n", fields)
		r := h.okIn(mcpHello+packet, "mcp")
		response := mcpResponses(t, r.out)[1]
		if failure, ok := response["error"].(map[string]any); !ok || failure["code"] != float64(-32602) {
			t.Fatalf("invalid body omission did not fail protocol validation: %s\n%s", fields, r.out)
		}
		if _, err := os.Stat(filepath.Join(h.home, "nine-tails.db")); !os.IsNotExist(err) {
			t.Fatalf("invalid body omission touched store: %v", err)
		}
	}
}

func TestMCPLearningMetadataOnlyPreservesTypeAndBoundaries(t *testing.T) {
	for _, tc := range []struct {
		command []string
		kind    string
		lane    string
	}{
		{[]string{"note"}, "note", "guidance"},
		{[]string{"prefer"}, "prefer", "guidance"},
		{[]string{"avoid"}, "avoid", "guidance"},
		{[]string{"remember"}, "memory", "recall"},
		{[]string{"append", "--lane", "guidance", "--kind", "principle"}, "principle", "guidance"},
		{[]string{"append", "--lane", "recall", "--kind", "incident"}, "incident", "recall"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			h := newHarness(t)
			h.ok("base", "a", "A.")
			h.ok("base", "b", "B.")
			ctx := contextID(t, h.ok("load", "a").out)
			other := contextID(t, h.ok("load", "b").out)
			old := h.ok(append(append([]string{}, tc.command...), "a", "Exact lesson")...).id(t)
			call := func(context string, extra string) map[string]any {
				packet := fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"nt_learn","arguments":{"context":%q,"supersedes":%q%s}}}`+"\n", context, old, extra)
				return mcpResponses(t, h.okIn(mcpHello+packet, "mcp").out)[1]
			}
			if call(other, "")["result"].(map[string]any)["isError"] != true {
				t.Fatal("metadata-only inference bypassed predecessor ownership")
			}
			crossLaneKind := "remember"
			if tc.lane == "recall" {
				crossLaneKind = "note"
			}
			if call(ctx, fmt.Sprintf(`,"kind":%q`, crossLaneKind))["result"].(map[string]any)["isError"] != true {
				t.Fatal("explicit metadata-only kind change crossed a lane boundary")
			}
			var record map[string]any
			if err := json.Unmarshal([]byte(mcpText(t, call(ctx, `,"meta":{"repo-id":"new"}`))), &record); err != nil {
				t.Fatal(err)
			}
			if record["kind"] != tc.kind || record["lane"] != tc.lane || record["body"] != "Exact lesson" || !reflect.DeepEqual(record["meta"], map[string]any{"repo-id": []any{"new"}}) {
				t.Fatalf("metadata-only repair changed meaning: %+v", record)
			}
			if call(ctx, "")["result"].(map[string]any)["isError"] != true {
				t.Fatal("metadata-only inference forwarded an inactive predecessor")
			}
		})
	}
}

func TestMCPLearningUnchangedCorrectionKeepsBriefCoverage(t *testing.T) {
	h := newHarness(t)
	base := h.ok("base", "a", "A.").id(t)
	old := h.ok("avoid", "a", "Duplicating mutable project status.").id(t)
	doc := fmt.Sprintf("input_entries: [%s]\nitems:\n  - {key: mutable-status, body: Keep current project status in one place.}\nentries:\n  - {id: %s, disposition: represented, items: [mutable-status]}\n", old, old)
	h.okIn(doc, "brief", "put", "a", "--expect-generation", "none", "--expect-base", base, "--stdin")
	before := h.ok("compile-input", "a").json(t)["active_generation"].(map[string]any)["id"]
	ctx := contextID(t, h.ok("load", "a").out)
	packet := fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"nt_learn","arguments":{"context":%q,"supersedes":%q}}}`+"\n", ctx, old)
	response := mcpResponses(t, h.okIn(mcpHello+packet, "mcp").out)[1]
	mcpText(t, response)
	if after := h.ok("compile-input", "a").json(t)["active_generation"].(map[string]any)["id"]; after != before {
		t.Fatal("omitted body/kind/scope changed the brief generation")
	}
	if out := h.ok("load", "a").out; strings.Contains(out, "## Recent adjustments") || !strings.Contains(out, "Keep current project status in one place.") {
		t.Fatalf("unchanged metadata-only correction lost brief coverage:\n%s", out)
	}
}

func TestMCPLearningExplicitKindAndBodyPresentDefault(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "a", "A.")
	ctx := contextID(t, h.ok("load", "a").out)
	old := h.ok("avoid", "a", "--meta", "repo-id=original", "Original lesson").id(t)
	for _, tc := range []struct {
		fields string
		kind   string
		body   string
	}{
		{`,"kind":"prefer"`, "prefer", "Original lesson"},
		{`,"body":"Corrected lesson"`, "note", "Corrected lesson"},
	} {
		packet := fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"nt_learn","arguments":{"context":%q,"supersedes":%q%s}}}`+"\n", ctx, old, tc.fields)
		response := mcpResponses(t, h.okIn(mcpHello+packet, "mcp").out)[1]
		var record map[string]any
		if err := json.Unmarshal([]byte(mcpText(t, response)), &record); err != nil {
			t.Fatal(err)
		}
		if record["kind"] != tc.kind || record["body"] != tc.body || !reflect.DeepEqual(record["meta"], map[string]any{"repo-id": []any{"original"}}) {
			t.Fatalf("explicit kind or body-present default changed: %+v", record)
		}
		old = record["id"].(string)
	}
}
