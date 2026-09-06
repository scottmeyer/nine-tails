package main

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// A correction can start with exactly what the model saw, without dumping
// the whole agent or guessing which historical record a summary represents.
func TestCapsuleRepairHandlesReachCurrentSources(t *testing.T) {
	h := newHarness(t)
	base := h.ok("base", "writer", "Write clearly.").id(t)
	original := h.ok("prefer", "writer", "Use elaborate prose.").id(t)
	first := h.ok("load", "writer").out
	originalRef := localRef(t, h, original)
	if !strings.Contains(first, "`"+originalRef+"` (prefer) Use elaborate prose.") {
		t.Fatal("recent guidance must expose the repair handle")
	}
	doc := fmt.Sprintf("input_entries: [%s]\nitems: [{key: style, body: Use elaborate prose.}]\nentries: [{id: %s, disposition: represented, items: [style]}]\n", original, original)
	h.okIn(doc, "brief", "put", "writer", "--expect-generation", "none", "--expect-base", base, "--stdin")
	capsule := h.ok("load", "writer").out
	matches := regexp.MustCompile("(?m)^- `(@[1-9][0-9]*)` ").FindStringSubmatch(capsule)
	if len(matches) != 2 {
		t.Fatalf("brief has no visible item handle: %s", capsule)
	}
	item := matches[1]
	// A changed scope invalidates the brief but retains its original evidence.
	// Inspection follows the source's replacement without pretending the
	// historical brief was derived from the newly scoped record.
	retagged := h.ok("prefer", "writer", "--supersedes", originalRef, "--meta", "repo-id=letters").id(t)
	for _, format := range []string{"json", "yaml"} {
		var view map[string]any
		if err := yaml.Unmarshal([]byte(h.ok("inspect", item, "--format", format).out), &view); err != nil {
			t.Fatal(err)
		}
		sources := view["sources"].(map[string]any)
		recorded := sources["recorded"].([]any)
		current := sources["current"].([]any)
		if len(recorded) != 1 || recorded[0].(map[string]any)["id"] != original || len(current) != 1 || current[0].(map[string]any)["id"] != retagged {
			t.Fatalf("%s source lineage is incomplete or duplicated: %v", format, sources)
		}
		meta := current[0].(map[string]any)["meta"].(map[string]any)
		if meta["repo-id"].([]any)[0] != "letters" {
			t.Fatal("current source scope missing")
		}
	}
	corrected := h.ok("prefer", "writer", "--supersedes", localRef(t, h, retagged), "--meta", "repo-id=letters", "Use concise prose.").id(t)
	next := h.ok("load", "writer", "--meta", "repo-id=letters").out
	if strings.Contains(next, "Use elaborate prose.") || !strings.Contains(next, "`"+localRef(t, h, corrected)+"` [repo-id=letters] (prefer) Use concise prose.") {
		t.Fatalf("repair did not take effect immediately: %s", next)
	}
	view := h.ok("inspect", item).json(t)
	current := view["sources"].(map[string]any)["current"].([]any)
	if len(current) != 1 || current[0].(map[string]any)["body"] != "Use concise prose." {
		t.Fatalf("historical item did not resolve its current repair target: %v", view)
	}
	h.ok("disable", corrected)
	view = h.ok("inspect", item).json(t)
	if view["sources"].(map[string]any)["current"].([]any)[0].(map[string]any)["status"] != "disabled" {
		t.Fatal("disabled source must remain distinguishable from usable repair targets")
	}
	if _, exists := h.ok("inspect", base).json(t)["sources"]; exists {
		t.Fatal("non-brief record acquired synthetic source evidence")
	}
}
