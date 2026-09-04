//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsHarnessCommandRejectsBatchShim(t *testing.T) {
	binDir := filepath.Join(t.TempDir(), "bin with spaces")
	if err := os.Mkdir(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	shim := filepath.Join(binDir, "codex.cmd")
	if err := os.WriteFile(shim, []byte("@exit /b 37\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PATHEXT", ".COM;.EXE;.BAT;.CMD")
	command, err := harnessCommand("codex", []string{"hostile&argument", "", `%PATH%`})
	if command != nil || err == nil || !strings.Contains(err.Error(), "native codex.exe") {
		t.Fatalf("command=%#v err=%v", command, err)
	}
}

func TestWindowsHarnessCommandExecutesEXEDirectly(t *testing.T) {
	command, err := harnessCommand(os.Args[0], []string{"-test.run=^$"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.EqualFold(filepath.Ext(command.Path), ".cmd") || strings.EqualFold(filepath.Ext(command.Path), ".bat") {
		t.Fatalf("exe unexpectedly resolved as batch file: %q", command.Path)
	}
}
