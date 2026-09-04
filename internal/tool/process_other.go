//go:build !unix

package tool

import "os/exec"

// CommandContext's default cancellation kills the direct process on platforms
// without Unix process groups. Cmd.WaitDelay still bounds inherited I/O held
// open by its descendants.
func configureProcessTreeCancellation(_ *exec.Cmd) {}

// Descendant cleanup is not available without Unix process groups. WaitDelay
// still closes inherited I/O pipes so a successful direct process can return.
func terminateProcessTree(_ *exec.Cmd) error { return nil }
