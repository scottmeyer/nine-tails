package capsule

import (
	"database/sql"
	"encoding/json"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	"github.com/scottmeyer/nine-tails/internal/store"
)

func setup(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func insert(t *testing.T, s *store.Store, nr store.NewRecord) *store.Record {
	t.Helper()
	var rec *store.Record
	if err := s.Tx(func(tx *sql.Tx) error {
		var err error
		rec, err = store.InsertRecord(tx, nr)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return rec
}

func TestLoadShape(t *testing.T) {
	s := setup(t)
	insert(t, s, store.NewRecord{Agent: "pr-review", Lane: "definition", Kind: "agent-base", Name: "base", Body: "## Purpose\n\nReview PRs."})
	insert(t, s, store.NewRecord{Agent: "pr-review", Lane: "state", Kind: "working-state", Name: "working", Body: "status: waiting"})
	g1 := insert(t, s, store.NewRecord{Agent: "pr-review", Lane: "guidance", Kind: "prefer", Body: "Lead with evidence.", Meta: store.Meta{"phase": {"review"}}})
	insert(t, s, store.NewRecord{Agent: "pr-review", Lane: "guidance", Kind: "avoid", Body: "Restating the finding.\nSecond line."})
	insert(t, s, store.NewRecord{Agent: "pr-review", Lane: "guidance", Kind: "note", Body: "Only for rust", Meta: store.Meta{"language": {"rust"}}})
	insert(t, s, store.NewRecord{Agent: "pr-review", Lane: "recall", Kind: "memory", Body: "never rendered"})
	insert(t, s, store.NewRecord{Agent: "pr-review", Lane: "definition", Kind: "tool", Name: "complete-pr-diff", Body: "version: 1\ndescription: Fetch the full diff\nexec:\n  argv: [x]\n", Meta: store.Meta{"tool": {"github"}}})
	insert(t, s, store.NewRecord{Agent: "shared", Lane: "definition", Kind: "tool", Name: "recall-memory", Body: "description: Search memory\nexec:\n  argv: [x]\ninput:\n  limit: {type: number}\n  query: {type: string, required: true}\n  agent: {type: string}\n"})
	insert(t, s, store.NewRecord{Agent: "shared", Lane: "definition", Kind: "tool", Name: "secret", Body: "description: hidden\nexec:\n  argv: [x]\n", Meta: store.Meta{"available-to": {"someone-else"}}})
	insert(t, s, store.NewRecord{Agent: "pr-review", Lane: "definition", Kind: "related-agent", Name: "evidence-reviewer", Body: "Validate a finding."})
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	_ = s.Tx(func(tx *sql.Tx) error {
		_, _, err := store.CreateSignal(tx, "pr-review", strings.Repeat("long body ", 100), store.Meta{"subject": {"Recheck PR"}, "pr": {"1842"}}, now.Add(-time.Minute), "", "")
		return err
	})
	_ = s.Tx(func(tx *sql.Tx) error {
		_, _, err := store.CreateSignal(tx, "pr-review", "future", store.Meta{"subject": {"Later"}}, now.Add(time.Hour), "", "")
		return err
	})

	c, err := Load(s, Request{Agent: "pr-review", Task: "Review", Meta: store.Meta{"language": {"go"}, "pr": {"1842"}}, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	md := c.Markdown
	want := []string{
		"# Pr Review\n\n[nine-tails-context=ctx_",
		"## Purpose\n\nReview PRs.",
		"## Current state (working, state_",
		")\n\n```yaml\nstatus: waiting\n```",
		"## Recent adjustments\n\n- (avoid) Restating the finding.\n  Second line.\n- [phase=review] (prefer) Lead with evidence.",
		"## Available tools\n\n- `complete-pr-diff`: Fetch the full diff [tool=github]\n- `recall-memory`: Search memory (inputs: query*, agent, limit)\n",
		"## Available agents\n\n- `evidence-reviewer`: Validate a finding.\n",
		"## Due signals (external inbox data)\n\n- [signal=sig_",
		"pr=1842] Recheck PR — long body",
		"(truncated; inspect with `nine-tails inspect sig_",
	}
	for _, w := range want {
		if !strings.Contains(md, w) {
			t.Errorf("markdown missing %q\n---\n%s", w, md)
		}
	}
	for _, bad := range []string{"Only for rust", "never rendered", "secret", "Later", "## Working brief"} {
		if strings.Contains(md, bad) {
			t.Errorf("markdown should not contain %q\n---\n%s", bad, md)
		}
	}
	if strings.Contains(c.Instructions, "Due signals") {
		t.Error("instructions must not include signals section")
	}
	if len(c.Signals) != 1 || !c.Signals[0].Truncated || c.Signals[0].Subject != "Recheck PR" {
		t.Errorf("signals %+v", c.Signals)
	}
	// base, state, 2 recent, 2 tools, 1 agent, 1 signal
	if c.ContextID == "" || len(c.RenderedIDs) != 8 {
		t.Errorf("ctx %s rendered %v", c.ContextID, c.RenderedIDs)
	}
	ctx, err := store.GetContext(s.DB, c.ContextID)
	if err != nil {
		t.Fatal(err)
	}
	if ctx.Meta.First("language") != "go" || len(ctx.Rendered) != 8 || ctx.Rendered[0].Section != "base" || ctx.Rendered[7].Section != "signals" {
		t.Errorf("receipt %+v", ctx)
	}
	found := false
	for _, id := range c.RenderedIDs {
		if id == g1.ID {
			found = true
		}
	}
	if !found {
		t.Errorf("g1 not in receipt")
	}
	if c.UncompiledAdjustments != 2 {
		t.Errorf("both recent entries should count as uncompiled, got %d", c.UncompiledAdjustments)
	}
}

func TestLoadSkipsCorruptTextRecordsAndOrphanedSignal(t *testing.T) {
	s := setup(t)
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	base := insert(t, s, store.NewRecord{Agent: "a", Lane: "definition", Kind: "agent-base", Name: "base", Body: "Healthy base."})
	goodRecent := insert(t, s, store.NewRecord{Agent: "a", Lane: "guidance", Kind: "note", Body: "Healthy recent guidance."})
	badRecent := insert(t, s, store.NewRecord{Agent: "a", Lane: "guidance", Kind: "note", Body: "Corrupt recent guidance."})
	goodAgent := insert(t, s, store.NewRecord{Agent: "a", Lane: "definition", Kind: "related-agent", Name: "healthy", Body: "Healthy related agent."})
	badAgent := insert(t, s, store.NewRecord{Agent: "a", Lane: "definition", Kind: "related-agent", Name: "corrupt", Body: "Corrupt related agent."})

	var goodItem, badItem *store.Record
	var goodSignal, badSignal, orphanedSignal *store.Signal
	invalid := string([]byte{0xff, 0xfe})
	if err := s.Tx(func(tx *sql.Tx) error {
		_, items, err := store.InstallGeneration(tx, "a", "", []store.NewItem{
			{Key: "healthy", Body: "Healthy brief item."},
			{Key: "corrupt", Body: "Corrupt brief item."},
		}, nil)
		if err != nil {
			return err
		}
		goodItem, badItem = items[0], items[1]
		if goodSignal, _, err = store.CreateSignal(tx, "a", "Healthy signal body.", store.Meta{"subject": {"Healthy signal"}}, now, "", ""); err != nil {
			return err
		}
		if badSignal, _, err = store.CreateSignal(tx, "a", "Corrupt signal body.", store.Meta{"subject": {"Corrupt signal"}}, now, "", ""); err != nil {
			return err
		}
		if orphanedSignal, _, err = store.CreateSignal(tx, "a", "Orphan this signal.", store.Meta{"subject": {"Orphan"}}, now, "", ""); err != nil {
			return err
		}
		for _, id := range []string{badRecent.ID, badAgent.ID, badItem.ID, badSignal.Record.ID} {
			if err := store.SetBody(tx, id, invalid); err != nil {
				return err
			}
		}
		_, err = tx.Exec(`DELETE FROM records WHERE id = ?`, orphanedSignal.Record.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	c, err := Load(s, Request{Agent: "a", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	for _, healthy := range []string{"Healthy base.", "Healthy recent guidance.", "Healthy brief item.", "Healthy related agent.", "Healthy signal body."} {
		if !strings.Contains(c.Markdown, healthy) {
			t.Errorf("capsule omitted healthy content %q:\n%s", healthy, c.Markdown)
		}
	}
	if !utf8.ValidString(c.Markdown) || !utf8.ValidString(c.Instructions) {
		t.Fatalf("capsule contains invalid UTF-8: markdown=%q instructions=%q", c.Markdown, c.Instructions)
	}

	wantReasons := map[string]string{
		badRecent.ID:             "recent guidance body is not valid UTF-8 text",
		badAgent.ID:              "related-agent body is not valid UTF-8 text",
		badItem.ID:               "brief item body is not valid UTF-8 text",
		badSignal.Record.ID:      "signal body is not valid UTF-8 text",
		orphanedSignal.Record.ID: "signal delivery references a missing record",
	}
	wantSkipped := make([]string, 0, len(wantReasons))
	for id := range wantReasons {
		wantSkipped = append(wantSkipped, id)
	}
	sort.Strings(wantSkipped)
	if len(c.Skipped) != len(wantSkipped) {
		t.Fatalf("skipped = %+v, want IDs %v", c.Skipped, wantSkipped)
	}
	for i, skipped := range c.Skipped {
		if skipped.ID != wantSkipped[i] || skipped.Reason != wantReasons[skipped.ID] {
			t.Errorf("skipped[%d] = %+v, want id=%s reason=%q", i, skipped, wantSkipped[i], wantReasons[wantSkipped[i]])
		}
	}

	rendered := make(map[string]bool, len(c.RenderedIDs))
	for _, id := range c.RenderedIDs {
		rendered[id] = true
	}
	for _, id := range []string{base.ID, goodRecent.ID, goodItem.ID, goodAgent.ID, goodSignal.Record.ID} {
		if !rendered[id] {
			t.Errorf("healthy record %s missing from rendered_record_ids: %v", id, c.RenderedIDs)
		}
	}
	for _, id := range wantSkipped {
		if rendered[id] {
			t.Errorf("skipped record %s appears in rendered_record_ids: %v", id, c.RenderedIDs)
		}
	}
	ctx, err := store.GetContext(s.DB, c.ContextID)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range ctx.Rendered {
		if wantReasons[row.RecordID] != "" {
			t.Errorf("skipped record %s appears in receipt %+v", row.RecordID, ctx.Rendered)
		}
	}
	if len(c.Agents) != 1 || c.Agents[0] != goodAgent.Name || len(c.Signals) != 1 || c.Signals[0].ID != goodSignal.Record.ID {
		t.Errorf("structured optional sections kept corrupt records: agents=%v signals=%+v", c.Agents, c.Signals)
	}
	if b, err := json.Marshal(c); err != nil || !utf8.Valid(b) {
		t.Errorf("JSON is not valid UTF-8: bytes=%q err=%v", b, err)
	}
	if b, err := yaml.Marshal(c); err != nil || !utf8.Valid(b) {
		t.Errorf("YAML is not valid UTF-8: bytes=%q err=%v", b, err)
	}

	for _, id := range []string{badRecent.ID, badAgent.ID, badItem.ID, badSignal.Record.ID} {
		rec, err := store.GetRecord(s.DB, id)
		if err != nil {
			t.Fatal(err)
		}
		if rec.Body != invalid || rec.Status != "active" {
			t.Errorf("load mutated corrupt record %s: body=%q status=%s", id, rec.Body, rec.Status)
		}
	}
	if _, err := store.GetDelivery(s.DB, orphanedSignal.Record.ID); err != nil {
		t.Fatalf("load mutated orphaned delivery: %v", err)
	}
}

func TestLoadRejectsCorruptBaseBody(t *testing.T) {
	s := setup(t)
	base := insert(t, s, store.NewRecord{Agent: "a", Lane: "definition", Kind: "agent-base", Name: "base", Body: "Healthy base."})
	invalid := string([]byte{0xff})
	if err := s.Tx(func(tx *sql.Tx) error { return store.SetBody(tx, base.ID, invalid) }); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(s, Request{Agent: "a"}); err == nil || !strings.Contains(err.Error(), base.ID) || !strings.Contains(err.Error(), "corrupt body") {
		t.Fatalf("Load error = %v, want fatal corrupt-base error naming %s", err, base.ID)
	}
	rec, err := store.GetRecord(s.DB, base.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Body != invalid || rec.Status != "active" {
		t.Errorf("load mutated corrupt base: body=%q status=%s", rec.Body, rec.Status)
	}
}

func TestInheritanceAndConflict(t *testing.T) {
	s := setup(t)
	insert(t, s, store.NewRecord{Agent: "a", Lane: "definition", Kind: "agent-base", Name: "base", Body: "A"})
	insert(t, s, store.NewRecord{Agent: "b", Lane: "definition", Kind: "agent-base", Name: "base", Body: "B", Meta: store.Meta{"title": {"Bee"}}})
	insert(t, s, store.NewRecord{Agent: "b", Lane: "guidance", Kind: "note", Body: "repo-specific", Meta: store.Meta{"repo-id": {"r1"}}})
	insert(t, s, store.NewRecord{Agent: "b", Lane: "guidance", Kind: "note", Body: "other-repo", Meta: store.Meta{"repo-id": {"r2"}}})
	insert(t, s, store.NewRecord{Agent: "b", Lane: "state", Kind: "working-state", Name: "w", Body: "x: 1", Meta: store.Meta{"repo-id": {"r2"}}})

	parent, err := Load(s, Request{Agent: "a", Meta: store.Meta{"repo-id": {"r1"}}})
	if err != nil {
		t.Fatal(err)
	}
	child, err := Load(s, Request{Agent: "b", Parent: parent.ContextID, Task: "narrow", Meta: store.Meta{"pr": {"9"}}})
	if err != nil {
		t.Fatal(err)
	}
	if child.Parent != parent.ContextID || child.Metadata.First("repo-id") != "r1" || child.Metadata.First("pr") != "9" {
		t.Errorf("child %+v", child)
	}
	if !strings.HasPrefix(child.Markdown, "# Bee\n") {
		t.Errorf("title: %s", child.Markdown[:20])
	}
	if !strings.Contains(child.Markdown, "repo-specific") || strings.Contains(child.Markdown, "other-repo") {
		t.Errorf("conflict rule failed:\n%s", child.Markdown)
	}
	if strings.Contains(child.Markdown, "Current state") {
		t.Errorf("conflicting state should be excluded:\n%s", child.Markdown)
	}
}

func TestRecentGuidanceUsesTimestampRecency(t *testing.T) {
	s := setup(t)
	oldClock := store.Clock
	t.Cleanup(func() { store.Clock = oldClock })
	store.Clock = func() time.Time { return time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC) }
	insert(t, s, store.NewRecord{Agent: "a", Lane: "definition", Kind: "agent-base", Name: "base", Body: "Base."})

	store.Clock = func() time.Time { return time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC) }
	insert(t, s, store.NewRecord{Agent: "a", Lane: "guidance", Kind: "note", Body: "newer timestamp"})
	store.Clock = func() time.Time { return time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC) }
	insert(t, s, store.NewRecord{Agent: "a", Lane: "guidance", Kind: "note", Body: "older timestamp but larger id"})

	c, err := Load(s, Request{Agent: "a", Now: time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	newer := strings.Index(c.Markdown, "newer timestamp")
	older := strings.Index(c.Markdown, "older timestamp but larger id")
	if newer < 0 || older < 0 || newer > older {
		t.Fatalf("recent guidance is not newest-by-timestamp first:\n%s", c.Markdown)
	}
}

func TestRepresentedEntriesLeaveRecent(t *testing.T) {
	s := setup(t)
	insert(t, s, store.NewRecord{Agent: "a", Lane: "definition", Kind: "agent-base", Name: "base", Body: "Base."})
	e1 := insert(t, s, store.NewRecord{Agent: "a", Lane: "guidance", Kind: "prefer", Body: "represented one"})
	e2 := insert(t, s, store.NewRecord{Agent: "a", Lane: "guidance", Kind: "prefer", Body: "deferred one"})
	if err := s.Tx(func(tx *sql.Tx) error {
		_, _, err := store.InstallGeneration(tx, "a", "",
			[]store.NewItem{{Key: "one", Body: "Compiled one.", Sources: []string{e1.ID}}},
			[]store.BriefInput{
				{EntryID: e1.ID, Disposition: "represented", Coverage: "novel", Items: []string{"one"}},
				{EntryID: e2.ID, Disposition: "deferred", Coverage: "unknown"},
			})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	e3 := insert(t, s, store.NewRecord{Agent: "a", Lane: "guidance", Kind: "avoid", Body: "after compile"})
	c, err := Load(s, Request{Agent: "a"})
	if err != nil {
		t.Fatal(err)
	}
	md := c.Markdown
	if !strings.Contains(md, "## Working brief\n\n- Compiled one.") {
		t.Errorf("brief missing:\n%s", md)
	}
	if strings.Contains(md, "represented one") {
		t.Errorf("represented entry should not render as recent:\n%s", md)
	}
	if !strings.Contains(md, "(avoid) after compile") || !strings.Contains(md, "(prefer) deferred one") {
		t.Errorf("recent missing:\n%s", md)
	}
	// newest first within recent
	if strings.Index(md, "after compile") > strings.Index(md, "deferred one") {
		t.Errorf("recent should be newest first:\n%s", md)
	}
	_ = e3
}

func TestCapsuleYAMLShape(t *testing.T) {
	c := Capsule{
		ContextID:    "ctx_7",
		Agent:        "a",
		Task:         "do the thing",
		Parent:       "ctx_6",
		Metadata:     store.Meta{"repo-id": {"r1"}},
		Instructions: "# A\n",
		State:        []StateView{{ID: "state_1", Name: "working", Format: "yaml", Body: "status: ready"}},
		Tools:        []string{"tool-a"},
		Agents:       []string{"helper"},
		Signals: []SignalView{{
			ID: "sig_2", Subject: "wake", Excerpt: "body", Truncated: true, State: "leased",
			LeasedUntil: "2026-09-04T12:05:00Z", Meta: store.Meta{}, Inspect: "nine-tails inspect sig_2",
		}},
		RenderedIDs:           []string{"base_1", "state_1", "sig_2"},
		EstimatedTokens:       42,
		UncompiledAdjustments: 2,
		Skipped:               []Skipped{{ID: "tool_9", Reason: "bad body"}},
		Markdown:              "must not be serialized",
	}
	b, err := yaml.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := yaml.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		"context_id", "agent", "task", "parent_context", "metadata", "instructions", "state", "tools", "agents",
		"signals", "rendered_record_ids", "estimated_tokens", "uncompiled_adjustments", "skipped",
	} {
		if _, ok := got[key]; !ok {
			t.Errorf("YAML missing %q:\n%s", key, b)
		}
	}
	for _, key := range []string{"contextid", "parent", "renderedids", "estimatedtokens", "markdown"} {
		if _, ok := got[key]; ok {
			t.Errorf("YAML must not contain implementation key %q:\n%s", key, b)
		}
	}
	signals, ok := got["signals"].([]any)
	if !ok || len(signals) != 1 {
		t.Fatalf("signals shape: %#v\n%s", got["signals"], b)
	}
	signal, ok := signals[0].(map[string]any)
	if !ok || signal["leased_until"] != "2026-09-04T12:05:00Z" {
		t.Errorf("nested signal must use leased_until: %#v\n%s", signals[0], b)
	}
	if strings.Contains(string(b), "must not be serialized") {
		t.Errorf("Markdown leaked into structured YAML:\n%s", b)
	}
}

func TestMarkdownListHelpers(t *testing.T) {
	t.Run("blank continuation lines stay inside the list item", func(t *testing.T) {
		const body = "first line\n\nthird line\n"
		const want = "first line\n  \n  third line\n  "
		if got := indentItem(body); got != want {
			t.Fatalf("indentItem() = %q, want %q", got, want)
		}
	})

	t.Run("all Unicode whitespace triggers metadata quoting", func(t *testing.T) {
		meta := store.Meta{
			"carriage-return": {"left\rright"},
			"em-space":        {"left\u2003right"},
			"no-break-space":  {"left\u00a0right"},
			"plain":           {"left-right"},
		}
		const want = "[carriage-return=\"left\rright\" em-space=\"left\u2003right\" no-break-space=\"left\u00a0right\" plain=left-right] "
		if got := bracket(meta, nil); got != want {
			t.Fatalf("bracket() = %q, want %q", got, want)
		}
	})
}

func TestOwnedToolShadowsSharedWhenOwnedConflicts(t *testing.T) {
	s := setup(t)
	insert(t, s, store.NewRecord{Agent: "a", Lane: "definition", Kind: "agent-base", Name: "base", Body: "Base."})
	insert(t, s, store.NewRecord{
		Agent: "a", Lane: "definition", Kind: "tool", Name: "same", Meta: store.Meta{"repo": {"one"}},
		Body: "description: owned\nexec:\n  argv: [owned]",
	})
	insert(t, s, store.NewRecord{
		Agent: "shared", Lane: "definition", Kind: "tool", Name: "same",
		Body: "description: shared\nexec:\n  argv: [shared]",
	})

	c, err := Load(s, Request{Agent: "a", Meta: store.Meta{"repo": {"two"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Tools) != 0 || strings.Contains(c.Markdown, "description: shared") || strings.Contains(c.Markdown, "`same`: shared") {
		t.Fatalf("conflicting owned name must shadow shared definition: tools=%v\n%s", c.Tools, c.Markdown)
	}
}
