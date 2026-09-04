package harness

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestInstallMergeIdempotentAndUninstallOwnedOnly(t *testing.T) {
	tests := []struct {
		name     Name
		env      string
		filename string
	}{
		{Claude, "CLAUDE_CONFIG_DIR", "settings.json"},
		{Codex, "CODEX_HOME", "hooks.json"},
	}
	for _, tt := range tests {
		t.Run(string(tt.name), func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv(tt.env, dir)
			adapter, err := For(tt.name)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, tt.filename)
			original := []byte(`{
  "large": 9007199254740993123456789,
  "theme": "dark",
  "hooks": {
    "UserPromptSubmit": [{"matcher":"", "hooks":[{"type":"command","command":"user-owned --owner=nine-tails-ish"}]}],
    "PreToolUse": [{"matcher":"Bash","hooks":[{"type":"command","command":"check.sh"}]}]
  }
}
`)
			if err := os.WriteFile(path, original, 0o640); err != nil {
				t.Fatal(err)
			}

			gotPath, changed, err := Install(adapter, "/opt/Nine Tails/bin/nine-tails")
			if err != nil || !changed || gotPath != path {
				t.Fatalf("path=%q changed=%v err=%v", gotPath, changed, err)
			}
			first, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(first, []byte("9007199254740993123456789")) || !bytes.Contains(first, []byte("check.sh")) || !bytes.Contains(first, []byte("user-owned")) {
				t.Fatalf("unrelated settings changed:\n%s", first)
			}
			if info, _ := os.Stat(path); runtime.GOOS != "windows" && info.Mode().Perm() != 0o640 {
				t.Fatalf("mode=%o", info.Mode().Perm())
			}
			assertOwnedCount(t, first, adapter, 3)

			_, changed, err = Install(adapter, "/opt/Nine Tails/bin/nine-tails")
			if err != nil || changed {
				t.Fatalf("second install changed=%v err=%v", changed, err)
			}
			second, _ := os.ReadFile(path)
			if !bytes.Equal(first, second) {
				t.Fatal("idempotent install rewrote settings")
			}

			_, changed, err = Install(adapter, "/new/path/nine-tails")
			if err != nil || !changed {
				t.Fatalf("upgrade changed=%v err=%v", changed, err)
			}
			upgraded, _ := os.ReadFile(path)
			assertOwnedCount(t, upgraded, adapter, 3)
			if bytes.Contains(upgraded, []byte("/opt/Nine Tails")) {
				t.Fatal("old owned executable survived reinstall")
			}

			_, changed, err = Uninstall(adapter)
			if err != nil || !changed {
				t.Fatalf("uninstall changed=%v err=%v", changed, err)
			}
			uninstalled, _ := os.ReadFile(path)
			assertOwnedCount(t, uninstalled, adapter, 0)
			if !bytes.Contains(uninstalled, []byte("9007199254740993123456789")) || !bytes.Contains(uninstalled, []byte("check.sh")) || !bytes.Contains(uninstalled, []byte("user-owned")) {
				t.Fatalf("uninstall removed unrelated settings:\n%s", uninstalled)
			}
			beforeNoop := append([]byte(nil), uninstalled...)
			_, changed, err = Uninstall(adapter)
			if err != nil || changed {
				t.Fatalf("second uninstall changed=%v err=%v", changed, err)
			}
			afterNoop, _ := os.ReadFile(path)
			if !bytes.Equal(beforeNoop, afterNoop) {
				t.Fatal("idempotent uninstall rewrote settings")
			}
		})
	}
}

func TestUninstallNeverInstalledIsByteForByteNoop(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CODEX_HOME", dir)
	path := filepath.Join(dir, "hooks.json")
	original := []byte("{ \"hooks\" : { \"SessionStart\" : [] }, \"odd\" : 1e+09 }\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	a, _ := For(Codex)
	_, changed, err := Uninstall(a)
	if err != nil || changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, original) {
		t.Fatalf("no-op uninstall rewrote bytes:\n%s", got)
	}
}

func TestInstallRejectsMalformedOrNonRegularSettingsWithoutChangingThem(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	path := filepath.Join(dir, "settings.json")
	original := []byte(`{"hooks":{"SessionStart":{}}}`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	a, _ := For(Claude)
	if _, _, err := Install(a, "/bin/nine-tails"); err == nil {
		t.Fatal("malformed event shape accepted")
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, original) {
		t.Fatal("rejected install changed settings")
	}

	if runtime.GOOS != "windows" {
		target := filepath.Join(dir, "target.json")
		if err := os.WriteFile(target, []byte(`{}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
		if _, _, err := Install(a, "/bin/nine-tails"); err == nil {
			t.Fatal("settings symlink accepted")
		}
	}
}

func TestUninstallMissingDoesNotCreateConfigHome(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "missing")
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	a, _ := For(Claude)
	path, changed, err := Uninstall(a)
	if err != nil || changed {
		t.Fatalf("path=%q changed=%v err=%v", path, changed, err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("uninstall created config home: %v", err)
	}
}

func TestCodexQuotesBothShellsAndClaudeUsesExecForm(t *testing.T) {
	codex, _ := For(Codex)
	h := codex.Handler("/tmp/it's here/nt", "UserPromptSubmit")
	if h["command"] != `'/tmp/it'"'"'s here/nt' hooks dispatch --codex --owner=`+OwnerTag() {
		t.Fatalf("POSIX command=%q", h["command"])
	}
	windowsCommand, present := h["commandWindows"]
	if runtime.GOOS == "windows" {
		command, ok := windowsCommand.(string)
		if !present || !ok {
			t.Fatalf("commandWindows=%#v", windowsCommand)
		}
		wantScript := `& '/tmp/it''s here/nt' hooks dispatch --codex --owner=` + OwnerTag()
		if got := decodePowerShellCommand(t, command); got != wantScript {
			t.Fatalf("PowerShell script=%q, want %q", got, wantScript)
		}
	} else if present {
		t.Fatalf("off-Windows handler persisted commandWindows=%#v", windowsCommand)
	}
	claude, _ := For(Claude)
	ch := claude.Handler("/tmp/it's here/nt", "UserPromptSubmit")
	args, ok := ch["args"].([]string)
	if !ok || len(args) != 4 || ch["command"] != "/tmp/it's here/nt" {
		t.Fatalf("Claude handler=%#v", ch)
	}
	if got := claude.CapsuleBudget(5000); got != 2800 {
		t.Fatalf("Claude capsule budget=%d", got)
	}
	if got := claude.CapsuleBudget(1200); got != 1200 {
		t.Fatalf("small Claude capsule budget=%d", got)
	}
	if got := codex.CapsuleBudget(5000); got != 5000 {
		t.Fatalf("Codex capsule budget=%d", got)
	}
	if got := codex.CapsuleBudget(50000); got != 40000 {
		t.Fatalf("large Codex capsule budget=%d", got)
	}
}

func TestCodexOwnershipRecognizesEitherExecutableField(t *testing.T) {
	a, _ := For(Codex)
	dispatch := ` hooks dispatch --codex --owner=` + OwnerTag()
	units := utf16.Encode([]rune(`& 'C:\Program Files\nine-tails.exe'` + dispatch))
	b := make([]byte, len(units)*2)
	for i, unit := range units {
		b[2*i] = byte(unit)
		b[2*i+1] = byte(unit >> 8)
	}
	windows := `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe -NoLogo -NoProfile -NonInteractive -EncodedCommand ` + base64.StdEncoding.EncodeToString(b)
	owned, _ := json.Marshal(map[string]any{"type": "command", "command": "user-edited", "commandWindows": windows})
	if !a.OwnsHandler(owned) {
		t.Fatal("intact owned commandWindows was hidden by edited command")
	}
	unrelated, _ := json.Marshal(map[string]any{"type": "command", "command": "user-edited", "commandWindows": windows + "broken"})
	if a.OwnsHandler(unrelated) {
		t.Fatal("malformed commandWindows was treated as owned")
	}
}

func decodePowerShellCommand(t *testing.T, command string) string {
	t.Helper()
	const options = " -NoLogo -NoProfile -NonInteractive -EncodedCommand "
	index := strings.Index(command, options)
	if index <= 0 {
		t.Fatalf("commandWindows does not explicitly launch PowerShell: %q", command)
	}
	assertPowerShellProgram(t, command[:index])
	return decodePowerShellPayload(t, command[index+len(options):])
}

func assertPowerShellProgram(t *testing.T, program string) {
	t.Helper()
	if runtime.GOOS != "windows" {
		if program != "powershell.exe" {
			t.Fatalf("PowerShell program=%q", program)
		}
		return
	}
	if !filepath.IsAbs(program) {
		t.Fatalf("Windows PowerShell path is not absolute: %q", program)
	}
	wantSuffix := strings.ToLower(filepath.Join("System32", "WindowsPowerShell", "v1.0", "powershell.exe"))
	if !strings.HasSuffix(strings.ToLower(program), wantSuffix) {
		t.Fatalf("Windows PowerShell path=%q, want System32 WindowsPowerShell", program)
	}
}

func decodePowerShellPayload(t *testing.T, payload string) string {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(payload)
	if err != nil || len(b)%2 != 0 {
		t.Fatalf("invalid encoded command: bytes=%d err=%v", len(b), err)
	}
	units := make([]uint16, len(b)/2)
	for i := range units {
		units[i] = uint16(b[2*i]) | uint16(b[2*i+1])<<8
	}
	return string(utf16.Decode(units))
}

func TestCodexWindowsCommandRunsThroughCmdAndPowerShell(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows shell integration")
	}
	dir := t.TempDir()
	fixture := filepath.Join(dir, "hook fixture.cmd")
	marker := filepath.Join(dir, "ran.txt")
	body := "@echo off\r\n" + "echo ran>\"" + marker + "\"\r\n"
	if err := os.WriteFile(fixture, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	a, _ := For(Codex)
	h := a.Handler(fixture, "UserPromptSubmit")
	command := h["commandWindows"].(string)
	for _, shell := range [][]string{
		{"cmd.exe", "/D", "/S", "/C", command},
		{"powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", command},
	} {
		_ = os.Remove(marker)
		if out, err := exec.Command(shell[0], shell[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("%s: %v\n%s", shell[0], err, out)
		}
		if _, err := os.Stat(marker); err != nil {
			t.Fatalf("%s did not launch quoted fixture: %v", shell[0], err)
		}
	}
}

func assertOwnedCount(t *testing.T, data []byte, a Adapter, want int) {
	t.Helper()
	var root rawObject
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	hooks, err := objectField(root, "hooks", false)
	if err != nil {
		t.Fatal(err)
	}
	got := 0
	for _, event := range installedEvents {
		groups, _ := eventGroups(hooks[event], event, false)
		for _, groupRaw := range groups {
			var group rawObject
			_ = json.Unmarshal(groupRaw, &group)
			var handlers []json.RawMessage
			_ = json.Unmarshal(group["hooks"], &handlers)
			for _, handler := range handlers {
				if a.OwnsHandler(handler) {
					got++
				}
			}
		}
	}
	if got != want {
		t.Fatalf("owned handler count=%d, want %d\n%s", got, want, data)
	}
}
