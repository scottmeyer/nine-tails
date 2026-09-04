//go:build windows

package main

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// harnessCommand rejects batch shims because Windows PowerShell 5.1 cannot
// preserve arbitrary argv when it remarshals them through cmd.exe. A native
// executable has well-defined CreateProcess argument handling.
func harnessCommand(name string, args []string) (*exec.Cmd, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return nil, err
	}
	if ext := strings.ToLower(filepath.Ext(path)); ext == ".cmd" || ext == ".bat" {
		return nil, fmt.Errorf("resolved %s to batch shim %s; install a native %s.exe harness executable", name, path, name)
	}
	return exec.Command(path, args...), nil
}
