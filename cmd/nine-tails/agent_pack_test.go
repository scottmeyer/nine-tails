package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestRepositoryAgentPackImportsIntoFreshStore(t *testing.T) {
	h := newHarness(t)
	initialPilot := h.ok("load", "pilot", "--task", "initialize", "--meta", "repo-id=nine-tails", "--meta", "harness=test")
	initialPilotID := contextID(t, initialPilot.out)

	pack := filepath.Join("..", "..", "agents")
	for _, name := range []string{
		"nine-tails.builder.yaml",
		"nine-tails.reviewer.yaml",
		"nine-tails.yaml",
		"pilot.catalog.yaml",
	} {
		r := h.ok("import", filepath.Join(pack, name))
		if strings.TrimSpace(r.out) == "" {
			t.Fatalf("import %s returned no record ids", name)
		}
	}

	agents := h.ok("agents").out
	for _, name := range []string{"pilot", "reflector", "nine-tails.builder", "nine-tails.reviewer", "nine-tails"} {
		if !strings.Contains(agents, name+"\n") {
			t.Errorf("agents output is missing %q:\n%s", name, agents)
		}
	}

	pilot := h.ok("load", "pilot", "--task", "choose", "--meta", "repo-id=nine-tails", "--meta", "harness=test").out
	for _, name := range []string{"`nine-tails.builder`", "`nine-tails.reviewer`", "`nine-tails`"} {
		if !strings.Contains(pilot, name) {
			t.Errorf("pilot catalog is missing %s:\n%s", name, pilot)
		}
	}

	builder := h.ok("load", "nine-tails.builder", "--task", "build", "--context", initialPilotID).out
	if !strings.Contains(builder, "# nine-tails Builder") || !strings.Contains(builder, "## Capsule protocol") {
		t.Fatalf("builder pack did not produce a guided capsule:\n%s", builder)
	}
	if !strings.Contains(builder, "parent `"+initialPilotID+"` -> `pilot`") {
		t.Fatalf("builder did not inherit the original pre-catalog pilot receipt:\n%s", builder)
	}
}
