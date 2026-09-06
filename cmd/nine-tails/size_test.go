package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Guidance remains complete and its size observable without turning ordinary
// loads into a compiler workflow, even with the legacy threshold configured.
func TestLoadPreservesGuidanceAndReportsSize(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "a", "Base.")
	for i := 0; i < 12; i++ {
		h.ok("prefer", "a", fmt.Sprintf("Adjustment %02d: %s", i, strings.Repeat("bounded text ", 8)))
	}
	r := h.ok("load", "a")
	if strings.Count(r.out, "Adjustment ") != 12 || r.err != "" {
		t.Fatalf("the default threshold must stay silent on a small capsule: stderr=%q\n%s", r.err, r.out)
	}
	if err := os.WriteFile(filepath.Join(h.home, "config.yaml"), []byte("compile_advice_tokens: 100\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r = h.ok("load", "a")
	if r.err != "" || strings.Contains(r.out, "nine-tails compile") {
		t.Fatalf("ordinary load advertises a compiler workflow: stderr=%q\n%s", r.err, r.out)
	}
	if strings.Count(r.out, "Adjustment ") != 12 {
		t.Fatalf("size handling must not cut guidance:\n%s", r.out)
	}
	m := h.ok("load", "a", "--format", "json").json(t)
	if m["uncompiled_adjustments"] != float64(12) || m["estimated_tokens"].(float64) <= 100 {
		t.Fatalf("json size fields: uncompiled=%v estimated=%v", m["uncompiled_adjustments"], m["estimated_tokens"])
	}
	for _, gone := range []string{"budget", "truncated"} {
		if _, ok := m[gone]; ok {
			t.Errorf("capsule json still carries %q", gone)
		}
	}
	receipt := h.ok("inspect", m["context_id"].(string)).json(t)
	if receipt["estimated_tokens"] != m["estimated_tokens"] {
		t.Fatalf("receipt should record the estimate: %v", receipt)
	}
	if _, ok := receipt["budget"]; ok {
		t.Fatalf("receipt still carries a budget: %v", receipt)
	}
	requireExit(t, h.run("load", "a", "--budget", "100"), 2, "unknown flag: --budget")

	// A large base also remains complete and silent on stderr.
	if err := os.WriteFile(filepath.Join(h.home, "config.yaml"), []byte("compile_advice_tokens: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.ok("base", "b", strings.Repeat("A long base. ", 40))
	if r := h.ok("load", "b"); r.err != "" {
		t.Fatalf("no adjustments, no advice: %q", r.err)
	}
}
