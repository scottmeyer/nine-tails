package main

import (
	"strings"
	"testing"
)

// A fresh store bootstraps itself: load pilot seeds pilot and reflector from
// the binary, exactly once, and never touches an agent that already exists.
func TestLoadPilotSeedsFreshStore(t *testing.T) {
	h := newHarness(t)
	if r := h.run("agents"); r.out != "" {
		t.Fatalf("fresh store should list no agents: %q", r.out)
	}
	r := h.ok("load", "pilot", "--task", "start", "--meta", "repo-id=x", "--meta", "harness=test")
	if !strings.HasPrefix(r.out, "# nine-tails Pilot\n\n[nine-tails-context=ctx_") {
		t.Fatalf("pilot capsule:\n%s", r.out)
	}
	for _, want := range []string{"## The loop", "## Adopting an existing agent file", "## Available agents\n\n- `reflector`: "} {
		if !strings.Contains(r.out, want) {
			t.Errorf("pilot capsule lacks %q:\n%s", want, r.out)
		}
	}
	if r.err != "nine-tails: seeded pilot and reflector from the built-in starter (ordinary agents; edit with nine-tails base <agent>)\n" {
		t.Fatalf("seed notice: %q", r.err)
	}
	if r := h.ok("agents"); r.out != "pilot\nreflector\n" {
		t.Fatalf("agents after seeding: %q", r.out)
	}
	// Reflector is loadable straight away, and a second pilot load is silent.
	if r := h.ok("load", "reflector"); !strings.HasPrefix(r.out, "# Reflector\n") {
		t.Fatalf("reflector capsule:\n%s", r.out)
	}
	if r := h.ok("load", "pilot"); r.err != "" {
		t.Fatalf("second load must not seed again: %q", r.err)
	}
	// The seeded pilot is an ordinary agent: correctable and replaceable.
	ctx := contextID(t, h.ok("load", "pilot").out)
	h.ok("note", "--context", ctx, "Also run make build first in this repo.")
	if r := h.ok("load", "pilot"); !strings.Contains(r.out, "## Recent adjustments\n\n- (note) Also run make build first in this repo.") {
		t.Fatalf("correction should render on the pilot:\n%s", r.out)
	}
}

func TestLoadPilotKeepsExistingAgents(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "reflector", "Mine, not the starter.")
	r := h.ok("load", "pilot")
	if r.err != "nine-tails: seeded pilot from the built-in starter (ordinary agents; edit with nine-tails base <agent>)\n" {
		t.Fatalf("seed notice with existing reflector: %q", r.err)
	}
	if r := h.ok("load", "reflector"); !strings.Contains(r.out, "Mine, not the starter.") {
		t.Fatalf("existing reflector was replaced:\n%s", r.out)
	}
	// A hand-written pilot is never overwritten; the missing reflector still
	// arrives, because each starter agent is seeded independently.
	h2 := newHarness(t)
	h2.ok("base", "pilot", "Hand-written pilot.")
	r = h2.ok("load", "pilot")
	if r.err != "nine-tails: seeded reflector from the built-in starter (ordinary agents; edit with nine-tails base <agent>)\n" || !strings.Contains(r.out, "Hand-written pilot.") {
		t.Fatalf("hand-written pilot: stderr=%q\n%s", r.err, r.out)
	}
}
