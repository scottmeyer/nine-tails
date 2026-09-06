package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestMCPSuccessDiagnosticsKeepLoadJSONAndConnectionReady(t *testing.T) {
	for _, tc := range []struct {
		name, agent, diagnostic string
		setup                   func(*harness)
		wantSkipped             int
	}{
		{"bootstrap", "pilot", "seeded pilot and reflector", func(*harness) {}, 0},
		{"missing-state", "a", "no active state other/missing", func(h *harness) {
			h.ok("base", "a", "A.")
			h.ok("state", "link", "a/project", "other/missing", "--expect", "none")
		}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			tc.setup(h)
			packet := mcpHello + fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"nt_load","arguments":{"agent":%q}}}
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"nt_inspect","arguments":{"target":%q}}}
{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"nt_load","arguments":{"agent":%q}}}
{"jsonrpc":"2.0","id":5,"method":"ping"}
`, tc.agent, tc.agent, tc.agent)
			r := h.okIn(packet, "mcp")
			responses := mcpResponses(t, r.out)
			if len(responses) != 5 {
				t.Fatalf("lost a response after diagnostics: %s", r.out)
			}
			for _, index := range []int{1, 3} {
				var capsule map[string]any
				text := mcpText(t, responses[index])
				if err := json.Unmarshal([]byte(text), &capsule); err != nil {
					t.Fatalf("successful capsule contains non-JSON diagnostics: %v\n%s", err, text)
				}
				id, ok := capsule["context_id"].(string)
				if !ok || id == "" || len(capsule["skipped"].([]any)) != tc.wantSkipped {
					t.Fatalf("lost receipt or structured diagnostic: %#v", capsule)
				}
			}
			if !json.Valid([]byte(mcpText(t, responses[2]))) || responses[4]["error"] != nil {
				t.Fatalf("connection stopped accepting operations: %s", r.out)
			}
			if !strings.Contains(r.err, tc.diagnostic) {
				t.Fatalf("diagnostics absent from server stderr: %q", r.err)
			}
		})
	}
}

func TestMCPToolSuccessStreamsAndFailureDetails(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "a", "A.")
	ctx := h.ok("load", "a", "--format", "json").id(t)
	for _, tc := range []struct {
		name, script, output string
		failed               bool
	}{
		{"whitespace", "printf '  kept\\n\\n'\nprintf 'diagnostic\\n' >&2\n", "  kept\n\n", false},
		{"empty", "printf 'diagnostic\\n' >&2\n", "", false},
		{"failure", "printf 'partial result\\n'\nprintf 'diagnostic\\n' >&2\nexit 9\n", "partial result", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script := writeScript(t, tc.name+".sh", tc.script)
			h.ok("tool", "add", "a", tc.name, "--script", script, "--description", "Check streams")
			packet := mcpHello + fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"nt_call","arguments":{"context":%q,"tool":%q}}}
{"jsonrpc":"2.0","id":3,"method":"ping"}
`, ctx, tc.name)
			r := h.okIn(packet, "mcp")
			responses := mcpResponses(t, r.out)
			result := responses[1]["result"].(map[string]any)
			text := result["content"].([]any)[0].(map[string]any)["text"].(string)
			if result["isError"] != tc.failed {
				t.Fatalf("wrong operation status: %#v", result)
			}
			if tc.failed {
				for _, detail := range []string{tc.output, "diagnostic", "exited with status 9"} {
					if !strings.Contains(text, detail) {
						t.Fatalf("failure lost %q: %q", detail, text)
					}
				}
			} else if text != tc.output || r.err != "diagnostic\n" {
				t.Fatalf("success streams changed: text=%q stderr=%q", text, r.err)
			}
			if responses[2]["error"] != nil {
				t.Fatalf("connection unusable after tool response: %s", r.out)
			}
		})
	}
}
