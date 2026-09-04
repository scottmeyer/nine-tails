package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	harnessadapter "github.com/scottmeyer/nine-tails/internal/harness"
)

func installTestHarnessAdapter(t *testing.T, name harnessadapter.Name) string {
	t.Helper()
	dir := t.TempDir()
	switch name {
	case harnessadapter.Claude:
		t.Setenv("CLAUDE_CONFIG_DIR", dir)
	case harnessadapter.Codex:
		t.Setenv("CODEX_HOME", dir)
	default:
		t.Fatalf("unsupported test harness %q", name)
	}
	adapter, err := harnessadapter.For(name)
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	path, changed, err := harnessadapter.Install(adapter, executable)
	if err != nil || !changed {
		t.Fatalf("install test adapter: path=%q changed=%v err=%v", path, changed, err)
	}
	return path
}

func TestHooksRunRefusesStaleOwnedAdapterBeforeLaunch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture executable is a POSIX script")
	}
	h := newHarness(t)
	h.ok("base", "reviewer", "Review carefully.")
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	adapter, err := harnessadapter.For(harnessadapter.Codex)
	if err != nil {
		t.Fatal(err)
	}
	settingsPath, _, err := harnessadapter.Install(adapter, filepath.Join(codexHome, "moved-nine-tails"))
	if err != nil {
		t.Fatal(err)
	}

	bin := t.TempDir()
	launched := filepath.Join(bin, "launched")
	script := filepath.Join(bin, "codex")
	body := "#!/bin/sh\nprintf launched > \"$NT_LAUNCHED\"\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("NT_LAUNCHED", launched)

	r := h.run("hooks", "run", "reviewer", "--codex")
	if r.code != 5 || r.out != "" || !strings.Contains(r.err, settingsPath) ||
		!strings.Contains(r.err, "nine-tails hooks install --codex") || !strings.Contains(r.err, "install or reinstall") {
		t.Fatalf("stale adapter result=%#v", r)
	}
	if _, err := os.Stat(launched); !os.IsNotExist(err) {
		t.Fatalf("harness launched through stale adapter: %v", err)
	}
}

func TestHooksRunRefusesCanonicalHandlerUnderRestrictiveMatcher(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture executable is a POSIX script")
	}
	h := newHarness(t)
	h.ok("base", "reviewer", "Review carefully.")
	settingsPath := installTestHarnessAdapter(t, harnessadapter.Claude)
	b, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	var settings map[string]any
	if err := json.Unmarshal(b, &settings); err != nil {
		t.Fatal(err)
	}
	hooks, ok := settings["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("hooks=%#v", settings["hooks"])
	}
	groups, ok := hooks["SessionStart"].([]any)
	if !ok || len(groups) != 1 {
		t.Fatalf("SessionStart groups=%#v", hooks["SessionStart"])
	}
	group, ok := groups[0].(map[string]any)
	if !ok {
		t.Fatalf("SessionStart group=%#v", groups[0])
	}
	group["matcher"] = "resume"
	b, err = json.MarshalIndent(settings, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath, append(b, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	bin := t.TempDir()
	launched := filepath.Join(bin, "launched")
	script := filepath.Join(bin, "claude")
	body := "#!/bin/sh\nprintf launched > \"$NT_LAUNCHED\"\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("NT_LAUNCHED", launched)

	r := h.run("hooks", "run", "reviewer", "--claude")
	if r.code != 5 || r.out != "" || !strings.Contains(r.err, "nine-tails hooks install --claude") ||
		!strings.Contains(r.err, "install or reinstall") {
		t.Fatalf("restrictive matcher result=%#v", r)
	}
	if _, err := os.Stat(launched); !os.IsNotExist(err) {
		t.Fatalf("harness launched through a filtered binding hook: %v", err)
	}
}

func TestHooksRunRefusesSilentFalseConfidenceWithoutOwnedAdapter(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture executable is a POSIX script")
	}
	h := newHarness(t)
	h.ok("base", "reviewer", "Review carefully.")
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	settingsPath := filepath.Join(codexHome, "hooks.json")
	if err := os.WriteFile(settingsPath, []byte(`{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"someone-elses-hook"}]}],"UserPromptSubmit":[],"SessionEnd":[]}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	bin := t.TempDir()
	launched := filepath.Join(bin, "launched")
	script := filepath.Join(bin, "codex")
	body := "#!/bin/sh\nprintf launched > \"$NT_LAUNCHED\"\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("NT_LAUNCHED", launched)

	r := h.run("hooks", "run", "reviewer", "--codex")
	if r.code != 5 || r.out != "" || !strings.Contains(r.err, "nine-tails-owned codex hook adapter is not installed") ||
		!strings.Contains(r.err, settingsPath) || !strings.Contains(r.err, "nine-tails hooks install --codex") {
		t.Fatalf("missing adapter result=%#v", r)
	}
	if _, err := os.Stat(launched); !os.IsNotExist(err) {
		t.Fatalf("harness launched without the owned adapter: %v", err)
	}
}

func TestHooksRunPilotSeedsAndSuppliesAuthoritativeHarnessMetadata(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture executable is a POSIX script")
	}
	h := newHarness(t)
	installTestHarnessAdapter(t, harnessadapter.Codex)
	bin := t.TempDir()
	capture := filepath.Join(bin, "run.json")
	script := filepath.Join(bin, "codex")
	body := "#!/bin/sh\ncp \"$NINE_TAILS_RUN_FILE\" \"$NT_RUN_CAPTURE\"\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("NT_RUN_CAPTURE", capture)

	r := h.run("hooks", "run", "pilot", "--codex", "--meta", "repo-id=nine-tails")
	if r.code != 0 || r.out != "" || r.err != "nine-tails: seeded pilot and reflector from the built-in starter (ordinary agents; edit with nine-tails base <agent>)\n" {
		t.Fatalf("fresh pilot run=%#v", r)
	}
	assertRunCaptureMetadata(t, capture, map[string][]string{
		"harness": {"codex"},
		"repo-id": {"nine-tails"},
	})
	if agents := h.ok("agents").out; agents != "pilot\nreflector\n" {
		t.Fatalf("seeded agents=%q", agents)
	}

	r = h.run("hooks", "run", "pilot", "--codex", "--meta", "harness=codex")
	if r.code != 0 || r.err != "" {
		t.Fatalf("identical explicit harness metadata=%#v", r)
	}
	assertRunCaptureMetadata(t, capture, map[string][]string{"harness": {"codex"}})

	if err := os.Remove(capture); err != nil {
		t.Fatal(err)
	}
	r = h.run("hooks", "run", "pilot", "--codex", "--meta", "harness=claude")
	if r.code != 2 || r.out != "" || !strings.Contains(r.err, "harness=claude conflicts with selected --codex") {
		t.Fatalf("conflicting harness metadata=%#v", r)
	}
	if _, err := os.Stat(capture); !os.IsNotExist(err) {
		t.Fatalf("conflicting metadata launched harness: %v", err)
	}
}

func assertRunCaptureMetadata(t *testing.T, path string, want map[string][]string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var state struct {
		Agent    string              `json:"agent"`
		Harness  harnessadapter.Name `json:"harness"`
		Metadata map[string][]string `json:"metadata"`
	}
	if err := json.Unmarshal(b, &state); err != nil {
		t.Fatalf("decode run capture: %v\n%s", err, b)
	}
	if state.Agent != "pilot" || state.Harness != harnessadapter.Codex {
		t.Fatalf("run identity: agent=%q harness=%q", state.Agent, state.Harness)
	}
	for key, values := range want {
		got := state.Metadata[key]
		if len(got) != len(values) {
			t.Fatalf("metadata[%q]=%#v, want %#v", key, got, values)
		}
		for i := range values {
			if got[i] != values[i] {
				t.Fatalf("metadata[%q]=%#v, want %#v", key, got, values)
			}
		}
	}
}

func TestHooksInstallAndRunHelpExplainTheActivationContract(t *testing.T) {
	h := newHarness(t)
	install := h.run("hooks", "install", "--help")
	if install.code != 0 || install.err != "" {
		t.Fatalf("install help=%#v", install)
	}
	for _, want := range []string{
		"prerequisite", "global but inactive gate", "successfully and silently",
		"first real prompt", "--codex", "/hooks",
		"nine-tails hooks install --claude", "nine-tails hooks install --codex",
	} {
		if !strings.Contains(install.out, want) {
			t.Errorf("install help missing %q:\n%s", want, install.out)
		}
	}

	run := h.run("hooks", "run", "--help")
	if run.code != 0 || run.err != "" {
		t.Fatalf("run help=%#v", run)
	}
	for _, want := range []string{
		"hooks install --claude", "PATH", "current directory",
		"stdin, stdout, and stderr", "after --", "forwarded unchanged",
		"exit status", "128+signal", "first real prompt", "harness=claude",
		"fresh pilot store", "inactive", "/hooks",
		"persisted as the receipt task", "first prompt containing secrets or raw external content",
		"manual load with a concise purpose",
		"nine-tails hooks run pilot --claude --meta repo-id=my-project",
		"nine-tails hooks run pr-review --codex --meta repo-id=my-project -- --model MODEL",
	} {
		if !strings.Contains(run.out, want) {
			t.Errorf("run help missing %q:\n%s", want, run.out)
		}
	}
}
