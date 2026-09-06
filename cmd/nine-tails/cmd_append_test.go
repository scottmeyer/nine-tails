package main

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// Scope repair replaces immutable source metadata and invalidates derived
// applicability. An unchanged correction retains coverage; a changed scope
// exposes the corrected source immediately without requiring a compile.
func TestSupersedeGuidanceRetag(t *testing.T) {
	h := newHarness(t)
	base := h.ok("base", "a", "Base.").id(t)
	old := h.ok("note", "a", "--meta", "source=dogfood-review", "Trace the hook runtime separately.").id(t)
	doc := fmt.Sprintf("input_entries: [%s]\nitems:\n  - {key: trace-runtime, body: Trace the hook runtime separately.}\nentries:\n  - {id: %s, disposition: represented, items: [trace-runtime]}\n", old, old)
	res := h.okIn(doc, "brief", "put", "a", "--expect-generation", "none", "--expect-base", base, "--stdin", "--format", "json").json(t)
	if len(res["warnings"].([]any)) != 1 {
		t.Fatalf("the dropped provenance tag should warn first: %v", res["warnings"])
	}

	preserved := h.ok("note", "a", "--supersedes", old, "--format", "json").json(t)
	preservedID := preserved["id"].(string)
	if got := preserved["meta"].(map[string]any)["source"]; !reflect.DeepEqual(got, []any{"dogfood-review"}) {
		t.Fatalf("body-preserving correction lost metadata: %v", preserved)
	}
	if out := h.ok("load", "a").out; strings.Contains(out, "## Recent adjustments") {
		t.Fatalf("same-body correction lost brief coverage:\n%s", out)
	}
	r := h.ok("note", "a", "--supersedes", preservedID, "--clear-meta", "--format", "json")
	m := r.json(t)
	nu := m["id"].(string)
	if m["supersedes"] != preservedID || m["body"] != "Trace the hook runtime separately." || len(m["meta"].(map[string]any)) != 0 {
		t.Fatalf("retag envelope: %s", r.out)
	}
	if got := h.ok("inspect", old).json(t)["status"]; got != "superseded" {
		t.Fatalf("old record status: %v", got)
	}
	if lint := h.ok("inspect", "a", "--lint", "condition-loss").json(t)["lint"].([]any); len(lint) != 0 {
		t.Fatalf("retag should clear the warning: %v", lint)
	}
	if out := h.ok("load", "a").out; !strings.Contains(out, "## Recent adjustments") || strings.Contains(out, "## Working brief") {
		t.Fatalf("changed scope must retire cached applicability:\n%s", out)
	}
	in := h.ok("compile-input", "a").json(t)
	if len(in["input_entries"].([]any)) != 1 || in["input_entries"].([]any)[0] != nu {
		t.Fatalf("corrected source must be available for optional condensation: %v", in)
	}
	if len(in["active_generation"].(map[string]any)["items"].([]any)) != 0 {
		t.Fatal("old derived scope survived repair")
	}

	// A changed body is new guidance: it invalidates the dependent cache and
	// renders as recent, with no obsolete compiled item left to lint.
	r = h.ok("prefer", "a", "--supersedes", nu, "--meta", "repo-id=r1", "Trace the runtime and the shell policy separately.")
	changed := r.id(t)
	if out := h.ok("load", "a").out; !strings.Contains(out, "## Recent adjustments\n\n- `"+referenceFor(t, h, changed)+"` [repo-id=r1] (prefer) Trace the runtime and the shell policy separately.\n") {
		t.Fatalf("a changed body should render as recent:\n%s", out)
	}
	if got := h.ok("inspect", nu).json(t)["status"]; got != "superseded" {
		t.Fatalf("retagged record status after edit: %v", got)
	}
	lint := h.ok("inspect", "a", "--lint", "condition-loss").json(t)["lint"].([]any)
	if len(lint) != 0 {
		t.Fatalf("invalidated items must not be linted: %v", lint)
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

func TestOrdinaryCorrectionsPreserveCompleteScope(t *testing.T) {
	for _, command := range [][]string{{"note"}, {"prefer"}, {"avoid"}, {"remember"}, {"append", "--lane", "guidance"}, {"append", "--lane", "recall"}} {
		t.Run(strings.Join(command, "-"), func(t *testing.T) {
			h := newHarness(t)
			h.ok("base", "a", "Base.")
			ctx := contextID(t, h.ok("load", "a", "--meta", "repo-id=ambient-other", "--meta", "harness=test").out)
			original := append(append([]string{}, command...), "--context", ctx, "--meta", "repo-id=original", "--meta", "language=go", "--meta", "language=typescript", "--meta", "owner=team", "Original lesson")
			old := h.ok(original...).id(t)
			oldMeta := h.ok("inspect", old).json(t)["meta"]
			correction := append(append([]string{}, command...), "--context", ctx, "--supersedes", old, "--format", "json", "Corrected lesson")
			changed := h.ok(correction...).json(t)
			if !reflect.DeepEqual(changed["meta"], oldMeta) || changed["body"] != "Corrected lesson" || changed["origin_context"] != ctx {
				t.Fatalf("correction changed scope or provenance: %+v", changed)
			}
			if !reflect.DeepEqual(h.ok("inspect", old).json(t)["meta"], oldMeta) {
				t.Fatal("correction rewrote predecessor metadata")
			}
			retag := append(append([]string{}, command...), "--context", ctx, "--supersedes", changed["id"].(string), "--meta", "repo-id=new", "--format", "json")
			replaced := h.ok(retag...).json(t)
			if !reflect.DeepEqual(replaced["meta"], map[string]any{"repo-id": []any{"new"}}) || replaced["body"] != "Corrected lesson" {
				t.Fatalf("explicit scope did not replace exactly: %+v", replaced)
			}
			clear := append(append([]string{}, command...), "--context", ctx, "--supersedes", replaced["id"].(string), "--clear-meta", "--format", "json")
			cleared := h.ok(clear...).json(t)
			if len(cleared["meta"].(map[string]any)) != 0 || cleared["body"] != "Corrected lesson" {
				t.Fatalf("explicit clear failed: %+v", cleared)
			}
			fresh := append(append([]string{}, command...), "--context", ctx, "--format", "json", "New unscoped lesson")
			if got := h.ok(fresh...).json(t)["meta"].(map[string]any); len(got) != 0 {
				t.Fatalf("new record inherited ambient scope: %v", got)
			}
		})
	}
}

func TestCorrectionScopeConflictDoesNotReplaceSource(t *testing.T) {
	h := newHarness(t)
	old := h.ok("note", "a", "--meta", "repo-id=original", "Original lesson").id(t)
	for _, args := range [][]string{
		{"note", "a", "--supersedes", old, "--clear-meta", "--meta", "repo-id=new", "Changed"},
		{"note", "a", "--supersedes", old, "--meta", "malformed", "Changed"},
	} {
		if r := h.run(args...); r.code != 2 {
			t.Fatalf("invalid scope flags: %+v", r)
		}
		if current := h.ok("inspect", old).json(t); current["status"] != "active" || current["body"] != "Original lesson" || !reflect.DeepEqual(current["meta"], map[string]any{"repo-id": []any{"original"}}) {
			t.Fatalf("invalid correction mutated source: %+v", current)
		}
	}
	if fresh := h.ok("note", "a", "--clear-meta", "--format", "json", "New lesson").json(t); len(fresh["meta"].(map[string]any)) != 0 {
		t.Fatalf("new --clear-meta record should be unscoped: %+v", fresh)
	}
}
