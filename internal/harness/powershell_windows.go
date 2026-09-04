//go:build windows

package harness

import (
	"path/filepath"
	"syscall"
	"unsafe"
)

var getSystemDirectoryWProc = kernel32DLL.NewProc("GetSystemDirectoryW")

// powershellExecutable derives the Windows PowerShell path from the actual
// system directory rather than PATH or the process working directory.
func powershellExecutable() (string, bool) {
	buffer := make([]uint16, syscall.MAX_PATH)
	length, _, _ := getSystemDirectoryWProc.Call(
		uintptr(unsafe.Pointer(&buffer[0])),
		uintptr(len(buffer)),
	)
	if length > 0 && length < uintptr(len(buffer)) {
		return filepath.Join(syscall.UTF16ToString(buffer[:length]), "WindowsPowerShell", "v1.0", "powershell.exe"), true
	}
	// Keep failure closed against current-directory/PATH hijacking. This may
	// fail to launch on a nonstandard installation, but remains an absolute
	// system location and yields adapter exit 5.
	return `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`, true
}
