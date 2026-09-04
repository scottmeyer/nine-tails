//go:build windows

package harness

import (
	"errors"
	"os"
	"strings"
	"syscall"
	"testing"
)

func TestWindowsReplaceFileRecoversError1177(t *testing.T) {
	source, destination := replacementFixture(t)
	originalReplace := replaceExistingWindowsAPI
	replaceExistingWindowsAPI = func(source, destination, backup string) error {
		if err := os.Rename(destination, backup); err != nil {
			t.Fatal(err)
		}
		return errorUnableToMoveReplacement2
	}
	t.Cleanup(func() { replaceExistingWindowsAPI = originalReplace })

	err := replaceExistingFile(source, destination)
	if !errors.Is(err, errorUnableToMoveReplacement2) || !strings.Contains(err.Error(), "original restored") {
		t.Fatalf("error=%v", err)
	}
	assertFileContents(t, destination, "old")
	assertFileContents(t, source, "new")
}

func TestWindowsReplaceFileRetainsBackupWhenRecoveryFails(t *testing.T) {
	source, destination := replacementFixture(t)
	originalReplace, originalMove := replaceExistingWindowsAPI, moveNewWindowsAPI
	var backup string
	replaceExistingWindowsAPI = func(source, destination, backupPath string) error {
		backup = backupPath
		if err := os.Rename(destination, backup); err != nil {
			t.Fatal(err)
		}
		return errorUnableToMoveReplacement2
	}
	moveNewWindowsAPI = func(source, destination string) error { return syscall.ERROR_ACCESS_DENIED }
	t.Cleanup(func() {
		replaceExistingWindowsAPI = originalReplace
		moveNewWindowsAPI = originalMove
	})

	err := replaceExistingFile(source, destination)
	if err == nil || backup == "" || !strings.Contains(err.Error(), backup) {
		t.Fatalf("error=%v backup=%q", err, backup)
	}
	assertFileContents(t, backup, "old")
	assertFileContents(t, source, "new")
	if _, statErr := os.Lstat(destination); !os.IsNotExist(statErr) {
		t.Fatalf("destination after failed recovery: %v", statErr)
	}
}

func replacementFixture(t *testing.T) (source, destination string) {
	t.Helper()
	dir := t.TempDir()
	source, destination = dir+`\source.json`, dir+`\destination.json`
	if err := os.WriteFile(source, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	return source, destination
}

func assertFileContents(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil || string(data) != want {
		t.Fatalf("%s: data=%q err=%v", path, data, err)
	}
}
