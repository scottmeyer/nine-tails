package main

import (
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
	sig := h.ok("signal", "--subject", "wake").id(t)
	if r := h.run("disable", sig); r.code != 2 || !strings.Contains(r.err, "signal ack") {
		t.Errorf("signal: %d %q", r.code, r.err)
	}
}
