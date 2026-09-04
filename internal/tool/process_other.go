//go:build !unix

package tool

import (
	"os"
	"os/exec"
)

// forwardedSignals: os.Interrupt is the only portable termination signal.
var forwardedSignals = []os.Signal{os.Interrupt}

// CommandContext's default cancellation kills the direct process on platforms
// without Unix process groups. Cmd.WaitDelay still bounds inherited I/O held
// open by its descendants.
func configureProcessTreeCancellation(_ *exec.Cmd) {}

// Descendant cleanup is not available without Unix process groups. WaitDelay
// still closes inherited I/O pipes so a successful direct process can return.
func terminateProcessTree(_ *exec.Cmd) error { return nil }

// forwardSignal ends the direct process; descendants are not tracked here.
func forwardSignal(cmd *exec.Cmd, _ os.Signal) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

// signalExitCode follows the shell convention for an interrupt.
func signalExitCode(_ os.Signal) int { return 130 }
