//go:build unix

package tool

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// configureProcessTreeCancellation starts the tool in its own process group
// and makes context cancellation kill that group. Without this, killing a
// shell leaves its children running and potentially holding the stdout/stderr
// pipes used by os/exec open.
func configureProcessTreeCancellation(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return terminateProcessTree(cmd) }
}

// terminateProcessTree kills the process group created for a tool. It is used
// both when the context expires and after WaitDelay proves that a successful
// direct executable left a descendant holding an inherited pipe open.
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
