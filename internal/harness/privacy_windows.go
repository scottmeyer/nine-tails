//go:build windows

package harness

import (
	"os"
	"syscall"
)

// Windows does not expose ACLs through FileMode.Perm: ordinary writable files
// report 0666 and directories 0777. These files inherit the ACL of the user's
// NINE_TAILS_HOME; reject reparse/special nodes here and rely on that home ACL.
func privateRuntimeDirectory(info os.FileInfo) bool {
	return info.IsDir() && !windowsReparsePoint(info)
}

func privateRunFile(info os.FileInfo) bool {
	return info.Mode().IsRegular() && !windowsReparsePoint(info)
}

func windowsReparsePoint(info os.FileInfo) bool {
	data, ok := info.Sys().(*syscall.Win32FileAttributeData)
	return !ok || data.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0
}
