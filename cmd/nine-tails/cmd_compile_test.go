package main

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/scottmeyer/nine-tails/internal/store"
)

func strs(t *testing.T, v any) []string {
	t.Helper()
	list, ok := v.([]any)
	if !ok {
		t.Fatalf("not a list: %#v", v)
	}
	out := make([]string, 0, len(list))
	for _, x := range list {
		out = append(out, fmt.Sprint(x))
	}
	return out
}

func equal(a, b []string) bool { return strings.Join(a, ",") == strings.Join(b, ",") }

func TestEmptyBriefDryRunKeepsInputsArrayOnlyInPlan(t *testing.T) {
	for _, format := range []string{"json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			h := newHarness(t)
			base := h.ok("base", "a", "Base.").id(t)
			doc := "input_entries: []\nitems: []\nentries: []\n"

			dry := h.okIn(doc, "brief", "put", "a", "--expect-generation", "none", "--expect-base", base, "--stdin", "--dry-run", "--format", format)
			var plan map[string]any
			if err := yaml.Unmarshal([]byte(dry.out), &plan); err != nil {
				t.Fatalf("decode dry-run %s: %v\n%s", format, err, dry.out)
			}
			inputs, ok := plan["inputs"].([]any)
			if !ok || len(inputs) != 0 {
				t.Fatalf("empty dry-run inputs = %#v, want []\n%s", plan["inputs"], dry.out)
			}

			installed := h.okIn(doc, "brief", "put", "a", "--expect-generation", "none", "--expect-base", base, "--stdin", "--format", format)
			var result map[string]any
			if err := yaml.Unmarshal([]byte(installed.out), &result); err != nil {
				t.Fatalf("decode installed %s: %v\n%s", format, err, installed.out)
			}
			if _, exists := result["inputs"]; exists {
				t.Fatalf("non-dry result contains inputs: %s", installed.out)
			}
		})
	}
}

func TestBriefPutRejectsWrongConditionalFieldsEvenWhenEmpty(t *testing.T) {
	h := newHarness(t)
	base := h.ok("base", "a", "Base.").id(t)
	e1 := h.ok("prefer", "a", "one").id(t)
	e2 := h.ok("prefer", "a", "two").id(t)

	cases := []struct {
		name string
		doc  string
		want string
	}{
		{
			"deferred empty items",
			fmt.Sprintf("input_entries: [%s]\nitems: []\nentries: [{id: %s, disposition: deferred, items: []}]\n", e1, e1),
			"entry " + e1 + " is deferred but lists items",
		},
		{
			"deferred empty successor",
			fmt.Sprintf("input_entries: [%s]\nitems: []\nentries: [{id: %s, disposition: deferred, successor: \"\"}]\n", e1, e1),
			"entry " + e1 + " is deferred but names a successor",
		},
		{
			"represented null successor",
			fmt.Sprintf("input_entries: [%s]\nitems: [{key: k, body: b}]\nentries: [{id: %s, disposition: represented, items: [k], successor: null}]\n", e1, e1),
			"entry " + e1 + " is represented but names a successor",
		},
		{
			"superseded null items",
			fmt.Sprintf("input_entries: [%s]\nitems: []\nentries: [{id: %s, disposition: superseded-by, successor: %s, items: null}]\n", e1, e1, e2),
			"entry " + e1 + " is superseded-by but lists items",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := h.runIn(tc.doc, "brief", "put", "a", "--expect-generation", "none", "--expect-base", base, "--stdin", "--dry-run")
			if r.code != 2 || r.err != "nine-tails: compiler output is invalid\n  "+tc.want+"\n" {
				t.Fatalf("code=%d stderr=%q", r.code, r.err)
			}
		})
	}
}

func TestCompileFlow(t *testing.T) {
	t.Setenv("NINE_TAILS_COMPILER", "")
	h := newHarness(t)
	if r := h.run("compile-input", "nobody"); r.code != 3 {
		t.Errorf("unknown agent should be 3, got %d: %s", r.code, r.err)
	}
	base := h.ok("base", "pr-review", "Review changes carefully.").id(t)
	r := h.ok("load", "pr-review", "--meta", "repo-id=r1")
	ctx := contextID(t, r.out)
	e1 := h.ok("prefer", "pr-review", "--context", ctx, "Lead with evidence.").id(t)
	e2 := h.ok("prefer", "pr-review", "Keep comments short.").id(t)
	e3 := h.ok("prefer", "pr-review", "--meta", "repo-id=r1", "Lead with evidence and cite line numbers.").id(t)

	// ---- compile-input shape ----
	r = h.ok("compile-input", "pr-review", "--budget", "500")
	m := r.json(t)
	if m["agent"] != "pr-review" || m["budget"] != float64(500) {
		t.Errorf("agent/budget: %v %v", m["agent"], m["budget"])
	}
	if s, _ := m["instructions"].(string); !strings.Contains(s, "Account for every supplied guidance entry") || !strings.Contains(s, "superseded-by") {
		t.Errorf("instructions: %q", s)
	}
	if m["expect_generation"] != "none" || m["expect_base"] != base {
		t.Errorf("expect: %v %v", m["expect_generation"], m["expect_base"])
	}
	if bm, _ := m["base"].(map[string]any); bm["id"] != base || bm["body"] != "Review changes carefully." {
		t.Errorf("base: %v", m["base"])
	}
	if m["active_generation"] != nil {
		t.Errorf("active_generation should be null: %v", m["active_generation"])
	}
	want := []string{e1, e2, e3}
	if got := strs(t, m["input_entries"]); !equal(got, want) {
		t.Errorf("input_entries %v want %v", got, want)
	}
	entries := m["entries"].([]any)
	var ids []string
	for _, e := range entries {
		ids = append(ids, e.(map[string]any)["id"].(string))
	}
	if !equal(ids, want) {
		t.Errorf("entries %v want %v", ids, want)
	}
	first := entries[0].(map[string]any)
	if first["origin_context"] != ctx || first["kind"] != "prefer" || first["lane"] != "guidance" {
		t.Errorf("entry 0: %v", first)
	}
	if got := strs(t, first["origin_context_rendered"]); !equal(got, []string{base}) {
		t.Errorf("origin_context_rendered %v", got)
	}
	if om, _ := first["origin_context_metadata"].(map[string]any); !equal(strs(t, om["repo-id"]), []string{"r1"}) {
		t.Errorf("origin_context_metadata %v", first["origin_context_metadata"])
	}
	if _, has := entries[1].(map[string]any)["origin_context_rendered"]; has {
		t.Errorf("entry without origin should not carry rendered ids")
	}
	if r := h.ok("compile-input", "pr-review", "--format", "yaml"); !strings.HasPrefix(r.out, "agent: pr-review\n") || !strings.Contains(r.out, "\nexpect_generation: none\n") {
		t.Errorf("yaml: %s", r.out)
	}

	// ---- brief put: one represented, one deferred, one superseded-by ----
	doc1 := fmt.Sprintf(`
input_entries: [%s, %s, %s]
items:
  - key: concise-evidence
    body: Lead review comments with concrete evidence.
    meta:
      phase: review
entries:
  - id: %s
    disposition: superseded-by
    successor: %s
  - id: %s
    disposition: deferred
  - id: %s
    disposition: represented
    items: [concise-evidence]
`, e1, e2, e3, e1, e3, e2, e3)
	r = h.okIn(doc1, "brief", "put", "pr-review", "--expect-generation", "none", "--expect-base", base, "--stdin")
	gen1 := strings.TrimSpace(r.out)
	if !strings.HasPrefix(gen1, "gen_") || strings.Contains(gen1, "\n") {
		t.Fatalf("brief put should print the generation id: %q", r.out)
	}
	if !strings.Contains(r.err, "nine-tails: warning: ") || !strings.Contains(r.err, "repo-id=r1") {
		t.Errorf("condition-loss warning expected on stderr: %q", r.err)
	}

	// AC8 / AC10: the brief renders as items, represented and superseded
	// entries leave "Recent adjustments", the deferred one stays.
	r = h.ok("load", "pr-review")
	if !strings.Contains(r.out, "## Working brief\n\n- [phase=review] Lead review comments with concrete evidence.\n") {
		t.Errorf("brief section:\n%s", r.out)
	}
	if !strings.Contains(r.out, "## Recent adjustments\n\n- (prefer) Keep comments short.\n") {
		t.Errorf("deferred entry should stay recent:\n%s", r.out)
	}
	if strings.Contains(r.out, "(prefer) Lead with evidence") {
		t.Errorf("represented/superseded entries must not render as recent:\n%s", r.out)
	}
	for _, id := range []string{e1, e2, e3} {
		if r := h.ok("inspect", id); !strings.Contains(r.out, `"status": "active"`) {
			t.Errorf("source entry %s must stay active: %s", id, r.out)
		}
	}
	r = h.ok("inspect", gen1)
	gm := r.json(t)
	if g := gm["generation"].(map[string]any); g["status"] != "active" || g["id"] != gen1 {
		t.Errorf("inspect gen: %v", g)
	}
	if inputs := gm["inputs"].([]any); len(inputs) != 3 {
		t.Errorf("inputs: %v", inputs)
	}
	item1 := gm["items"].([]any)[0].(map[string]any)["id"].(string)

	// a new prefer after the compile is recent again
	e4 := h.ok("prefer", "pr-review", "--meta", "repo-id=r1", "Use bullet lists.").id(t)
	if r = h.ok("load", "pr-review"); !strings.Contains(r.out, "- [repo-id=r1] (prefer) Use bullet lists.\n") {
		t.Errorf("new prefer should be recent:\n%s", r.out)
	}

	// ---- second compile-input: lineage rule ----
	m = h.ok("compile-input", "pr-review").json(t)
	if got := strs(t, m["input_entries"]); !equal(got, []string{e2, e4}) {
		t.Errorf("second input_entries %v want [%s %s]", got, e2, e4)
	}
	if m["expect_generation"] != gen1 {
		t.Errorf("expect_generation %v want %s", m["expect_generation"], gen1)
	}
	ag := m["active_generation"].(map[string]any)
	if ag["id"] != gen1 || ag["items"].([]any)[0].(map[string]any)["key"] != "concise-evidence" {
		t.Errorf("active_generation: %v", ag)
	}
	if m["budget"] != float64(800) {
		t.Errorf("default budget should be brief_floor × default_budget = 800, got %v", m["budget"])
	}

	doc2 := fmt.Sprintf(`{
  "input-entries": ["%s", "%s"],
  "items": [
    {"key": "concise-evidence", "body": "Lead review comments with concrete evidence; keep them short.", "meta": {"phase": "review"}},
    {"key": "bullets", "body": "Use bullet lists."}
  ],
  "entries": [
    {"id": "%s", "disposition": "represented", "items": ["concise-evidence"]},
    {"id": "%s", "disposition": "represented", "items": ["bullets"]}
  ]
}`, e2, e4, e2, e4)

	// stale expectations → 7
	r = h.runIn(doc2, "brief", "put", "pr-review", "--expect-generation", "none", "--expect-base", base, "--stdin")
	if r.code != 7 || !strings.Contains(r.err, "expected none but "+gen1+" is active") {
		t.Errorf("stale generation: %d %q", r.code, r.err)
	}
	r = h.runIn(doc2, "brief", "put", "pr-review", "--expect-generation", gen1, "--expect-base", "base_999", "--stdin")
	if r.code != 7 || !strings.Contains(r.err, "expected base_999 but "+base+" is active") {
		t.Errorf("stale base: %d %q", r.code, r.err)
	}
	// output missing an entry → 2 with the problem line
	doc3 := fmt.Sprintf("input_entries: [%s, %s]\nitems: []\nentries:\n  - {id: %s, disposition: deferred}\n", e2, e4, e2)
	r = h.runIn(doc3, "brief", "put", "pr-review", "--expect-generation", gen1, "--expect-base", base, "--stdin", "--format", "json")
	if r.code != 2 || !strings.HasPrefix(r.err, "nine-tails: compiler output is invalid\n  entry "+e4+" is missing from entries\n") {
		t.Errorf("missing entry: %d %q", r.code, r.err)
	}
	if !strings.Contains(r.out, `"code": 2`) {
		t.Errorf("json error envelope expected on stdout: %q", r.out)
	}
	if r = h.runIn(doc2, "brief", "put", "pr-review", "--expect-generation", gen1, "--expect-base", base); r.code != 2 {
		t.Errorf("--stdin required: %d %q", r.code, r.err)
	}
	// dry run: everything runs, nothing is written
	r = h.okIn(doc2, "brief", "put", "pr-review", "--expect-generation", gen1, "--expect-base", base, "--stdin", "--dry-run")
	dm := r.json(t)
	if dm["dry_run"] != true || !strings.HasPrefix(dm["generation"].(string), "gen_") || len(dm["items"].([]any)) != 2 || len(dm["inputs"].([]any)) != 2 {
		t.Errorf("dry run output: %s", r.out)
	}
	if !strings.Contains(r.err, "nine-tails: warning: ") {
		t.Errorf("dry run should still report lint: %q", r.err)
	}
	if m = h.ok("compile-input", "pr-review").json(t); m["expect_generation"] != gen1 {
		t.Errorf("dry run must not install: %v", m["expect_generation"])
	}

	// ---- second brief put re-using a key ----
	r = h.okIn(doc2, "brief", "put", "pr-review", "--expect-generation", gen1, "--expect-base", base, "--stdin", "--format", "json")
	res := r.json(t)
	gen2 := res["generation"].(string)
	items := strs(t, res["items"])
	if len(items) != 2 || len(res["warnings"].([]any)) != 1 {
		t.Errorf("result: %s", r.out)
	}
	if _, has := res["inputs"]; has {
		t.Errorf("inputs belong to dry-run output only: %s", r.out)
	}
	r = h.ok("inspect", items[0])
	if !strings.Contains(r.out, `"supersedes": "`+item1+`"`) || !strings.Contains(r.out, `"name": "concise-evidence"`) {
		t.Errorf("re-used key should link lineage: %s", r.out)
	}
	if r = h.ok("inspect", item1); !strings.Contains(r.out, `"status": "superseded"`) {
		t.Errorf("old item should be superseded: %s", r.out)
	}
	r = h.ok("load", "pr-review")
	if !strings.Contains(r.out, "## Working brief\n\n- [phase=review] Lead review comments with concrete evidence; keep them short.\n- Use bullet lists.\n") {
		t.Errorf("second generation brief:\n%s", r.out)
	}
	if strings.Contains(r.out, "## Recent adjustments") {
		t.Errorf("nothing should be recent:\n%s", r.out)
	}

	// ---- AC11: coverage; AC12: lint ----
	r = h.ok("inspect", "pr-review", "--coverage", "novel")
	cm := r.json(t)
	cov := cm["coverage"].([]any)
	if cm["agent"] != "pr-review" || len(cov) != 1 {
		t.Fatalf("coverage novel: %s", r.out)
	}
	row := cov[0].(map[string]any)
	if row["entry"].(map[string]any)["id"] != e1 || row["disposition"] != "superseded-by" || row["coverage"] != "novel" {
		t.Errorf("coverage row: %v", row)
	}
	if r = h.ok("inspect", "pr-review", "--coverage", "unknown"); len(r.json(t)["coverage"].([]any)) != 3 {
		t.Errorf("coverage unknown: %s", r.out)
	} else {
		for _, value := range r.json(t)["coverage"].([]any) {
			row := value.(map[string]any)
			if _, ok := row["items"].([]any); !ok {
				t.Errorf("coverage items must be an array, got %#v", row["items"])
			}
			if _, ok := row["equivalent_records"].([]any); !ok {
				t.Errorf("coverage equivalent_records must be an array, got %#v", row["equivalent_records"])
			}
		}
	}
	r = h.ok("inspect", "pr-review", "--lint", "condition-loss")
	lm := r.json(t)
	lint := lm["lint"].([]any)
	if lm["agent"] != "pr-review" || len(lint) != 1 {
		t.Fatalf("lint: %s", r.out)
	}
	w := lint[0].(map[string]any)
	if w["item"] != items[1] || w["key"] != "repo-id" || w["strength"] != "strong" || !equal(strs(t, w["values"]), []string{"r1"}) ||
		!equal(strs(t, w["sources"]), []string{e4}) || !strings.Contains(w["message"].(string), "repo-id=r1") {
		t.Errorf("lint row: %v", w)
	}

	// ---- compile with a fixture compiler ----
	e5 := h.ok("prefer", "pr-review", "Cite the diff hunk.").id(t)
	dir := t.TempDir()
	outPath := filepath.Join(dir, "out.yaml")
	inPath := filepath.Join(dir, "in.json")
	doc4 := fmt.Sprintf("input_entries: [%s]\nitems:\n  - key: cite-hunk\n    body: Cite the diff hunk.\nentries:\n  - id: %s\n    disposition: represented\n    items: [cite-hunk]\n", e5, e5)
	if err := os.WriteFile(outPath, []byte(doc4), 0o644); err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(dir, "fixture.sh")
	if err := os.WriteFile(fixture, []byte("#!/bin/sh\ncat > "+inPath+"\necho 'compiler says hi' >&2\ncat "+outPath+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if r = h.run("compile", "pr-review"); r.code != 2 || !strings.Contains(r.err, "config.yaml") {
		t.Errorf("no compiler configured: %d %q", r.code, r.err)
	}
	r = h.ok("compile", "pr-review", "--compiler", "sh "+fixture)
	gen3 := strings.TrimSpace(r.out)
	if !strings.HasPrefix(gen3, "gen_") || gen3 == gen2 {
		t.Errorf("compile should print a new generation id: %q", r.out)
	}
	if !strings.Contains(r.err, "compiler says hi") {
		t.Errorf("compiler stderr should pass through: %q", r.err)
	}
	if in, err := os.ReadFile(inPath); err != nil || !strings.Contains(string(in), `"expect_generation": "`+gen2+`"`) || !strings.Contains(string(in), `"`+e5+`"`) {
		t.Errorf("compiler should receive compile-input JSON on stdin: %s", in)
	}
	if r = h.ok("load", "pr-review"); !strings.Contains(r.out, "## Working brief\n\n- Cite the diff hunk.\n") {
		t.Errorf("compiled generation should render:\n%s", r.out)
	}
	// the same fixture now echoes an entry that is no longer an input → 2
	if r = h.run("compile", "pr-review", "--compiler", "sh "+fixture); r.code != 2 || !strings.Contains(r.err, "input_entries must echo") || !strings.HasPrefix(r.err, "nine-tails:") || !strings.Contains(r.err, "\n  compiler says hi\n") {
		t.Errorf("stale echo: %d %q", r.code, r.err)
	}
	// env fallback and --dry-run
	t.Setenv("NINE_TAILS_COMPILER", "sh "+fixture)
	if r = h.run("compile", "pr-review", "--dry-run"); r.code != 2 {
		t.Errorf("env compiler should be used: %d %q", r.code, r.err)
	}
	t.Setenv("NINE_TAILS_COMPILER", "")
	// compiler failures → 5
	fail := filepath.Join(dir, "fail.sh")
	if err := os.WriteFile(fail, []byte("#!/bin/sh\necho compiler-failed >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if r = h.run("compile", "pr-review", "--compiler", "sh "+fail); r.code != 5 || !strings.HasPrefix(r.err, "nine-tails: compiler sh exited with status 1\n  compiler-failed\n") {
		t.Errorf("failing compiler: %d %q", r.code, r.err)
	}
	if r = h.run("compile", "pr-review", "--compiler", filepath.Join(dir, "does-not-exist")); r.code != 5 {
		t.Errorf("unstartable compiler: %d %q", r.code, r.err)
	}
	slow := filepath.Join(dir, "slow.sh")
	if err := os.WriteFile(slow, []byte("#!/bin/sh\nsleep 5\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.home, "config.yaml"), []byte("compiler:\n  argv: [sh, "+slow+"]\n  timeout: 200ms\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if r = h.run("compile", "pr-review"); r.code != 5 || !strings.Contains(r.err, "timed out") {
		t.Errorf("timeout: %d %q", r.code, r.err)
	}
}

func TestBriefPutRejectsUnknownAgentAndBadDocs(t *testing.T) {
	h := newHarness(t)
	if r := h.runIn("input_entries: []\n", "brief", "put", "nobody", "--expect-generation", "none", "--expect-base", "base_1", "--stdin"); r.code != 3 {
		t.Errorf("unknown agent: %d %q", r.code, r.err)
	}
	base := h.ok("base", "a", "Base.").id(t)
	r := h.runIn("- not\n- a mapping\n", "brief", "put", "a", "--expect-generation", "none", "--expect-base", base, "--stdin")
	if r.code != 2 || !strings.Contains(r.err, "compiler output is invalid\n  the document must be a mapping") {
		t.Errorf("bad shape: %d %q", r.code, r.err)
	}
	if r = h.runIn("input_entries: []\n", "brief", "put", "a", "--expect-base", base, "--stdin"); r.code != 2 {
		t.Errorf("missing --expect-generation: %d %q", r.code, r.err)
	}
	// an empty compile (no entries, no items) is valid and installs an empty generation
	r = h.okIn("input_entries: []\nitems: []\nentries: []\n", "brief", "put", "a", "--expect-generation", "none", "--expect-base", base, "--stdin", "--format", "json")
	if res := r.json(t); len(res["items"].([]any)) != 0 || len(res["warnings"].([]any)) != 0 {
		t.Errorf("empty generation: %s", r.out)
	}
}

// AC20: concurrent installers never activate conflicting generations.
func TestCompileAC20ConcurrentInstall(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "a", "Base.")
	e := h.ok("prefer", "a", "one").id(t)
	const n = 8
	results := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s, err := store.Open(h.home)
			if err != nil {
				results[i] = err
				return
			}
			defer s.Close()
			results[i] = s.Tx(func(tx *sql.Tx) error {
				_, _, err := store.InstallGeneration(tx, "a", "",
					[]store.NewItem{{Key: fmt.Sprintf("k%d", i), Body: "body", Sources: []string{e}}},
					[]store.BriefInput{{EntryID: e, Disposition: "represented", Coverage: "unknown"}})
				return err
			})
		}(i)
	}
	wg.Wait()
	succeeded := 0
	for i, err := range results {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, store.ErrConflict), strings.Contains(err.Error(), "UNIQUE"):
			// expected: the one-active-generation index or the CAS refused it
		default:
			t.Errorf("goroutine %d: unexpected error: %v", i, err)
		}
	}
	if succeeded != 1 {
		t.Fatalf("exactly one install should succeed, got %d", succeeded)
	}
	s, err := store.Open(h.home)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	gens, err := store.ListGenerations(s.DB, "a")
	if err != nil {
		t.Fatal(err)
	}
	active := 0
	for _, g := range gens {
		if g.Status == "active" {
			active++
		}
	}
	if active != 1 {
		t.Fatalf("exactly one active generation expected, got %d of %d", active, len(gens))
	}
	if g, err := store.ActiveGeneration(s.DB, "a"); err != nil || g.Status != "active" {
		t.Fatalf("ActiveGeneration: %+v %v", g, err)
	}
	if r := h.ok("load", "a"); !strings.Contains(r.out, "## Working brief\n\n- body\n") {
		t.Errorf("the surviving generation should load:\n%s", r.out)
	}
}

// Fix-round regressions: explicit --budget 0, one combined problem report,
// deterministic diagnostics, verbatim input_entries, symmetric --expect-*
// handling, and origin_context_metadata present as {} for a bare origin.
func TestCompileFixRound(t *testing.T) {
	t.Setenv("NINE_TAILS_COMPILER", "")
	h := newHarness(t)
	base := h.ok("base", "a", "Base.").id(t)
	r := h.ok("load", "a", "--task", "t1") // no --meta: the receipt has empty metadata
	ctx := contextID(t, r.out)
	e1 := h.ok("prefer", "a", "--context", ctx, "one").id(t)
	e2 := h.ok("prefer", "a", "two").id(t)

	// --budget 0 is exit 2 like any other non-positive budget (DESIGN §5)
	for _, args := range [][]string{
		{"compile-input", "a", "--budget", "0"},
		{"compile-input", "a", "--budget=-5"},
		{"compile", "a", "--budget", "0", "--compiler", "sh /nonexistent.sh"},
	} {
		if r := h.run(args...); r.code != 2 || !strings.Contains(r.err, "--budget must be positive") {
			t.Errorf("%v: %d %q %q", args, r.code, r.err, r.out)
		}
	}
	if m := h.ok("compile-input", "a").json(t); m["budget"] != float64(800) {
		t.Errorf("unset --budget should still default: %v", m["budget"])
	}

	// origin_context_metadata is {} (not absent) when the origin carried no metadata
	m := h.ok("compile-input", "a").json(t)
	entries := m["entries"].([]any)
	first := entries[0].(map[string]any)
	om, has := first["origin_context_metadata"]
	if first["origin_context"] != ctx || !has || om == nil || len(om.(map[string]any)) != 0 {
		t.Errorf("entry with bare origin: %v", first)
	}
	if got := strs(t, first["origin_context_rendered"]); !equal(got, []string{base}) {
		t.Errorf("origin_context_rendered %v", got)
	}
	if _, has := entries[1].(map[string]any)["origin_context_metadata"]; has {
		t.Errorf("entry without origin must not carry origin_context_metadata: %v", entries[1])
	}
	if r := h.ok("compile-input", "a", "--format", "yaml"); !strings.Contains(r.out, "\n    origin_context_metadata: {}\n") {
		t.Errorf("yaml should show {}:\n%s", r.out)
	}

	// parse-shape and validation problems arrive in one report
	doc := fmt.Sprintf("input_entries: [%s, %s, rec_88]\nitems: [{key: Bad_Key, body: x, meta: {\"a=b\": 1}}]\nentries:\n  - {id: %s, disposition: represented}\n  - {id: %s, disposition: superseded-by, successor: %s, refinement: maybe}\n", e1, e2, e1, e2, e2)
	r = h.runIn(doc, "brief", "put", "a", "--stdin", "--expect-generation", "none", "--expect-base", base)
	wantErr := "nine-tails: compiler output is invalid\n" +
		"  item Bad_Key metadata key \"a=b\" may not be empty or contain whitespace, '=', '[' or ']'\n" +
		"  entry " + e2 + " refinement must be true or false\n" +
		"  brief item name \"Bad_Key\" must match ^[a-z0-9][a-z0-9.-]*$ (lowercase, no _ or /)\n" +
		"  entry rec_88 is missing from entries\n" +
		"  entry " + e1 + " is represented but lists no items\n" +
		"  entry " + e2 + " cannot supersede itself\n"
	if r.code != 2 || r.err != wantErr {
		t.Errorf("combined report: %d\n got: %q\nwant: %q", r.code, r.err, wantErr)
	}

	// the same document always produces the same diagnostics, in sorted key order
	doc = "input_entries: []\nitems: [{key: k, body: b, meta: {\"a=b\": 1, \"sp ace\": 2, \"c[\": 3}}]\nentries: []\n"
	wantErr = "nine-tails: compiler output is invalid\n" +
		"  item k metadata key \"a=b\" may not be empty or contain whitespace, '=', '[' or ']'\n" +
		"  item k metadata key \"c[\" may not be empty or contain whitespace, '=', '[' or ']'\n" +
		"  item k metadata key \"sp ace\" may not be empty or contain whitespace, '=', '[' or ']'\n"
	for i := 0; i < 5; i++ {
		if r = h.runIn(doc, "brief", "put", "a", "--stdin", "--expect-generation", "none", "--expect-base", base); r.code != 2 || r.err != wantErr {
			t.Fatalf("run %d: %d %q", i, r.code, r.err)
		}
	}

	// a duplicated id in input_entries is a problem, not silently collapsed
	doc = fmt.Sprintf("input_entries: [%s, %s, %s]\nitems: []\nentries:\n  - {id: %s, disposition: deferred}\n  - {id: %s, disposition: deferred}\n", e1, e1, e2, e1, e2)
	r = h.runIn(doc, "brief", "put", "a", "--stdin", "--expect-generation", "none", "--expect-base", base, "--dry-run")
	if r.code != 2 || r.err != "nine-tails: compiler output is invalid\n  input_entries lists "+e1+" more than once\n" {
		t.Errorf("duplicate input id: %d %q", r.code, r.err)
	}

	// --expect-generation and --expect-base are handled alike: padding is
	// tolerated on both, malformed ids are exit 2 on both
	doc = fmt.Sprintf("input_entries: [%s, %s]\nitems: []\nentries:\n  - {id: %s, disposition: deferred}\n  - {id: %s, disposition: deferred}\n", e1, e2, e1, e2)
	if r = h.runIn(doc, "brief", "put", "a", "--stdin", "--expect-generation", " none ", "--expect-base", " "+base, "--dry-run"); r.code != 0 {
		t.Errorf("padded expect ids: %d %q", r.code, r.err)
	}
	if r = h.runIn(doc, "brief", "put", "a", "--stdin", "--expect-generation", "none", "--expect-base", "base1"); r.code != 2 || !strings.HasPrefix(r.err, "nine-tails: --expect-base must be the active base id like base_4") {
		t.Errorf("malformed base: %d %q", r.code, r.err)
	}
	if r = h.runIn(doc, "brief", "put", "a", "--stdin", "--expect-generation", "gen1", "--expect-base", base); r.code != 2 || !strings.HasPrefix(r.err, "nine-tails: --expect-generation must be 'none' or the active generation id like gen_11") {
		t.Errorf("malformed generation: %d %q", r.code, r.err)
	}
	if r = h.runIn(doc, "brief", "put", "a", "--stdin", "--expect-generation", "none", "--expect-base", " base_99 "); r.code != 7 || r.err != "nine-tails: expected base_99 but "+base+" is active\n" {
		t.Errorf("wrong base: %d %q", r.code, r.err)
	}

	// compile: input_entries must be echoed verbatim, order and all; the echo
	// problem joins the rest of the report instead of pre-empting it
	dir := t.TempDir()
	reordered := filepath.Join(dir, "reordered.sh")
	if err := os.WriteFile(reordered, []byte(fmt.Sprintf("#!/bin/sh\ncat >/dev/null\nprintf 'input_entries: [%s, %s]\\nitems: [{key: k, body: \"\"}]\\nentries:\\n  - {id: %s, disposition: deferred}\\n  - {id: %s, disposition: deferred}\\n'\n", e2, e1, e1, e2)), 0o755); err != nil {
		t.Fatal(err)
	}
	r = h.run("compile", "a", "--compiler", "sh "+reordered)
	wantErr = "nine-tails: compiler output is invalid\n" +
		"  input_entries must echo the compile input's input_entries [" + e1 + ", " + e2 + "] unchanged, got [" + e2 + ", " + e1 + "]\n" +
		"  item k has an empty body\n"
	if r.code != 2 || r.err != wantErr {
		t.Errorf("reordered echo: %d\n got: %q\nwant: %q", r.code, r.err, wantErr)
	}
	if m := h.ok("compile-input", "a").json(t); m["expect_generation"] != "none" {
		t.Errorf("nothing should have been installed: %v", m["expect_generation"])
	}
	// and a verbatim echo installs
	exact := filepath.Join(dir, "exact.sh")
	if err := os.WriteFile(exact, []byte(fmt.Sprintf("#!/bin/sh\ncat >/dev/null\nprintf 'input_entries: [%s, %s]\\nitems: [{key: k, body: b}]\\nentries:\\n  - {id: %s, disposition: represented, items: [k], equivalent_records: [%s]}\\n  - {id: %s, disposition: deferred}\\n'\n", e1, e2, e1, base, e2)), 0o755); err != nil {
		t.Fatal(err)
	}
	if r = h.run("compile", "a", "--compiler", "sh "+exact, "--budget", "0"); r.code != 2 {
		t.Errorf("compile --budget 0: %d %q", r.code, r.err)
	}
	gen := strings.TrimSpace(h.ok("compile", "a", "--compiler", "sh "+exact).out)
	if !strings.HasPrefix(gen, "gen_") {
		t.Fatalf("compile should install: %q", gen)
	}
	// the installed accounting carries items and equivalents (what inspect --coverage must show)
	gm := h.ok("inspect", gen).json(t)
	inputs := gm["inputs"].([]any)
	row := inputs[0].(map[string]any)
	if row["id"] != e1 || row["coverage"] != "covered-rendered" || len(strs(t, row["items"])) != 1 || !equal(strs(t, row["equivalent_records"]), []string{base}) {
		t.Errorf("stored accounting row: %v", row)
	}
	if r = h.ok("inspect", "a", "--coverage", "covered-rendered"); len(r.json(t)["coverage"].([]any)) != 1 {
		t.Errorf("coverage covered-rendered: %s", r.out)
	}
}
