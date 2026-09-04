package main

import (
	"os"
	"reflect"
	"testing"
)

func TestStateMetadataFlagConflictDoesNotOpenStore(t *testing.T) {
	for _, args := range [][]string{
		{"state", "put", "a/working", "--expect", "none", "status: ready"},
		{"put", "a", "--lane", "state", "--kind", "working-state", "--name", "working", "status: ready"},
	} {
		h := newHarness(t)
		requireExit(t, h.run(append(args, "--clear-meta", "--meta", "repo-id=a")...), 2, "mutually exclusive")
		entries, err := os.ReadDir(h.home)
		if err != nil || len(entries) != 0 {
			t.Fatalf("invalid flags touched store: %v, %v", entries, err)
		}
	}
}

func TestStateUpdateKeepsLoadApplicability(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "a", "General purpose.")
	id := h.ok("state", "put", "a/working", "--expect", "none", "--meta", "repo-id=first", "status: old").id(t)
	h.ok("state", "put", "a/working", "--expect", id, "status: new")
	for project, want := range map[string]int{"first": 1, "second": 0} {
		cap := h.ok("load", "a", "--meta", "repo-id="+project, "--format", "json").json(t)
		if got := len(cap["state"].([]any)); got != want {
			t.Fatalf("project %s: %d states, want %d", project, got, want)
		}
	}
}

func TestStatePutMetadataIntent(t *testing.T) {
	for _, generic := range []bool{false, true} {
		name := "state-put"
		if generic {
			name = "generic-put"
		}
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			put := func(expect string, extra ...string) result {
				args := []string{"state", "put", "a/working"}
				if generic {
					args = []string{"put", "a", "--lane", "state", "--kind", "working-state", "--name", "working"}
				}
				args = append(args, "--expect", expect, "--format", "json", "status: ready")
				return h.run(append(args, extra...)...)
			}
			check := func(r result, want map[string]any) string {
				t.Helper()
				requireExit(t, r, 0, "")
				if got := r.json(t)["meta"]; !reflect.DeepEqual(got, want) {
					t.Fatalf("metadata = %#v, want %#v", got, want)
				}
				return r.id(t)
			}
			unqualified := map[string]any{}
			scoped := map[string]any{"repo-id": []any{"first"}, "audience": []any{"a", "b"}}
			id := check(put("none"), unqualified)
			id = check(put(id, "--meta", "repo-id=first", "--meta", "audience=a", "--meta", "audience=b"), scoped)
			original := id
			id = check(put(id), scoped)
			// A stale write must not clear or alter either the body or scope.
			requireExit(t, put(original, "--clear-meta"), 7, "expected")
			if got := h.ok("state", "get", "a/working", "--format", "json").id(t); got != id {
				t.Fatalf("stale write changed state to %s", got)
			}
			id = check(put(id, "--meta", "repo-id=second"), map[string]any{"repo-id": []any{"second"}})
			requireExit(t, put(id, "--clear-meta", "--meta", "repo-id=third"), 2, "--clear-meta")
			id = check(put(id, "--clear-meta"), unqualified)
			check(put(id), unqualified)
			// Historical versions retain their exact metadata.
			check(h.ok("inspect", original, "--format", "json"), scoped)
		})
	}
}

func TestStatePutDoesNotInferScopeFromContext(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "a", "General purpose.")
	ctx := h.ok("load", "a", "--meta", "repo-id=ambient", "--format", "json").id(t)
	for _, args := range [][]string{
		{"state", "put", "working", "--context", ctx, "--expect", "none", "status: new", "--format", "json"},
		{"put", "a", "--lane", "state", "--kind", "working-state", "--name", "other", "--context", ctx, "status: new", "--format", "json"},
	} {
		r := h.ok(args...).json(t)
		if len(r["meta"].(map[string]any)) != 0 {
			t.Fatalf("inferred ambient scope: %v", r)
		}
		if r["origin_context"] != ctx {
			t.Fatalf("lost context provenance: %v", r)
		}
	}
}

func TestDefinitionPutMetadataStillReplaces(t *testing.T) {
	h := newHarness(t)
	args := []string{"put", "a", "--lane", "definition", "--kind", "custom", "--name", "x", "body", "--format", "json"}
	h.ok(append(args, "--meta", "repo-id=first")...)
	if got := h.ok(args...).json(t)["meta"].(map[string]any); len(got) != 0 {
		t.Fatalf("definition metadata inherited: %v", got)
	}
	requireExit(t, h.run(append(args, "--clear-meta")...), 2, "state")
}
