package main

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/scottmeyer/nine-tails/internal/capsule"
)

func loadLinkedState(t *testing.T, h *harness, args ...string) capsule.Capsule {
	t.Helper()
	var c capsule.Capsule
	r := h.ok(append(append([]string{"load"}, args...), "--format", "json")...)
	if err := json.Unmarshal([]byte(r.out), &c); err != nil {
		t.Fatal(err)
	}
	return c
}

func linkedReceipt(t *testing.T, h *harness, context string) map[string]string {
	t.Helper()
	var c contextView
	if err := json.Unmarshal([]byte(h.ok("inspect", context, "--format", "json").out), &c); err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, r := range c.Rendered {
		if _, exists := ids[r.ID]; exists {
			t.Fatalf("duplicate receipt record %s", r.ID)
		}
		ids[r.ID] = r.Section
	}
	return ids
}

func TestStateLinkSurfacesCurrentDataAndExactReceipts(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "engineer", "Implement the selected task.")
	first := h.ok("state", "put", "workshop/project", "--expect", "none", "--meta", "repo-id=game", "decision: first shared decision").id(t)
	link := h.ok("state", "link", "engineer/project", "workshop/project", "--expect", "none", "--meta", "repo-id=game").id(t)
	alias := h.ok("state", "link", "engineer/decisions", "workshop/project", "--expect", "none", "--meta", "repo-id=game").id(t)
	c := loadLinkedState(t, h, "engineer", "--meta", "repo-id=game")
	if len(c.State) != 1 || c.State[0].Agent != "workshop" || c.State[0].ID != first || c.State[0].Name != "project" || len(c.StateLinks) != 2 {
		t.Fatalf("shared state: %#v / %#v", c.State, c.StateLinks)
	}
	if !reflect.DeepEqual(c.State[0].References, []string{alias, link}) || strings.Contains(c.Instructions, "first shared decision") {
		t.Fatal("data missing lineage or included in instructions")
	}
	ids := linkedReceipt(t, h, c.ContextID)
	if ids[first] != "referenced-state" || ids[link] != "state-links" || ids[alias] != "state-links" || len(ids) != 4 {
		t.Fatalf("receipt: %#v", ids)
	}
	md := h.ok("load", "engineer", "--meta", "repo-id=game").out
	if strings.Count(md, "decision: first shared decision") != 1 || !strings.Contains(md, "### workshop/project (`"+c.State[0].Ref+"`)") || !strings.Contains(md, "Owner: `workshop`") {
		t.Fatalf("duplicate or ownerless state:\n%s", md)
	}
	second := h.ok("state", "put", "workshop/project", "--expect", first, "decision: revised shared decision").id(t)
	later := loadLinkedState(t, h, "engineer", "--meta", "repo-id=game")
	if len(later.State) != 1 || later.State[0].ID != second || !strings.Contains(later.State[0].Body, "revised") {
		t.Fatal("link did not follow current named version")
	}
	if !reflect.DeepEqual(ids, linkedReceipt(t, h, c.ContextID)) || linkedReceipt(t, h, later.ContextID)[first] != "" {
		t.Fatal("historical receipt changed or later receipt retained stale version")
	}
	var decoded capsule.Capsule
	if err := yaml.Unmarshal([]byte(h.ok("load", "engineer", "--meta", "repo-id=game", "--format", "yaml").out), &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(later.State, decoded.State) || !reflect.DeepEqual(later.StateLinks, decoded.StateLinks) {
		t.Fatal("JSON/YAML lineage differs")
	}
	// A self-link is still visible but cannot duplicate the already-rendered state.
	h.ok("base", "workshop", "Keep project decisions.")
	self := h.ok("state", "link", "workshop/alias", "workshop/project", "--expect", "none").id(t)
	own := loadLinkedState(t, h, "workshop", "--meta", "repo-id=game")
	if len(own.State) != 1 || !reflect.DeepEqual(own.State[0].References, []string{self}) || linkedReceipt(t, h, own.ContextID)[second] != "state" {
		t.Fatal("self-link duplicated state or receipt")
	}
	if md := h.ok("load", "workshop", "--meta", "repo-id=game").out; strings.Count(md, "decision: revised shared decision") != 1 {
		t.Fatal("self-link duplicated body")
	}
}

func TestStateLinkRequiresBothScopesAndHonorsProjectSwitch(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "engineer", "Build.")
	h.ok("base", "pilot-test", "Delegate.")
	state := h.ok("state", "put", "workshop/project", "--expect", "none", "--meta", "repo-id=game", "secret: game-only-value").id(t)
	link := h.ok("state", "link", "engineer/project", "workshop/project", "--expect", "none", "--meta", "phase=build").id(t)
	for _, metadata := range [][]string{{"repo-id=other", "phase=build"}, {"repo-id=game", "phase=review"}} {
		args := []string{"engineer"}
		for _, m := range metadata {
			args = append(args, "--meta", m)
		}
		c := loadLinkedState(t, h, args...)
		if len(c.State) != 0 || len(c.StateLinks) != 0 || len(c.Skipped) != 0 || linkedReceipt(t, h, c.ContextID)[state] != "" || linkedReceipt(t, h, c.ContextID)[link] != "" {
			t.Fatalf("scope leak: %#v", c)
		}
		md := h.ok(append([]string{"load"}, args...)...).out
		if strings.Contains(md, "game-only-value") || strings.Contains(md, state) || strings.Contains(md, link) {
			t.Fatal("scope leaked into Markdown")
		}
	}
	parent := loadLinkedState(t, h, "pilot-test", "--meta", "repo-id=other", "--meta", "phase=build")
	c := loadLinkedState(t, h, "engineer", "--context", parent.ContextID, "--meta", "repo-id=game")
	if len(c.State) != 1 || c.State[0].ID != state {
		t.Fatal("explicit child project switch did not resolve scoped state")
	}
	// Missing invocation metadata is unknown, not a conflict, as for ordinary state.
	if len(loadLinkedState(t, h, "engineer").State) != 1 {
		t.Fatal("link invented stricter scope semantics")
	}
}

func TestStateLinkMissingDisabledAndOneHop(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "engineer", "Build.")
	link := h.ok("state", "link", "engineer/project", "middle/project", "--expect", "none").id(t)
	h.ok("state", "link", "middle/project", "workshop/project", "--expect", "none")
	state := h.ok("state", "put", "workshop/project", "--expect", "none", "value: do not recurse").id(t)
	c := loadLinkedState(t, h, "engineer")
	if len(c.State) != 0 || len(c.StateLinks) != 0 || len(c.Skipped) != 1 || c.Skipped[0].ID != link {
		t.Fatalf("missing state or recursive resolution: %#v", c)
	}
	if ids := linkedReceipt(t, h, c.ContextID); ids[link] != "" || ids[state] != "" {
		t.Fatal("receipt claims unresolved data")
	}
	md := h.ok("load", "engineer").out
	if !strings.Contains(md, "Unresolved state link `"+c.Skipped[0].Ref+"`") || !strings.Contains(md, "nine-tails inspect "+c.Skipped[0].Ref) || !strings.Contains(md, "no active state middle/project") || strings.Contains(md, "do not recurse") {
		t.Fatal(md)
	}
	// Arbitrary YAML pointer fields remain data; only the explicit definition resolves.
	actual := h.ok("state", "put", "middle/project", "--expect", "none", "state_pointer: workshop/project").id(t)
	c = loadLinkedState(t, h, "engineer")
	if len(c.State) != 1 || c.State[0].ID != actual || strings.Contains(c.State[0].Body, "do not recurse") {
		t.Fatal("parsed state body as reference")
	}
	h.ok("disable", actual)
	if len(loadLinkedState(t, h, "engineer").Skipped) != 1 {
		t.Fatal("disabled target still resolved")
	}
	h.ok("disable", link)
	c = loadLinkedState(t, h, "engineer")
	if len(c.StateLinks) != 0 || len(c.Skipped) != 0 || strings.Contains(h.ok("load", "engineer").out, "Referenced state (data") {
		t.Fatal("disabled link remains selected")
	}
}

func TestStateLinkCASValidationAndOwnership(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "engineer", "Build.")
	ctx := loadLinkedState(t, h, "engineer", "--meta", "repo-id=ambient").ContextID
	for _, args := range [][]string{
		{"state", "link", "engineer/project", "workshop/project"},
		{"state", "link", "project", "workshop/project", "--expect", "none"},
		{"state", "link", "other/project", "workshop/project", "--expect", "none", "--context", ctx},
	} {
		requireExit(t, h.run(args...), 2, "")
	}
	for _, target := range []string{"workshop", "../project", "workshop/a/b", "workshop/project\nextra: data", "workshop/", "/project"} {
		requireExit(t, h.run("state", "link", "engineer/bad", target, "--expect", "none"), 2, "")
		requireExit(t, h.run("put", "engineer", "--lane", "definition", "--kind", "state-link", "--name", "bad", target), 2, "")
	}
	link := h.ok("state", "link", "project", "workshop/first", "--context", ctx, "--expect", "none", "--meta", "repo-id=game", "--format", "json")
	old := link.id(t)
	if link.json(t)["origin_context"] != ctx {
		t.Fatal("missing origin")
	}
	requireExit(t, h.run("state", "link", "engineer/project", "workshop/second", "--expect", "none"), 7, "expected")
	next := h.ok("state", "link", "engineer/project", "workshop/second", "--expect", old, "--format", "json")
	if next.id(t) == old || len(next.json(t)["meta"].(map[string]any)) != 0 || next.json(t)["supersedes"] != old {
		t.Fatal("link did not use normal immutable definition replacement")
	}
	requireExit(t, h.run("state", "link", "engineer/project", "workshop/third", "--expect", old), 7, "expected")
	if h.ok("inspect", old, "--format", "json").json(t)["body"] != "workshop/first" {
		t.Fatal("historical link changed")
	}
	requireExit(t, h.run("state", "get", "workshop/second"), 3, "no active state")
}

func TestStateLinkExportImportDoesNotCopyTarget(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "engineer", "Build.")
	h.ok("state", "put", "workshop/project", "--expect", "none", "value: authoritative-foreign-data")
	old := h.ok("state", "link", "engineer/project", "workshop/project", "--expect", "none", "--meta", "repo-id=game").id(t)
	if got := h.ok("inspect", "engineer", "--include", "state", "--format", "json").json(t)["state"].([]any); len(got) != 1 || got[0].(map[string]any)["kind"] != "state-link" {
		t.Fatal("state inspection omitted link definition")
	}
	doc := h.ok("export", "engineer", "--include", "base,state").out
	if !strings.Contains(doc, "state-link") || strings.Contains(doc, "authoritative-foreign-data") {
		t.Fatal("export missed reference or copied foreign state")
	}
	dest := newHarness(t)
	imported := dest.okIn(doc, "import", "--stdin", "--format", "json").json(t)["ids"].(map[string]any)[old].(string)
	c := loadLinkedState(t, dest, "engineer", "--meta", "repo-id=game")
	if len(c.Skipped) != 1 || c.Skipped[0].ID != imported {
		t.Fatal("import did not preserve missing reference")
	}
	dest.ok("state", "put", "workshop/project", "--expect", "none", "value: destination-truth")
	if got := loadLinkedState(t, dest, "engineer", "--meta", "repo-id=game").State; len(got) != 1 || got[0].Body != "value: destination-truth" {
		t.Fatal("import did not resolve destination state")
	}
	bad := strings.Replace(doc, "workshop/project", "invalid-target", 1)
	requireExit(t, dest.runIn(bad, "import", "--stdin"), 2, "target")
	if got := loadLinkedState(t, dest, "engineer", "--meta", "repo-id=game").StateLinks; len(got) != 1 || got[0].ID != imported {
		t.Fatal("invalid import partially replaced link")
	}
}

func TestStateLinkAcceptsTypedLocalReferences(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "engineer", "Build.")
	c := loadLinkedState(t, h, "engineer")
	created := h.ok("state", "link", "project", "workshop/project", "--context", c.ContextRef, "--expect", "none").id(t)
	ref := h.ok("inspect", created, "--format", "json").json(t)["ref"].(string)
	next := h.ok("state", "link", "project", "workshop/updated", "--context", c.ContextRef, "--expect", ref, "--format", "json").json(t)
	if next["origin_context"] != c.ContextID || next["supersedes"] != created {
		t.Fatal("local references changed canonical lineage")
	}
	requireExit(t, h.run("state", "link", "project", "workshop/project", "--context", ref, "--expect", "none"), 3, "context")
	requireExit(t, h.run("state", "link", "project", "workshop/project", "--context", c.ContextRef, "--expect", c.ContextRef), 7, "expected")
}

func TestMCPStateLinksUseExistingMenuAndDefinitionScope(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "engineer", "Build.")
	h.ok("state", "put", "workshop/project", "--expect", "none", "phase: current")
	ctx := loadLinkedState(t, h, "engineer", "--meta", "repo-id=ambient").ContextID
	call := func(args map[string]any) map[string]any {
		t.Helper()
		body, _ := json.Marshal(args)
		packet := mcpHello + fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"nt_state","arguments":%s}}`+"\n", body)
		return mcpResponses(t, h.okIn(packet, "mcp").out)[1]
	}
	var rec map[string]any
	created := call(map[string]any{"name": "project", "target": "workshop/project", "context": ctx, "expect": "none", "meta": map[string]any{"repo-id": "game"}})
	if err := json.Unmarshal([]byte(mcpText(t, created)), &rec); err != nil {
		t.Fatal(err)
	}
	if rec["kind"] != "state-link" || rec["origin_context"] != ctx {
		t.Fatal(rec)
	}
	for _, args := range []map[string]any{
		{"name": "engineer/project", "target": "workshop/project"},
		{"name": "engineer/project", "target": "", "expect": "none"},
		{"name": "engineer/project", "target": "workshop/project", "body": "x: y", "expect": "none"},
	} {
		if call(args)["result"].(map[string]any)["isError"] != true {
			t.Fatal("invalid MCP link accepted")
		}
	}
	for _, withMeta := range []bool{true, false} {
		args := map[string]any{"name": "engineer/project", "target": "workshop/project", "expect": rec["id"]}
		if withMeta {
			args["meta"] = map[string]any{}
		}
		if err := json.Unmarshal([]byte(mcpText(t, call(args))), &rec); err != nil {
			t.Fatal(err)
		}
		if len(rec["meta"].(map[string]any)) != 0 {
			t.Fatal("link inherited old or context scope")
		}
	}
	if len(loadLinkedState(t, h, "engineer").State) != 1 {
		t.Fatal("MCP link did not surface")
	}
}
