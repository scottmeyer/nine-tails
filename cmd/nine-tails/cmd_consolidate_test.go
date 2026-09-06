package main

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestConsolidateCLIReplacesCompiledGuidanceAndPreservesScope(t *testing.T) {
	h := newHarness(t)
	base := h.ok("base", "writer", "Write clearly.").id(t)
	origin := contextID(t, h.ok("load", "writer").out)
	one := h.ok("prefer", "--context", origin, "--meta", "repo-id=letters", "Lead with the answer.").id(t)
	two := h.ok("prefer", "--context", origin, "--meta", "repo-id=letters", "Keep opening sentences direct.").id(t)
	doc := fmt.Sprintf("input_entries: [%s, %s]\nitems: [{key: style, body: Old compiled advice., meta: {repo-id: [letters]}}]\nentries: [{id: %s, disposition: represented, items: [style]}, {id: %s, disposition: represented, items: [style]}]\n", one, two, one, two)
	h.okIn(doc, "brief", "put", "writer", "--expect-generation", "none", "--expect-base", base, "--stdin")
	before := h.ok("load", "writer", "--meta", "repo-id=letters").out
	if !strings.Contains(before, "Old compiled advice.") {
		t.Fatal("fixture did not surface compiled guidance")
	}
	context := contextID(t, before)
	beforeReceipt := h.ok("inspect", context).json(t)
	merged := h.okIn("Lead with a direct answer before explaining details.\n", "consolidate", "--context", localRef(t, h, context), "--source", localRef(t, h, one), "--source", two, "--reason", "Both instructions govern the opening sentence.", "--stdin", "--format", "json").json(t)
	id := merged["id"].(string)
	if merged["kind"] != "prefer" || merged["origin_context"] != context || merged["supersedes"] != nil || merged["body"] != "Lead with a direct answer before explaining details." || merged["ref"] == "" {
		t.Fatalf("unexpected result envelope: %v", merged)
	}
	if meta := merged["meta"].(map[string]any); len(meta) != 1 || meta["repo-id"].([]any)[0] != "letters" {
		t.Fatalf("scope changed: %v", meta)
	}
	audit := merged["consolidation"].(map[string]any)
	sources := audit["sources"].([]any)
	if len(sources) != 2 || sources[0].(map[string]any)["id"] != one || sources[1].(map[string]any)["id"] != two || sources[0].(map[string]any)["origin_context"] != origin || sources[0].(map[string]any)["status"] != "superseded" {
		t.Fatalf("source evidence incomplete: %v", audit)
	}
	afterReceipt := h.ok("inspect", context).json(t)
	if !reflect.DeepEqual(beforeReceipt, afterReceipt) {
		t.Fatal("consolidation rewrote the historical load receipt")
	}
	after := h.ok("load", "writer", "--meta", "repo-id=letters").out
	if strings.Contains(after, "Old compiled advice.") || strings.Contains(after, "Keep opening sentences direct.") || strings.Contains(after, "Lead with the answer.") || !strings.Contains(after, "`"+merged["ref"].(string)+"` [repo-id=letters] (prefer) Lead with a direct answer before explaining details.") {
		t.Fatalf("consolidation did not replace old instructions on next load: %s", after)
	}
	if other := h.ok("load", "writer", "--meta", "repo-id=other").out; strings.Contains(other, "Lead with a direct answer before explaining details.") {
		t.Fatal("consolidated instruction escaped its scope")
	}
	for _, format := range []string{"json", "yaml"} {
		var view map[string]any
		if err := yaml.Unmarshal([]byte(h.ok("inspect", id, "--format", format).out), &view); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(view["consolidation"], audit) {
			t.Fatalf("%s inspection omitted source intent: %v", format, view)
		}
	}
	corrected := h.ok("prefer", "--context", context, "--supersedes", merged["ref"].(string), "Use concise answers with sufficient context.").id(t)
	for _, source := range []string{one, two, id} {
		view := h.ok("inspect", localRef(t, h, source)).json(t)
		if view["id"] != source || view["current"].(map[string]any)["id"] != corrected {
			t.Fatalf("old reference lost latest correction: %v", view)
		}
	}
	stale := h.run("consolidate", "--context", context, "--source", one, "--source", two, "--reason", "stale retry", "new text")
	if stale.code != 7 || !strings.Contains(stale.err, "inspect "+localRef(t, h, corrected)) {
		t.Fatalf("stale consolidation lacks current repair handle: %+v", stale)
	}
}

func TestConsolidateCLIFormatsAndDeliberateKind(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "a", "Role.")
	ctx := contextID(t, h.ok("load", "a", "--meta", "repo-id=ambient").out)
	one := h.ok("prefer", "a", "positive wording").id(t)
	two := h.ok("avoid", "a", "negative wording").id(t)
	r := h.run("consolidate", "--context", ctx, "--source", one, "--source", two, "--reason", "same rule", "one rule")
	if r.code != 2 || !strings.Contains(r.err, "--kind") {
		t.Fatalf("mixed kinds were guessed: %+v", r)
	}
	merged := h.ok("consolidate", "--context", ctx, "--source", one, "--source", two, "--reason", "same rule", "--kind", "policy", "one rule").id(t)
	if !strings.HasPrefix(merged, "rec_") || strings.Contains(merged, "\n") {
		t.Fatalf("default output is not one canonical ID: %q", merged)
	}
	third := h.ok("append", "a", "--lane", "guidance", "--kind", "policy", "related rule").id(t)
	var view map[string]any
	if err := yaml.Unmarshal([]byte(h.ok("consolidate", "--context", ctx, "--source", merged, "--source", third, "--reason", "another related condition", "--format", "yaml", "Complete rule.").out), &view); err != nil {
		t.Fatal(err)
	}
	if view["kind"] != "policy" || len(view["meta"].(map[string]any)) != 0 || len(view["consolidation"].(map[string]any)["sources"].([]any)) != 2 {
		t.Fatalf("YAML output lost sources or inherited ambient metadata: %v", view)
	}
}

func TestConsolidateCLIRecallIsImmediatelyRetrievableAndRetirementDoesNotResurrect(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "a", "Role.")
	origin := contextID(t, h.ok("load", "a").out)
	one := h.ok("remember", "--context", origin, "Passing route failed after a late run.").id(t)
	two := h.ok("remember", "--context", origin, "Late passing run exposed the same route failure.").id(t)
	before := h.ok("load", "a", "--query", "passing route failure").out
	context := contextID(t, before)
	beforeReceipt := h.ok("inspect", context).json(t)
	merged := h.ok("consolidate", "--context", context, "--source", one, "--source", two, "--reason", "Both observations record the same route failure", "A late run made the passing route fail.").id(t)
	view := h.ok("inspect", merged).json(t)
	if view["lane"] != "recall" || view["kind"] != "memory" {
		t.Fatalf("recall lane or inferred kind changed: %v", view)
	}
	if afterReceipt := h.ok("inspect", context).json(t); !reflect.DeepEqual(beforeReceipt, afterReceipt) {
		t.Fatal("recall consolidation rewrote the historical load receipt")
	}
	after := h.ok("load", "a", "--query", "passing route failure").out
	if !strings.Contains(after, "A late run made the passing route fail.") || strings.Contains(after, "Passing route failed after") || strings.Contains(after, "Late passing run exposed") {
		t.Fatalf("ordinary retrieval did not replace recall sources: %s", after)
	}
	page := h.ok("inspect", "--page", "--context", context, "--query", "passing route fail").out
	if !strings.Contains(page, "A late run made the passing route fail.") || strings.Contains(page, "Passing route failed after") || strings.Contains(page, "Late passing run exposed") {
		t.Fatalf("recall library did not expose only the replacement: %s", page)
	}
	h.ok("disable", merged)
	retired := h.ok("load", "a", "--query", "passing route failure").out
	if strings.Contains(retired, "passing route") || strings.Contains(retired, "Passing route") {
		t.Fatalf("retiring recall consolidation resurrected its predecessors: %s", retired)
	}
}

func TestConsolidateCLIValidationLeavesSourcesActive(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "a", "Role.")
	ctx := contextID(t, h.ok("load", "a").out)
	one := h.ok("note", "a", "one").id(t)
	two := h.ok("note", "a", "two").id(t)
	base := []string{"consolidate", "--context", ctx, "--source", one, "--source", two}
	for name, args := range map[string][]string{
		"reason required":      append(append([]string{}, base...), "new text"),
		"blank reason":         append(append([]string{}, base...), "--reason", " ", "new text"),
		"body required":        append(append([]string{}, base...), "--reason", "same rule"),
		"empty explicit kind":  append(append([]string{}, base...), "--reason", "same rule", "--kind", "", "new text"),
		"forbidden brief kind": append(append([]string{}, base...), "--reason", "same rule", "--kind", "brief-item", "new text"),
		"scope cannot widen":   append(append([]string{}, base...), "--reason", "same rule", "--meta", "repo-id=other", "new text"),
		"duplicate reference":  {"consolidate", "--context", ctx, "--source", one, "--source", localRef(t, h, one), "--reason", "same rule", "new text"},
		"too few sources":      {"consolidate", "--context", ctx, "--source", one, "--reason", "same rule", "new text"},
		"context required":     {"consolidate", "--source", one, "--source", two, "--reason", "same rule", "new text"},
	} {
		t.Run(name, func(t *testing.T) {
			if r := h.run(args...); r.code != 2 {
				t.Fatalf("expected invalid input: %+v", r)
			}
		})
	}
	for _, id := range []string{one, two} {
		if view := h.ok("inspect", id).json(t); view["status"] != "active" {
			t.Fatalf("invalid call retired source: %v", view)
		}
	}
}
