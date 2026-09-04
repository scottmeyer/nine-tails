package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Size is advice, not enforcement: load renders everything, reports the
// estimate, and past the configured threshold says to compile.
func TestLoadAdvisesCompileInsteadOfCutting(t *testing.T) {
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
	if !strings.HasPrefix(r.err, "nine-tails: capsule is ") || !strings.HasSuffix(r.err, " estimated tokens with 12 uncompiled adjustments; compile with `nine-tails compile a`\n") {
		t.Fatalf("advice line: %q", r.err)
	}
	if strings.Count(r.out, "Adjustment ") != 12 {
		t.Fatalf("advice must not cut anything:\n%s", r.out)
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

	// Nothing uncompiled means nothing to advise, however large the capsule.
	if err := os.WriteFile(filepath.Join(h.home, "config.yaml"), []byte("compile_advice_tokens: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.ok("base", "b", strings.Repeat("A long base. ", 40))
	if r := h.ok("load", "b"); r.err != "" {
		t.Fatalf("no adjustments, no advice: %q", r.err)
	}
}
