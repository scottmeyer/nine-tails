//go:build unix

package main

import (
	"fmt"
	"os/exec"
	"syscall"
)

func describeHarnessExit(exit *exec.ExitError) (int, string) {
	if status, ok := exit.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		sig := status.Signal()
		return 128 + int(sig), fmt.Sprintf("terminated by signal %s", sig)
	}
	code := exit.ExitCode()
	if code < 1 {
		code = 1
	}
	return code, fmt.Sprintf("exited with status %d", exit.ExitCode())
}
