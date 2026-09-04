package main

import (
	"fmt"
	"strings"
	"testing"
)

// close is the run's verdict on its receipt: one mark per rendered record,
// ? by default, X only with its correction, once per receipt; tallies then
// reach inspect, compile-input and the lint.
func TestCloseMarksReceipt(t *testing.T) {
	h := newHarness(t)
	base := h.ok("base", "a", "Base.").id(t)
	g1 := h.ok("prefer", "a", "Lead with evidence.").id(t)
	g2 := h.ok("avoid", "a", "Restating the finding.").id(t)
	ctx := h.ok("load", "a", "--format", "json").json(t)["context_id"].(string)

	view := h.ok("inspect", ctx).json(t)
	rendered := view["rendered"].([]any)
	if len(rendered) != 3 {
		t.Fatalf("rendered: %v", rendered)
	}
	first := rendered[0].(map[string]any)
	if first["id"] != base || first["ordinal"] != float64(0) || first["excerpt"] != "Base." || first["kind"] != "agent-base" {
		t.Fatalf("receipt view lacks ordinal and excerpt: %v", first)
	}

	// An X without its correction is refused; a bad mark, an unrendered id
	// and a bad ordinal too.
	if r := h.run("close", ctx, g1+"=X"); r.code != 2 || !strings.Contains(r.err, "needs its correction first") {
		t.Fatalf("X without correction: %d %q", r.code, r.err)
	}
	if r := h.run("close", ctx, g1+"=++"); r.code != 2 {
		t.Fatalf("bad mark: %d %q", r.code, r.err)
	}
	if r := h.run("close", ctx, "rec_999=+"); r.code != 2 || !strings.Contains(r.err, "did not render") {
		t.Fatalf("unrendered id: %d %q", r.code, r.err)
	}
	if r := h.run("close", ctx, "9=+"); r.code != 2 || !strings.Contains(r.err, "ordinal") {
		t.Fatalf("bad ordinal: %d %q", r.code, r.err)
	}
	if r := h.run("close", ctx, "0=+", base+"=+++"); r.code != 2 || !strings.Contains(r.err, "twice") {
		t.Fatalf("duplicate: %d %q", r.code, r.err)
	}

	h.ok("avoid", "a", "--context", ctx, "Leading with evidence buried the finding.")
	r := h.ok("close", ctx, "0=+++", g1+"=X", "--format", "json")
	m := r.json(t)
	marks := m["marks"].(map[string]any)
	if marks[base] != "+++" || marks[g1] != "X" || marks[g2] != "?" || m["closed_at"] == "" {
		t.Fatalf("close result: %s", r.out)
	}
	if r := h.run("close", ctx, "0=+"); r.code != 7 {
		t.Fatalf("a receipt closes once: %d %q", r.code, r.err)
	}
	if got := h.ok("inspect", ctx).json(t)["rendered"].([]any)[0].(map[string]any)["mark"]; got != "+++" {
		t.Fatalf("mark on the receipt view: %v", got)
	}
	if r := h.run("close", "rec_999", "0=+"); r.code != 2 {
		t.Fatalf("not a context id: %d %q", r.code, r.err)
	}
	if r := h.run("close", "ctx_999", "0=+"); r.code != 3 {
		t.Fatalf("unknown context: %d %q", r.code, r.err)
	}

	// A compiled item accumulates a tally that compile-input, inspect and
	// the lint all read.
	doc := fmt.Sprintf("input_entries: [%s, %s]\nitems:\n  - {key: evidence, body: Lead with evidence.}\nentries:\n  - {id: %s, disposition: represented, items: [evidence]}\n  - {id: %s, disposition: deferred}\n", g1, g2, g1, g2)
	res := h.okIn(doc, "brief", "put", "a", "--expect-generation", "none", "--expect-base", base, "--stdin", "--format", "json").json(t)
	item := strs(t, res["items"])[0]
	for i := 0; i < 2; i++ {
		c := h.ok("load", "a", "--format", "json").json(t)["context_id"].(string)
		h.ok("avoid", "a", "--context", c, "Evidence first buried the point.")
		h.ok("close", c, item+"=X")
	}
	for _, mark := range []string{"-", "---", "+"} {
		c := h.ok("load", "a", "--format", "json").json(t)["context_id"].(string)
		h.ok("close", c, item+"="+mark)
	}
	in := h.ok("compile-input", "a").json(t)
	tally := in["active_generation"].(map[string]any)["items"].([]any)[0].(map[string]any)["tally"].(map[string]any)
	if tally["renders"] != float64(5) || tally["closes"] != float64(5) || tally["wrong"] != float64(2) ||
		tally["minus"] != float64(2) || tally["plus"] != float64(1) || tally["minus_weight"] != float64(4) || tally["last_applied"] == nil {
		t.Fatalf("tally: %v", tally)
	}
	brief := h.ok("inspect", "a", "--include", "brief").json(t)["brief"].(map[string]any)
	if tallies := brief["tallies"].(map[string]any); tallies[item].(map[string]any)["wrong"] != float64(2) {
		t.Fatalf("inspect tallies: %v", brief["tallies"])
	}
	lint := h.ok("inspect", "a", "--lint", "condition-loss").json(t)["lint"].([]any)
	var msgs []string
	for _, w := range lint {
		msgs = append(msgs, w.(map[string]any)["message"].(string))
	}
	joined := strings.Join(msgs, "\n")
	if len(lint) != 2 || !strings.Contains(joined, "was marked wrong by 2 runs") || !strings.Contains(joined, "hindered 2 runs and helped 1") {
		t.Fatalf("practice lint: %v", msgs)
	}
	// Unlisted renders after the item's closes stay unknown, not negative.
	c := h.ok("load", "a", "--format", "json").json(t)["context_id"].(string)
	h.ok("close", c)
	in = h.ok("compile-input", "a").json(t)
	tally = in["active_generation"].(map[string]any)["items"].([]any)[0].(map[string]any)["tally"].(map[string]any)
	if tally["unknown"] != float64(1) || tally["closes"] != float64(6) {
		t.Fatalf("unknown mark: %v", tally)
	}
}
