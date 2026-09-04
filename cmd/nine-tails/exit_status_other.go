//go:build !unix

package main

import (
	"fmt"
	"os/exec"
)

func describeHarnessExit(exit *exec.ExitError) (int, string) {
	code := exit.ExitCode()
	if code < 1 {
		code = 1
	}
	return code, fmt.Sprintf("exited with status %d", exit.ExitCode())
}
