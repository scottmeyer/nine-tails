package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/scottmeyer/nine-tails/internal/capsule"
	"github.com/scottmeyer/nine-tails/internal/store"
)

func referenceFor(t *testing.T, h *harness, id string) string {
	t.Helper()
	var rows []store.ReferenceView
	if err := json.Unmarshal([]byte(h.ok("refs", "--limit", "1000", "--format", "json").out), &rows); err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.ID == id {
			return row.Ref
		}
	}
	t.Fatalf("no reference for %s", id)
	return ""
}

func TestReferencesDriveLearningAndStateWithoutRewritingData(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "writer", "Write clearly.")
	var c capsule.Capsule
	json.Unmarshal([]byte(h.ok("load", "writer", "--task", "Discuss @999", "--meta", "repo-id=story", "--format", "json").out), &c)
	if c.ContextRef == "" || referenceFor(t, h, c.ContextID) != c.ContextRef {
		t.Fatal(c)
	}
	view := h.ok("inspect", c.ContextRef, "--format", "json").json(t)
	if view["context_id"] != c.ContextID || view["ref"] != c.ContextRef || view["task"] != "Discuss @999" {
		t.Fatal(view)
	}
	old := h.ok("prefer", "--context", c.ContextRef, "--meta", "literal=@999", "Use @999 literally.").id(t)
	oldRef := referenceFor(t, h, old)
	next := h.ok("prefer", "--context", c.ContextRef, "--supersedes", oldRef, "Use @888 literally.").id(t)
	rec := h.ok("inspect", next, "--format", "json").json(t)
	if rec["body"] != "Use @888 literally." || rec["origin_context"] != c.ContextID {
		t.Fatal(rec)
	}
	state := h.ok("state", "put", "working", "--context", c.ContextRef, "--expect", "none", "value: '@999'").id(t)
	nextState := h.ok("state", "put", "working", "--context", c.ContextRef, "--expect", referenceFor(t, h, state), "value: '@888'").id(t)
	if got := h.ok("state", "get", "writer/working", "--format", "id").id(t); got != nextState {
		t.Fatal(got)
	}
	child := h.ok("load", "writer", "--context", c.ContextRef, "--format", "json").json(t)
	if child["parent_context"] != c.ContextID {
		t.Fatal(child)
	}
	h.ok("context", "pin", c.ContextRef)
	h.ok("context", "unpin", c.ContextRef)
	h.ok("close", c.ContextRef)
	h.ok("disable", referenceFor(t, h, next))
	requireExit(t, h.run("note", "--context", oldRef, "wrong type"), 3, "")
	requireExit(t, h.run("inspect", "@999999"), 3, "")
	for _, bad := range []string{"@0", "@01", "@-1", "@1extra", "@"} {
		requireExit(t, h.run("inspect", bad), 2, "")
	}
}

func TestReferencesDescribeSignalsAndGroupProjects(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "coach", "Coach.")
	sig := h.ok("signal", "coach", "--subject", "Soccer follow-up", "--body", "A longer body", "--meta", "repo-id=soccer-chess").id(t)
	ref := referenceFor(t, h, sig)
	h.ok("signal", "coach", "--subject", "Unrelated")
	table := h.ok("refs", "--kind", "signal", "--meta", "repo-id=soccer-chess", "--limit", "1").out
	if !strings.Contains(table, ref) || !strings.Contains(table, "Soccer follow-up") || strings.Contains(table, sig) || strings.Contains(table, "Unrelated") {
		t.Fatal(table)
	}
	var c capsule.Capsule
	json.Unmarshal([]byte(h.ok("load", "coach", "--format", "json").out), &c)
	if len(c.Signals) != 2 || c.Signals[0].ID != sig || c.Signals[0].Ref != ref || c.Signals[0].Inspect != h.command("inspect "+ref) {
		t.Fatal(c.Signals)
	}
	md := h.ok("load", "coach").out
	if !strings.Contains(md, "[signal="+ref+" repo-id=soccer-chess] Soccer follow-up") || !strings.Contains(md, "Loaded: `coach` receipt `@") {
		t.Fatal(md)
	}
	rows := tickRows(t, h.ok("tick", "--agent", "coach", "--claim"))
	var lease string
	for _, row := range rows {
		if row["id"] == sig {
			lease = row["lease_token"].(string)
		}
	}
	h.ok("signal", "ack", ref, "--lease", lease)
	if got := h.ok("refs", "--kind", "signal", "--meta", "repo-id=soccer-chess").out; !strings.Contains(got, "acknowledged") {
		t.Fatal(got)
	}
}

func TestReferencesRespectSelectedHomeAndHelpIsReadOnly(t *testing.T) {
	h := newHarness(t)
	h.ok("refs", "--help")
	entries, err := os.ReadDir(h.home)
	if err != nil || len(entries) != 0 {
		t.Fatalf("help wrote store: %v %v", entries, err)
	}
	other := newHarness(t)
	id := other.ok("base", "other", "Other store.").id(t)
	ref := referenceFor(t, other, id)
	got := h.ok("--home", other.home, "inspect", ref, "--format", "json").json(t)
	if got["id"] != id {
		t.Fatal(got)
	}
	for _, args := range [][]string{{"refs", "--kind", "bogus"}, {"refs", "--limit", "0"}, {"refs", "--format", "bogus"}} {
		requireExit(t, h.run(args...), 2, "")
	}
}

func TestMCPAcceptsReferencesOnlyInIdentitySlots(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "writer", "Write.")
	ctx := contextID(t, h.ok("load", "writer").out)
	ref := referenceFor(t, h, ctx)
	invoke := func(name string, args map[string]any) string {
		t.Helper()
		encoded, _ := json.Marshal(args)
		wire := fmt.Sprintf("{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":{\"name\":%q,\"arguments\":%s}}\n", name, encoded)
		return mcpText(t, mcpResponses(t, h.okIn(mcpHello+wire, "mcp").out)[1])
	}
	var receipt map[string]any
	json.Unmarshal([]byte(invoke("nt_inspect", map[string]any{"target": ref})), &receipt)
	if receipt["context_id"] != ctx {
		t.Fatal(receipt)
	}
	invoke("nt_learn", map[string]any{"context": ref, "kind": "prefer", "body": "Keep @999 literally."})
	if got := h.ok("load", "writer").out; !strings.Contains(got, "Keep @999 literally.") {
		t.Fatal(got)
	}
	state := h.ok("state", "put", "writer/working", "--expect", "none", "status: old").id(t)
	invoke("nt_state", map[string]any{"context": ref, "name": "working", "expect": referenceFor(t, h, state), "body": "status: '@999'"})
	if got := h.ok("state", "get", "writer/working").out; !strings.Contains(got, "@999") {
		t.Fatal(got)
	}
}

func TestMalformedReferencesDoNotCreateAStore(t *testing.T) {
	for _, args := range [][]string{
		{"close", "@0"}, {"note", "--context", "@0", "test"},
		{"state", "put", "coach/state", "--expect", "@0", "x: 1"},
		{"inspect", "@9223372036854775808"},
	} {
		h := newHarness(t)
		requireExit(t, h.run(args...), 2, "")
		entries, err := os.ReadDir(h.home)
		if err != nil || len(entries) != 0 {
			t.Fatalf("%v touched store: %v %v", args, entries, err)
		}
	}
}
