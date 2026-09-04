package capsule

import (
	"database/sql"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

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
	insert(t, s, store.NewRecord{Agent: "shared", Lane: "definition", Kind: "tool", Name: "recall-memory", Body: "description: Search memory\nexec:\n  argv: [x]\n"})
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

	c, err := Load(s, Request{Agent: "pr-review", Task: "Review", Meta: store.Meta{"language": {"go"}, "pr": {"1842"}}, Budget: 2000, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	md := c.Markdown
	want := []string{
		"# Pr Review\n\n[nine-tails-context=ctx_",
		"## Purpose\n\nReview PRs.",
		"## Current state (working, state_2)\n\n```yaml\nstatus: waiting\n```",
		"## Recent adjustments\n\n- (avoid) Restating the finding.\n  Second line.\n- [phase=review] (prefer) Lead with evidence.",
		"## Available tools\n\n- `complete-pr-diff`: Fetch the full diff [tool=github]\n- `recall-memory`: Search memory\n",
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
	if c.Truncated == nil || len(c.Truncated) != 0 {
		t.Errorf("unexpected truncation %+v", c.Truncated)
	}
}

func TestInheritanceAndConflict(t *testing.T) {
	s := setup(t)
	insert(t, s, store.NewRecord{Agent: "a", Lane: "definition", Kind: "agent-base", Name: "base", Body: "A"})
	insert(t, s, store.NewRecord{Agent: "b", Lane: "definition", Kind: "agent-base", Name: "base", Body: "B", Meta: store.Meta{"title": {"Bee"}}})
	insert(t, s, store.NewRecord{Agent: "b", Lane: "guidance", Kind: "note", Body: "repo-specific", Meta: store.Meta{"repo-id": {"r1"}}})
	insert(t, s, store.NewRecord{Agent: "b", Lane: "guidance", Kind: "note", Body: "other-repo", Meta: store.Meta{"repo-id": {"r2"}}})
	insert(t, s, store.NewRecord{Agent: "b", Lane: "state", Kind: "working-state", Name: "w", Body: "x: 1", Meta: store.Meta{"repo-id": {"r2"}}})

	parent, err := Load(s, Request{Agent: "a", Meta: store.Meta{"repo-id": {"r1"}}, Budget: 500})
	if err != nil {
		t.Fatal(err)
	}
	child, err := Load(s, Request{Agent: "b", Parent: parent.ContextID, Task: "narrow", Meta: store.Meta{"pr": {"9"}}, Budget: 500})
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

	c, err := Load(s, Request{Agent: "a", Budget: 1000, Now: time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	newer := strings.Index(c.Markdown, "newer timestamp")
	older := strings.Index(c.Markdown, "older timestamp but larger id")
	if newer < 0 || older < 0 || newer > older {
		t.Fatalf("recent guidance is not newest-by-timestamp first:\n%s", c.Markdown)
	}
}

func TestBudgetFloorsAndCaps(t *testing.T) {
	s := setup(t)
	insert(t, s, store.NewRecord{Agent: "a", Lane: "definition", Kind: "agent-base", Name: "base", Body: "Base."})
	// 10 brief items in a generation, each ~15 tokens
	var items []store.NewItem
	for i := 0; i < 10; i++ {
		items = append(items, store.NewItem{Key: "k" + string(rune('a'+i)), Body: strings.Repeat("brief item text ", 3)})
	}
	if err := s.Tx(func(tx *sql.Tx) error {
		_, _, err := store.InstallGeneration(tx, "a", "", items, nil)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	// 20 recent entries, each ~15 tokens, newest first; a burst must not evict the brief
	for i := 0; i < 20; i++ {
		insert(t, s, store.NewRecord{Agent: "a", Lane: "guidance", Kind: "note", Body: strings.Repeat("recent note ", 4)})
	}
	c, err := Load(s, Request{Agent: "a", Budget: 300})
	if err != nil {
		t.Fatal(err)
	}
	if c.EstimatedTokens > 300 {
		t.Errorf("over budget: %d", c.EstimatedTokens)
	}
	brief := strings.Count(c.Markdown, "- brief item")
	recent := strings.Count(c.Markdown, "(note) recent note")
	if brief == 0 || recent == 0 {
		t.Errorf("brief=%d recent=%d\n%s", brief, recent, c.Markdown)
	}
	if brief < 4 {
		t.Errorf("brief floor not honored: %d items\n%s", brief, c.Markdown)
	}
	var recentTrunc, briefTrunc int
	for _, tr := range c.Truncated {
		switch tr.Section {
		case "recent":
			recentTrunc = tr.Omitted
		case "brief":
			briefTrunc = tr.Omitted
		}
	}
	if recentTrunc+recent != 20 || briefTrunc+brief != 10 {
		t.Errorf("truncation accounting: %+v brief=%d recent=%d", c.Truncated, brief, recent)
	}

	// budget too small for base → budget error
	insert(t, s, store.NewRecord{Agent: "big", Lane: "definition", Kind: "agent-base", Name: "base", Body: strings.Repeat("x", 4000)})
	if _, err := Load(s, Request{Agent: "big", Budget: 100}); !IsBudgetError(err) {
		t.Errorf("want budget error, got %v", err)
	}
	// missing agent → not found
	if _, err := Load(s, Request{Agent: "nope", Budget: 100}); err == nil {
		t.Errorf("want not found")
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
	c, err := Load(s, Request{Agent: "a", Budget: 1000})
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
		RenderedIDs:     []string{"base_1", "state_1", "sig_2"},
		Budget:          2000,
		EstimatedTokens: 42,
		Truncated:       []Truncation{{Section: "recent", Omitted: 2}},
		Skipped:         []Skipped{{ID: "tool_9", Reason: "bad body"}},
		Markdown:        "must not be serialized",
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
		"signals", "rendered_record_ids", "budget", "estimated_tokens", "truncated", "skipped",
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

func TestLoadRejectsMalformedPolicy(t *testing.T) {
	s := setup(t)
	insert(t, s, store.NewRecord{Agent: "a", Lane: "definition", Kind: "agent-base", Name: "base", Body: "Base."})

	negative := DefaultPolicy
	negative.BriefFloor = -0.1
	tooLarge := DefaultPolicy
	tooLarge.ToolsCap = 1.1
	overTotal := DefaultPolicy
	overTotal.SignalsCap = 0.25
	nan := DefaultPolicy
	nan.RecentCap = math.NaN()
	noExcerpt := DefaultPolicy
	noExcerpt.SignalExcerptChars = 0
	cases := []struct {
		name   string
		policy Policy
	}{
		{"negative allocation", negative},
		{"allocation over one", tooLarge},
		{"allocations total over one", overTotal},
		{"non-finite allocation", nan},
		{"non-positive excerpt", noExcerpt},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(s, Request{Agent: "a", Budget: 500, Policy: tc.policy})
			if !errors.Is(err, store.ErrInvalid) {
				t.Fatalf("want ErrInvalid, got %v", err)
			}
		})
	}
}

func TestLoadAllowsZeroSectionAllocations(t *testing.T) {
	s := setup(t)
	insert(t, s, store.NewRecord{Agent: "a", Lane: "definition", Kind: "agent-base", Name: "base", Body: "Base."})
	policy := Policy{SignalExcerptChars: DefaultPolicy.SignalExcerptChars}
	if _, err := Load(s, Request{Agent: "a", Budget: 500, Policy: policy}); err != nil {
		t.Fatalf("zero section allocations should be valid: %v", err)
	}
}

func TestLoadDefensivelyClampsToGlobalBudget(t *testing.T) {
	s := setup(t)
	insert(t, s, store.NewRecord{Agent: "a", Lane: "definition", Kind: "agent-base", Name: "base", Body: "Base."})
	items := make([]store.NewItem, 8)
	for i := range items {
		items[i] = store.NewItem{Key: "item" + string(rune('a'+i)), Body: strings.Repeat("bounded brief text ", 4)}
	}
	if err := s.Tx(func(tx *sql.Tx) error {
		_, _, err := store.InstallGeneration(tx, "a", "", items, nil)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	// Bypass Load's policy validation to exercise the final defensive guard.
	// Even an impossible over-allocation must not violate the caller's maximum.
	badPolicy := Policy{BriefFloor: 2, RecentCap: 1, ToolsCap: 1, SignalsCap: 1, SignalExcerptChars: 300}
	var c *Capsule
	if err := s.Tx(func(tx *sql.Tx) error {
		var err error
		c, err = load(tx, Request{Agent: "a", Budget: 100, Policy: badPolicy, Now: time.Now()})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if c.EstimatedTokens > c.Budget {
		t.Fatalf("estimated tokens %d exceed budget %d", c.EstimatedTokens, c.Budget)
	}
	if len(c.RenderedIDs) < 2 {
		t.Fatalf("test did not admit any optional item: %v", c.RenderedIDs)
	}
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

	c, err := Load(s, Request{Agent: "a", Meta: store.Meta{"repo": {"two"}}, Budget: 500})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Tools) != 0 || strings.Contains(c.Markdown, "description: shared") || strings.Contains(c.Markdown, "`same`: shared") {
		t.Fatalf("conflicting owned name must shadow shared definition: tools=%v\n%s", c.Tools, c.Markdown)
	}
}
