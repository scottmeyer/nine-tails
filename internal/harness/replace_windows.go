//go:build windows

package harness

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

const (
	moveFileWriteThrough          = 0x00000008
	errorUnableToMoveReplacement2 = syscall.Errno(1177)
)

var (
	kernel32DLL               = syscall.NewLazyDLL("kernel32.dll")
	replaceFileWProc          = kernel32DLL.NewProc("ReplaceFileW")
	moveFileExWProc           = kernel32DLL.NewProc("MoveFileExW")
	replaceExistingWindowsAPI = invokeReplaceFileW
	moveNewWindowsAPI         = invokeMoveFileExW
)

// replaceFile uses ReplaceFileW for an existing destination so its ACL and
// attributes survive replacement. A new same-directory destination uses
// MoveFileExW with write-through. A destination race returns an error for a
// later invocation to retry rather than guessing about recovery state.
func replaceFile(source, destination string) error {
	_, err := os.Lstat(destination)
	switch {
	case err == nil:
		return replaceExistingFile(source, destination)
	case os.IsNotExist(err):
		return moveNewFile(source, destination)
	default:
		return err
	}
}

func replaceExistingFile(source, destination string) error {
	id, err := randomString(18)
	if err != nil {
		return fmt.Errorf("create replacement backup name: %w", err)
	}
	backup := filepath.Join(filepath.Dir(destination), ".nine-tails-backup-"+id)
	err = replaceExistingWindowsAPI(source, destination, backup)
	if err == nil {
		if verifyErr := verifyReplacement(destination); verifyErr != nil {
			return fmt.Errorf("verify replaced file: %w; original retained at %s", verifyErr, backup)
		}
		// The new state is committed. Backup cleanup is best effort so callers do
		// not suppress already-committed output or retry the logical mutation.
		_ = os.Remove(backup)
		return nil
	}

	// With a backup name, ReplaceFileW error 1177 leaves the original at the
	// backup path and the new file at source. Restore the original synchronously
	// before returning; the caller may then safely discard its temporary source.
	if errors.Is(err, errorUnableToMoveReplacement2) {
		if restoreErr := moveNewWindowsAPI(backup, destination); restoreErr != nil {
			return fmt.Errorf("replace file: %w; restore failed: %v; original retained at %s", err, restoreErr, backup)
		}
		if verifyErr := verifyReplacement(destination); verifyErr != nil {
			return fmt.Errorf("replace file: %w; original moved back but verification failed: %v", err, verifyErr)
		}
		return fmt.Errorf("replace file: %w (original restored)", err)
	}

	// Microsoft documents that errors 1175/1176 and ordinary errors retain the
	// original names when a backup is supplied. If an unexpected backup exists,
	// preserve it and make its recovery path explicit instead of deleting it.
	if _, backupErr := os.Lstat(backup); backupErr == nil {
		return fmt.Errorf("replace file: %w; original backup retained at %s", err, backup)
	}
	return err
}

func verifyReplacement(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("destination is not a regular file")
	}
	return nil
}

func invokeReplaceFileW(source, destination, backup string) error {
	destinationPtr, err := windowsPathPointer(destination)
	if err != nil {
		return err
	}
	sourcePtr, err := windowsPathPointer(source)
	if err != nil {
		return err
	}
	backupPtr, err := windowsPathPointer(backup)
	if err != nil {
		return err
	}
	result, _, callErr := replaceFileWProc.Call(
		uintptr(unsafe.Pointer(destinationPtr)),
		uintptr(unsafe.Pointer(sourcePtr)),
		uintptr(unsafe.Pointer(backupPtr)),
		0,
		0,
		0,
	)
	if result != 0 {
		return nil
	}
	if callErr != syscall.Errno(0) {
		return callErr
	}
	return syscall.EINVAL
}

func moveNewFile(source, destination string) error {
	return moveNewWindowsAPI(source, destination)
}

func invokeMoveFileExW(source, destination string) error {
	sourcePtr, err := windowsPathPointer(source)
	if err != nil {
		return err
	}
	destinationPtr, err := windowsPathPointer(destination)
	if err != nil {
		return err
	}
	result, _, callErr := moveFileExWProc.Call(
		uintptr(unsafe.Pointer(sourcePtr)),
		uintptr(unsafe.Pointer(destinationPtr)),
		moveFileWriteThrough,
	)
	if result != 0 {
		return nil
	}
	if callErr != syscall.Errno(0) {
		return callErr
	}
	return syscall.EINVAL
}

func windowsPathPointer(path string) (*uint16, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	abs = filepath.Clean(abs)
	switch {
	case strings.HasPrefix(abs, `\\?\`):
	case strings.HasPrefix(abs, `\\`):
		abs = `\\?\UNC\` + strings.TrimPrefix(abs, `\\`)
	default:
		abs = `\\?\` + abs
	}
	return syscall.UTF16PtrFromString(abs)
}
