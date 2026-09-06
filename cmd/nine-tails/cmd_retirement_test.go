package main

import (
	"strings"
	"testing"
)

func TestAuditedForgettingRetainsDecisionAndStopsRecall(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "a", "Base.")
	ctx := contextID(t, h.ok("load", "a").out)
	old := h.ok("remember", "--context", ctx, "Passing route defect is unresolved.").id(t)
	if out := h.ok("load", "a", "--task", "passing route").out; !strings.Contains(out, "Passing route defect") {
		t.Fatal("precondition: recall not surfaced")
	}
	reason := "The route defect was fixed and verified; this report has no remaining recovery lesson."
	h.ok("disable", referenceFor(t, h, old), "--context", referenceFor(t, h, ctx), "--reason", reason)
	if out := h.ok("load", "a", "--task", "passing route").out; strings.Contains(out, "Passing route defect") {
		t.Fatal("retired memory still surfaced")
	}
	v := h.ok("inspect", old, "--format", "json").json(t)
	if v["status"] != "disabled" || v["body"] != "Passing route defect is unresolved." {
		t.Fatalf("history lost: %v", v)
	}
	audit := v["retirement"].(map[string]any)
	if audit["reason"] != reason || audit["context"] != ctx || audit["context_ref"] != referenceFor(t, h, ctx) || audit["created_at"] == "" {
		t.Fatalf("retirement decision incomplete: %v", audit)
	}
	if r := h.run("disable", old, "--context", ctx, "--reason", "Overwrite earlier reason"); r.code != 7 {
		t.Fatalf("inactive retirement must conflict: %+v", r)
	}
	if got := h.ok("inspect", old).json(t)["retirement"].(map[string]any)["reason"]; got != reason {
		t.Fatalf("earlier reason changed: %v", got)
	}
}

func TestForgettingValidationHasNoPartialEffect(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "a", "Base.")
	h.ok("base", "b", "Base.")
	ctx := contextID(t, h.ok("load", "a").out)
	other := contextID(t, h.ok("load", "b").out)
	note := h.ok("note", "a", "Keep the exception for young players.").id(t)
	for _, tc := range []struct {
		args []string
		code int
	}{
		{[]string{"--reason", "obsolete"}, 2},
		{[]string{"--context", ctx}, 2},
		{[]string{"--context", ctx, "--reason", "  "}, 2},
		{[]string{"--context", "", "--reason", ""}, 2},
		{[]string{"--context", other, "--reason", "obsolete"}, 2},
		{[]string{"--context", "ctx_999", "--reason", "obsolete"}, 3},
	} {
		args := append([]string{"disable", note}, tc.args...)
		if r := h.run(args...); r.code != tc.code || r.out != "" {
			t.Fatalf("invalid retirement %v: %+v", tc.args, r)
		}
		v := h.ok("inspect", note).json(t)
		if v["status"] != "active" || v["retirement"] != nil {
			t.Fatalf("failed retirement changed record: %v", v)
		}
	}
}

func TestHistoricSuccessorShowsRetirementDecision(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "a", "Base.")
	ctx := contextID(t, h.ok("load", "a").out)
	old := h.ok("note", "a", "Old rule.").id(t)
	current := h.ok("note", "--context", ctx, "--supersedes", old, "Updated rule.").id(t)
	h.ok("disable", current, "--context", ctx, "--reason", "User retired this workflow.")
	v := h.ok("inspect", old).json(t)
	cur := v["current"].(map[string]any)
	if cur["status"] != "disabled" || cur["retirement"].(map[string]any)["reason"] != "User retired this workflow." {
		t.Fatalf("historic repair path lost retirement: %v", cur)
	}
}
