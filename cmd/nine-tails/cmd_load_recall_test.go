package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/scottmeyer/nine-tails/internal/capsule"
	"gopkg.in/yaml.v3"
)

func TestLoadExplicitRecallAcrossFormatsAndScope(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "a", "Base.")
	oldTool := h.ok("remember", "a", "Old semantic-search tool trial.").id(t)
	chosen := h.ok("remember", "a", "--meta", "repo-id=letters", "Current source relationship proposal.").id(t)
	other := h.ok("remember", "a", "--meta", "repo-id=other", "Different project.").id(t)
	chosenRef := referenceFor(t, h, chosen)
	parent := contextID(t, h.ok("load", "a", "--meta", "repo-id=letters").out)
	for _, format := range []string{"json", "yaml"} {
		r := h.ok("load", "a", "--context", parent, "--task", "semantic learning", "--query", "", "--recall", chosenRef, "--recall", chosen, "--format", format)
		var c capsule.Capsule
		var err error
		if format == "json" {
			err = json.Unmarshal([]byte(r.out), &c)
		} else {
			err = yaml.Unmarshal([]byte(r.out), &c)
		}
		if err != nil || len(c.Recall) != 1 || c.Recall[0].ID != chosen || c.Recall[0].Ref != chosenRef {
			t.Fatalf("%s selected recall: %+v (%v)", format, c, err)
		}
		receipt := h.ok("inspect", c.ContextID).json(t)
		var got []string
		for _, entry := range receipt["rendered"].([]any) {
			r := entry.(map[string]any)
			if r["section"] == "recall" {
				got = append(got, r["id"].(string))
			}
		}
		if !reflect.DeepEqual(got, []string{chosen}) {
			t.Fatalf("receipt lost explicit selection: %v", got)
		}
	}
	if r := h.run("load", "a", "--context", parent, "--recall", chosen, "--recall", other); r.code != 2 {
		t.Fatalf("scope conflict should fail: %+v", r)
	}
	fallback := h.ok("load", "a", "--task", "semantic learning", "--format", "json").json(t)["recall"].([]any)
	if len(fallback) != 1 || fallback[0].(map[string]any)["id"] != oldTool {
		t.Fatalf("omitted selection changed lexical fallback: %+v", fallback)
	}
}

func TestMCPLoadRecallSelectionAndEmptyArray(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "a", "Base.")
	lexical := h.ok("remember", "a", "Old semantic-search tool trial.").id(t)
	chosen := h.ok("remember", "a", "Current source relationship proposal.").id(t)
	chosenRef := referenceFor(t, h, chosen)
	for _, tc := range []struct {
		fields string
		want   string
	}{
		{"", lexical},
		{`,"recall":[]`, ""},
		{fmt.Sprintf(`,"recall":[%q,%q]`, chosenRef, chosen), chosen},
	} {
		packet := fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"nt_load","arguments":{"agent":"a","task":"semantic learning"%s}}}`+"\n", tc.fields)
		response := mcpResponses(t, h.okIn(mcpHello+packet, "mcp").out)[1]
		var c capsule.Capsule
		if err := json.Unmarshal([]byte(mcpText(t, response)), &c); err != nil {
			t.Fatal(err)
		}
		if (tc.want == "" && len(c.Recall) != 0) || (tc.want != "" && (len(c.Recall) != 1 || c.Recall[0].ID != tc.want)) {
			t.Fatalf("MCP selection %s: %+v", tc.fields, c.Recall)
		}
	}
}

func TestMCPRecallArrayValidationDoesNotOpenStore(t *testing.T) {
	h := newHarness(t)
	for _, value := range []string{`null`, `"@1"`, `[1]`, `[""]`, `[false]`, `[{}]`} {
		packet := fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"nt_load","arguments":{"agent":"a","recall":%s}}}`+"\n", value)
		response := mcpResponses(t, h.okIn(mcpHello+packet, "mcp").out)[1]
		if err, ok := response["error"].(map[string]any); !ok || err["code"] != float64(-32602) {
			t.Fatalf("invalid recall array %s: %+v", value, response)
		}
	}
	if _, err := os.Stat(filepath.Join(h.home, "nine-tails.db")); !os.IsNotExist(err) {
		t.Fatalf("invalid recall arrays opened store: %v", err)
	}
}
