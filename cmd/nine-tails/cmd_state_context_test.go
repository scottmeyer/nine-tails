package main

import (
	"os"
	"testing"
)

func TestStateGetContextMatchesQualifiedStreams(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "a", "Maintain state.")
	first := h.ok("state", "put", "a/working", "--expect", "none", "status: first").id(t)
	ctx := h.ok("load", "a", "--meta", "repo-id=elsewhere", "--format", "json").id(t)
	// Context selects the owner, not a historical version or metadata filter.
	latest := h.ok("state", "put", "working", "--context", ctx, "--expect", first, "--meta", "repo-id=project", "status: latest").id(t)
	for _, format := range []string{"yaml", "json", "id"} {
		want := h.ok("state", "get", "a/working", "--format", format)
		for _, target := range []string{"working", "a/working"} {
			got := h.ok("state", "get", target, "--context", ctx, "--format", format)
			if got != want {
				t.Fatalf("%s/%s streams = %#v, want %#v", target, format, got, want)
			}
		}
	}
	if got := h.ok("state", "get", "working", "--context", ctx, "--format", "id").id(t); got != latest {
		t.Fatalf("returned historical state %s, want %s", got, latest)
	}
	requireExit(t, h.run("state", "get", "missing", "--context", ctx), 3, "no active state")
}

func TestStateTargetContextOwnerAndID(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "a", "Maintain state.")
	h.ok("base", "b", "Other role.")
	ctx := h.ok("load", "a", "--format", "json").id(t)
	state := h.ok("state", "put", "b/working", "--expect", "none", "status: untouched").id(t)
	for _, args := range [][]string{
		{"state", "get", "b/working", "--context", ctx},
		{"state", "put", "b/working", "--context", ctx, "--expect", state, "status: forbidden"},
	} {
		requireExit(t, h.run(args...), 2, "belongs to a, not b")
	}
	for _, bad := range []string{"ctx_999", state} {
		requireExit(t, h.run("state", "get", "working", "--context", bad), 3, "context")
	}
	if got := h.ok("state", "get", "b/working", "--format", "id").id(t); got != state {
		t.Fatal("owner mismatch changed state")
	}
}

func TestInvalidStateTargetsDoNotOpenStore(t *testing.T) {
	for _, args := range [][]string{
		{"state", "get", "working"},
		{"state", "get", "a/b/c", "--context", "ctx_999"},
		{"state", "get", "../x", "--context", "ctx_999"},
		{"state", "get", "BAD", "--context", "ctx_999"},
		{"state", "get", "working", "--context", "ctx_999", "--format", "bogus"},
		{"state", "put", "working", "--expect", "none", "status: invalid"},
	} {
		h := newHarness(t)
		requireExit(t, h.run(args...), 2, "")
		files, err := os.ReadDir(h.home)
		if err != nil || len(files) != 0 {
			t.Fatalf("invalid invocation created store: %v %v", files, err)
		}
	}
}
