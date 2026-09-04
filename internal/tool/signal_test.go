//go:build unix

package tool

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A SIGINT delivered to nine-tails (what Ctrl-C does, since the tool runs in
// its own process group) must end the tool too, and Run must say so.
func TestRunForwardsInterruptToToolGroup(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "survived")
	d, err := Parse("description: slow\nexec:\n  argv: [/bin/sh, -c, 'sleep 0.6; touch \"$NINE_TAILS_TEST_MARKER\"']\n")
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	errc := make(chan error, 1)
	start := time.Now()
	go func() {
		errc <- d.Run(Call{Env: map[string]string{"NINE_TAILS_TEST_MARKER": marker}, Stdout: &stdout, Stderr: &stderr})
	}()
	time.Sleep(150 * time.Millisecond)
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-errc:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after SIGINT")
	}
	var interrupted *Interrupted
	if !errors.As(err, &interrupted) {
		t.Fatalf("Run returned %v, want *Interrupted", err)
	}
	if interrupted.Signal != syscall.SIGINT || interrupted.ExitCode() != 130 {
		t.Fatalf("interrupted by %v with exit %d, want SIGINT/130", interrupted.Signal, interrupted.ExitCode())
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Run took %s to return after SIGINT", elapsed)
	}
	time.Sleep(700 * time.Millisecond)
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("tool survived the forwarded SIGINT; marker stat: %v", err)
	}
}
