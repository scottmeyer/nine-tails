//go:build !windows

package main

import (
	"os/exec"
)

func harnessCommand(name string, args []string) (*exec.Cmd, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return nil, err
	}
	return exec.Command(path, args...), nil
}
