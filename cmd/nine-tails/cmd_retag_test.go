package main

import (
	"fmt"
	"strings"
	"testing"
)

func TestCompiledScopeFollowsSourceCorrectionsImmediately(t *testing.T) {
	for _, mode := range []string{"narrow", "broaden", "move", "unchanged", "reorder", "kind"} {
		t.Run(mode, func(t *testing.T) {
			h := newHarness(t)
			base := h.ok("base", "writer", "Write.").id(t)
			args := []string{"prefer", "writer"}
			itemMeta := ""
			if mode == "broaden" || mode == "move" || mode == "unchanged" {
				args = append(args, "--meta", "repo-id=letters")
				itemMeta = "meta: {repo-id: [letters]}, "
			}
			if mode == "reorder" {
				args = append(args, "--meta", "repo-id=letters", "--meta", "repo-id=game")
				itemMeta = "meta: {repo-id: [letters, game]}, "
			}
			old := h.ok(append(args, "Use terse prose.")...).id(t)
			doc := fmt.Sprintf("input_entries: [%s]\nitems:\n  - {key: tone, %sbody: Be terse.}\nentries:\n  - {id: %s, disposition: represented, items: [tone]}\n", old, itemMeta, old)
			generation := h.okIn(doc, "brief", "put", "writer", "--expect-generation", "none", "--expect-base", base, "--stdin").id(t)
			past := h.ok("load", "writer", "--meta", "repo-id=letters", "--format", "json").json(t)
			oldReceipt := h.ok("inspect", past["context_id"].(string)).out
			change := []string{"prefer", "writer", "--supersedes", old}
			switch mode {
			case "narrow":
				change = append(change, "--meta", "repo-id=letters")
			case "broaden":
				change = append(change, "--clear-meta")
			case "move":
				change = append(change, "--meta", "repo-id=game")
			case "reorder":
				change = append(change, "--meta", "repo-id=game", "--meta", "repo-id=letters")
			case "kind":
				change[0] = "avoid"
			}
			current := h.ok(change...).id(t)
			in := h.ok("compile-input", "writer").json(t)
			unchanged := mode == "unchanged" || mode == "reorder"
			if (in["expect_generation"] == generation) != unchanged {
				t.Fatalf("wrong invalidation mode=%s: %v", mode, in)
			}
			for _, project := range []string{"letters", "game"} {
				loaded := h.ok("load", "writer", "--meta", "repo-id="+project).out
				if unchanged {
					want := project == "letters" || mode == "reorder"
					if strings.Contains(loaded, "Be terse.") != want || strings.Contains(loaded, "## Recent adjustments") {
						t.Fatal(loaded)
					}
				} else {
					want := mode == "broaden" || mode == "kind" || (mode == "narrow" && project == "letters") || (mode == "move" && project == "game")
					if strings.Contains(loaded, "Use terse prose.") != want || strings.Contains(loaded, "Be terse.") {
						t.Fatal(loaded)
					}
					if mode == "kind" && !strings.Contains(loaded, "(avoid)") {
						t.Fatal(loaded)
					}
				}
			}
			if got := h.ok("inspect", past["context_id"].(string)).out; got != oldReceipt {
				t.Fatal("historical projection changed")
			}
			if !unchanged {
				requireExit(t, h.runIn(doc, "brief", "put", "writer", "--expect-generation", generation, "--expect-base", base, "--stdin"), 7, "")
				if len(in["input_entries"].([]any)) != 1 || in["input_entries"].([]any)[0] != current {
					t.Fatal(in)
				}
			}
		})
	}
}
