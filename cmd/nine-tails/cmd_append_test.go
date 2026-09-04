package main

import (
	"fmt"
	"strings"
	"testing"
)

// --supersedes fixes a record's scope without editing history: the old record
// is superseded, the new one carries exactly the given metadata, and a
// same-body guidance successor keeps the brief coverage so it never renders
// as a recent adjustment and the lint judges the item by the new record.
func TestSupersedeGuidanceRetag(t *testing.T) {
	h := newHarness(t)
	base := h.ok("base", "a", "Base.").id(t)
	old := h.ok("note", "a", "--meta", "source=dogfood-review", "Trace the hook runtime separately.").id(t)
	doc := fmt.Sprintf("input_entries: [%s]\nitems:\n  - {key: trace-runtime, body: Trace the hook runtime separately.}\nentries:\n  - {id: %s, disposition: represented, items: [trace-runtime]}\n", old, old)
	res := h.okIn(doc, "brief", "put", "a", "--expect-generation", "none", "--expect-base", base, "--stdin", "--format", "json").json(t)
	if len(res["warnings"].([]any)) != 1 {
		t.Fatalf("the dropped provenance tag should warn first: %v", res["warnings"])
	}

	r := h.ok("note", "a", "--supersedes", old, "--format", "json")
	m := r.json(t)
	nu := m["id"].(string)
	if m["supersedes"] != old || m["body"] != "Trace the hook runtime separately." || len(m["meta"].(map[string]any)) != 0 {
		t.Fatalf("retag envelope: %s", r.out)
	}
	if got := h.ok("inspect", old).json(t)["status"]; got != "superseded" {
		t.Fatalf("old record status: %v", got)
	}
	if lint := h.ok("inspect", "a", "--lint", "condition-loss").json(t)["lint"].([]any); len(lint) != 0 {
		t.Fatalf("retag should clear the warning: %v", lint)
	}
	if out := h.ok("load", "a").out; strings.Contains(out, "## Recent adjustments") {
		t.Fatalf("a retag must not render as recent:\n%s", out)
	}
	if in := h.ok("compile-input", "a").json(t); len(in["input_entries"].([]any)) != 0 {
		t.Fatalf("a retag must not need compiling: %v", in["input_entries"])
	}

	// A changed body is new guidance: it renders as recent and the item's
	// source resolves to it (unqualified, so no warning either way).
	r = h.ok("prefer", "a", "--supersedes", nu, "--meta", "repo-id=r1", "Trace the runtime and the shell policy separately.")
	changed := r.id(t)
	if out := h.ok("load", "a").out; !strings.Contains(out, "## Recent adjustments\n\n- [repo-id=r1] (prefer) Trace the runtime and the shell policy separately.\n") {
		t.Fatalf("a changed body should render as recent:\n%s", out)
	}
	if got := h.ok("inspect", nu).json(t)["status"]; got != "superseded" {
		t.Fatalf("retagged record status after edit: %v", got)
	}
	lint := h.ok("inspect", "a", "--lint", "condition-loss").json(t)["lint"].([]any)
	if len(lint) != 1 || lint[0].(map[string]any)["key"] != "repo-id" || !equal(strs(t, lint[0].(map[string]any)["sources"]), []string{changed}) {
		t.Fatalf("the item is judged by the latest successor: %v", lint)
	}

	// Refusals: wrong agent or lane, not active, unknown, not an id.
	h.ok("base", "b", "Base.")
	if r := h.run("note", "b", "--supersedes", changed); r.code != 2 || !strings.Contains(r.err, "belongs to a/guidance") {
		t.Errorf("other agent: %d %q", r.code, r.err)
	}
	if r := h.run("remember", "a", "--supersedes", changed); r.code != 2 || !strings.Contains(r.err, "belongs to a/guidance, not a/recall") {
		t.Errorf("other lane: %d %q", r.code, r.err)
	}
	if r := h.run("note", "a", "--supersedes", old); r.code != 7 {
		t.Errorf("superseded predecessor: %d %q", r.code, r.err)
	}
	if r := h.run("note", "a", "--supersedes", "rec_999"); r.code != 3 {
		t.Errorf("unknown predecessor: %d %q", r.code, r.err)
	}
	if r := h.run("note", "a", "--supersedes", "not-an-id"); r.code != 2 {
		t.Errorf("malformed predecessor: %d %q", r.code, r.err)
	}
	// Without --supersedes the text is still required.
	if r := h.run("note", "a"); r.code != 2 {
		t.Errorf("note without text: %d %q", r.code, r.err)
	}
	// --context supplies the agent and the body may still be kept.
	ctx := h.ok("load", "a", "--format", "json").json(t)["context_id"].(string)
	r = h.ok("note", "--context", ctx, "--supersedes", changed, "--meta", "repo-id=r1", "--format", "json")
	if m := r.json(t); m["body"] != "Trace the runtime and the shell policy separately." || m["origin_context"] != ctx {
		t.Fatalf("context retag: %s", r.out)
	}
}
