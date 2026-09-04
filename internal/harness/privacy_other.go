//go:build !unix && !windows

package harness

import "os"

func privateRuntimeDirectory(info os.FileInfo) bool {
	return info.IsDir() && info.Mode().Perm()&0o077 == 0
}

func privateRunFile(info os.FileInfo) bool {
	return info.Mode().IsRegular() && info.Mode().Perm()&0o077 == 0
}
