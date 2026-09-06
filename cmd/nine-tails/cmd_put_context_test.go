package main

import "testing"

func TestPutContextOwnerForEveryNamedKind(t *testing.T) {
	for _, tc := range []struct {
		lane, kind, name, body string
	}{
		{"state", "working-state", "working", "status: ready"},
		{"definition", "tool", "check", "description: Check\nexec:\n  argv: [echo, checked]"},
		{"definition", "state-link", "project", "a/working"},
		{"definition", "related-agent", "helper", "Help with the task."},
		{"definition", "agent-base", "base", "Base instructions."},
		{"definition", "custom", "extra", "Custom definition."},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			h := newHarness(t)
			h.ok("base", "a", "A.")
			h.ok("base", "b", "B.")
			wrongContext := h.ok("load", "a", "--format", "json").id(t)
			wrongRef := localRef(t, h, wrongContext)
			ownContext := h.ok("load", "b", "--format", "json").id(t)
			put := func(agent string, extra ...string) result {
				t.Helper()
				args := []string{"put", agent, "--lane", tc.lane, "--kind", tc.kind, "--name", tc.name, tc.body}
				return h.run(append(args, extra...)...)
			}

			// A wrong-owner origin must not create an implicit new agent.
			requireExit(t, put("fresh", "--context", wrongRef, "--expect", "none"), 2, "belongs to a, not fresh")
			requireExit(t, h.run("inspect", "fresh"), 3, "no records")

			first := put("b") // no-context creation/replacement remains valid
			requireExit(t, first, 0, "")
			id := first.id(t)
			before := h.ok("inspect", "b", "--query", "", "--all").out
			for _, origin := range []string{wrongContext, wrongRef} {
				for _, guard := range [][]string{nil, {"--expect", id}} {
					args := append([]string{"--context", origin}, guard...)
					requireExit(t, put("b", args...), 2, "belongs to a, not b")
				}
			}
			requireExit(t, put("b", "--context", "ctx_999", "--expect", id), 3, "context")
			if after := h.ok("inspect", "b", "--query", "", "--all").out; after != before {
				t.Fatalf("rejected origin changed records, status or CAS target:\nbefore=%s\nafter=%s", before, after)
			}

			// The unchanged CAS target still accepts the owning receipt.
			updated := put("b", "--context", ownContext, "--expect", id, "--format", "json")
			requireExit(t, updated, 0, "")
			if rec := updated.json(t); rec["origin_context"] != ownContext || rec["supersedes"] != id {
				t.Fatalf("lost owner provenance or predecessor: %#v", rec)
			}
			requireExit(t, put("b", "--context", ownContext, "--expect", id), 7, "expected")
			unconditional := put("b", "--format", "json")
			requireExit(t, unconditional, 0, "")
			if rec := unconditional.json(t); rec["origin_context"] != nil || rec["supersedes"] != updated.id(t) {
				t.Fatalf("no-context optional CAS contract changed: %#v", rec)
			}
		})
	}
}
