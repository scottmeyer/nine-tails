//go:build unix

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/scottmeyer/nine-tails/internal/cli"
)

func waitForCompilerFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("compiler child did not record its pid")
}

func requireCompilerMarkerAbsent(t *testing.T, marker string) {
	t.Helper()
	time.Sleep(3100 * time.Millisecond)
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("compiler descendant survived; marker stat: %v", err)
	}
}

func TestRunCompilerCleansDescendantHoldingPipesAfterSuccess(t *testing.T) {
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	marker := filepath.Join(dir, "descendant-finished")
	script := filepath.Join(dir, "compiler.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n(sleep 3; touch \"$2\") &\ntouch \"$1\"\nprintf 'items: []\\nentries: []\\ninput_entries: []\\n'\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	a := &app{home: dir}
	started := time.Now()
	out, _, err := a.runCompiler([]string{script, ready, marker}, nil, 5*time.Second, "a")
	if err != nil || !strings.Contains(string(out), "items: []") {
		t.Fatalf("runCompiler output=%q err=%v", out, err)
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("inherited pipe delayed successful compiler for %s", elapsed)
	}
	waitForCompilerFile(t, ready)
	requireCompilerMarkerAbsent(t, marker)
}

func TestRunCompilerTimeoutKillsDescendantsAndReportsTimeout(t *testing.T) {
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	marker := filepath.Join(dir, "descendant-finished")
	script := filepath.Join(dir, "compiler.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n(sleep 3; touch \"$2\") &\ntouch \"$1\"\nwait\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	a := &app{home: dir}
	_, _, err := a.runCompiler([]string{script, ready, marker}, nil, 100*time.Millisecond, "a")
	if err == nil || cli.CodeOf(err) != cli.ExitTool || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("timeout error=%v code=%d", err, cli.CodeOf(err))
	}
	waitForCompilerFile(t, ready)
	requireCompilerMarkerAbsent(t, marker)
}

func TestRunCompilerPreservesNonzeroStatusWhileCleaningDescendants(t *testing.T) {
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	marker := filepath.Join(dir, "descendant-finished")
	script := filepath.Join(dir, "compiler.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n(sleep 3; touch \"$2\") &\ntouch \"$1\"\necho detail >&2\nexit 23\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	a := &app{home: dir}
	_, diagnostics, err := a.runCompiler([]string{script, ready, marker}, nil, 5*time.Second, "a")
	if err == nil || cli.CodeOf(err) != cli.ExitTool || !strings.Contains(err.Error(), "exited with status 23") || string(diagnostics) != "detail\n" {
		t.Fatalf("nonzero error=%v code=%d diagnostics=%q", err, cli.CodeOf(err), diagnostics)
	}
	waitForCompilerFile(t, ready)
	requireCompilerMarkerAbsent(t, marker)
}

func TestRunCompilerForwardsParentInterruptToProcessGroup(t *testing.T) {
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	marker := filepath.Join(dir, "descendant-finished")
	script := filepath.Join(dir, "compiler.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n(sleep 3; touch \"$2\") &\ntouch \"$1\"\nwait\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	a := &app{home: dir}
	errCh := make(chan error, 1)
	go func() {
		_, _, err := a.runCompiler([]string{script, ready, marker}, nil, 5*time.Second, "a")
		errCh <- err
	}()
	waitForCompilerFile(t, ready)
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	var err error
	select {
	case err = <-errCh:
	case <-time.After(3 * time.Second):
		t.Fatal("compiler did not return after interrupt")
	}
	if err == nil || cli.CodeOf(err) != 130 || !strings.Contains(err.Error(), "interrupted") {
		t.Fatalf("interrupt error=%v code=%d", err, cli.CodeOf(err))
	}
	requireCompilerMarkerAbsent(t, marker)
}
