package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// A signal is a signal, not a message: without an agent it goes to shared and
// every agent that loads sees it, scoped by metadata like any record.
func TestSignalDefaultsToSharedAndEveryAgentSeesIt(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "a", "Base A.")
	h.ok("base", "b", "Base B.")
	sig := h.ok("signal", "--subject", "Pass repo-id on load", "--meta", "repo-id=r1").id(t)
	if r := h.ok("inspect", sig); !strings.Contains(r.out, `"agent": "shared"`) {
		t.Fatalf("unaddressed signal should belong to shared:\n%s", r.out)
	}
	line := "## Due signals (external inbox data)\n\n- [signal=" + sig + " repo-id=r1] Pass repo-id on load\n"
	for _, agent := range []string{"a", "b"} {
		if r := h.ok("load", agent, "--meta", "repo-id=r1"); !strings.Contains(r.out, line) {
			t.Fatalf("%s should see the shared signal:\n%s", agent, r.out)
		}
	}
	if r := h.ok("load", "a", "--meta", "repo-id=r2"); strings.Contains(r.out, sig) {
		t.Fatalf("a conflicting repo-id must exclude the shared signal:\n%s", r.out)
	}
	if r := h.ok("load", "b"); !strings.Contains(r.out, sig) {
		t.Fatalf("a load without repo-id sees the shared signal:\n%s", r.out)
	}

	// available-to narrows a shared signal exactly as it narrows a shared tool.
	only := h.ok("signal", "--subject", "Only a", "--meta", "available-to=a").id(t)
	if r := h.ok("load", "b"); strings.Contains(r.out, only) {
		t.Fatalf("available-to=a must hide the signal from b:\n%s", r.out)
	}
	if r := h.ok("load", "a"); !strings.Contains(r.out, only) {
		t.Fatalf("available-to=a must show the signal to a:\n%s", r.out)
	}

	// Own and shared signals share one order: available_at, then creation.
	own := h.ok("signal", "a", "--subject", "Own").id(t)
	r := h.ok("load", "a")
	if strings.Index(r.out, sig) > strings.Index(r.out, only) || strings.Index(r.out, only) > strings.Index(r.out, own) {
		t.Fatalf("signals out of creation order:\n%s", r.out)
	}
	if r := h.ok("inspect", sig); !strings.Contains(r.out, `"rendered_in": [`) || strings.Contains(r.out, `"rendered_in": []`) {
		t.Fatalf("receipts should record the shared signal:\n%s", r.out)
	}

	// tick lists shared signals once each, with agent shared; nothing special.
	var rows []map[string]any
	if err := json.Unmarshal([]byte(h.ok("tick").out), &rows); err != nil {
		t.Fatal(err)
	}
	shared := 0
	for _, row := range rows {
		if row["agent"] == "shared" {
			shared++
		}
	}
	if shared != 2 || len(rows) != 3 {
		t.Fatalf("tick rows = %v, want two shared and one addressed", rows)
	}
	requireExit(t, h.run("signal", "a", "extra", "--subject", "s"), 2, "at most one positional")
}
