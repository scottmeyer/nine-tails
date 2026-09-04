package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/scottmeyer/nine-tails/internal/store"
)

type gatedReader struct {
	started chan struct{}
	release chan struct{}
	body    []byte
	sent    bool
}

func (r *gatedReader) Read(p []byte) (int, error) {
	if r.sent {
		return 0, io.EOF
	}
	close(r.started)
	<-r.release
	r.sent = true
	return copy(p, r.body), nil
}

func requireExit(t *testing.T, r result, code int, contains string) {
	t.Helper()
	if r.code != code || (contains != "" && !strings.Contains(r.err, contains)) {
		t.Fatalf("exit=%d stdout=%q stderr=%q; want exit %d containing %q", r.code, r.out, r.err, code, contains)
	}
}

func TestInvalidMutationFormatsDoNotWrite(t *testing.T) {
	h := newHarness(t)

	// Even creation of the first agent must not happen before format validation.
	requireExit(t, h.run("base", "ghost", "--format", "bogus", "Ghost base."), 2, "unknown format")
	requireExit(t, h.run("inspect", "ghost"), 3, "no records")

	base := h.ok("base", "a", "Base one.").id(t)
	if base != "base_1" {
		t.Fatalf("invalid base consumed an id: got %s", base)
	}

	requireExit(t, h.run("prefer", "a", "--format", "bogus", "must not persist"), 2, "unknown format")
	flat := h.ok("inspect", "a", "--lane", "guidance").json(t)
	if records, ok := flat["records"].([]any); !ok || len(records) != 0 {
		t.Fatalf("invalid append persisted or lost empty records shape: %s", h.ok("inspect", "a", "--lane", "guidance").out)
	}

	requireExit(t, h.run("base", "a", "--format", "bogus", "Base two."), 2, "unknown format")
	full := h.ok("inspect", "a").json(t)
	if got := full["base"].(map[string]any)["body"]; got != "Base one." {
		t.Fatalf("invalid base replacement persisted: %v", got)
	}

	requireExit(t, h.run("put", "a", "--lane", "definition", "--kind", "custom", "--name", "x", "--format", "bogus", "body"), 2, "unknown format")
	if got := h.ok("inspect", "a", "--kind", "custom").json(t)["records"].([]any); len(got) != 0 {
		t.Fatalf("invalid put persisted: %v", got)
	}

	requireExit(t, h.run("state", "put", "a/working", "--expect", "none", "--format", "bogus", "status: bad"), 2, "unknown format")
	requireExit(t, h.run("state", "get", "a/working"), 3, "no active state")

	script := writeScript(t, "contract.sh", "echo contract\n")
	requireExit(t, h.run("tool", "add", "a", "contract", "--script", script, "--description", "contract", "--format", "bogus"), 2, "unknown format")
	entries, err := os.ReadDir(filepath.Join(h.home, "artifacts"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("invalid tool add left artifacts: %v", entries)
	}

	requireExit(t, h.run("agent", "add", "a", "helper", "--description", "Helps.", "--format", "bogus"), 2, "unknown format")
	if got := h.ok("inspect", "a", "--include", "agents").json(t)["agents"].([]any); len(got) != 0 {
		t.Fatalf("invalid agent add persisted: %v", got)
	}

	emptyCompile := "input_entries: []\nitems: []\nentries: []\n"
	requireExit(t, h.runIn(emptyCompile, "brief", "put", "a", "--expect-generation", "none", "--expect-base", base, "--stdin", "--format", "bogus"), 2, "unknown format")
	if got := h.ok("compile-input", "a").json(t)["expect_generation"]; got != "none" {
		t.Fatalf("invalid brief put installed generation %v", got)
	}

	// Format validation also precedes invoking an external compiler.
	requireExit(t, h.run("compile", "a", "--compiler", "/definitely/not/a/compiler", "--format", "bogus"), 2, "unknown format")

	// None of the invalid mutations may consume the next global id.
	if got := h.ok("prefer", "a", "valid").id(t); got != "rec_2" {
		t.Fatalf("invalid mutations consumed ids: next id is %s, want rec_2", got)
	}
}

func TestContextCommandValidationPrecedesGC(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "a", "Base.")
	ctx := contextID(t, h.ok("load", "a").out)

	for _, args := range [][]string{
		{"context", "list", "--agent", "Bad_Name"},
		{"context", "list", "--limit", "0"},
		{"context", "list", "--limit=-1"},
		{"context", "list", "--format", "bogus"},
	} {
		requireExit(t, h.run(args...), 2, "")
	}

	// Make the receipt old enough that GC would delete it if validation ran late.
	h.now = h.now.Add(48 * time.Hour)
	for _, args := range [][]string{
		{"context", "gc", "--older-than", "1h", "--format", "bogus"},
		{"context", "gc", "--older-than=-1d"},
		{"context", "gc", "--older-than", "0s"},
	} {
		requireExit(t, h.run(args...), 2, "")
		if r := h.run("inspect", ctx); r.code != 0 {
			t.Fatalf("invalid gc deleted %s: exit=%d stderr=%q", ctx, r.code, r.err)
		}
	}

	for _, args := range [][]string{
		{"context", "pin", "not-an-id"},
		{"context", "pin", "base_1"},
		{"context", "unpin", "ctx-nope"},
	} {
		requireExit(t, h.run(args...), 2, "expected a context id")
	}
	requireExit(t, h.run("context", "pin", "ctx_999"), 3, "context ctx_999")
	h.ok("context", "pin", ctx)
	h.ok("context", "unpin", ctx)

	if r := h.ok("context", "list", "--agent", "a", "--limit", "1", "--format", "yaml"); !strings.Contains(r.out, "context_id: "+ctx) {
		t.Fatalf("valid context list changed: %s", r.out)
	}
	var listed []map[string]any
	if r := h.ok("context", "list", "--agent", "a"); yaml.Unmarshal([]byte(r.out), &listed) != nil || len(listed) != 1 {
		t.Fatalf("context list is malformed: %s", r.out)
	}
	if rendered, ok := listed[0]["rendered"].([]any); !ok || len(rendered) == 0 {
		t.Fatalf("context list lost the receipt's rendered records: %#v", listed[0])
	}
	aggregate := h.ok("inspect", "a", "--include", "contexts").json(t)
	contexts := aggregate["contexts"].([]any)
	if rendered, ok := contexts[0].(map[string]any)["rendered"].([]any); !ok || len(rendered) == 0 {
		t.Fatalf("aggregate inspect lost the receipt's rendered records: %#v", contexts[0])
	}
}

func TestOriginContextIsRecheckedInsideMutationTransaction(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "a", "Base.")
	ctx := contextID(t, h.ok("load", "a").out)

	reader := &gatedReader{started: make(chan struct{}), release: make(chan struct{}), body: []byte("must not acquire a dangling origin")}
	var stdout, stderr bytes.Buffer
	a := &app{stdout: &stdout, stderr: &stderr, stdin: reader, now: func() time.Time { return h.now }, home: h.home}
	done := make(chan int, 1)
	go func() { done <- run(a, []string{"prefer", "--context", ctx, "--stdin"}) }()
	<-reader.started // the command resolved the context and is now blocked on input

	gcStore, err := store.Open(h.home)
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := store.GCContexts(gcStore, h.now.Add(time.Hour), false)
	_ = gcStore.Close()
	if err != nil || len(deleted) != 1 || deleted[0] != ctx {
		t.Fatalf("GC did not remove the raced receipt: deleted=%v err=%v", deleted, err)
	}
	close(reader.release)
	if code := <-done; code != 3 {
		t.Fatalf("mutation with deleted origin: code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if rows := h.ok("inspect", "a", "--lane", "guidance").json(t)["records"].([]any); len(rows) != 0 {
		t.Fatalf("mutation wrote a dangling origin record: %#v", rows)
	}
}

func TestInspectModesRequireAgentAndKeepStableShapes(t *testing.T) {
	h := newHarness(t)
	for _, args := range [][]string{
		{"inspect", "nobody", "--query", "x"},
		{"inspect", "nobody", "--lane", "guidance"},
		{"inspect", "nobody", "--kind", "prefer"},
		{"inspect", "nobody", "--name", "x"},
		{"inspect", "nobody", "--coverage", "novel"},
		{"inspect", "nobody", "--lint", "condition-loss"},
	} {
		requireExit(t, h.run(args...), 3, "no records")
	}

	// An agent may exist without a base; inspecting that repairable state must
	// return base: null rather than panicking in YAML serialization.
	h.ok("remember", "baseless", "still inspectable")
	for _, format := range []string{"json", "yaml"} {
		r := h.ok("inspect", "baseless", "--include", "base", "--format", format)
		var got map[string]any
		if err := yaml.Unmarshal([]byte(r.out), &got); err != nil || got["base"] != nil {
			t.Fatalf("baseless %s inspect: err=%v value=%#v output=%s", format, err, got["base"], r.out)
		}
	}

	base := h.ok("base", "a", "Base.").id(t)
	full := h.ok("inspect", "a").json(t)
	for _, key := range []string{"state", "journal", "tools", "agents", "signals"} {
		rows, ok := full[key].([]any)
		if !ok || len(rows) != 0 {
			t.Fatalf("full inspect %s must be []: %#v\n%s", key, full[key], h.ok("inspect", "a").out)
		}
	}
	if brief, ok := full["brief"]; !ok || brief != nil {
		t.Fatalf("full inspect brief must be present as null: %#v", full)
	}

	selected := h.ok("inspect", "a", "--include", "state,brief,contexts").json(t)
	for _, key := range []string{"state", "brief", "contexts"} {
		if _, ok := selected[key]; !ok {
			t.Fatalf("selected section %s omitted: %#v", key, selected)
		}
	}
	for _, key := range []string{"base", "journal", "tools", "agents", "signals"} {
		if _, ok := selected[key]; ok {
			t.Fatalf("unselected section %s was emitted: %#v", key, selected)
		}
	}

	flat := h.ok("inspect", "a", "--query", "missing").json(t)
	if rows, ok := flat["records"].([]any); !ok || len(rows) != 0 {
		t.Fatalf("flat empty records must be []: %#v", flat)
	}

	var yamlView map[string]any
	if r := h.ok("inspect", "a", "--include", "state,brief", "--format", "yaml"); yaml.Unmarshal([]byte(r.out), &yamlView) != nil {
		t.Fatalf("inspect YAML is invalid: %s", r.out)
	}
	if _, ok := yamlView["state"].([]any); !ok {
		t.Fatalf("YAML state must be []: %#v", yamlView)
	}
	if brief, ok := yamlView["brief"]; !ok || brief != nil {
		t.Fatalf("YAML brief must be null: %#v", yamlView)
	}

	empty := "input_entries: []\nitems: []\nentries: []\n"
	gen := h.okIn(empty, "brief", "put", "a", "--expect-generation", "none", "--expect-base", base, "--stdin").id(t)
	byID := h.ok("inspect", gen).json(t)
	for _, key := range []string{"items", "inputs"} {
		rows, ok := byID[key].([]any)
		if !ok || len(rows) != 0 {
			t.Fatalf("inspect empty generation %s must be []: %#v", key, byID[key])
		}
	}
}

func TestInspectExplicitEmptyFiltersSelectFlatView(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "a", "Base.")
	h.ok("prefer", "a", "Keep evidence concise.")

	for _, flag := range []string{"query", "lane", "kind", "name"} {
		t.Run(flag, func(t *testing.T) {
			got := h.ok("inspect", "a", "--"+flag+"=").json(t)
			rows, ok := got["records"].([]any)
			if !ok || len(rows) != 2 {
				t.Fatalf("explicitly empty --%s did not select the unfiltered flat view: %#v", flag, got)
			}
			if _, ok := got["base"]; ok {
				t.Fatalf("explicitly empty --%s selected aggregate output: %#v", flag, got)
			}
		})
	}
}

func TestInspectSignalsAreFlattenedInFullAndFlatViews(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "a", "Base.")
	sig := h.ok("signal", "a", "--subject", "Ping", "--body", "payload").id(t)

	assertFlattened := func(t *testing.T, value any) {
		t.Helper()
		row, ok := value.(map[string]any)
		if !ok {
			t.Fatalf("signal row is %T: %#v", value, value)
		}
		if row["id"] != sig || row["lane"] != "signal" {
			t.Fatalf("signal envelope is not flattened: %#v", row)
		}
		if _, ok := row["record"]; ok {
			t.Fatalf("signal has nested record envelope: %#v", row)
		}
		if delivery, ok := row["delivery"].(map[string]any); !ok || delivery["state"] != "pending" {
			t.Fatalf("signal delivery missing: %#v", row)
		}
	}

	full := h.ok("inspect", "a", "--include", "signals").json(t)
	assertFlattened(t, full["signals"].([]any)[0])
	flat := h.ok("inspect", "a", "--lane", "signal").json(t)
	assertFlattened(t, flat["records"].([]any)[0])
}

func TestInspectSignalsUseRecordCreationOrder(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "a", "Base.")
	first := h.ok("signal", "a", "--subject", "created first", "--at", "2026-09-05T00:00:00Z").id(t)
	second := h.ok("signal", "a", "--subject", "created second", "--at", "2026-09-04T00:00:00Z").id(t)

	view := h.ok("inspect", "a", "--include", "signals").json(t)
	rows := view["signals"].([]any)
	if len(rows) != 2 || rows[0].(map[string]any)["id"] != first || rows[1].(map[string]any)["id"] != second {
		t.Fatalf("inspect signals not in creation order: %#v", rows)
	}
}

func TestInspectAllIncludesSectionHistories(t *testing.T) {
	h := newHarness(t)
	base1 := h.ok("base", "a", "Base one.").id(t)
	base2 := h.ok("base", "a", "--expect", base1, "Base two.").id(t)

	e1 := h.ok("prefer", "a", "first guidance").id(t)
	doc1 := "input_entries: [" + e1 + "]\nitems: [{key: first, body: first brief}]\nentries: [{id: " + e1 + ", disposition: represented, items: [first]}]\n"
	gen1 := h.okIn(doc1, "brief", "put", "a", "--expect-generation", "none", "--expect-base", base2, "--stdin").id(t)
	e2 := h.ok("prefer", "a", "second guidance").id(t)
	doc2 := "input_entries: [" + e2 + "]\nitems: [{key: second, body: second brief}]\nentries: [{id: " + e2 + ", disposition: represented, items: [second]}]\n"
	gen2 := h.okIn(doc2, "brief", "put", "a", "--expect-generation", gen1, "--expect-base", base2, "--stdin").id(t)

	toolBody1 := "description: old shared\nexec:\n  argv: [/bin/true]"
	h.ok("put", "shared", "--lane", "definition", "--kind", "tool", "--name", "shared-tool", toolBody1)
	toolBody2 := "description: new shared\nexec:\n  argv: [/bin/true]"
	h.ok("put", "shared", "--lane", "definition", "--kind", "tool", "--name", "shared-tool", toolBody2)

	sig := h.ok("signal", "a", "--subject", "done").id(t)
	h.ok("tick", "--claim")
	byID := h.ok("inspect", sig).json(t)
	token := byID["delivery"].(map[string]any)["lease_token"].(string)
	h.ok("signal", "ack", sig, "--lease", token)

	all := h.ok("inspect", "a", "--all", "--include", "base,brief,tools,signals").json(t)
	if active := all["base"].(map[string]any); active["id"] != base2 {
		t.Fatalf("active base changed under --all: %#v", active)
	}
	bases := all["base_history"].([]any)
	if len(bases) != 2 || bases[0].(map[string]any)["id"] != base1 || bases[0].(map[string]any)["status"] != "superseded" || bases[1].(map[string]any)["id"] != base2 {
		t.Fatalf("base history is incomplete: %#v", bases)
	}
	if active := all["brief"].(map[string]any)["generation"].(map[string]any); active["id"] != gen2 {
		t.Fatalf("active brief changed under --all: %#v", active)
	}
	briefs := all["brief_history"].([]any)
	if len(briefs) != 2 || briefs[0].(map[string]any)["generation"].(map[string]any)["id"] != gen1 || briefs[0].(map[string]any)["generation"].(map[string]any)["status"] != "superseded" || briefs[0].(map[string]any)["items"].([]any)[0].(map[string]any)["status"] != "superseded" {
		t.Fatalf("brief history is incomplete: %#v", briefs)
	}
	tools := all["tools"].([]any)
	if len(tools) != 2 || tools[0].(map[string]any)["status"] != "superseded" || tools[1].(map[string]any)["status"] != "active" {
		t.Fatalf("visible shared tool history is incomplete: %#v", tools)
	}
	signals := all["signals"].([]any)
	if len(signals) != 1 || signals[0].(map[string]any)["id"] != sig || signals[0].(map[string]any)["delivery"].(map[string]any)["state"] != "acknowledged" {
		t.Fatalf("acknowledged signals are missing: %#v", signals)
	}

	activeOnly := h.ok("inspect", "a", "--include", "base,brief,tools,signals").json(t)
	if _, ok := activeOnly["base_history"]; ok {
		t.Fatalf("default inspect unexpectedly changed shape: %#v", activeOnly)
	}
	if _, ok := activeOnly["brief_history"]; ok {
		t.Fatalf("default inspect unexpectedly changed shape: %#v", activeOnly)
	}
	if got := activeOnly["signals"].([]any); len(got) != 0 {
		t.Fatalf("acknowledged signal leaked into active inspect: %#v", got)
	}
}

func TestStateGetOnlyResolvesWorkingStateKind(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "a", "Base.")
	h.ok("put", "a", "--lane", "state", "--kind", "archived-state", "--name", "working", "source: archive")
	h.ok("state", "put", "a/working", "--expect", "none", "source: live")
	if r := h.ok("state", "get", "a/working"); r.out != "source: live\n" {
		t.Fatalf("state get resolved another kind: %q", r.out)
	}
	h.ok("put", "a", "--lane", "state", "--kind", "archived-state", "--name", "archive-only", "source: archive")
	requireExit(t, h.run("state", "get", "a/archive-only"), 3, "no active state")
	for _, target := range []string{"Bad_Name/working", "a/Bad_Name", "a/none", "a/work/extra"} {
		requireExit(t, h.run("state", "get", target), 2, "")
	}
}

func TestReservedBaseTupleCannotBeCreatedAsState(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "a", "Base.")
	for _, args := range [][]string{
		{"put", "a", "--lane", "state", "--kind", "agent-base", "--name", "base", "status: invalid"},
		{"put", "a", "--lane", "definition", "--kind", "agent-base", "--name", "other", "invalid base"},
	} {
		requireExit(t, h.run(args...), 2, "agent-base")
	}
}

func TestStartupErrorHonorsJSONFormat(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := errors.New("NINE_TAILS_NOW must be RFC 3339: bad timestamp")
	code := reportStartupError(&stdout, &stderr, []string{"inspect", "a", "--format", "json"}, err)
	if code != 2 || !strings.HasPrefix(stderr.String(), "nine-tails: NINE_TAILS_NOW") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	m := (result{out: stdout.String()}).json(t)
	if m["code"] != float64(2) || m["error"] != err.Error() {
		t.Fatalf("startup JSON error: %s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	reportStartupError(&stdout, &stderr, []string{"inspect", "a"}, err)
	if stdout.Len() != 0 {
		t.Fatalf("text startup error wrote stdout: %q", stdout.String())
	}

	h := newHarness(t)
	if r := h.run("note", "Bad_Name", "--", "--format", "json"); r.code != 2 || r.out != "" {
		t.Fatalf("positional format text selected JSON errors: code=%d stdout=%q stderr=%q", r.code, r.out, r.err)
	}
	if r := h.run("inspect", "nobody", "--format", "json", "--format", "yaml"); r.code != 3 || r.out != "" {
		t.Fatalf("overridden JSON format remained active: code=%d stdout=%q stderr=%q", r.code, r.out, r.err)
	}
	if r := h.run("inspect", "nobody", "--format", "yaml", "--format=json"); r.code != 3 || !strings.Contains(r.out, `"code": 3`) {
		t.Fatalf("final JSON format was not honored: code=%d stdout=%q stderr=%q", r.code, r.out, r.err)
	}
}
