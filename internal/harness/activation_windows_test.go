//go:build windows

package harness

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestWindowsBeginRunProbeAndLifecycle(t *testing.T) {
	run, err := BeginRun(t.TempDir(), "reviewer", Codex, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer run.Close()
	for _, entry := range run.Environment(nil) {
		key, value, _ := strings.Cut(entry, "=")
		t.Setenv(key, value)
	}
	capability, ok := Probe(Codex)
	if !ok {
		t.Fatal("Windows run did not pass capability probe")
	}
	if decision, err := capability.Admit(Event{Name: "SessionStart", SessionID: "root", Source: "startup"}); err != nil || !decision.Active {
		t.Fatalf("SessionStart decision=%#v err=%v", decision, err)
	}
	decision, err := capability.Admit(Event{Name: "UserPromptSubmit", SessionID: "root", Prompt: "task"})
	if err != nil || !decision.Load || decision.Claim == "" {
		t.Fatalf("prompt decision=%#v err=%v", decision, err)
	}
	if committed, err := capability.CommitContext("root", decision.Claim, "ctx_1", "capsule"); err != nil || !committed {
		t.Fatalf("commit=%v err=%v", committed, err)
	}
}

func TestWindowsExitedProcessWithStillActiveCodeIsNotAlive(t *testing.T) {
	if os.Getenv("NINE_TAILS_EXIT_259_HELPER") == "1" {
		os.Exit(259)
	}
	command := exec.Command(os.Args[0], "-test.run=^TestWindowsExitedProcessWithStillActiveCodeIsNotAlive$")
	command.Env = append(os.Environ(), "NINE_TAILS_EXIT_259_HELPER=1")
	if err := command.Run(); err == nil {
		t.Fatal("exit-259 helper unexpectedly succeeded")
	}
	if processAlive(command.Process.Pid) {
		t.Fatal("exited process with code 259 reported alive")
	}
}
