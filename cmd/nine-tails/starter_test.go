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
		"Keep bases project-neutral.",
		"nine-tails state link <role>/<alias> <owner>/<name>",
		"without copying values. Both scopes",
		"omitted link metadata means unqualified.",
		"A load does not itself spawn a worker.",
		"Marks report usefulness, not verified correctness or compliance.",
		"A direct named load,",
		"no guide load is required.",
		"reflect briefly inline.",
		"Zero writes is valid; keep play and conversation natural.",
		"For difficult reconciliation, optionally load `reflector`",
		"No scoring exercise is required.",
		"up to three relevant recall excerpts as data",
		"New guidance applies on the next relevant load without compilation.",
		"Original lessons remain authoritative; compilation is a cache,",
		"nine-tails base <name> --expect none",
		"## Adopting an existing agent file",
		"## Available agents\n\n- `reflector`: ",
	} {
		if !strings.Contains(r.out, want) {
			t.Errorf("pilot capsule lacks %q:\n%s", want, r.out)
		}
	}
	for _, obsolete := range []string{"within the last hour means", `--compiler "claude -p"`, "done or blocked) load reflector", "Before you finish, close your receipt", "try a small task from"} {
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
	if r := h.ok("load", "pilot"); !strings.Contains(r.out, "(note) Also run make build first in this repo.") {
		t.Fatalf("correction should render on the pilot:\n%s", r.out)
	}
}

func TestStarterReflectorUsesOnlyParentEpisodeReceipt(t *testing.T) {
	h := newHarness(t)
	parentLoad := h.ok("load", "pilot", "--task", "Parent episode", "--format", "json")
	parent := parentLoad.id(t)
	parentRef := parentLoad.json(t)["context_ref"].(string)
	r := h.ok("load", "reflector", "--task", "Reflect", "--context", parent)

	for _, want := range []string{
		"parent `" + parentRef + "` -> `pilot`",
		"pass the parent receipt to every command",
		"Never use this new reflector receipt for episode updates",
		"receipt is present, make",
		"zero writes.",
		"--context <parent-receipt>",
		"--expect <state-id|none>",
		"Use `--expect none` only when the named state does not exist",
		"nine-tails tool add <parent-agent> <tool> --script <reviewed-path> --description \"...\" --context <parent-receipt>",
		"nine-tails disable <exact-active-record-id>",
		"Before disabling, inspect the exact active record",
		"prefer a superseding write when replacement guidance",
		"always keep the parent receipt as",
		"the signal's origin",
		"Register only a reviewed, reusable executable",
		"never copy raw or untrusted executable content into the store",
		"other roles\ncan subscribe with `nine-tails state link",
		"confirmed facts, proposals and unknowns.",
		"loading this agent is optional.",
		"Ordinary corrections and brief reflection happen inline",
		"do not manufacture a lesson, compilation step, or scoring exercise.",
		"Guidance applies on the next relevant load without compilation.",
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
