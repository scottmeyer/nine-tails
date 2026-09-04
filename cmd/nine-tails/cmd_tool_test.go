package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/scottmeyer/nine-tails/internal/store"
)

var toolIDRe = regexp.MustCompile(`^tool_[0-9A-Z]+$`)

// writeScript writes a shell script (not executable: tool add must chmod it).
func writeScript(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// execScript writes an executable script for tools registered via put.
func execScript(t *testing.T, name, body string) string {
	t.Helper()
	p := writeScript(t, name, body)
	if err := os.Chmod(p, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func toolBody(t *testing.T, r result) map[string]any {
	t.Helper()
	body, _ := r.json(t)["body"].(string)
	var m map[string]any
	if err := yaml.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("tool body is not YAML: %v\n%s", err, body)
	}
	return m
}

func argvOf(t *testing.T, body map[string]any) []string {
	t.Helper()
	ex, _ := body["exec"].(map[string]any)
	raw, _ := ex["argv"].([]any)
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		out = append(out, v.(string))
	}
	return out
}

func TestToolAddWithDescription(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "a", "Base.")
	script := writeScript(t, "echo-input.sh", "cat\necho \"$1\"\n")
	r := h.ok("tool", "add", "a", "echo-input", "--script", script, "--description", "Echo the input", "--meta", "tool=shell")
	id := r.id(t)
	if !toolIDRe.MatchString(id) || strings.Count(r.out, "\n") != 1 {
		t.Fatalf("tool add should print one tool id line, got %q", r.out)
	}
	// The artifact is copied under the harness home and made executable.
	p := filepath.Join(h.home, "artifacts", id, "echo-input.sh")
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatalf("artifact missing: %v", err)
	}
	if fi.Mode()&0o111 == 0 {
		t.Errorf("artifact %s is not executable: %v", p, fi.Mode())
	}
	// inspect shows the body with argv[0] pointing at the artifact.
	r = h.ok("inspect", id)
	body := toolBody(t, r)
	argv := argvOf(t, body)
	if len(argv) != 1 || argv[0] != "artifacts/"+id+"/echo-input.sh" {
		t.Errorf("argv = %q", argv)
	}
	if body["description"] != "Echo the input" || body["version"] != 1 {
		t.Errorf("body = %v", body)
	}
	if ex, _ := body["exec"].(map[string]any); ex["stdin"] != "json" {
		t.Errorf("--description form should default stdin to json: %v", body)
	}
	if !strings.Contains(r.out, `"shell"`) {
		t.Errorf("meta not stored:\n%s", r.out)
	}
	// load lists it under Available tools with its description and meta.
	r = h.ok("load", "a")
	if !strings.Contains(r.out, "## Available tools\n\n- `echo-input`: Echo the input [tool=shell]\n") {
		t.Errorf("capsule:\n%s", r.out)
	}
	// call runs it: stdin json → the script cats it, then echoes $1 (empty).
	r = h.ok("call", "--agent", "a", "echo-input", "--input", `{"pr": 1842, "repo": "acme/payments"}`)
	if r.out != `{"pr":1842,"repo":"acme/payments"}`+"\n" {
		t.Errorf("call stdout %q", r.out)
	}
	if r.err != "" {
		t.Errorf("call stderr should be empty: %q", r.err)
	}
	// Re-adding the name supersedes the old definition and keeps its artifact.
	r = h.ok("tool", "add", "a", "echo-input", "--script", script, "--description", "Echo v2")
	id2 := r.id(t)
	if id2 == id {
		t.Fatalf("second add reused %s", id)
	}
	if _, err := os.Stat(p); err != nil {
		t.Errorf("old artifact should be kept: %v", err)
	}
	r = h.ok("inspect", id)
	if !strings.Contains(r.out, `"status": "superseded"`) {
		t.Errorf("old definition should be superseded:\n%s", r.out)
	}
	r = h.ok("inspect", id2)
	if !strings.Contains(r.out, `"supersedes": "`+id+`"`) {
		t.Errorf("new definition should record what it superseded:\n%s", r.out)
	}
	r = h.ok("load", "a")
	if !strings.Contains(r.out, "- `echo-input`: Echo v2\n") || strings.Contains(r.out, "Echo the input") {
		t.Errorf("capsule should show only the new definition:\n%s", r.out)
	}
}

func TestToolAddWithStdinBody(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "a", "Base.")
	script := writeScript(t, "pr.sh", "printf 'in=%s pr=%s price=%s\\n' \"$(cat)\" \"$1\" \"$2\"\n")
	body := "description: Fetch a PR\nexec:\n  argv: [\"{{ pr }}\", \"{{ price }}\"]\n  stdin: json\n  timeout: 10s\ninput:\n  pr: {type: integer, required: true}\n  price: {type: number}\noutput:\n  format: text\n"
	r := h.okIn(body, "tool", "add", "a", "fetch-pr", "--script", script, "--stdin")
	id := r.id(t)
	r = h.ok("inspect", id)
	m := toolBody(t, r)
	argv := argvOf(t, m)
	want := []string{"artifacts/" + id + "/pr.sh", "{{ pr }}", "{{ price }}"}
	if strings.Join(argv, "|") != strings.Join(want, "|") {
		t.Errorf("argv = %q, want %q", argv, want)
	}
	if m["version"] != 1 {
		t.Errorf("version should be filled in: %v", m["version"])
	}
	if out, _ := m["output"].(map[string]any); out["format"] != "text" {
		t.Errorf("extra sections should be preserved: %v", m)
	}
	// Numbers keep their literal text; each placeholder is one argument.
	r = h.ok("call", "--agent", "a", "fetch-pr", "--input", `{"pr": 1842, "price": 1.50}`)
	if r.out != `in={"pr":1842,"price":1.50} pr=1842 price=1.50`+"\n" {
		t.Errorf("call stdout %q", r.out)
	}
	// Optional placeholder absent → removed from argv.
	r = h.okIn(`{"pr": 7}`, "call", "--agent", "a", "fetch-pr", "--stdin")
	if r.out != `in={"pr":7} pr=7 price=`+"\n" {
		t.Errorf("call --stdin stdout %q", r.out)
	}
	// Missing required input → 2, tool not run.
	r = h.run("call", "--agent", "a", "fetch-pr")
	if r.code != 2 || !strings.Contains(r.err, "missing required input: pr") || r.out != "" {
		t.Errorf("missing input: code=%d out=%q err=%q", r.code, r.out, r.err)
	}
	// Bad --input → 2.
	r = h.run("call", "--agent", "a", "fetch-pr", "--input", `[1]`)
	if r.code != 2 || !strings.Contains(r.err, "JSON object") {
		t.Errorf("array input: code=%d err=%q", r.code, r.err)
	}
	r = h.run("call", "--agent", "a", "fetch-pr", "--input", `{"pr":1}`, "--stdin")
	if r.code != 2 {
		t.Errorf("--input with --stdin should be 2, got %d", r.code)
	}
	// Flag presence, rather than a non-empty value, controls exclusivity and
	// whether the default empty object applies.
	for _, tc := range []struct {
		stdin string
		args  []string
	}{
		{"{}", []string{"call", "--agent", "a", "fetch-pr", "--input", "", "--stdin"}},
		{"", []string{"call", "--agent", "a", "fetch-pr", "--input", ""}},
		{"", []string{"call", "--agent", "a", "fetch-pr", "--stdin"}},
	} {
		r = h.runIn(tc.stdin, tc.args...)
		if r.code != 2 || !strings.Contains(r.err, "nine-tails:") {
			t.Errorf("empty/ambiguous explicit input %v: code=%d err=%q", tc.args, r.code, r.err)
		}
	}
}

func TestToolAddErrors(t *testing.T) {
	h := newHarness(t)
	script := writeScript(t, "s.sh", "true\n")
	cases := []struct {
		name  string
		stdin string
		args  []string
	}{
		{"unreadable script", "", []string{"tool", "add", "a", "x", "--script", filepath.Join(t.TempDir(), "missing.sh"), "--description", "d"}},
		{"script is a directory", "", []string{"tool", "add", "a", "x", "--script", t.TempDir(), "--description", "d"}},
		{"no script", "", []string{"tool", "add", "a", "x", "--description", "d"}},
		{"no description", "", []string{"tool", "add", "a", "x", "--script", script}},
		{"both description and stdin", "description: d\n", []string{"tool", "add", "a", "x", "--script", script, "--description", "d", "--stdin"}},
		{"bad tool name", "", []string{"tool", "add", "a", "Bad_Name", "--script", script, "--description", "d"}},
		{"reserved tool name", "", []string{"tool", "add", "a", "none", "--script", script, "--description", "d"}},
		{"bad agent name", "", []string{"tool", "add", "A", "x", "--script", script, "--description", "d"}},
		{"bad meta", "", []string{"tool", "add", "a", "x", "--script", script, "--description", "d", "--meta", "novalue"}},
		{"stdin not yaml", "description: [oops\n", []string{"tool", "add", "a", "x", "--script", script, "--stdin"}},
		{"stdin undeclared placeholder", "description: d\nexec:\n  argv: [\"{{ pr }}\"]\n", []string{"tool", "add", "a", "x", "--script", script, "--stdin"}},
		{"stdin placeholder inside element", "description: d\nexec:\n  argv: [\"pr={{ pr }}\"]\ninput:\n  pr: {}\n", []string{"tool", "add", "a", "x", "--script", script, "--stdin"}},
		{"stdin missing description", "exec:\n  argv: [x]\n", []string{"tool", "add", "a", "x", "--script", script, "--stdin"}},
		{"stdin zero timeout", "description: d\nexec:\n  timeout: 0s\n", []string{"tool", "add", "a", "x", "--script", script, "--stdin"}},
		{"stdin negative timeout", "description: d\nexec:\n  timeout: -5s\n", []string{"tool", "add", "a", "x", "--script", script, "--stdin"}},
		{"stdin empty", "", []string{"tool", "add", "a", "x", "--script", script, "--stdin"}},
		{"missing name", "", []string{"tool", "add", "a", "--script", script, "--description", "d"}},
	}
	for _, c := range cases {
		r := h.runIn(c.stdin, c.args...)
		if r.code != 2 {
			t.Errorf("%s: want exit 2, got %d (out=%q err=%q)", c.name, r.code, r.out, r.err)
		}
		if !strings.HasPrefix(r.err, "nine-tails: ") {
			t.Errorf("%s: stderr should start with nine-tails:, got %q", c.name, r.err)
		}
	}
	// No artifact directory survives a failed add, and no id was consumed.
	entries, _ := os.ReadDir(filepath.Join(h.home, "artifacts"))
	if len(entries) != 0 {
		t.Errorf("failed adds left artifacts: %v", entries)
	}
	first := h.ok("tool", "add", "a", "x", "--script", script, "--description", "d").id(t)
	if !strings.HasPrefix(first, "tool_") {
		t.Errorf("tool id: %s", first)
	}
	r := h.ok("tool", "add", "a", "x", "--script", script, "--description", "json out", "--format", "json")
	m := r.json(t)
	if id, _ := m["id"].(string); !strings.HasPrefix(id, "tool_") || id == first || m["kind"] != "tool" || m["lane"] != "definition" || m["name"] != "x" || m["supersedes"] != first {
		t.Errorf("json envelope: %v", m)
	}
}

func TestToolAddRefusesPreexistingArtifactSymlink(t *testing.T) {
	h := newHarness(t)
	h.ok("config") // create the store layout
	outside := t.TempDir()
	// Ids are unpredictable, so stage the collision against the artifact
	// writer directly: a symlink already occupying the record's directory.
	link := filepath.Join(h.home, "artifacts", "tool_STAGED")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := createToolArtifact(h.home, "tool_STAGED", "escape.sh", []byte("echo unsafe\n")); err == nil || !strings.Contains(err.Error(), "artifact directory") {
		t.Fatalf("pre-existing symlink: err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "escape.sh")); !os.IsNotExist(err) {
		t.Fatalf("artifact writer wrote outside its directory: %v", err)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("pre-existing symlink was removed or replaced: info=%v err=%v", info, err)
	}
	if r := h.run("inspect", "a"); r.code != 3 {
		t.Fatalf("no record should exist: code=%d output=%s", r.code, r.out)
	}
}

func TestCallExitCodes(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "a", "Base.")
	fail := writeScript(t, "fail.sh", "echo before >&2\necho partial\nexit 3\n")
	h.ok("tool", "add", "a", "fails", "--script", fail, "--description", "Fails with 3")
	r := h.run("call", "--agent", "a", "fails")
	if r.code != 3 {
		t.Errorf("tool exit status should pass through: got %d", r.code)
	}
	if r.out != "partial\n" || r.err != "before\nnine-tails: fails exited with status 3\n" {
		t.Errorf("out=%q err=%q", r.out, r.err)
	}
	// Cannot start → 5.
	h.okIn("description: gone\nexec:\n  argv: [/nonexistent/nine-tails-tool]\n", "put", "a", "--lane", "definition", "--kind", "tool", "--name", "gone", "--stdin")
	r = h.run("call", "--agent", "a", "gone")
	if r.code != 5 || !strings.Contains(r.err, "gone") {
		t.Errorf("cannot start: code=%d err=%q", r.code, r.err)
	}
	// Timeout → 5.
	slow := execScript(t, "slow.sh", "exec sleep 2\n")
	h.okIn("description: slow\nexec:\n  argv: ["+slow+"]\n  timeout: 100ms\n", "put", "a", "--lane", "definition", "--kind", "tool", "--name", "slow", "--stdin")
	r = h.run("call", "--agent", "a", "slow")
	if r.code != 5 || !strings.Contains(r.err, "timed out") {
		t.Errorf("timeout: code=%d err=%q", r.code, r.err)
	}
	// Unknown tool → 3; unknown agent → 3 (nothing visible); unknown context → 3.
	r = h.run("call", "--agent", "a", "nope")
	if r.code != 3 {
		t.Errorf("unknown tool: %d %s", r.code, r.err)
	}
	r = h.run("call", "--agent", "nobody", "fails")
	if r.code != 3 {
		t.Errorf("unknown agent: %d %s", r.code, r.err)
	}
	r = h.run("call", "--context", "ctx_999", "fails")
	if r.code != 3 {
		t.Errorf("unknown context: %d %s", r.code, r.err)
	}
	// Malformed tool name → 2.
	r = h.run("call", "--agent", "a", "Not/Valid")
	if r.code != 2 {
		t.Errorf("bad name: %d %s", r.code, r.err)
	}
	// Corrupt stored body (AC19) → 4 with a clear message; the capsule still loads.
	r = h.ok("tool", "add", "a", "corrupt", "--script", fail, "--description", "Will be corrupted")
	id := r.id(t)
	st, err := store.Open(h.home)
	if err != nil {
		t.Fatal(err)
	}
	err = st.Tx(func(tx *sql.Tx) error { return store.SetBody(tx, id, "exec: [not: a: tool") })
	_ = st.Close()
	if err != nil {
		t.Fatal(err)
	}
	r = h.run("call", "--agent", "a", "corrupt")
	if r.code != 4 || !strings.Contains(r.err, id) || !strings.Contains(r.err, "corrupt body") {
		t.Errorf("corrupt body: code=%d err=%q", r.code, r.err)
	}
	r = h.ok("load", "a")
	if !strings.Contains(r.err, "nine-tails: skipped "+id) || !strings.Contains(r.out, "- `fails`: Fails with 3") {
		t.Errorf("corrupt tool should be skipped, not fatal: out=%q err=%q", r.out, r.err)
	}
}

func TestCallContextSuppliesAgentAndEnv(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "a", "Base.")
	h.ok("base", "b", "Base.")
	env := writeScript(t, "env.sh", "echo \"agent=$NINE_TAILS_AGENT ctx=$NINE_TAILS_CONTEXT home=$NINE_TAILS_HOME\"\n")
	h.ok("tool", "add", "a", "env", "--script", env, "--description", "Print env")
	ctx := contextID(t, h.ok("load", "a").out)
	r := h.ok("call", "--context", ctx, "env")
	if r.out != "agent=a ctx="+ctx+" home="+h.home+"\n" {
		t.Errorf("env via context: %q", r.out)
	}
	r = h.ok("call", "--agent", "a", "env")
	if r.out != "agent=a ctx= home="+h.home+"\n" {
		t.Errorf("env via --agent: %q", r.out)
	}
	// --context and --agent agreeing is fine; disagreeing is 2.
	r = h.ok("call", "--context", ctx, "--agent", "a", "env")
	if !strings.HasPrefix(r.out, "agent=a ctx="+ctx) {
		t.Errorf("agreeing flags: %q", r.out)
	}
	r = h.run("call", "--context", ctx, "--agent", "b", "env")
	if r.code != 2 || !strings.Contains(r.err, ctx+" belongs to a, not b") {
		t.Errorf("disagreeing flags: code=%d err=%q", r.code, r.err)
	}
	// b cannot see a's tool through a's context.
	ctxB := contextID(t, h.ok("load", "b").out)
	if r := h.run("call", "--context", ctxB, "env"); r.code != 3 {
		t.Errorf("b should not see a's tool: %d", r.code)
	}
}

func TestCallRejectsExplicitEmptySelectors(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "a", "Base.")
	shared := execScript(t, "empty-selector-shared.sh", "echo shared\n")
	owned := execScript(t, "empty-selector-owned.sh", "echo owned\n")
	h.okIn("description: shared\nexec:\n  argv: ["+shared+"]\n", "put", "shared", "--lane", "definition", "--kind", "tool", "--name", "shared-probe", "--stdin")
	h.okIn("description: owned\nexec:\n  argv: ["+owned+"]\n", "put", "a", "--lane", "definition", "--kind", "tool", "--name", "owned-probe", "--stdin")
	ctx := contextID(t, h.ok("load", "a").out)

	for _, tc := range []struct {
		name string
		args []string
		err  string
	}{
		{"empty agent cannot fall back to shared", []string{"call", "--agent=", "shared-probe"}, "nine-tails: --agent must not be empty\n"},
		{"empty context cannot fall back to shared", []string{"call", "--context=", "shared-probe"}, "nine-tails: --context must not be empty\n"},
		{"empty agent cannot take context scope", []string{"call", "--agent=", "--context", ctx, "owned-probe"}, "nine-tails: --agent must not be empty\n"},
		{"empty context cannot take agent scope", []string{"call", "--context=", "--agent", "a", "owned-probe"}, "nine-tails: --context must not be empty\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := h.run(tc.args...)
			want := result{code: 2, err: tc.err}
			if r != want {
				t.Fatalf("result = %#v, want %#v", r, want)
			}
		})
	}

	if r := h.ok("call", "--agent", "a", "--context", ctx, "owned-probe"); r.out != "owned\n" || r.err != "" {
		t.Fatalf("agreeing non-empty selectors changed: %#v", r)
	}
	r := h.run("call", "--agent", "shared", "--context", ctx, "owned-probe")
	wantErr := "nine-tails: " + ctx + " belongs to a, not shared\n"
	if r != (result{code: 2, err: wantErr}) {
		t.Fatalf("disagreeing non-empty selectors = %#v, want stderr %q", r, wantErr)
	}
}

func TestCallContextAppliesMetadataAndOwnedShadowing(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "a", "Base.")
	shared := execScript(t, "shared.sh", "echo shared\n")
	owned := execScript(t, "owned.sh", "echo owned\n")
	h.okIn("description: shared\nexec:\n  argv: ["+shared+"]\n", "put", "shared", "--lane", "definition", "--kind", "tool", "--name", "scoped", "--stdin")
	h.okIn("description: owned\nexec:\n  argv: ["+owned+"]\n", "put", "a", "--lane", "definition", "--kind", "tool", "--name", "scoped", "--stdin", "--meta", "repo=one")

	matching := contextID(t, h.ok("load", "a", "--meta", "repo=one").out)
	if r := h.ok("call", "--context", matching, "scoped"); r.out != "owned\n" {
		t.Errorf("matching owned tool: %q", r.out)
	}

	conflicting := contextID(t, h.ok("load", "a", "--meta", "repo=two").out)
	r := h.run("call", "--context", conflicting, "scoped")
	if r.code != 3 || !strings.Contains(r.err, "not applicable") {
		t.Fatalf("conflicting owned tool: code=%d stderr=%q", r.code, r.err)
	}
	if strings.Contains(r.out, "shared") {
		t.Fatalf("conflicting owned name must still shadow shared: %q", r.out)
	}

	// Direct agent calls have no invocation metadata and remain callable.
	if r := h.ok("call", "--agent", "a", "scoped"); r.out != "owned\n" {
		t.Errorf("direct agent call: %q", r.out)
	}
}

func TestSharedToolsAndShadowing(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "a", "Base.")
	h.ok("base", "other", "Base.")
	say := func(what string) string { return execScript(t, what+".sh", "echo "+what+"\n") }
	// AC14: a shared tool restricted to `other` is not callable by a.
	h.okIn("description: restricted\nexec:\n  argv: ["+say("restricted")+"]\n", "put", "shared", "--lane", "definition", "--kind", "tool", "--name", "x", "--stdin", "--meta", "available-to=other")
	r := h.run("call", "--agent", "a", "x")
	if r.code != 3 || !strings.Contains(r.err, "not available to a") {
		t.Errorf("restricted shared tool for a: code=%d err=%q", r.code, r.err)
	}
	r = h.ok("call", "--agent", "other", "x")
	if r.out != "restricted\n" {
		t.Errorf("restricted shared tool for other: %q", r.out)
	}
	// No --agent and no --context resolves in the shared namespace itself.
	r = h.ok("call", "x")
	if r.out != "restricted\n" {
		t.Errorf("shared default: %q", r.out)
	}
	// A shared tool without available-to is callable by anyone, and it shows
	// under Available tools only for agents that may use it.
	h.okIn("description: open to all\nexec:\n  argv: ["+say("open")+"]\n", "put", "shared", "--lane", "definition", "--kind", "tool", "--name", "y", "--stdin")
	if r := h.ok("call", "--agent", "a", "y"); r.out != "open\n" {
		t.Errorf("open shared tool: %q", r.out)
	}
	r = h.ok("load", "a")
	if strings.Contains(r.out, "- `x`") || !strings.Contains(r.out, "- `y`: open to all") {
		t.Errorf("a's capsule tools:\n%s", r.out)
	}
	r = h.ok("load", "other")
	if !strings.Contains(r.out, "- `x`: restricted") || !strings.Contains(r.out, "- `y`: open to all") {
		t.Errorf("other's capsule tools:\n%s", r.out)
	}
	// AC13/AC14: an agent-owned tool shadows a shared one of the same name
	// for that agent only.
	h.ok("tool", "add", "a", "y", "--script", say("own"), "--description", "a's own y")
	if r := h.ok("call", "--agent", "a", "y"); r.out != "own\n" {
		t.Errorf("agent-owned should shadow shared: %q", r.out)
	}
	if r := h.ok("call", "--agent", "other", "y"); r.out != "open\n" {
		t.Errorf("other agents still see the shared tool: %q", r.out)
	}
	if r := h.ok("call", "y"); r.out != "open\n" {
		t.Errorf("shared namespace itself is unaffected: %q", r.out)
	}
	r = h.ok("load", "a")
	if !strings.Contains(r.out, "- `y`: a's own y") || strings.Contains(r.out, "open to all") {
		t.Errorf("a's capsule should list only its own y:\n%s", r.out)
	}
}

func TestAgentAdd(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "pr-review", "Review changes.")
	r := h.ok("agent", "add", "pr-review", "evidence-reviewer", "--description", "Validate whether a finding is demonstrably supported.")
	id := r.id(t)
	if !strings.HasPrefix(id, "rel_") || strings.Count(r.out, "\n") != 1 {
		t.Fatalf("agent add output %q", r.out)
	}
	h.ok("agent", "add", "pr-review", "comment-editor", "--description", "Rewrite a finding as a concise review comment.", "--meta", "phase=review-comment")
	r = h.ok("load", "pr-review")
	want := "## Available agents\n\n- `comment-editor`: Rewrite a finding as a concise review comment.\n- `evidence-reviewer`: Validate whether a finding is demonstrably supported.\n"
	if !strings.Contains(r.out, want) {
		t.Errorf("capsule:\n%s", r.out)
	}
	r = h.ok("inspect", id)
	m := r.json(t)
	if m["lane"] != "definition" || m["kind"] != "related-agent" || m["name"] != "evidence-reviewer" || m["body"] != "Validate whether a finding is demonstrably supported." {
		t.Errorf("envelope: %v", m)
	}
	// Re-adding supersedes; the capsule shows the new description.
	r = h.ok("agent", "add", "pr-review", "evidence-reviewer", "--description", "Check evidence.")
	if r.id(t) == id {
		t.Errorf("re-add should create a new record")
	}
	r = h.ok("load", "pr-review")
	if !strings.Contains(r.out, "- `evidence-reviewer`: Check evidence.\n") || strings.Contains(r.out, "demonstrably") {
		t.Errorf("capsule after re-add:\n%s", r.out)
	}
	// The agent exists implicitly after agent add.
	h.ok("agent", "add", "fresh", "helper", "--description", "Helps.")
	if r := h.ok("agents"); !strings.Contains(r.out, "fresh\n") {
		t.Errorf("agents: %q", r.out)
	}
	for _, args := range [][]string{
		{"agent", "add", "pr-review", "helper"},
		{"agent", "add", "pr-review", "helper", "--description", ""},
		{"agent", "add", "pr-review", "Bad/Name", "--description", "d"},
		{"agent", "add", "pr-review", "--description", "d"},
		{"agent", "add", "base", "helper", "--description", "d"},
	} {
		if r := h.run(args...); r.code != 2 {
			t.Errorf("%v: want 2, got %d (%s)", args, r.code, r.err)
		}
	}
}

// DESIGN §9: "Argv elements beginning with artifacts/ resolve relative to
// NINE_TAILS_HOME at call time" — any element, so the interpreter-plus-script
// shape [/bin/sh, artifacts/tool_N/hi.sh] runs from any working directory.
func TestCallInterpreterShapeRunsFromAnyCwd(t *testing.T) {
	h := newHarness(t)
	t.Chdir(t.TempDir()) // no artifacts/ here; only NINE_TAILS_HOME has one
	h.ok("base", "a", "Base A.")
	hi := writeScript(t, "hi.sh", "echo \"hi $1\"\n")
	id := h.ok("tool", "add", "a", "hi", "--script", hi, "--description", "hi").id(t)
	body := "description: interp\nexec:\n  argv: [/bin/sh, artifacts/" + id + "/hi.sh, \"{{ x }}\"]\ninput:\n  x: {}\n"
	h.okIn(body, "put", "a", "--lane", "definition", "--kind", "tool", "--name", "interp", "--stdin")
	r := h.ok("call", "--agent", "a", "interp", "--input", `{"x":"there"}`)
	if r.out != "hi there\n" || r.err != "" {
		t.Errorf("interp: out=%q err=%q", r.out, r.err)
	}
	// Caller-supplied values are verbatim, even when they look like artifact paths.
	r = h.ok("call", "--agent", "a", "interp", "--input", `{"x":"artifacts/keep"}`)
	if r.out != "hi artifacts/keep\n" {
		t.Errorf("input value should not be resolved: %q", r.out)
	}
	// The same shape with the artifact in a later position still resolves.
	data := writeScript(t, "data.sh", "true\n")
	id2 := h.ok("tool", "add", "a", "data", "--script", data, "--description", "data").id(t)
	body = "description: two\nexec:\n  argv: [/bin/sh, artifacts/" + id + "/hi.sh, artifacts/" + id2 + "/data.sh]\n"
	h.okIn(body, "put", "a", "--lane", "definition", "--kind", "tool", "--name", "two", "--stdin")
	r = h.ok("call", "--agent", "a", "two")
	if r.out != "hi "+filepath.Join(h.home, "artifacts", id2, "data.sh")+"\n" {
		t.Errorf("later artifacts/ element not resolved: %q", r.out)
	}
	if r := h.ok("load", "a"); !strings.Contains(r.out, "- `interp`: interp\n") {
		t.Errorf("interp should be listed:\n%s", r.out)
	}
}

// DESIGN §9: --input must be a JSON object. Trailing bytes or a second object
// are a quoting mistake and are exit 2; the tool is not run.
func TestCallRejectsTrailingInput(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "a", "Base.")
	args := writeScript(t, "args.sh", "cat\n")
	h.ok("tool", "add", "a", "args", "--script", args, "--description", "cat input")
	bad := []string{`{"v":"ok"} trailing garbage`, `{"v":"ok"}}`, `{"pr":1} {"pr":2}`, "{\"v\":\"ok\"} junk\n", `{"v":"ok"},`}
	for _, in := range bad {
		r := h.run("call", "--agent", "a", "args", "--input", in)
		if r.code != 2 || r.out != "" || !strings.HasPrefix(r.err, "nine-tails: ") || !strings.Contains(r.err, "JSON object") {
			t.Errorf("--input %q: code=%d out=%q err=%q", in, r.code, r.out, r.err)
		}
		r = h.runIn(in, "call", "--agent", "a", "args", "--stdin")
		if r.code != 2 || r.out != "" || !strings.Contains(r.err, "JSON object") {
			t.Errorf("--stdin %q: code=%d out=%q err=%q", in, r.code, r.out, r.err)
		}
	}
	// Exactly one object, with surrounding whitespace, still runs.
	r := h.ok("call", "--agent", "a", "args", "--input", "  {\"v\":\"ok\"} \n")
	if r.out != `{"v":"ok"}` {
		t.Errorf("padded input: %q", r.out)
	}
	r = h.okIn("\n{\"v\":\"ok\"}\n", "call", "--agent", "a", "args", "--stdin")
	if r.out != `{"v":"ok"}` {
		t.Errorf("padded stdin input: %q", r.out)
	}
}

// A non-positive exec.timeout is mechanically unrunnable; validation at
// put / tool add rejects it (exit 2) instead of every call failing with a
// misleading "timed out after -5s".
func TestToolTimeoutMustBePositive(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "a", "Base.")
	for _, tmo := range []string{"-5s", "0s", "0"} {
		body := "description: neg\nexec:\n  argv: [/bin/echo]\n  timeout: " + tmo + "\n"
		r := h.runIn(body, "put", "a", "--lane", "definition", "--kind", "tool", "--name", "neg", "--stdin")
		if r.code != 2 || !strings.Contains(r.err, "exec.timeout") || !strings.Contains(r.err, "positive") {
			t.Errorf("put timeout %s: code=%d err=%q", tmo, r.code, r.err)
		}
	}
	if r := h.run("call", "--agent", "a", "neg"); r.code != 3 {
		t.Errorf("rejected tool must not exist: code=%d err=%q", r.code, r.err)
	}
	if r := h.ok("load", "a"); strings.Contains(r.out, "neg") {
		t.Errorf("rejected tool listed:\n%s", r.out)
	}
	// A positive timeout is accepted and honored.
	script := writeScript(t, "quick.sh", "echo quick\n")
	h.okIn("description: quick\nexec:\n  timeout: 100ms\n", "tool", "add", "a", "quick", "--script", script, "--stdin")
	if r := h.ok("call", "--agent", "a", "quick"); r.out != "quick\n" {
		t.Errorf("quick: %q", r.out)
	}
}

func TestToolBodiesMustBeOneDocument(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "a", "Base.")
	script := writeScript(t, "one.sh", "exit 0\n")
	for _, body := range []string{
		"description: first\nexec: {argv: []}\n---\ndescription: ignored\n",
		"description: first\nexec: {argv: []}\n---\nnot: [valid\n",
	} {
		if r := h.runIn(body, "tool", "add", "a", "one", "--script", script, "--stdin"); r.code != 2 {
			t.Errorf("tool add accepted multiple documents: code=%d stderr=%q", r.code, r.err)
		}
		full := "description: first\nexec: {argv: [/bin/true]}\n---\nignored: true\n"
		if r := h.runIn(full, "put", "a", "--lane", "definition", "--kind", "tool", "--name", "one", "--stdin"); r.code != 2 {
			t.Errorf("generic put accepted multiple tool documents: code=%d stderr=%q", r.code, r.err)
		}
	}
	if rows := h.ok("inspect", "a", "--kind", "tool").json(t)["records"].([]any); len(rows) != 0 {
		t.Fatalf("invalid multi-document tool was stored: %#v", rows)
	}
}
