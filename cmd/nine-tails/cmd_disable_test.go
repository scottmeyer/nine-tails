package main

import (
	"fmt"
	"strings"
	"testing"
)

// disable retires a record: it leaves every capsule, call and compile, frees
// its name, keeps history, and refuses what has its own lifecycle.
func TestDisableRetiresRecord(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "a", "Base.")
	script := writeScript(t, "x.sh", "echo x\n")
	tool := h.ok("tool", "add", "a", "x", "--script", script, "--description", "X tool").id(t)
	if out := h.ok("load", "a").out; !strings.Contains(out, "- `x`: X tool") {
		t.Fatalf("tool should be advertised first:\n%s", out)
	}
	if got := h.ok("disable", tool).id(t); got != tool {
		t.Fatalf("disable prints the id: %s", got)
	}
	if out := h.ok("load", "a").out; strings.Contains(out, "## Available tools") {
		t.Fatalf("disabled tool still advertised:\n%s", out)
	}
	if r := h.run("call", "--agent", "a", "x"); r.code != 3 {
		t.Fatalf("disabled tool should not resolve: %d %q", r.code, r.err)
	}
	if got := h.ok("inspect", tool).json(t)["status"]; got != "disabled" {
		t.Fatalf("status: %v", got)
	}
	if r := h.ok("inspect", "a", "--all", "--format", "json"); !strings.Contains(r.out, tool) {
		t.Fatalf("inspect --all should still list it")
	}
	// The name is free again.
	again := h.ok("tool", "add", "a", "x", "--script", script, "--description", "X again").id(t)
	if m := h.ok("inspect", again).json(t); m["supersedes"] != nil {
		t.Fatalf("a new definition after disable supersedes nothing: %v", m["supersedes"])
	}

	// Guidance and state retire the same way.
	note := h.ok("note", "a", "Old advice.").id(t)
	h.ok("disable", note)
	if out := h.ok("load", "a").out; strings.Contains(out, "Old advice.") {
		t.Fatalf("disabled guidance still rendered:\n%s", out)
	}
	st := h.ok("state", "put", "a/working", "--expect", "none", "status: busy").id(t)
	h.ok("disable", st)
	if r := h.run("state", "get", "a/working"); r.code != 3 {
		t.Fatalf("disabled state should be gone: %d %q", r.code, r.err)
	}

	// Refusals.
	if r := h.run("disable", tool); r.code != 7 {
		t.Errorf("already disabled: %d %q", r.code, r.err)
	}
	if r := h.run("disable", "rec_999"); r.code != 3 {
		t.Errorf("unknown id: %d %q", r.code, r.err)
	}
	if r := h.run("disable", "nope"); r.code != 2 {
		t.Errorf("not an id: %d %q", r.code, r.err)
	}
	ctx := contextID(t, h.ok("load", "a").out)
	if r := h.run("disable", ctx); r.code != 2 || r.out != "" || !strings.Contains(r.err, "context receipt") {
		t.Errorf("context receipt: code=%d stdout=%q stderr=%q", r.code, r.out, r.err)
	}
	sig := h.ok("signal", "--subject", "wake").id(t)
	if r := h.run("disable", sig); r.code != 2 || !strings.Contains(r.err, "signal ack") {
		t.Errorf("signal: %d %q", r.code, r.err)
	}
}

func TestDisableInvalidatesSingleSourceBrief(t *testing.T) {
	h := newHarness(t)
	base := h.ok("base", "a", "Base.").id(t)
	source := h.ok("prefer", "a", "Obsolete source guidance.").id(t)
	doc := fmt.Sprintf(`input_entries: [%s]
items:
  - key: obsolete
    body: Obsolete compiled guidance.
entries:
  - id: %s
    disposition: represented
    items: [obsolete]
`, source, source)
	first := h.okIn(doc, "brief", "put", "a", "--expect-generation", "none", "--expect-base", base, "--stdin").id(t)
	if out := h.ok("load", "a").out; !strings.Contains(out, "Obsolete compiled guidance.") || strings.Contains(out, "Obsolete source guidance.") {
		t.Fatalf("precondition: compiled brief did not replace source:\n%s", out)
	}

	h.ok("disable", source)
	if out := h.ok("load", "a").out; strings.Contains(out, "Obsolete compiled guidance.") || strings.Contains(out, "Obsolete source guidance.") {
		t.Fatalf("disabled guidance survived capsule invalidation:\n%s", out)
	}
	compiled := h.ok("compile-input", "a")
	if strings.Contains(compiled.out, source) || strings.Contains(compiled.out, "Obsolete") {
		t.Fatalf("disabled guidance survived compiler input:\n%s", compiled.out)
	}
	m := compiled.json(t)
	if m["expect_generation"] == first {
		t.Fatalf("active generation was not replaced: %v", m["expect_generation"])
	}
	active := m["active_generation"].(map[string]any)
	if items := active["items"].([]any); len(items) != 0 {
		t.Fatalf("invalidating generation contains items: %v", items)
	}
	if entries := strs(t, m["input_entries"]); len(entries) != 0 {
		t.Fatalf("disabled single source is still compile input: %v", entries)
	}
}

func TestDisableMixedSourceBriefResurfacesAllSurvivors(t *testing.T) {
	h := newHarness(t)
	base := h.ok("base", "a", "Base.").id(t)
	disabled := h.ok("prefer", "a", "Doomed source guidance.").id(t)
	mixedSurvivor := h.ok("prefer", "a", "Live source guidance.").id(t)
	unaffected := h.ok("note", "a", "Unaffected source guidance.").id(t)
	doc := fmt.Sprintf(`input_entries: [%s, %s, %s]
items:
  - key: mixed
    body: Merged compiled guidance.
  - key: independent
    body: Unaffected compiled guidance.
entries:
  - id: %s
    disposition: represented
    items: [mixed]
  - id: %s
    disposition: represented
    items: [mixed]
  - id: %s
    disposition: represented
    items: [independent]
`, disabled, mixedSurvivor, unaffected, disabled, mixedSurvivor, unaffected)
	h.okIn(doc, "brief", "put", "a", "--expect-generation", "none", "--expect-base", base, "--stdin")
	if out := h.ok("load", "a").out; !strings.Contains(out, "Merged compiled guidance.") || !strings.Contains(out, "Unaffected compiled guidance.") {
		t.Fatalf("precondition: compiled items missing:\n%s", out)
	}

	h.ok("disable", disabled)
	out := h.ok("load", "a").out
	for _, absent := range []string{"Doomed source guidance.", "Merged compiled guidance.", "Unaffected compiled guidance."} {
		if strings.Contains(out, absent) {
			t.Errorf("%q survived invalidation:\n%s", absent, out)
		}
	}
	for _, present := range []string{"Live source guidance.", "Unaffected source guidance."} {
		if !strings.Contains(out, present) {
			t.Errorf("surviving source %q did not resurface:\n%s", present, out)
		}
	}

	compiled := h.ok("compile-input", "a")
	if strings.Contains(compiled.out, disabled) || strings.Contains(compiled.out, "Doomed source guidance.") {
		t.Fatalf("disabled source survived compiler input:\n%s", compiled.out)
	}
	m := compiled.json(t)
	active := m["active_generation"].(map[string]any)
	if items := active["items"].([]any); len(items) != 0 {
		t.Fatalf("invalidating generation contains items: %v", items)
	}
	if got := strs(t, m["input_entries"]); !equal(got, []string{mixedSurvivor, unaffected}) {
		t.Fatalf("compiler inputs = %v, want surviving sources [%s %s]", got, mixedSurvivor, unaffected)
	}
}

func TestDisableUnrepresentedGuidanceDoesNotChurnBrief(t *testing.T) {
	h := newHarness(t)
	base := h.ok("base", "a", "Base.").id(t)
	source := h.ok("prefer", "a", "Compiled source guidance.").id(t)
	doc := fmt.Sprintf(`input_entries: [%s]
items:
  - key: stable
    body: Stable compiled guidance.
entries:
  - id: %s
    disposition: represented
    items: [stable]
`, source, source)
	first := h.okIn(doc, "brief", "put", "a", "--expect-generation", "none", "--expect-base", base, "--stdin").id(t)
	recent := h.ok("note", "a", "Unrepresented recent guidance.").id(t)

	h.ok("disable", recent)
	compiled := h.ok("compile-input", "a")
	m := compiled.json(t)
	if m["expect_generation"] != first {
		t.Fatalf("unrepresented disable replaced generation: got %v want %s", m["expect_generation"], first)
	}
	active := m["active_generation"].(map[string]any)
	items := active["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["body"] != "Stable compiled guidance." {
		t.Fatalf("compiled brief changed: %v", items)
	}
	if strings.Contains(compiled.out, recent) || strings.Contains(compiled.out, "Unrepresented recent guidance.") {
		t.Fatalf("disabled unrepresented guidance survived compiler input:\n%s", compiled.out)
	}
	if out := h.ok("load", "a").out; !strings.Contains(out, "Stable compiled guidance.") || strings.Contains(out, "Unrepresented recent guidance.") {
		t.Fatalf("capsule changed incorrectly:\n%s", out)
	}
}
