//go:build unix

package main

import (
	"os"
	"syscall"
)

func supervisorSignals() []os.Signal {
	return []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT}
}

func forwardSupervisorSignal(sig os.Signal) bool {
	// Foreground terminal INT and QUIT already reach the child process group.
	// Keeping them out of the parent preserves cleanup without double delivery.
	return sig != os.Interrupt && sig != syscall.SIGQUIT
}
