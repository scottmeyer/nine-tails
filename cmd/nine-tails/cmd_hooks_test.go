package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	harnessadapter "github.com/scottmeyer/nine-tails/internal/harness"
)

func TestInactiveHookDispatchIsSilentBeforeInputConfigAndStore(t *testing.T) {
	h := newHarness(t)
	if err := os.WriteFile(filepath.Join(h.home, "config.yaml"), []byte("not: [valid"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(harnessadapter.EnvRunFile, "")
	t.Setenv(harnessadapter.EnvRunToken, "")
	t.Setenv("NINE_TAILS_HOME", "")
	t.Setenv("NINE_TAILS_NOW", "not-a-clock")
	r := h.runIn("{ definitely not hook json", "hooks", "dispatch", "--claude", "--owner="+harnessadapter.OwnerTag())
	if r.code != 0 || r.out != "" || r.err != "" {
		t.Fatalf("inactive dispatch: code=%d stdout=%q stderr=%q", r.code, r.out, r.err)
	}
	if _, err := os.Stat(filepath.Join(h.home, "nine-tails.db")); !os.IsNotExist(err) {
		t.Fatalf("inactive dispatch touched store: %v", err)
	}
}

func TestInactiveHookDispatchBypassesMainClockParsing(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "untouched-nine-tails-home")
	claudeHome := filepath.Join(root, "untouched-claude-home")
	cmd := exec.Command(os.Args[0], "-test.run=^TestHookDispatchMainHelper$")
	cmd.Env = hookHelperEnvironment(os.Environ(), map[string]string{
		"NINE_TAILS_HOOK_MAIN_HELPER": "1",
		"NINE_TAILS_NOW":              "not-a-clock",
		"NINE_TAILS_HOME":             home,
		"CLAUDE_CONFIG_DIR":           claudeHome,
		"NINE_TAILS_RUN_FILE":         "",
		"NINE_TAILS_RUN_TOKEN":        "",
	})
	cmd.Stdin = strings.NewReader("{ malformed hook input")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("inactive main dispatch: %v, stdout=%q stderr=%q", err, stdout.String(), stderr.String())
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("inactive main dispatch emitted bytes: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	for _, path := range []string{home, claudeHome} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("inactive main dispatch touched %s: %v", path, err)
		}
	}
}

func TestHookDispatchMainHelper(t *testing.T) {
	if os.Getenv("NINE_TAILS_HOOK_MAIN_HELPER") != "1" {
		return
	}
	os.Args = []string{"nine-tails", "hooks", "dispatch", "--claude", "--owner=" + harnessadapter.OwnerTag()}
	main()
}

func hookHelperEnvironment(base []string, replacements map[string]string) []string {
	out := make([]string, 0, len(base)+len(replacements))
	for _, entry := range base {
		key, _, _ := strings.Cut(entry, "=")
		if _, replace := replacements[key]; !replace {
			out = append(out, entry)
		}
	}
	for key, value := range replacements {
		out = append(out, key+"="+value)
	}
	return out
}

func TestActiveDispatchLoadsOnceAndRehydratesCachedCapsule(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "reviewer", "Review carefully.")
	h.ok("note", "reviewer", "--meta", "repo-id=repo-one", "Apply the repo-one rule.")
	h.ok("note", "reviewer", "--meta", "repo-id=repo-two", "Apply the repo-two rule.")
	run, err := harnessadapter.BeginRun(h.home, "reviewer", harnessadapter.Codex,
		harnessadapter.Metadata{"repo-id": {"repo-one"}, "phase": {"review", "comment"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = run.Close() })
	setRunEnvironment(t, run)
	owner := "--owner=" + harnessadapter.OwnerTag()

	r := h.runIn(`{"hook_event_name":"SessionStart","session_id":"root","source":"startup"}`, "hooks", "dispatch", "--codex", owner)
	if r.code != 0 || r.out != "" || r.err != "" {
		t.Fatalf("session start: %#v", r)
	}
	r = h.runIn(`{"hook_event_name":"UserPromptSubmit","session_id":"root","prompt":"review change 17"}`, "hooks", "dispatch", "--codex", owner)
	if r.code != 0 || r.err != "" {
		t.Fatalf("first prompt: %#v", r)
	}
	firstContext, firstCapsule := decodeHookContext(t, r.out, "UserPromptSubmit")
	if !strings.Contains(firstCapsule, "Review carefully.") {
		t.Fatalf("capsule missing base:\n%s", firstCapsule)
	}
	if !strings.Contains(firstCapsule, "Apply the repo-one rule.") || strings.Contains(firstCapsule, "Apply the repo-two rule.") {
		t.Fatalf("activation metadata did not filter guidance:\n%s", firstCapsule)
	}
	inspected := h.ok("inspect", firstContext, "--format", "json")
	if !strings.Contains(inspected.out, `"task": "review change 17"`) {
		t.Fatalf("real prompt was not receipt task: %s", inspected.out)
	}
	assertReceiptMetadata(t, inspected, "repo-id", []string{"repo-one"})
	assertReceiptMetadata(t, inspected, "phase", []string{"review", "comment"})

	r = h.runIn(`{"hook_event_name":"UserPromptSubmit","session_id":"root","prompt":"follow up"}`, "hooks", "dispatch", "--codex", owner)
	if r.code != 0 || r.out != "" || r.err != "" {
		t.Fatalf("later same-episode prompt was not silent: %#v", r)
	}
	r = h.runIn(`{"hook_event_name":"SessionStart","session_id":"root","source":"compact"}`, "hooks", "dispatch", "--codex", owner)
	_, compactCapsule := decodeHookContext(t, r.out, "SessionStart")
	if compactCapsule != firstCapsule {
		t.Fatal("compaction did not rehydrate the exact cached capsule")
	}

	r = h.runIn(`{"hook_event_name":"SessionStart","session_id":"root","source":"clear"}`, "hooks", "dispatch", "--codex", owner)
	if r.code != 0 || r.out != "" {
		t.Fatalf("clear emitted context: %#v", r)
	}
	r = h.runIn(`{"hook_event_name":"UserPromptSubmit","session_id":"root","prompt":"new episode task"}`, "hooks", "dispatch", "--codex", owner)
	newContext, _ := decodeHookContext(t, r.out, "UserPromptSubmit")
	if newContext == firstContext {
		t.Fatal("new episode reused old receipt")
	}
	inspected = h.ok("inspect", newContext, "--format", "json")
	if !strings.Contains(inspected.out, `"task": "new episode task"`) || !strings.Contains(inspected.out, `"parent_context": "`+firstContext+`"`) {
		t.Fatalf("new episode receipt did not chain task/parent: %s", inspected.out)
	}
	assertReceiptMetadata(t, inspected, "repo-id", []string{"repo-one"})
}

func TestActiveDispatchAppliesClockOnlyAfterAdmission(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "reviewer", "Review carefully.")
	run, err := harnessadapter.BeginRun(h.home, "reviewer", harnessadapter.Claude, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = run.Close() })
	setRunEnvironment(t, run)
	owner := "--owner=" + harnessadapter.OwnerTag()
	h.runIn(`{"hook_event_name":"SessionStart","session_id":"root","source":"startup"}`, "hooks", "dispatch", "--claude", owner)
	t.Setenv("NINE_TAILS_NOW", "bad")
	r := h.runIn(`{"hook_event_name":"UserPromptSubmit","session_id":"root","prompt":"task"}`, "hooks", "dispatch", "--claude", owner)
	if r.code != 5 || r.out != "" || !strings.Contains(r.err, "NINE_TAILS_NOW") {
		t.Fatalf("active bad clock: %#v", r)
	}
}

func TestHooksInstallUsesHarnessConfigOverridesOnly(t *testing.T) {
	h := newHarness(t)
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	r := h.run("hooks", "install", "--codex")
	want := filepath.Join(codexHome, "hooks.json") + "\n"
	if r.code != 0 || r.out != want || !strings.Contains(r.err, "review and trust") {
		t.Fatalf("install: %#v", r)
	}
	if _, err := os.Stat(filepath.Join(h.home, "nine-tails.db")); !os.IsNotExist(err) {
		t.Fatalf("install opened nine-tails store: %v", err)
	}
	r = h.run("hooks", "uninstall", "--codex")
	if r.code != 0 || r.out != want || r.err != "" {
		t.Fatalf("uninstall: %#v", r)
	}

	claudeHome := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claudeHome)
	r = h.run("hooks", "install", "--claude")
	if r.code != 0 || r.out != filepath.Join(claudeHome, "settings.json")+"\n" || r.err != "" {
		t.Fatalf("Claude install: %#v", r)
	}
}

func TestHooksRunOwnsChildLifetimeAndCleansCapability(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture executable is a POSIX script")
	}
	h := newHarness(t)
	h.ok("base", "reviewer", "Review carefully.")
	bin := t.TempDir()
	script := filepath.Join(bin, "codex")
	body := `#!/bin/sh
test -n "$NINE_TAILS_RUN_TOKEN" || exit 91
test -f "$NINE_TAILS_RUN_FILE" || exit 92
grep -Fq '"repo-id":["my_repo"]' "$NINE_TAILS_RUN_FILE" || exit 93
grep -Fq '"phase":["review","comment"]' "$NINE_TAILS_RUN_FILE" || exit 94
printf 'runfile:%s\n' "$NINE_TAILS_RUN_FILE"
printf 'home:%s\n' "$NINE_TAILS_HOME"
printf 'args:%s\n' "$*"
`
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv(harnessadapter.EnvRunFile, "/forged")
	t.Setenv(harnessadapter.EnvRunToken, "forged")
	r := h.run("hooks", "run", "reviewer", "--codex", "--meta", "repo-id=my_repo", "--meta", "phase=review", "--meta", "phase=comment", "--", "--model", "test-model")
	if r.code != 0 || r.err != "" {
		t.Fatalf("run: %#v", r)
	}
	lines := strings.Split(strings.TrimSpace(r.out), "\n")
	if len(lines) != 3 || lines[1] != "home:"+h.home || lines[2] != "args:--model test-model" {
		t.Fatalf("child output=%q", r.out)
	}
	path := strings.TrimPrefix(lines[0], "runfile:")
	if path == "/forged" {
		t.Fatal("inherited forged capability was not replaced")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("run capability survived child exit: %v", err)
	}
}

func TestHooksRunRejectsMetadataBeforeOpeningStore(t *testing.T) {
	h := newHarness(t)
	r := h.run("hooks", "run", "reviewer", "--codex", "--meta", "missing-equals")
	if r.code != 2 || !strings.Contains(r.err, "key=value") {
		t.Fatalf("invalid metadata result=%#v", r)
	}
	for _, path := range []string{filepath.Join(h.home, "nine-tails.db"), filepath.Join(h.home, "runtime")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("invalid metadata touched %s: %v", path, err)
		}
	}

	r = h.run("hooks", "run", "reviewer", "--codex", "--meta", "scope="+strings.Repeat("\x01", 22_000))
	if r.code != 2 || !strings.Contains(r.err, "activation metadata exceeds 131072 encoded bytes") {
		t.Fatalf("oversized metadata result=%#v", r)
	}
	for _, path := range []string{filepath.Join(h.home, "nine-tails.db"), filepath.Join(h.home, "runtime")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("oversized metadata touched %s: %v", path, err)
		}
	}
}

func TestHooksRunRequiresEveryHarnessArgumentAfterDash(t *testing.T) {
	h := newHarness(t)
	for _, args := range [][]string{
		{"hooks", "run", "reviewer", "stray", "--codex", "--", "--model", "test-model"},
		{"hooks", "run", "--codex", "--", "reviewer"},
	} {
		r := h.run(args...)
		if r.code != 2 || !strings.Contains(r.err, "put harness arguments after --") {
			t.Fatalf("invalid passthrough arguments %v result=%#v", args, r)
		}
	}
	for _, path := range []string{filepath.Join(h.home, "nine-tails.db"), filepath.Join(h.home, "runtime")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("invalid passthrough arguments touched %s: %v", path, err)
		}
	}
}

func setRunEnvironment(t *testing.T, run *harnessadapter.Run) {
	t.Helper()
	for _, entry := range run.Environment(nil) {
		key, value, _ := strings.Cut(entry, "=")
		t.Setenv(key, value)
	}
}

func decodeHookContext(t *testing.T, raw, wantEvent string) (string, string) {
	t.Helper()
	var out struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("hook output is not JSON: %v\n%s", err, raw)
	}
	if out.HookSpecificOutput.HookEventName != wantEvent {
		t.Fatalf("event=%q, want %q", out.HookSpecificOutput.HookEventName, wantEvent)
	}
	return contextID(t, out.HookSpecificOutput.AdditionalContext), out.HookSpecificOutput.AdditionalContext
}

func assertReceiptMetadata(t *testing.T, inspected result, key string, want []string) {
	t.Helper()
	receipt := inspected.json(t)
	metadata, ok := receipt["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("receipt metadata=%#v", receipt["metadata"])
	}
	values, ok := metadata[key].([]any)
	if !ok || len(values) != len(want) {
		t.Fatalf("receipt metadata[%q]=%#v, want %#v", key, metadata[key], want)
	}
	for i := range want {
		if values[i] != want[i] {
			t.Fatalf("receipt metadata[%q]=%#v, want %#v", key, values, want)
		}
	}
}

func TestWriteHookContextUsesHarnessCamelCaseSchema(t *testing.T) {
	var out strings.Builder
	adapter, _ := harnessadapter.For(harnessadapter.Codex)
	if err := adapter.EncodeContext(&out, "UserPromptSubmit", "capsule"); err != nil {
		t.Fatal(err)
	}
	want := `{"hookSpecificOutput":{"hookEventName":"UserPromptSubmit","additionalContext":"capsule"}}` + "\n"
	if out.String() != want {
		t.Fatalf("got %q, want %q", out.String(), want)
	}
}

func TestHooksRequireExactlyOneHarness(t *testing.T) {
	h := newHarness(t)
	for _, args := range [][]string{{"hooks", "install"}, {"hooks", "uninstall", "--claude", "--codex"}, {"hooks", "run", "a"}} {
		r := h.run(args...)
		if r.code != 2 || !strings.Contains(r.err, "exactly one") {
			t.Fatalf("%v: %#v", args, r)
		}
	}
}
