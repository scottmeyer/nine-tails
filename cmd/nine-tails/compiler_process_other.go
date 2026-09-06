//go:build !unix

package main

import (
	"os"
	"os/exec"
)

func configureCompilerProcess(_ *exec.Cmd)       {}
func terminateCompilerProcess(_ *exec.Cmd) error { return nil }

var compilerSignals = []os.Signal{os.Interrupt}

func forwardCompilerSignal(cmd *exec.Cmd, _ os.Signal) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
func compilerSignalExitCode(_ os.Signal) int { return 130 }
