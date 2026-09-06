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

func mcpInspectResponse(t *testing.T, h *harness, args map[string]any) map[string]any {
	t.Helper()
	payload, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	packet := fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"nt_inspect","arguments":%s}}`+"\n", payload)
	return mcpResponses(t, h.okIn(mcpHello+packet, "mcp").out)[1]
}

func TestMCPInspectPagesUseReceiptScopeAndContinuation(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "a", "Base.")
	want := map[string]bool{}
	for i := 0; i < 20; i++ {
		id := h.ok("remember", "a", "--meta", "repo-id=letters", fmt.Sprintf("Needle memory %02d. %s", i, strings.Repeat("Useful historical context. ", 15))).id(t)
		want[id] = true
	}
	h.ok("remember", "a", "--meta", "repo-id=games", "Needle belongs to another project.")
	h.ok("remember", "b", "--meta", "repo-id=letters", "Needle belongs to another agent.")
	h.ok("remember", "a", "--meta", "repo-id=letters", "Unrelated query content.")
	loaded := h.ok("load", "a", "--meta", "repo-id=letters", "--query", "", "--format", "json").json(t)
	ctx := loaded["context_id"].(string)
	contextRef := loaded["context_ref"].(string)
	before := h.ok("inspect", ctx).json(t)["rendered"]
	args := map[string]any{"page": true, "context": contextRef, "query": "needle", "lane": "recall"}
	seen := map[string]bool{}
	pages := 0
	var fullRef string
	for {
		var page libraryPage
		response := mcpInspectResponse(t, h, args)
		if err := json.Unmarshal([]byte(mcpText(t, response)), &page); err != nil {
			t.Fatal(err)
		}
		pages++
		if page.Agent != "a" || page.Context != ctx || page.Query != "needle" || len(page.Entries) == 0 {
			t.Fatalf("page lost context, query or entries: %+v", page)
		}
		for _, entry := range page.Entries {
			if !want[entry.ID] || seen[entry.ID] {
				t.Fatalf("page leaked scope or repeated a cursor entry: %+v", entry)
			}
			seen[entry.ID] = true
			fullRef = entry.Ref
		}
		if page.Next == nil {
			break
		}
		if pages > 20 || page.Next.After != page.Entries[len(page.Entries)-1].Ref {
			t.Fatalf("invalid continuation: %+v", page.Next)
		}
		args["after"] = page.Next.After
	}
	if pages < 2 || !reflect.DeepEqual(seen, want) {
		t.Fatalf("continuation lost the library: pages=%d seen=%v", pages, seen)
	}
	var full map[string]any
	if err := json.Unmarshal([]byte(mcpText(t, mcpInspectResponse(t, h, map[string]any{"target": fullRef}))), &full); err != nil || !strings.Contains(full["body"].(string), "Useful historical context.") {
		t.Fatalf("ordinary full record inspection failed after paging: %+v, %v", full, err)
	}
	if after := h.ok("inspect", ctx).json(t)["rendered"]; !reflect.DeepEqual(before, after) {
		t.Fatal("paging rewrote the immutable load receipt")
	}
	if mcpInspectResponse(t, h, map[string]any{"page": true, "target": "b", "context": contextRef})["result"].(map[string]any)["isError"] != true {
		t.Fatal("explicit page agent bypassed receipt ownership")
	}
	// Explicit agent paging is still available without ambient context.
	mcpText(t, mcpInspectResponse(t, h, map[string]any{"page": true, "target": "a", "query": "another project"}))
}

func TestMCPInspectPageArgumentsValidateBeforeStore(t *testing.T) {
	h := newHarness(t)
	for _, args := range []map[string]any{
		{},
		{"page": true},
		{"page": false, "context": "ctx_MISSING"},
		{"page": "true", "context": "ctx_MISSING"},
		{"page": nil, "target": "a"},
		{"page": true, "context": ""},
		{"page": true, "target": ""},
		{"page": true, "target": "a", "after": ""},
		{"page": true, "target": "a", "after": []any{}},
		{"page": true, "target": "a", "include": ""},
		{"page": true, "target": "a", "lane": "guidance"},
		{"page": true, "target": "a", "lane": ""},
		{"target": "a", "context": "ctx_MISSING"},
		{"target": "a", "after": "@1"},
		{"target": "@999999", "context": "@999998", "lane": "recall"},
		{"target": "@999999", "context": "@999998", "include": "journal"},
		{"target": "@999999", "context": "@999998", "after": "@999997"},
	} {
		response := mcpInspectResponse(t, h, args)
		if failure, ok := response["error"].(map[string]any); !ok || failure["code"] != float64(-32602) {
			t.Fatalf("invalid page arguments bypassed validation: %+v => %+v", args, response)
		}
	}
	if _, err := os.Stat(filepath.Join(h.home, "nine-tails.db")); !os.IsNotExist(err) {
		t.Fatalf("invalid page arguments opened store: %v", err)
	}
}

func TestMCPInspectPageSyntaxBeforeReferencesAndStore(t *testing.T) {
	for _, args := range []map[string]any{
		{"page": true, "target": "rec_123"},
		{"page": true, "target": "bad name"},
		{"page": true, "target": "@123"},
		{"page": true, "context": "rec_123"},
		{"page": true, "context": "a"},
		{"page": true, "context": "@0"},
		{"page": true, "context": "@123", "after": "ctx_123"},
		{"page": true, "context": "@123", "after": "@0"},
	} {
		h := newHarness(t)
		h.home = filepath.Join(h.home, "unopened")
		response := mcpInspectResponse(t, h, args)
		result, ok := response["result"].(map[string]any)
		if !ok || result["isError"] != true {
			t.Fatalf("invalid page syntax escaped CLI preflight: %+v => %+v", args, response)
		}
		body := result["content"].([]any)[0].(map[string]any)["text"].(string)
		if body == "" || strings.Contains(body, "not found") {
			t.Fatalf("reference resolution masked page syntax: %+v => %s", args, body)
		}
		if _, err := os.Stat(h.home); !os.IsNotExist(err) {
			t.Fatalf("page syntax opened a store: %v", err)
		}
	}
}
