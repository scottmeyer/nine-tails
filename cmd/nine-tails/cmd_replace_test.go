package main

import (
	"fmt"
	"strings"
	"testing"
)

func TestReplacementInvalidatesCompiledMeaning(t *testing.T) {
	for _, retag := range []bool{false, true} {
		t.Run(fmt.Sprintf("retag=%t", retag), func(t *testing.T) {
			h := newHarness(t)
			base := h.ok("base", "writer", "Write notices.").id(t)
			old := h.ok("prefer", "writer", "Use 70–110 words.").id(t)
			magic := h.ok("prefer", "writer", "Use one impossible property.").id(t)
			ending := h.ok("prefer", "writer", "End with a concrete action.").id(t)
			doc := fmt.Sprintf(`input_entries: [%s, %s, %s]
items:
  - {key: mixed, body: "Use 70–110 words and one impossible property."}
  - {key: ending, body: "Close on a tangible gesture."}
entries:
  - {id: %s, disposition: represented, items: [mixed]}
  - {id: %s, disposition: represented, items: [mixed]}
  - {id: %s, disposition: represented, items: [ending]}
`, old, magic, ending, old, magic, ending)
			first := h.okIn(doc, "brief", "put", "writer", "--expect-generation", "none", "--expect-base", base, "--stdin").id(t)
			past := h.ok("load", "writer", "--format", "json").json(t)
			if retag {
				old = h.ok("prefer", "writer", "--supersedes", old).id(t)
			}
			next := h.ok("prefer", "writer", "--supersedes", old, "Use 140–170 words.").id(t)
			out := h.ok("load", "writer").out
			for _, absent := range []string{"70–110", "Close on a tangible gesture.", "## Working brief"} {
				if strings.Contains(out, absent) {
					t.Errorf("retired cache %q survived:\n%s", absent, out)
				}
			}
			for _, present := range []string{"140–170", "Use one impossible property.", "End with a concrete action."} {
				if !strings.Contains(out, present) {
					t.Errorf("live source %q missing:\n%s", present, out)
				}
			}
			in := h.ok("compile-input", "writer").json(t)
			if in["expect_generation"] == first || !equal(strs(t, in["input_entries"]), []string{magic, ending, next}) {
				t.Fatalf("replacement must invalidate and resurface surviving inputs: %v", in)
			}
			if len(in["active_generation"].(map[string]any)["items"].([]any)) != 0 {
				t.Fatal("replacement left active compiled items")
			}
			if got := h.ok("inspect", old).json(t); got["body"] != "Use 70–110 words." || got["status"] != "superseded" {
				t.Fatalf("history was rewritten: %v", got)
			}
			if out := h.ok("inspect", past["context_id"].(string)).out; !strings.Contains(out, "70–110") {
				t.Fatal("old receipt lost historical compiled evidence")
			}
			// An in-flight compiler cannot reinstall the now-stale generation.
			if r := h.runIn(doc, "brief", "put", "writer", "--expect-generation", first, "--expect-base", base, "--stdin"); r.code != 7 {
				t.Fatalf("stale compile: code=%d stderr=%s", r.code, r.err)
			}
		})
	}
}

func TestReplacementLeavesUnrelatedGenerationAlone(t *testing.T) {
	for _, kind := range []string{"recent", "deferred", "recall"} {
		t.Run(kind, func(t *testing.T) {
			h := newHarness(t)
			base := h.ok("base", "a", "Base.").id(t)
			source := h.ok("prefer", "a", "Keep this.").id(t)
			command := "note"
			if kind == "recall" {
				command = "remember"
			}
			old := h.ok(command, "a", "Before.").id(t)
			inputs, rows := source, fmt.Sprintf("  - {id: %s, disposition: represented, items: [keep]}\n", source)
			if kind != "recall" {
				inputs += ", " + old
				rows += fmt.Sprintf("  - {id: %s, disposition: deferred}\n", old)
			}
			doc := fmt.Sprintf("input_entries: [%s]\nitems: [{key: keep, body: Keep this.}]\nentries:\n%s", inputs, rows)
			gen := h.okIn(doc, "brief", "put", "a", "--expect-generation", "none", "--expect-base", base, "--stdin").id(t)
			if kind == "recent" {
				old = h.ok("note", "a", "Appended after compile.").id(t)
			}
			h.ok(command, "a", "--supersedes", old, "After.")
			if got := h.ok("compile-input", "a").json(t)["expect_generation"]; got != gen {
				t.Fatalf("unrelated replacement churned brief: got %v want %s", got, gen)
			}
			if out := h.ok("load", "a").out; !strings.Contains(out, "Keep this.") {
				t.Fatal("unrelated compiled guidance disappeared")
			}
		})
	}
}
