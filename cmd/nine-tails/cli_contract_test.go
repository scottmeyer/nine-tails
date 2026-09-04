package main

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
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

func TestStatePutStaleCASHasExactStream(t *testing.T) {
	h := newHarness(t)
	state1 := h.ok("state", "put", "a/working", "--expect", "none", "status: first").id(t)
	state2 := h.ok("state", "put", "a/working", "--expect", state1, "status: second").id(t)

	got := h.run("state", "put", "a/working", "--expect", state1, "status: stale")
	want := result{
		code: 7,
		err:  "nine-tails: expected " + state1 + " but " + state2 + " is active\n",
	}
	if got != want {
		t.Fatalf("stale state CAS = %#v, want %#v", got, want)
	}
	if current := h.ok("state", "get", "a/working", "--format", "id"); current.out != state2+"\n" || current.err != "" {
		t.Fatalf("stale state CAS changed active state: %#v", current)
	}
}

func TestStatePutRejectsMalformedExpectWithoutMutation(t *testing.T) {
	h := newHarness(t)

	missing := h.run("state", "put", "a/working", "status: missing")
	wantMissing := result{
		code: 2,
		err:  "nine-tails: --expect is required: 'none' to create, or the current state id (shown in the capsule heading and by `state get`)\n",
	}
	if missing != wantMissing {
		t.Fatalf("missing --expect = %#v, want %#v", missing, wantMissing)
	}

	for _, expect := range []string{"", "None", "state1", "base_1", "state_1x"} {
		t.Run(fmt.Sprintf("expect_%q", expect), func(t *testing.T) {
			got := h.run("state", "put", "a/working", "--expect="+expect, "status: rejected")
			want := result{
				code: 2,
				err:  fmt.Sprintf("nine-tails: --expect must be 'none' or a state id like state_18, got %q\n", expect),
			}
			if got != want {
				t.Fatalf("malformed --expect = %#v, want %#v", got, want)
			}
		})
	}

	entries, err := os.ReadDir(h.home)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("invalid state puts touched the store: %v", entries)
	}
	if id := h.ok("state", "put", "a/working", "--expect", "none", "status: accepted").id(t); !strings.HasPrefix(id, "state_") {
		t.Fatalf("state put after invalid attempts: got %s", id)
	}
}

func TestInvalidMutationFormatsDoNotWrite(t *testing.T) {
	h := newHarness(t)

	// Even creation of the first agent must not happen before format validation.
	requireExit(t, h.run("base", "ghost", "--format", "bogus", "Ghost base."), 2, "unknown format")
	requireExit(t, h.run("inspect", "ghost"), 3, "no records")

	base := h.ok("base", "a", "Base one.").id(t)
	if !strings.HasPrefix(base, "base_") {
		t.Fatalf("base after invalid attempts: got %s", base)
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

	if got := h.ok("prefer", "a", "valid").id(t); !strings.HasPrefix(got, "rec_") {
		t.Fatalf("valid append after invalid mutations: %s", got)
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
	// The state lane has exactly one kind, so put and state get always agree.
	requireExit(t, h.run("put", "a", "--lane", "state", "--kind", "archived-state", "--name", "working", "source: archive"), 2, "working-state")
	h.ok("state", "put", "a/working", "--expect", "none", "source: live")
	if r := h.ok("state", "get", "a/working"); r.out != "source: live\n" {
		t.Fatalf("state get: %q", r.out)
	}
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

func TestCallUnsupportedFormatKeepsToolStdoutEmpty(t *testing.T) {
	h := newHarness(t)
	tests := []struct {
		name string
		args []string
		err  string
	}{
		{name: "format separate", args: []string{"call", "--format", "json", "anything"}, err: "nine-tails: unknown flag: --format\n"},
		{name: "call first", args: []string{"call", "anything", "--format=json"}, err: "nine-tails: unknown flag: --format\n"},
		{name: "long help", args: []string{"--help", "call", "anything", "--format=json"}, err: "nine-tails: unknown command \"anything\" for \"nine-tails\"\n"},
		{name: "short help", args: []string{"-h", "call", "anything", "--format=json"}, err: "nine-tails: unknown command \"anything\" for \"nine-tails\"\n"},
		{name: "long help explicit false", args: []string{"--help=false", "call", "anything", "--format=json"}, err: "nine-tails: unknown flag: --format\n"},
		{name: "short help explicit false", args: []string{"-h=false", "call", "anything", "--format=json"}, err: "nine-tails: unknown flag: --format\n"},
		{name: "long help explicit true", args: []string{"--help=true", "call", "anything", "--format=json"}, err: "nine-tails: unknown flag: --format\n"},
		{name: "short help explicit true", args: []string{"-h=true", "call", "anything", "--format=json"}, err: "nine-tails: unknown flag: --format\n"},
		{name: "long version", args: []string{"--version", "call", "anything", "--format=json"}, err: "nine-tails: unknown command \"anything\" for \"nine-tails\"\n"},
		{name: "short version", args: []string{"-v", "call", "anything", "--format=json"}, err: "nine-tails: unknown command \"anything\" for \"nine-tails\"\n"},
		{name: "long version explicit false", args: []string{"--version=false", "call", "anything", "--format=json"}, err: "nine-tails: unknown flag: --version\n"},
		{name: "short version explicit false", args: []string{"-v=false", "call", "anything", "--format=json"}, err: "nine-tails: unknown shorthand flag: 'v' in -v=false\n  text starting with '-' needs -- before it, or --stdin\n"},
		{name: "long version explicit true", args: []string{"--version=true", "call", "anything", "--format=json"}, err: "nine-tails: unknown flag: --version\n"},
		{name: "short version explicit true", args: []string{"-v=true", "call", "anything", "--format=json"}, err: "nine-tails: unknown shorthand flag: 'v' in -v=true\n  text starting with '-' needs -- before it, or --stdin\n"},
		{name: "home separate", args: []string{"--home", h.home, "call", "anything", "--format=json"}, err: "nine-tails: unknown flag: --format\n"},
		{name: "home joined after boolean", args: []string{"--help=false", "--home=" + h.home, "call", "anything", "--format=json"}, err: "nine-tails: unknown flag: --format\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name+"/normal", func(t *testing.T) {
			r := h.run(tt.args...)
			want := result{code: 2, err: tt.err}
			if r != want {
				t.Fatalf("unsupported call format = %#v, want %#v", r, want)
			}
		})

		t.Run(tt.name+"/startup", func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := errors.New("startup failed")
			if code := reportStartupError(&stdout, &stderr, tt.args, err); code != 2 {
				t.Fatalf("startup exit = %d, want 2", code)
			}
			if stdout.String() != "" || stderr.String() != "nine-tails: startup failed\n" {
				t.Fatalf("call startup streams: stdout=%q stderr=%q", stdout.String(), stderr.String())
			}
		})
	}

	entries, err := os.ReadDir(h.home)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("rejected call invocations touched the store: %v", entries)
	}
}

func TestCallOwnershipRecognizesRootBooleanSpellings(t *testing.T) {
	h := newHarness(t)
	boolValues := []string{"true", "false", "1", "0", "t", "f", "T", "F", "TRUE", "FALSE", "True", "False"}
	flags := []string{"--help", "--version", "-h", "-v", "-hv", "-vh", "-hh", "-vv", "-hvvh"}
	for _, name := range []string{"--help", "--version", "-h", "-v"} {
		for _, value := range boolValues {
			flags = append(flags, name+"="+value)
		}
	}
	flags = append(flags, "-hv=false", "-vh=TRUE")

	for _, flag := range flags {
		t.Run(strings.ReplaceAll(flag, "=", "_"), func(t *testing.T) {
			args := []string{flag, "--home=" + h.home, "call", "anything", "--format=json"}
			if got := topLevelCommand(args); got != "call" {
				t.Fatalf("top-level command = %q, want call", got)
			}
			if wantsJSON(args) {
				t.Fatal("call invocation selected the root JSON error envelope")
			}
			r := h.run(args...)
			if r.code != 2 || r.out != "" {
				t.Fatalf("call-owned stdout: %#v", r)
			}

			var stdout, stderr bytes.Buffer
			reportStartupError(&stdout, &stderr, args, errors.New("startup failed"))
			if stdout.Len() != 0 {
				t.Fatalf("call startup error wrote stdout: %q", stdout.String())
			}
		})
	}
}

func TestPureCommandGroupsRejectUnknownChildrenAndBareShowsHelp(t *testing.T) {
	h := newHarness(t)
	groups := map[string]struct {
		child    string
		helpHash string
	}{
		"state":   {child: "get", helpHash: "acc0cfdd20e05ae0ceb90051ba0c22d38ed9cb78aa3dfc97723d5cd25ca711a2"},
		"context": {child: "list", helpHash: "b66f989a979e7856a46f76b357fda88876442930cc387a71112883cfbcf6e0a6"},
		"tool":    {child: "add", helpHash: "e7da017ff2f59b4490cbdb911f4380611ee2b70322f38636e5335b00e4d6ea81"},
		"agent":   {child: "add", helpHash: "dcc6fe5421d3756a8668c2004d056d623864d61d45a215b3cf2fb4ce9831127a"},
		"brief":   {child: "put", helpHash: "70d7cdaa03ebcd76ea85da64bf1a8c382380dc0b48ff275c53f5f42e0fc14328"},
	}
	for group, baseline := range groups {
		t.Run(group, func(t *testing.T) {
			want := result{
				code: 2,
				err:  "nine-tails: unknown command \"nope\" for \"nine-tails " + group + "\"\n",
			}
			for _, args := range [][]string{
				{group, "nope"},
				{group, "nope", "--help"},
				{group, "--help", "nope"},
				{group, "nope", "-h"},
				{group, "-h", "nope"},
				{group, "--", "nope"},
				{group, "--help=true", "nope"},
				{group, "--help=false", "nope"},
				{group, "-h=true", "nope"},
				{group, "-h=false", "nope"},
				{group, "--home", h.home, "nope"},
				{group, "--home=" + h.home, "nope"},
				{"--home", h.home, group, "nope"},
				{"--home=" + h.home, group, "nope"},
				{"--help=true", group, "nope"},
				{"-h=true", group, "nope"},
				{group, "--", baseline.child, "nope"},
				{group, "--help", baseline.child, "nope"},
			} {
				if r := h.run(args...); r != want {
					t.Fatalf("%v = %#v, want %#v", args, r, want)
				}
			}

			bare := h.run(group)
			explicit := h.run(group, "--help")
			short := h.run(group, "-h")
			if bare != explicit || bare != short || bare.code != 0 || bare.out == "" || bare.err != "" {
				t.Fatalf("bare group = %#v; explicit help = %#v; short help = %#v", bare, explicit, short)
			}
			wantUsage := "Usage:\n  nine-tails " + group + " [command]\n"
			if !strings.Contains(bare.out, wantUsage) {
				t.Fatalf("group help changed from its non-runnable shape:\n%s", bare.out)
			}
			if got := fmt.Sprintf("%x", sha256.Sum256([]byte(bare.out))); got != baseline.helpHash {
				t.Fatalf("group help bytes changed from f22f6b4: sha256=%s, want %s\n%s", got, baseline.helpHash, bare.out)
			}

			// A space-form help flag precedes a known child in Cobra's baseline
			// command search, so it still renders the group's help. Explicit true
			// is parsed as part of the flag and reaches the child's help instead.
			for _, args := range [][]string{
				{group, "--help", baseline.child},
				{group, "-h", baseline.child},
				{group, "--home", h.home, "--help", baseline.child},
				{"--home", h.home, group, "--help", baseline.child},
			} {
				if r := h.run(args...); r != bare {
					t.Fatalf("%v = %#v, want baseline group help", args, r)
				}
			}
			childHelp := h.run(group, baseline.child, "--help")
			for _, args := range [][]string{
				{group, "--help=true", baseline.child},
				{group, "-h=true", baseline.child},
			} {
				if r := h.run(args...); r != childHelp {
					t.Fatalf("%v = %#v, want child help %#v", args, r, childHelp)
				}
			}
		})
	}

	for _, group := range []string{"state", "context", "tool", "agent", "brief"} {
		for _, args := range [][]string{
			{group, "--bogus", "nope"},
			{group, "nope", "--bogus"},
		} {
			r := h.run(args...)
			if r.code != 2 || r.out != "" || r.err != "nine-tails: unknown flag: --bogus\n" {
				t.Fatalf("%v unknown flag precedence = %#v", args, r)
			}
		}
		if r := h.run(group, "nope", "--home"); r.code != 2 || r.out != "" || !strings.Contains(r.err, "flag needs an argument: --home") {
			t.Fatalf("%s missing flag value precedence = %#v", group, r)
		}
		if r := h.run(group, "nope", "--help=bogus"); r.code != 2 || r.out != "" || !strings.Contains(r.err, `invalid argument "bogus"`) || !strings.Contains(r.err, `--help" flag`) {
			t.Fatalf("%s invalid flag value precedence = %#v", group, r)
		}
	}

	entries, err := os.ReadDir(h.home)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("invalid and help-only group invocations touched the store: %v", entries)
	}
	if id := h.ok("base", "a", "Base.").id(t); !strings.HasPrefix(id, "base_") {
		t.Fatalf("base after group invocations: got %s", id)
	}
}

func TestPureCommandGroupCompletionMatchesBaseline(t *testing.T) {
	h := newHarness(t)
	const completionErr = "Completion ended with directive: ShellCompDirectiveNoFileComp\n"
	groups := map[string]string{
		"state": "get\tPrint the current state document\n" +
			"put\tReplace state with compare-and-swap (--expect none to create)\n:4\n",
		"context": "gc\tDelete old unpinned receipts that no active record references as origin\n" +
			"list\tList receipts, newest first\n" +
			"pin\tpin a receipt so garbage collection keeps it\n" +
			"unpin\tunpin a receipt so garbage collection may delete it\n:4\n",
		"tool":  "add\tCopy a script into the artifact store and register it as a named tool\n:4\n",
		"agent": "add\tRegister a related agent (supersedes any active one of the same name)\n:4\n",
		"brief": "put\tValidate compiler output and install it as the next generation\n:4\n",
	}
	partials := map[string]struct {
		prefix string
		out    string
	}{
		"state":   {prefix: "g", out: "get\tPrint the current state document\n:4\n"},
		"context": {prefix: "p", out: "pin\tpin a receipt so garbage collection keeps it\n:4\n"},
		"tool":    {prefix: "a", out: "add\tCopy a script into the artifact store and register it as a named tool\n:4\n"},
		"agent":   {prefix: "a", out: "add\tRegister a related agent (supersedes any active one of the same name)\n:4\n"},
		"brief":   {prefix: "p", out: "put\tValidate compiler output and install it as the next generation\n:4\n"},
	}

	for group, children := range groups {
		t.Run(group, func(t *testing.T) {
			flags := result{
				code: 0,
				out:  "--home\toverride NINE_TAILS_HOME\n--help\thelp for " + group + "\n:4\n",
				err:  completionErr,
			}
			if r := h.run("__complete", group, "--"); r != flags {
				t.Fatalf("flag completion = %#v, want %#v", r, flags)
			}
			if r := h.run("__complete", group, "--h"); r != flags {
				t.Fatalf("partial flag completion = %#v", r)
			}
			if r := h.run("__complete", group, ""); r != (result{code: 0, out: children, err: completionErr}) {
				t.Fatalf("child completion = %#v", r)
			}
			partial := partials[group]
			if r := h.run("__complete", group, partial.prefix); r != (result{code: 0, out: partial.out, err: completionErr}) {
				t.Fatalf("partial child completion = %#v", r)
			}
		})
	}
}

func TestToolAddEmptyDescriptionStillConflictsWithStdin(t *testing.T) {
	h := newHarness(t)
	script := writeScript(t, "empty-description.sh", "exit 0\n")
	body := "description: supplied on stdin\nexec:\n  argv: []\n"
	r := h.runIn(body, "tool", "add", "a", "empty-description", "--script", script, "--description=", "--stdin")
	want := result{
		code: 2,
		err:  "nine-tails: give --description or --stdin, not both\n",
	}
	if r != want {
		t.Fatalf("empty --description with --stdin = %#v, want %#v", r, want)
	}
	entries, err := os.ReadDir(h.home)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("rejected tool add touched the store: %v", entries)
	}
	if id := h.ok("tool", "add", "a", "valid", "--script", script, "--description", "valid").id(t); !strings.HasPrefix(id, "tool_") {
		t.Fatalf("tool add after rejected attempts: got %s", id)
	}
}
