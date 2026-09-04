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
	"time"

	"github.com/scottmeyer/nine-tails/internal/store"
)

func acRecordByID(t *testing.T, raw any, id string) map[string]any {
	t.Helper()
	rows, ok := raw.([]any)
	if !ok {
		t.Fatalf("records are %T, want a list: %#v", raw, raw)
	}
	for _, value := range rows {
		row, ok := value.(map[string]any)
		if ok && row["id"] == id {
			return row
		}
	}
	t.Fatalf("record %s not found in %#v", id, raw)
	return nil
}

func acReceiptIDs(t *testing.T, raw any) []string {
	t.Helper()
	rows, ok := raw.([]any)
	if !ok {
		t.Fatalf("rendered receipt is %T, want a list: %#v", raw, raw)
	}
	ids := make([]string, 0, len(rows))
	for _, value := range rows {
		row, ok := value.(map[string]any)
		if !ok {
			t.Fatalf("receipt row is %T: %#v", value, value)
		}
		ids = append(ids, fmt.Sprint(row["id"]))
	}
	return ids
}

// AC01: a named agent can be created with base instructions.
func TestAC01(t *testing.T) {
	h := newHarness(t)
	base := h.ok("base", "reviewer", "--meta", "title=Evidence Reviewer", "Review changes using direct evidence.").id(t)
	if !strings.HasPrefix(base, "base_") {
		t.Fatalf("base id = %q", base)
	}
	r := h.ok("load", "reviewer")
	if !strings.HasPrefix(r.out, "# Evidence Reviewer\n\n[nine-tails-context=ctx_") ||
		!strings.Contains(r.out, "Review changes using direct evidence.") {
		t.Fatalf("created agent did not load coherently:\n%s", r.out)
	}
	if agents := h.ok("agents").out; !strings.Contains(agents, "reviewer\n") {
		t.Fatalf("created agent missing from agents output: %q", agents)
	}
}

// AC02: load returns a coherent capsule within a caller-supplied budget.
func TestAC02(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "budgeted", "Base instructions.")
	for i := 0; i < 16; i++ {
		h.ok("prefer", "budgeted", fmt.Sprintf("Adjustment %02d: %s", i, strings.Repeat("bounded text ", 8)))
	}
	m := h.ok("load", "budgeted", "--budget", "140", "--format", "json").json(t)
	estimated := int(m["estimated_tokens"].(float64))
	if m["budget"] != float64(140) || estimated > 140 {
		t.Fatalf("budget=%v estimated_tokens=%d", m["budget"], estimated)
	}
	if m["agent"] != "budgeted" || m["context_id"] == "" || !strings.Contains(m["instructions"].(string), "Base instructions.") {
		t.Fatalf("incoherent capsule: %#v", m)
	}
	if truncated, ok := m["truncated"].([]any); !ok || len(truncated) == 0 {
		t.Fatalf("test did not exercise budget selection: %#v", m["truncated"])
	}
}

// AC03: a correction appended in one invocation appears in the next.
func TestAC03(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "correctable", "Base.")
	ctx := contextID(t, h.ok("load", "correctable").out)
	correction := h.ok("prefer", "correctable", "--context", ctx, "Lead with the failing assertion.").id(t)
	r := h.ok("load", "correctable")
	if !strings.Contains(r.out, "## Recent adjustments\n\n- (prefer) Lead with the failing assertion.\n") {
		t.Fatalf("correction %s missing from next invocation:\n%s", correction, r.out)
	}
}

// AC04: a load persists an immutable receipt containing every emitted id.
func TestAC04(t *testing.T) {
	h := newHarness(t)
	base := h.ok("base", "receipts", "Base v1.").id(t)
	state := h.ok("state", "put", "receipts/working", "--expect", "none", "status: ready").id(t)
	guidance := h.ok("prefer", "receipts", "Prefer evidence.").id(t)
	capsule := h.ok("load", "receipts", "--format", "json").json(t)
	ctx := capsule["context_id"].(string)
	emitted := strs(t, capsule["rendered_record_ids"])
	for _, id := range []string{base, state, guidance} {
		if !slicesContain(emitted, id) {
			t.Fatalf("emitted ids %v omit %s", emitted, id)
		}
	}
	receipt := h.ok("inspect", ctx).json(t)
	if got := acReceiptIDs(t, receipt["rendered"]); !equal(got, emitted) {
		t.Fatalf("receipt ids %v, emitted ids %v", got, emitted)
	}
	h.ok("base", "receipts", "--expect", base, "Base v2.")
	after := h.ok("inspect", ctx).json(t)
	if got := acReceiptIDs(t, after["rendered"]); !equal(got, emitted) {
		t.Fatalf("receipt changed after superseding a record: %v, want %v", got, emitted)
	}
}

// AC05: origin context is recorded without inheriting ambient scope.
func TestAC05(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "origins", "Base.")
	ctx := contextID(t, h.ok("load", "origins", "--meta", "repo=one").out)
	correction := h.ok("prefer", "--context", ctx, "This applies across repositories.").id(t)
	record := h.ok("inspect", correction).json(t)
	meta, ok := record["meta"].(map[string]any)
	if record["origin_context"] != ctx || !ok || len(meta) != 0 {
		t.Fatalf("correction inherited ambient scope: %#v", record)
	}
	r := h.ok("load", "origins", "--meta", "repo=two")
	if !strings.Contains(r.out, "This applies across repositories.") {
		t.Fatalf("unscoped correction was excluded from another ambient context:\n%s", r.out)
	}
}

// AC06: state is capped, compare-and-swapped, immutable, and loaded losslessly.
func TestAC06(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "stateful", "Base.")
	body1 := "status: ready\nnested:\n  literal: \"a:b # c\""
	state1 := h.ok("state", "put", "stateful/working", "--expect", "none", "--meta", "env=prod", body1).id(t)
	if got := h.ok("state", "get", "stateful/working").out; got != body1+"\n" {
		t.Fatalf("state was not returned losslessly: %q", got)
	}
	if r := h.ok("load", "stateful", "--meta", "env=dev"); strings.Contains(r.out, "## Current state") {
		t.Fatalf("ineligible state rendered:\n%s", r.out)
	}
	if r := h.ok("load", "stateful", "--meta", "env=prod"); !strings.Contains(r.out, "```yaml\n"+body1+"\n```") {
		t.Fatalf("eligible state did not load losslessly:\n%s", r.out)
	}
	if r := h.run("state", "put", "stateful/working", "--expect", state1, strings.Repeat("x", 9000)); r.code != 2 {
		t.Fatalf("oversize state exit=%d stderr=%q", r.code, r.err)
	}
	if r := h.run("state", "put", "stateful/working", "--expect", "state_999", "status: wrong"); r.code != 7 {
		t.Fatalf("stale state CAS exit=%d stderr=%q", r.code, r.err)
	}
	state2 := h.ok("state", "put", "stateful/working", "--expect", state1, "status: complete").id(t)
	old := h.ok("inspect", state1).json(t)
	if old["status"] != "superseded" || old["body"] != body1 || state2 == state1 {
		t.Fatalf("prior state version was not preserved: %#v", old)
	}
}

// AC07: arbitrary metadata is preserved/rendered and disjoint scope excludes.
func TestAC07(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "metadata", "Base.")
	r := h.ok("prefer", "metadata", "--meta", "custom=two words", "--meta", "scope=one", "--meta", "scope=two", "--format", "json", "Scoped guidance.")
	record := r.json(t)
	meta := record["meta"].(map[string]any)
	if !equal(strs(t, meta["custom"]), []string{"two words"}) || !equal(strs(t, meta["scope"]), []string{"one", "two"}) {
		t.Fatalf("metadata was not preserved: %#v", meta)
	}
	matching := h.ok("load", "metadata", "--meta", "scope=two")
	if !strings.Contains(matching.out, `- [custom="two words" scope=one scope=two] (prefer) Scoped guidance.`) {
		t.Fatalf("arbitrary metadata was not rendered:\n%s", matching.out)
	}
	if conflicting := h.ok("load", "metadata", "--meta", "scope=other"); strings.Contains(conflicting.out, "Scoped guidance.") {
		t.Fatalf("disjoint metadata did not exclude the record:\n%s", conflicting.out)
	}
}

// AC08: the brief is a generation of independently selectable metadata items
// with protected phase-one budget.
func TestAC08(t *testing.T) {
	h := newHarness(t)
	base := h.ok("base", "briefed", "Base.").id(t)
	doc := `input_entries: []
items:
  - key: repo-one
    body: One-scoped brief survives a burst of recent guidance.
    meta: {repo: one}
  - key: repo-two
    body: Two-scoped brief is independently selectable.
    meta: {repo: two}
entries: []
`
	h.okIn(doc, "brief", "put", "briefed", "--expect-generation", "none", "--expect-base", base, "--stdin")
	for i := 0; i < 16; i++ {
		h.ok("prefer", "briefed", fmt.Sprintf("Recent burst %02d %s", i, strings.Repeat("noise ", 12)))
	}
	one := h.ok("load", "briefed", "--meta", "repo=one", "--budget", "160", "--format", "json").json(t)
	instructions := one["instructions"].(string)
	if !strings.Contains(instructions, "One-scoped brief") || strings.Contains(instructions, "Two-scoped brief") {
		t.Fatalf("repo-one item selection failed:\n%s", instructions)
	}
	if !strings.Contains(instructions, "Recent burst") || len(one["truncated"].([]any)) == 0 || one["estimated_tokens"].(float64) > 160 {
		t.Fatalf("scenario did not exercise brief reservation under contention: %#v", one)
	}
	two := h.ok("load", "briefed", "--meta", "repo=two", "--budget", "160", "--format", "json").json(t)
	if text := two["instructions"].(string); !strings.Contains(text, "Two-scoped brief") || strings.Contains(text, "One-scoped brief") {
		t.Fatalf("repo-two item selection failed:\n%s", text)
	}
	view := h.ok("inspect", "briefed", "--include", "brief").json(t)["brief"].(map[string]any)
	if len(view["items"].([]any)) != 2 || view["generation"].(map[string]any)["status"] != "active" {
		t.Fatalf("brief is not an active two-item generation: %#v", view)
	}
}

// AC09: guidance, recall, and state are distinct; unknown append kinds remain recall.
func TestAC09(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "lanes", "Base.")
	guidance := h.ok("append", "lanes", "--lane", "guidance", "--kind", "observation", "Guidance body.").id(t)
	recall := h.ok("remember", "lanes", "Recall body.").id(t)
	unknown := h.ok("append", "lanes", "--kind", "surprise", "Unknown-kind body.").id(t)
	state := h.ok("state", "put", "lanes/working", "--expect", "none", "status: distinct").id(t)
	loaded := h.ok("load", "lanes").out
	if !strings.Contains(loaded, "Guidance body.") || !strings.Contains(loaded, "status: distinct") ||
		strings.Contains(loaded, "Recall body.") || strings.Contains(loaded, "Unknown-kind body.") {
		t.Fatalf("lane treatment crossed boundaries:\n%s", loaded)
	}
	for id, want := range map[string][2]string{
		guidance: {"guidance", "observation"}, recall: {"recall", "memory"}, unknown: {"recall", "surprise"}, state: {"state", "working-state"},
	} {
		m := h.ok("inspect", id).json(t)
		if m["lane"] != want[0] || m["kind"] != want[1] {
			t.Fatalf("record %s lane/kind = %v/%v, want %v", id, m["lane"], m["kind"], want)
		}
	}
}

// AC10: the compiler accounts for each input and CAS-installs a generation
// without consuming source guidance.
func TestAC10(t *testing.T) {
	h := newHarness(t)
	base := h.ok("base", "compiled", "Base.").id(t)
	e1 := h.ok("prefer", "compiled", "Represent this.").id(t)
	e2 := h.ok("avoid", "compiled", "Defer this.").id(t)
	doc := fmt.Sprintf(`input_entries: [%s, %s]
items:
  - key: represented
    body: Compiled representation.
entries:
  - {id: %s, disposition: represented, items: [represented]}
  - {id: %s, disposition: deferred}
`, e1, e2, e1, e2)
	dir := t.TempDir()
	outputPath := filepath.Join(dir, "output.yaml")
	inputPath := filepath.Join(dir, "input.json")
	fixture := filepath.Join(dir, "compiler.sh")
	if err := os.WriteFile(outputPath, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\ncat > " + inputPath + "\ncat " + outputPath + "\n"
	if err := os.WriteFile(fixture, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	gen := strings.TrimSpace(h.ok("compile", "compiled", "--compiler", "sh "+fixture).out)
	if !strings.HasPrefix(gen, "gen_") {
		t.Fatalf("compile result = %q", gen)
	}
	in, err := os.ReadFile(inputPath)
	if err != nil || !strings.Contains(string(in), `"input_entries": [`) || !strings.Contains(string(in), `"`+e1+`"`) || !strings.Contains(string(in), `"`+e2+`"`) {
		t.Fatalf("compiler input did not account for both entries: %v\n%s", err, in)
	}
	installed := h.ok("inspect", gen).json(t)
	if len(installed["inputs"].([]any)) != 2 || installed["generation"].(map[string]any)["status"] != "active" {
		t.Fatalf("generation/input accounting: %#v", installed)
	}
	for _, id := range []string{e1, e2} {
		if m := h.ok("inspect", id).json(t); m["status"] != "active" {
			t.Fatalf("source %s was consumed: %#v", id, m)
		}
	}
	loaded := h.ok("load", "compiled").out
	if !strings.Contains(loaded, "Compiled representation.") || !strings.Contains(loaded, "Defer this.") || strings.Contains(loaded, "Represent this.") {
		t.Fatalf("installed accounting is not reflected in load:\n%s", loaded)
	}
	if r := h.runIn(doc, "brief", "put", "compiled", "--expect-generation", "none", "--expect-base", base, "--stdin"); r.code != 7 {
		t.Fatalf("stale generation CAS exit=%d stderr=%q", r.code, r.err)
	}
}

// AC11: all five coverage classes are derived from the originating capsule.
func TestAC11(t *testing.T) {
	h := newHarness(t)
	base := h.ok("base", "coverage", "Base.").id(t)
	unrendered := h.ok("remember", "coverage", "A record absent from capsules.").id(t)
	ctx := contextID(t, h.ok("load", "coverage").out)
	coveredRendered := h.ok("prefer", "coverage", "--context", ctx, "Already rendered.").id(t)
	coveredUnrendered := h.ok("prefer", "coverage", "--context", ctx, "Known but not rendered.").id(t)
	novel := h.ok("prefer", "coverage", "--context", ctx, "Novel correction.").id(t)
	refinement := h.ok("prefer", "coverage", "--context", ctx, "Refinement.").id(t)
	unknown := h.ok("prefer", "coverage", "No origin.").id(t)
	doc := fmt.Sprintf(`input_entries: [%s, %s, %s, %s, %s]
items: []
entries:
  - {id: %s, disposition: deferred, equivalent_records: [%s]}
  - {id: %s, disposition: deferred, equivalent_records: [%s]}
  - {id: %s, disposition: deferred}
  - {id: %s, disposition: deferred, refinement: true}
  - {id: %s, disposition: deferred}
`, coveredRendered, coveredUnrendered, novel, refinement, unknown,
		coveredRendered, base, coveredUnrendered, unrendered, novel, refinement, unknown)
	h.okIn(doc, "brief", "put", "coverage", "--expect-generation", "none", "--expect-base", base, "--stdin")
	for class, id := range map[string]string{
		"covered-rendered": coveredRendered, "covered-unrendered": coveredUnrendered,
		"novel": novel, "refinement": refinement, "unknown": unknown,
	} {
		rows := h.ok("inspect", "coverage", "--coverage", class).json(t)["coverage"].([]any)
		if len(rows) != 1 || rows[0].(map[string]any)["entry"].(map[string]any)["id"] != id {
			t.Fatalf("coverage %s = %#v, want entry %s", class, rows, id)
		}
	}
}

// AC12: condition loss from a source entry is surfaced as a lint warning.
func TestAC12(t *testing.T) {
	h := newHarness(t)
	base := h.ok("base", "linted", "Base.").id(t)
	source := h.ok("prefer", "linted", "--meta", "repo=one", "Keep repository scope.").id(t)
	doc := fmt.Sprintf(`input_entries: [%s]
items:
  - key: scope-lost
    body: This item forgot its source scope.
entries:
  - {id: %s, disposition: represented, items: [scope-lost]}
`, source, source)
	result := h.okIn(doc, "brief", "put", "linted", "--expect-generation", "none", "--expect-base", base, "--stdin", "--format", "json").json(t)
	if len(result["warnings"].([]any)) != 1 {
		t.Fatalf("install warnings = %#v", result["warnings"])
	}
	lint := h.ok("inspect", "linted", "--lint", "condition-loss").json(t)["lint"].([]any)
	if len(lint) != 1 {
		t.Fatalf("lint rows = %#v", lint)
	}
	warning := lint[0].(map[string]any)
	if warning["key"] != "repo" || warning["strength"] != "strong" ||
		!equal(strs(t, warning["values"]), []string{"one"}) || !equal(strs(t, warning["sources"]), []string{source}) {
		t.Fatalf("condition-loss warning = %#v", warning)
	}
}

// AC13: a YAML tool backed by a copied script preserves substituted argv boundaries.
func TestAC13(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "tools", "Base.")
	script := writeScript(t, "arguments.sh", "printf 'argc=%s\\narg1=<%s>\\narg2=<%s>\\n' \"$#\" \"$1\" \"$2\"\n")
	body := `description: Preserve argument boundaries
exec:
  argv: ["{{ phrase }}", "{{ amount }}"]
  stdin: none
input:
  phrase: {type: string, required: true}
  amount: {type: number, required: true}
`
	toolID := h.okIn(body, "tool", "add", "tools", "arguments", "--script", script, "--stdin").id(t)
	stored := toolBody(t, h.ok("inspect", toolID))
	argv := argvOf(t, stored)
	if len(argv) != 3 || argv[0] != "artifacts/"+toolID+"/arguments.sh" {
		t.Fatalf("stored artifact argv = %q", argv)
	}
	if _, err := os.Stat(filepath.Join(h.home, argv[0])); err != nil {
		t.Fatalf("copied artifact missing: %v", err)
	}
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho changed\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := h.ok("call", "--agent", "tools", "arguments", "--input", `{"phrase":"two words * ; echo bad","amount":1.50}`)
	want := "argc=2\narg1=<two words * ; echo bad>\narg2=<1.50>\n"
	if r.out != want {
		t.Fatalf("substituted arguments split or source artifact leaked: got %q want %q", r.out, want)
	}
}

// AC14: agent-owned named definitions shadow shared definitions predictably.
func TestAC14(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "owner", "Base.")
	h.ok("base", "other", "Base.")
	shared := execScript(t, "shared-shadow.sh", "echo shared\n")
	owned := execScript(t, "owned-shadow.sh", "echo owned\n")
	h.okIn("description: shared definition\nexec:\n  argv: ["+shared+"]\n", "put", "shared", "--lane", "definition", "--kind", "tool", "--name", "same", "--stdin")
	h.okIn("description: owned definition\nexec:\n  argv: ["+owned+"]\n", "put", "owner", "--lane", "definition", "--kind", "tool", "--name", "same", "--meta", "repo=one", "--stdin")
	if r := h.ok("call", "--agent", "owner", "same"); r.out != "owned\n" {
		t.Fatalf("owner resolved %q", r.out)
	}
	if r := h.ok("call", "--agent", "other", "same"); r.out != "shared\n" {
		t.Fatalf("other agent resolved %q", r.out)
	}
	matching := h.ok("load", "owner", "--meta", "repo=one").out
	if !strings.Contains(matching, "`same`: owned definition") || strings.Contains(matching, "shared definition") {
		t.Fatalf("matching capsule shadowing failed:\n%s", matching)
	}
	conflicting := h.ok("load", "owner", "--meta", "repo=two").out
	if strings.Contains(conflicting, "`same`") {
		t.Fatalf("conflicting owned name fell through to shared:\n%s", conflicting)
	}
	ctx := contextID(t, conflicting)
	if r := h.run("call", "--context", ctx, "same"); r.code != 3 || !strings.Contains(r.err, "not applicable") {
		t.Fatalf("conflicting owned call fell through: exit=%d stdout=%q stderr=%q", r.code, r.out, r.err)
	}
}

// AC15: a caller explicitly loads another agent while inheriting ambient context.
func TestAC15(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "coordinator", "Coordinate the review.")
	h.ok("base", "worker", "Verify the evidence.")
	h.ok("agent", "add", "coordinator", "worker", "--description", "Checks evidence.")
	parent := h.ok("load", "coordinator", "--task", "Review PR", "--meta", "repo=acme", "--meta", "pr=42", "--format", "json").json(t)
	parentID := parent["context_id"].(string)
	child := h.ok("load", "worker", "--context", parentID, "--task", "Check finding", "--meta", "phase=evidence", "--format", "json").json(t)
	meta := child["metadata"].(map[string]any)
	if child["agent"] != "worker" || child["parent_context"] != parentID ||
		!equal(strs(t, meta["repo"]), []string{"acme"}) || !equal(strs(t, meta["pr"]), []string{"42"}) ||
		!equal(strs(t, meta["phase"]), []string{"evidence"}) {
		t.Fatalf("child capsule did not inherit ambient context: %#v", child)
	}
	if text := child["instructions"].(string); !strings.Contains(text, "Verify the evidence.") || strings.Contains(text, "Coordinate the review.") {
		t.Fatalf("caller did not explicitly select the worker capsule:\n%s", text)
	}
}

// AC16: reflection may make no writes or use existing operations for every
// supported repair surface.
func TestAC16(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "reflectee", "Base.")
	before := h.ok("inspect", "reflectee").out
	after := h.ok("inspect", "reflectee").out
	if after != before {
		t.Fatalf("read-only reflection changed state:\nbefore %s\nafter %s", before, after)
	}
	ctx := contextID(t, h.ok("load", "reflectee", "--task", "Reflect on recent behavior").out)
	h.ok("state", "put", "reflectee/working", "--context", ctx, "--expect", "none", "status: reflected")
	h.ok("prefer", "reflectee", "--context", ctx, "Prefer explicit checks.")
	h.ok("remember", "reflectee", "--context", ctx, "Observed a transient failure.")
	h.ok("signal", "reflectee", "--context", ctx, "--subject", "Recheck later")
	script := writeScript(t, "reflect.sh", "echo reflected\n")
	h.ok("tool", "add", "reflectee", "reflect", "--script", script, "--description", "Run a reflection check")
	loaded := h.ok("load", "reflectee").out
	for _, want := range []string{"status: reflected", "Prefer explicit checks.", "`reflect`: Run a reflection check", "Recheck later"} {
		if !strings.Contains(loaded, want) {
			t.Fatalf("reflection update %q missing:\n%s", want, loaded)
		}
	}
	if strings.Contains(loaded, "Observed a transient failure.") {
		t.Fatalf("recall leaked into the capsule:\n%s", loaded)
	}
	full := h.ok("inspect", "reflectee").json(t)
	if len(full["state"].([]any)) != 1 || len(full["journal"].([]any)) != 2 || len(full["tools"].([]any)) != 1 || len(full["signals"].([]any)) != 1 {
		t.Fatalf("reflection operations were not inspectable: %#v", full)
	}
}

// AC17: a future signal becomes due, deduplicates, leases, and returns pending.
func TestAC17(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "signaled", "Base.")
	sig := h.ok("signal", "signaled", "--subject", "Recheck", "--body", "CI result", "--at", "+1h", "--dedupe-key", "ci:1").id(t)
	if r := h.ok("signal", "signaled", "--subject", "Duplicate", "--dedupe-key", "ci:1"); r.id(t) != sig || !strings.Contains(r.err, "deduplicated against "+sig) {
		t.Fatalf("signal did not deduplicate: stdout=%q stderr=%q", r.out, r.err)
	}
	if r := h.ok("tick"); strings.TrimSpace(r.out) != "[]" {
		t.Fatalf("future signal was due early: %s", r.out)
	}
	if r := h.ok("load", "signaled"); strings.Contains(r.out, sig) {
		t.Fatalf("future signal loaded early:\n%s", r.out)
	}
	h.now = h.now.Add(time.Hour)
	if r := h.ok("load", "signaled"); !strings.Contains(r.out, "[signal="+sig+"] Recheck — CI result") {
		t.Fatalf("due signal did not appear:\n%s", r.out)
	}
	rows := tickRows(t, h.ok("tick", "--claim", "--lease", "1m"))
	if len(rows) != 1 || rows[0]["id"] != sig || rows[0]["state"] != "leased" {
		t.Fatalf("claim output: %#v", rows)
	}
	oldToken := rows[0]["lease_token"].(string)
	h.now = h.now.Add(time.Minute + time.Second)
	rows = tickRows(t, h.ok("tick"))
	if len(rows) != 1 || rows[0]["state"] != "pending" || rows[0]["lease_token"] != "" {
		t.Fatalf("expired lease did not return pending: %#v", rows)
	}
	rows = tickRows(t, h.ok("tick", "--claim"))
	newToken := rows[0]["lease_token"].(string)
	if newToken == oldToken {
		t.Fatalf("reclaim reused lease token %s", oldToken)
	}
	h.ok("signal", "ack", sig, "--lease", newToken)
	if r := h.ok("tick"); strings.TrimSpace(r.out) != "[]" {
		t.Fatalf("acknowledged signal remained due: %s", r.out)
	}
}

// AC18: a separate session can inspect, explain, and repair via documented CLI operations.
func TestAC18(t *testing.T) {
	owner := newHarness(t)
	badBase := owner.ok("base", "target", "Always guess.").id(t)
	badState := owner.ok("state", "put", "target/working", "--expect", "none", "mode: reckless").id(t)
	owner.ok("base", "maintainer", "Inspect and repair other agents.")

	maintainer := &harness{t: t, home: owner.home, now: owner.now}
	view := maintainer.ok("inspect", "target").json(t)
	if view["base"].(map[string]any)["body"] != "Always guess." ||
		acRecordByID(t, view["state"], badState)["body"] != "mode: reckless" {
		t.Fatalf("separate session could not inspect complete target state: %#v", view)
	}
	explanation := maintainer.ok("remember", "maintainer", "--meta", "target=target", "The target guesses because its base and state explicitly demand it.").id(t)
	maintainer.ok("base", "target", "--expect", badBase, "Verify evidence before answering.")
	maintainer.ok("state", "put", "target/working", "--expect", badState, "mode: evidence-first")
	loaded := owner.ok("load", "target").out
	if !strings.Contains(loaded, "Verify evidence before answering.") || !strings.Contains(loaded, "mode: evidence-first") ||
		strings.Contains(loaded, "Always guess.") || strings.Contains(loaded, "mode: reckless") {
		t.Fatalf("CLI repair did not take effect:\n%s", loaded)
	}
	if note := owner.ok("inspect", explanation).json(t); note["agent"] != "maintainer" || note["lane"] != "recall" {
		t.Fatalf("explanation was not recorded by the maintainer: %#v", note)
	}
	for _, id := range []string{badBase, badState} {
		if old := owner.ok("inspect", id).json(t); old["status"] != "superseded" {
			t.Fatalf("repaired version %s was not preserved: %#v", id, old)
		}
	}
}

// AC19: corrupt optional records are skipped while mechanical errors stay clear.
func TestAC19(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "resilient", "Base.")
	h.ok("prefer", "resilient", "Healthy guidance.")
	goodScript := writeScript(t, "good.sh", "echo good\n")
	badScript := writeScript(t, "bad.sh", "echo bad\n")
	h.ok("tool", "add", "resilient", "good", "--script", goodScript, "--description", "Healthy tool")
	corrupt := h.ok("tool", "add", "resilient", "corrupt", "--script", badScript, "--description", "Will corrupt").id(t)

	st, err := store.Open(h.home)
	if err != nil {
		t.Fatal(err)
	}
	err = st.Tx(func(tx *sql.Tx) error {
		return store.SetBody(tx, corrupt, "exec: [not: a: tool")
	})
	if closeErr := st.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}

	r := h.run("load", "resilient")
	if r.code != 0 || !strings.Contains(r.out, "Healthy guidance.") || !strings.Contains(r.out, "`good`: Healthy tool") ||
		strings.Contains(r.out, "`corrupt`") || !strings.Contains(r.err, "nine-tails: skipped "+corrupt) {
		t.Fatalf("corrupt optional tool broke capsule: exit=%d stdout=%q stderr=%q", r.code, r.out, r.err)
	}
	r = h.run("call", "--agent", "resilient", "corrupt")
	if r.code != 4 || !strings.HasPrefix(r.err, "nine-tails: ") || !strings.Contains(r.err, corrupt) || !strings.Contains(r.err, "corrupt body") {
		t.Fatalf("corrupt tool error was unclear: exit=%d stdout=%q stderr=%q", r.code, r.out, r.err)
	}
}

// AC20: concurrent local generation installs leave exactly one active generation.
func TestAC20(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "parallel", "Base.")
	source := h.ok("prefer", "parallel", "Source guidance.").id(t)
	const n = 8
	results := make([]error, n)
	ready := make(chan struct{}, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			st, err := store.Open(h.home)
			if err != nil {
				results[i] = err
				ready <- struct{}{}
				return
			}
			defer st.Close()
			ready <- struct{}{}
			<-start
			results[i] = st.Tx(func(tx *sql.Tx) error {
				_, _, err := store.InstallGeneration(tx, "parallel", "",
					[]store.NewItem{{Key: fmt.Sprintf("candidate-%d", i), Body: "Concurrent winner.", Sources: []string{source}}},
					[]store.BriefInput{{EntryID: source, Disposition: "represented", Coverage: "unknown"}})
				return err
			})
		}(i)
	}
	for i := 0; i < n; i++ {
		<-ready
	}
	close(start)
	wg.Wait()

	succeeded := 0
	for i, err := range results {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, store.ErrConflict), strings.Contains(err.Error(), "UNIQUE"):
		default:
			t.Errorf("installer %d: unexpected error: %v", i, err)
		}
	}
	if succeeded != 1 {
		t.Fatalf("successful installs = %d, want 1 (errors=%v)", succeeded, results)
	}
	st, err := store.Open(h.home)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	generations, err := store.ListGenerations(st.DB, "parallel")
	if err != nil {
		t.Fatal(err)
	}
	active := 0
	for _, generation := range generations {
		if generation.Status == "active" {
			active++
		}
	}
	if active != 1 {
		t.Fatalf("active generations = %d of %d, want exactly 1", active, len(generations))
	}
	if r := h.ok("load", "parallel"); !strings.Contains(r.out, "Concurrent winner.") {
		t.Fatalf("surviving generation does not load:\n%s", r.out)
	}
}

func slicesContain(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
