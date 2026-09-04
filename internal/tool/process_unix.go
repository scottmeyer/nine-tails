//go:build unix

package tool

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// forwardedSignals are the termination signals nine-tails relays to a tool.
var forwardedSignals = []os.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP}

// configureProcessTreeCancellation starts the tool in its own process group
// and makes context cancellation kill that group. Without this, killing a
// shell leaves its children running and potentially holding the stdout/stderr
// pipes used by os/exec open.
func configureProcessTreeCancellation(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return terminateProcessTree(cmd) }
}

// terminateProcessTree kills the process group created for a tool. It is used
// when the context expires, when a second termination signal arrives, and
// after WaitDelay proves that a successful direct executable left a
// descendant holding an inherited pipe open.
func terminateProcessTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return os.ErrProcessDone
	}
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	return err
}

// forwardSignal sends sig to the tool's process group: what the terminal
// would have done had the tool stayed in nine-tails' own group.
func forwardSignal(cmd *exec.Cmd, sig os.Signal) {
	if cmd.Process == nil {
		return
	}
	s, ok := sig.(syscall.Signal)
	if !ok {
		s = syscall.SIGTERM
	}
	_ = syscall.Kill(-cmd.Process.Pid, s)
}

// signalExitCode is the shell convention for a process ended by sig.
func signalExitCode(sig os.Signal) int {
	if s, ok := sig.(syscall.Signal); ok {
		return 128 + int(s)
	}
	return 130
}
