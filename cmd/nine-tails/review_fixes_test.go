package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestCallStreamsToolStderrBeforeSummary(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "a", "Base.")
	fail := writeScript(t, "fail.sh", "echo before >&2\necho partial\nexit 3\n")
	h.ok("tool", "add", "a", "fails", "--script", fail, "--description", "Fails with 3")
	r := h.run("call", "--agent", "a", "fails")
	want := result{code: 3, out: "partial\n", err: "before\nnine-tails: fails exited with status 3\n"}
	if r != want {
		t.Fatalf("failed call = %#v, want %#v", r, want)
	}
	ok := writeScript(t, "ok.sh", "echo progress >&2\necho done\n")
	h.ok("tool", "add", "a", "ok", "--script", ok, "--description", "Succeeds")
	r = h.run("call", "--agent", "a", "ok")
	want = result{code: 0, out: "done\n", err: "progress\n"}
	if r != want {
		t.Fatalf("successful call = %#v, want %#v", r, want)
	}
}

func TestStateLaneHasOneKind(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "a", "Base.")
	requireExit(t, h.run("put", "a", "--lane", "state", "--kind", "archived-state", "--name", "working", "source: archive"), 2, "working-state")
	h.ok("put", "a", "--lane", "state", "--kind", "working-state", "--name", "working", "source: put")
	if r := h.ok("state", "get", "a/working"); r.out != "source: put\n" {
		t.Fatalf("state get after a generic put: %q", r.out)
	}
	if r := h.ok("load", "a"); strings.Count(r.out, "## Current state (working, ") != 1 {
		t.Fatalf("expected exactly one working state in the capsule:\n%s", r.out)
	}
}

func TestCapsuleAndReceiptKeepEmptyTaskAndParent(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "a", "Base.")
	r := h.ok("load", "a", "--format", "json")
	for _, key := range []string{`"task": ""`, `"parent_context": ""`} {
		if !strings.Contains(r.out, key) {
			t.Errorf("capsule json lacks %s:\n%s", key, r.out)
		}
	}
	r = h.ok("inspect", r.id(t))
	for _, key := range []string{`"task": ""`, `"parent_context": ""`} {
		if !strings.Contains(r.out, key) {
			t.Errorf("receipt json lacks %s:\n%s", key, r.out)
		}
	}
}

func TestAckExpiredLeaseSaysSo(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "a", "Base.")
	sig := h.ok("signal", "a", "--subject", "Ping").id(t)
	var rows []map[string]any
	if err := json.Unmarshal([]byte(h.ok("tick", "--claim", "--lease", "5m").out), &rows); err != nil || len(rows) != 1 {
		t.Fatalf("tick --claim: err=%v rows=%v", err, rows)
	}
	token, _ := rows[0]["lease_token"].(string)
	h.now = h.now.Add(10 * time.Minute)
	r := h.run("signal", "ack", sig, "--lease", token)
	want := "nine-tails: conflict: lease " + token + " on signal " + sig + " expired at 2026-09-04T12:05:00Z; claim it again with tick --claim\n"
	if r.code != 7 || r.err != want {
		t.Fatalf("ack after expiry = %#v, want exit 7 with stderr %q", r, want)
	}
}
