//go:build unix

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	harnessadapter "github.com/scottmeyer/nine-tails/internal/harness"
)

func TestHooksRunSurvivesForegroundInterruptAndCleansCapability(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "reviewer", "Review carefully.")
	installTestHarnessAdapter(t, harnessadapter.Codex)
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	interrupted := filepath.Join(dir, "interrupted")
	release := filepath.Join(dir, "release")
	done := filepath.Join(dir, "done")
	runPath := filepath.Join(dir, "run-path")
	fixture := filepath.Join(dir, "codex")
	body := `#!/bin/sh
trap 'printf interrupted > "$NT_INT_MARKER"' INT
printf '%s' "$NINE_TAILS_RUN_FILE" > "$NT_RUN_PATH"
printf ready > "$NT_READY"
while [ ! -f "$NT_RELEASE" ]; do sleep 0.05; done
test -f "$NINE_TAILS_RUN_FILE" || exit 93
printf done > "$NT_DONE"
`
	if err := os.WriteFile(fixture, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestHooksRunSignalMainHelper$")
	cmd.Env = hookHelperEnvironment(os.Environ(), map[string]string{
		"NINE_TAILS_HOOK_SIGNAL_HELPER": "1",
		"NINE_TAILS_HOME":               h.home,
		"PATH":                          dir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"NT_READY":                      ready,
		"NT_INT_MARKER":                 interrupted,
		"NT_RELEASE":                    release,
		"NT_DONE":                       done,
		"NT_RUN_PATH":                   runPath,
	})
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	finished := false
	t.Cleanup(func() {
		if !finished {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			_ = cmd.Wait()
		}
	})
	waitForFile(t, ready)
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	waitForFile(t, interrupted)
	if err := cmd.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("wrapper died on foreground interrupt: %v", err)
	}
	pathBytes, err := os.ReadFile(runPath)
	if err != nil {
		t.Fatal(err)
	}
	capabilityPath := strings.TrimSpace(string(pathBytes))
	if _, err := os.Stat(capabilityPath); err != nil {
		t.Fatalf("capability disappeared while harness remained live: %v", err)
	}
	if err := os.WriteFile(release, []byte("release"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitErr := cmd.Wait()
	finished = true
	if waitErr != nil {
		t.Fatalf("wrapper exit: %v", waitErr)
	}
	if _, err := os.Stat(done); err != nil {
		t.Fatalf("harness did not continue after interrupt: %v", err)
	}
	if _, err := os.Stat(capabilityPath); !os.IsNotExist(err) {
		t.Fatalf("capability survived harness exit: %v", err)
	}
}

func TestHooksRunReportsShellConventionalSignalStatus(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "reviewer", "Review carefully.")
	installTestHarnessAdapter(t, harnessadapter.Codex)
	dir := t.TempDir()
	fixture := filepath.Join(dir, "codex")
	if err := os.WriteFile(fixture, []byte("#!/bin/sh\nkill -TERM $$\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	r := h.run("hooks", "run", "reviewer", "--codex")
	if r.code != 128+int(syscall.SIGTERM) || !strings.Contains(r.err, "terminated by signal") || strings.Contains(r.err, "status -1") {
		t.Fatalf("signaled child result=%#v", r)
	}
}

func TestHooksRunSignalMainHelper(t *testing.T) {
	if os.Getenv("NINE_TAILS_HOOK_SIGNAL_HELPER") != "1" {
		return
	}
	os.Args = []string{"nine-tails", "hooks", "run", "reviewer", "--codex"}
	main()
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}
