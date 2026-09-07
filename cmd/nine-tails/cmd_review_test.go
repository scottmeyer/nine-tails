package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scottmeyer/nine-tails/internal/store"
)

func reviewResult(t *testing.T, r result) reviewPacket {
	t.Helper()
	if r.code != 0 {
		t.Fatalf("review failed: exit %d\nstdout: %s\nstderr: %s", r.code, r.out, r.err)
	}
	var packet reviewPacket
	if err := json.Unmarshal([]byte(r.out), &packet); err != nil {
		t.Fatalf("review is not JSON: %v\n%s", err, r.out)
	}
	return packet
}

func findReviewEntry(t *testing.T, packet reviewPacket, event, id string) reviewEntry {
	t.Helper()
	for _, entry := range packet.Entries {
		if entry.Event == event && entry.ID == id {
			return entry
		}
	}
	t.Fatalf("missing %s %s in %+v", event, id, packet.Entries)
	return reviewEntry{}
}

func TestInspectReviewSeparatesDeliveryWritesAndRetirements(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "a", "Base instructions.")
	delivered := h.ok("note", "a", "Review the current evidence before deciding.").id(t)
	retired := h.ok("remember", "a", "An obsolete recovery from an earlier episode.").id(t)
	loaded := h.ok("load", "a", "--task", "review evidence", "--format", "json").json(t)
	ctx := loaded["context_id"].(string)
	ctxRef := loaded["context_ref"].(string)

	successor := h.ok("note", "--context", ctx, "--supersedes", delivered, "Review evidence and verify the result.").id(t)
	write := h.ok("remember", "--context", ctx, "This episode found a repeatable boundary.").id(t)
	h.ok("disable", retired, "--context", ctx, "--reason", "The old recovery no longer applies.")
	before := h.ok("inspect", ctx, "--format", "json").out

	packet := reviewResult(t, h.run("inspect", ctxRef, "--review", "--format", "json"))
	if packet.Version != 1 || packet.Context.ID != ctx || packet.Context.Ref != ctxRef || packet.Context.Agent != "a" {
		t.Fatalf("review lost receipt identity: %+v", packet.Context)
	}
	if packet.Counts.Delivered < 2 || packet.Counts.Writes != 2 || packet.Counts.Retirements != 1 ||
		packet.Counts.Total != packet.Counts.Delivered+packet.Counts.Writes+packet.Counts.Retirements || packet.Counts.Remaining != 0 {
		t.Fatalf("wrong evidence counts: %+v", packet.Counts)
	}
	old := findReviewEntry(t, packet, "delivered", delivered)
	if old.Ordinal == nil || old.Section != "recent" || old.Current == nil || old.Current.ID != successor || old.Current.Relation != "successor" {
		t.Fatalf("historical delivery lost order or current successor: %+v", old)
	}
	written := findReviewEntry(t, packet, "write", write)
	if written.Current == nil || written.Current.ID != write || written.Current.Relation != "self" || written.Current.InspectionPreview != "" {
		t.Fatalf("current write should not duplicate its own preview: %+v", written)
	}
	decision := findReviewEntry(t, packet, "retirement", retired)
	if decision.Status != "disabled" || decision.Current == nil || decision.Current.Relation != "retired" ||
		decision.ReasonPreview != "The old recovery no longer applies." {
		t.Fatalf("retirement evidence incomplete: %+v", decision)
	}
	if got := h.ok("inspect", ctx, "--format", "json").out; got != before {
		t.Fatal("review changed its immutable receipt")
	}

	var viaMCP reviewPacket
	if err := json.Unmarshal([]byte(mcpText(t, mcpInspectResponse(t, h, map[string]any{"target": ctxRef, "review": true}))), &viaMCP); err != nil {
		t.Fatal(err)
	}
	if viaMCP.Context.ID != packet.Context.ID || viaMCP.Counts != packet.Counts || len(viaMCP.Entries) != len(packet.Entries) {
		t.Fatalf("MCP review diverged from CLI: cli=%+v mcp=%+v", packet, viaMCP)
	}
	if yaml := h.ok("inspect", ctxRef, "--review", "--format", "yaml").out; !strings.Contains(yaml, "context_id: "+ctx) || !strings.Contains(yaml, "event: retirement") {
		t.Fatalf("YAML review lost evidence: %s", yaml)
	}
}

func TestInspectReviewPreservesForeignDeliveryAndSingleConsolidationWrite(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "source", "Source base.")
	h.ok("base", "a", "Subscriber base.")
	foreign := h.ok("note", "source", "Shared evidence standard.").id(t)
	h.ok("agent", "follow", "a/shared-standard", "source", "--expect", "none")
	left := h.ok("remember", "a", "First observation.").id(t)
	right := h.ok("remember", "a", "Second observation.").id(t)
	loaded := h.ok("load", "a", "--task", "observation evidence", "--format", "json").json(t)
	ctx := loaded["context_id"].(string)
	consolidated := h.ok("consolidate", "Merged observation.", "--context", ctx, "--source", left, "--source", right, "--reason", "Both observations support one lesson.").id(t)

	packet := reviewResult(t, h.run("inspect", ctx, "--review"))
	shared := findReviewEntry(t, packet, "delivered", foreign)
	if shared.Agent != "source" || shared.Section != "shared-guidance" {
		t.Fatalf("review reassigned foreign delivery: %+v", shared)
	}
	if packet.Counts.Writes != 1 {
		t.Fatalf("consolidation sources multiplied writes: %+v", packet.Counts)
	}
	findReviewEntry(t, packet, "write", consolidated)
}

func TestInspectReviewPagesBeforeInspectingFarTailHistory(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "a", "Base.")
	loaded := h.ok("load", "a", "--format", "json").json(t)
	ctx := loaded["context_id"].(string)
	for i := 0; i < 35; i++ {
		h.ok("remember", "--context", ctx, fmt.Sprintf("Episode memory %02d %s", i, strings.Repeat("bounded review material ", 40)))
	}
	cycleA := h.ok("remember", "--context", ctx, "Far tail cycle A.").id(t)
	cycleB := h.ok("remember", "--context", ctx, "Far tail cycle B.").id(t)

	s, err := store.Open(h.home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`UPDATE records SET supersedes_id = ? WHERE id = ?`, cycleB, cycleA); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`UPDATE records SET supersedes_id = ? WHERE id = ?`, cycleA, cycleB); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	first := reviewResult(t, h.run("inspect", ctx, "--review", "--format", "json"))
	if first.Next == nil || first.Counts.Remaining == 0 {
		t.Fatalf("large review did not paginate: %+v", first.Counts)
	}
	for _, entry := range first.Entries {
		if entry.ID == cycleA || entry.ID == cycleB {
			t.Fatal("far-tail corrupt history was processed on the first page")
		}
	}
	other := h.ok("load", "a", "--format", "json").id(t)
	if r := h.run("inspect", other, "--review", "--review-after", first.Next.ReviewAfter); r.code != 2 || !strings.Contains(r.err, "another context") {
		t.Fatalf("cursor escaped its receipt: code=%d stderr=%q", r.code, r.err)
	}

	next := first.Next.ReviewAfter
	for pages := 0; pages < 20; pages++ {
		r := h.run("inspect", ctx, "--review", "--review-after", next, "--format", "json")
		if r.code != 0 {
			if !strings.Contains(r.err, "cyclic supersession history") {
				t.Fatalf("tail failed for the wrong reason: %s", r.err)
			}
			return
		}
		page := reviewResult(t, r)
		if page.Next == nil {
			t.Fatal("review reached the end without inspecting the corrupt tail")
		}
		next = page.Next.ReviewAfter
	}
	t.Fatal("review pagination did not terminate")
}

func TestInspectReviewSyntaxPrecedesReferencesAndStore(t *testing.T) {
	cases := [][]string{
		{"inspect", "@999", "--review", "--review-after", ""},
		{"inspect", "@999", "--review", "--query", "x"},
		{"inspect", "@999", "--review", "--review-after", "not-a-cursor"},
		{"inspect", "rec_123", "--review"},
		{"inspect", "@999", "--review=false", "--review-after", "cursor"},
	}
	for _, args := range cases {
		h := newHarness(t)
		h.home = filepath.Join(h.home, "unopened")
		r := h.run(args...)
		if r.code != 2 || strings.Contains(r.err, "not found") {
			t.Fatalf("syntax was masked by lookup: %v => code=%d stderr=%q", args, r.code, r.err)
		}
		if _, err := os.Stat(h.home); !os.IsNotExist(err) {
			t.Fatalf("invalid review syntax opened the store: %v", err)
		}
	}

	h := newHarness(t)
	response := mcpInspectResponse(t, h, map[string]any{"target": "@999", "review": false, "review_after": "cursor"})
	if failure, ok := response["error"].(map[string]any); !ok || failure["code"] != float64(-32602) {
		t.Fatalf("MCP accepted review_after outside review mode: %+v", response)
	}
}

func TestInspectReviewRejectsLocalReferencesOfOtherKinds(t *testing.T) {
	h := newHarness(t)
	base := h.ok("base", "a", "Base.").id(t)
	record := h.ok("remember", "a", "Memory.").id(t)
	signal := h.ok("signal", "a", "--subject", "review").id(t)
	generation := h.okIn("input_entries: []\nitems: []\nentries: []\n", "brief", "put", "a", "--expect-generation", "none", "--expect-base", base, "--stdin").id(t)
	for _, id := range []string{record, signal, generation} {
		ref := localRef(t, h, id)
		r := h.run("inspect", ref, "--review")
		if r.code != 2 || !strings.Contains(r.err, "context receipt") {
			t.Fatalf("review accepted %s reference %s: code=%d stderr=%q", id, ref, r.code, r.err)
		}
	}
}

func TestReviewTextCollapsesWhitespaceAndTruncatesByRune(t *testing.T) {
	preview, truncated := reviewText(" \n  α\tβ  " + strings.Repeat("界", reviewPreviewRunes))
	if !truncated || strings.ContainsAny(preview, "\n\t") || len([]rune(preview)) != reviewPreviewRunes || !strings.HasPrefix(preview, "α β 界") || !strings.HasSuffix(preview, "…") {
		t.Fatalf("bad Unicode preview: %q truncated=%v runes=%d", preview, truncated, len([]rune(preview)))
	}
}
