//go:build unix

package main

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func configureCompilerProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return terminateCompilerProcess(cmd) }
}

func terminateCompilerProcess(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return os.ErrProcessDone
	}
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	return err
}

var compilerSignals = []os.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP}

func forwardCompilerSignal(cmd *exec.Cmd, sig os.Signal) {
	s, ok := sig.(syscall.Signal)
	if !ok {
		s = syscall.SIGTERM
	}
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, s)
	}
}

func compilerSignalExitCode(sig os.Signal) int {
	if s, ok := sig.(syscall.Signal); ok {
		return 128 + int(s)
	}
	return 130
}
