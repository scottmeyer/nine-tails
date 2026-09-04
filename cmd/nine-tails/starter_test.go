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
	for _, want := range []string{
		"## Capsule protocol",
		"## The loop",
		"On the first load in a session",
		"nine-tails call --context ctx_M <tool>",
		"Context receipts prove past loads, not live workers.",
		"nine-tails base <name> --expect none",
		"## Adopting an existing agent file",
		"## Available agents\n\n- `reflector`: ",
	} {
		if !strings.Contains(r.out, want) {
			t.Errorf("pilot capsule lacks %q:\n%s", want, r.out)
		}
	}
	for _, obsolete := range []string{"within the last hour means", `--compiler "claude -p"`} {
		if strings.Contains(r.out, obsolete) {
			t.Errorf("pilot capsule retains obsolete guidance %q:\n%s", obsolete, r.out)
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

func TestStarterReflectorUsesOnlyParentEpisodeReceipt(t *testing.T) {
	h := newHarness(t)
	parentLoad := h.ok("load", "pilot", "--task", "Parent episode")
	parent := contextID(t, parentLoad.out)
	r := h.ok("load", "reflector", "--task", "Reflect", "--context", parent)

	for _, want := range []string{
		"parent `" + parent + "` -> `pilot`",
		"pass the parent receipt to every command",
		"Never use this new reflector receipt for episode updates",
		"receipt is present, make",
		"zero writes.",
		"--context <parent-receipt>",
		"--expect <state-id|none>",
		"Use `--expect none` only when the named state does not exist",
		"nine-tails tool add <parent-agent> <tool> --script <reviewed-path> --description \"...\" --context <parent-receipt>",
		"always keep the parent receipt as",
		"the signal's origin",
		"Register only a reviewed, reusable executable",
		"never copy raw or untrusted executable content into the store",
	} {
		if !strings.Contains(r.out, want) {
			t.Errorf("reflector capsule lacks %q:\n%s", want, r.out)
		}
	}
	for _, unsafe := range []string{"--context ctx_N", "tool add <reflector>"} {
		if strings.Contains(r.out, unsafe) {
			t.Errorf("reflector capsule retains unsafe episode-write guidance %q:\n%s", unsafe, r.out)
		}
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
