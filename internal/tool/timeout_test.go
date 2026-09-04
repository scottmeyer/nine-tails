//go:build unix

package tool

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunSuccessfulParentDoesNotFailOnInheritedStderr(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "descendant-finished")
	d, err := Parse("description: background child\nexec:\n  argv: [/bin/sh, -c, '(sleep 0.5; touch \"$NINE_TAILS_TEST_MARKER\") >&2 & printf direct-success\\\\n']\n")
	if err != nil {
		t.Fatal(err)
	}

	// A non-file writer makes os/exec copy stderr through a pipe. The direct
	// shell exits zero while its background child keeps that pipe open.
	var stdout, stderr bytes.Buffer
	start := time.Now()
	err = d.Run(Call{
		Env:    map[string]string{"NINE_TAILS_TEST_MARKER": marker},
		Stdout: &stdout,
		Stderr: &stderr,
	})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("successful direct executable returned %v", err)
	}
	if elapsed >= 400*time.Millisecond {
		t.Fatalf("successful direct executable returned after %s; descendant held stderr open", elapsed)
	}
	if stdout.String() != "direct-success\n" || stderr.Len() != 0 {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}

	// ErrWaitDelay means a descendant remained in the process group. Run's
	// best-effort cleanup should stop it before it can perform later work.
	time.Sleep(550 * time.Millisecond)
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("background descendant was not cleaned up; marker stat: %v", err)
	}
}

func TestRunTimeoutDoesNotWaitForShellDescendant(t *testing.T) {
	for _, shellCommand := range []string{"sleep 1", "sleep 1 & wait"} {
		t.Run(shellCommand, func(t *testing.T) {
			d, err := Parse("description: slow child\nexec:\n  argv: [/bin/sh, -c, '" + shellCommand + "']\n  timeout: 100ms\n")
			if err != nil {
				t.Fatal(err)
			}

			// Non-file writers make os/exec use copy pipes. The sleep process
			// inherits their write ends on shells that fork for the command; the
			// background form forces that shape on shells that optimize the simple
			// command into exec.
			var stdout, stderr bytes.Buffer
			start := time.Now()
			err = d.Run(Call{Stdout: &stdout, Stderr: &stderr})
			elapsed := time.Since(start)

			if elapsed >= 750*time.Millisecond {
				t.Fatalf("100ms timeout returned after %s; shell descendant kept Run blocked", elapsed)
			}
			if !errors.Is(err, ErrStart) {
				t.Fatalf("timeout should wrap ErrStart, got %v", err)
			}
			if !strings.Contains(err.Error(), "timed out after 100ms") {
				t.Errorf("timeout error = %q, want configured duration", err)
			}
		})
	}
}
