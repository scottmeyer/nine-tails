// Package harness contains the thin, reversible integration layer between
// nine-tails and supported agent harnesses. It translates lifecycle JSON and
// settings shapes, but deliberately owns no knowledge records or agent loop.
package harness

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"
)

// Name identifies a supported harness adapter.
type Name string

const (
	Claude Name = "claude"
	Codex  Name = "codex"
)

const ownerTag = "nine-tails.hooks/v1"

// OwnerTag is passed by every installed entry. It is both a dispatch guard and
// the recognizable marker used to remove only entries installed by us.
func OwnerTag() string { return ownerTag }

// Event is the shared lifecycle subset used by nine-tails. Both supported
// harnesses currently provide these fields, though each adapter remains
// responsible for decoding its own input contract.
type Event struct {
	Name      string
	SessionID string
	Source    string
	Reason    string
	Prompt    string
}

// Adapter keeps harness-specific paths, config handlers, and event decoding
// behind one small contract. Installation and activation operate only on this
// interface.
type Adapter interface {
	Name() Name
	SettingsPath() (string, error)
	Handler(executable, event string) map[string]any
	OwnsHandler(json.RawMessage) bool
	DecodeEvent(io.Reader) (Event, error)
	EncodeContext(io.Writer, string, string) error
	// CapsuleMaxBytes is the harness's hard ceiling on one injected capsule;
	// a capsule over it is not injected and not recorded (capsule.TooLargeError).
	CapsuleMaxBytes() int
}

// For returns the adapter for name.
func For(name Name) (Adapter, error) {
	switch name {
	case Claude:
		return claudeAdapter{}, nil
	case Codex:
		return codexAdapter{}, nil
	default:
		return nil, fmt.Errorf("unsupported harness %q", name)
	}
}

type wireEvent struct {
	HookEventName string  `json:"hook_event_name"`
	SessionID     string  `json:"session_id"`
	Source        *string `json:"source"`
	Reason        *string `json:"reason"`
	Prompt        *string `json:"prompt"`
}

func decodeEvent(r io.Reader, name Name) (Event, error) {
	dec := json.NewDecoder(io.LimitReader(r, 8<<20))
	var in wireEvent
	if err := dec.Decode(&in); err != nil {
		return Event{}, fmt.Errorf("decode hook input: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return Event{}, fmt.Errorf("decode hook input: expected one JSON object")
		}
		return Event{}, fmt.Errorf("decode hook input: %w", err)
	}
	if in.SessionID == "" || in.HookEventName == "" {
		return Event{}, fmt.Errorf("hook input requires session_id and hook_event_name")
	}
	event := Event{Name: in.HookEventName, SessionID: in.SessionID}
	switch event.Name {
	case "SessionStart":
		if in.Source == nil || !validStartSource(name, *in.Source) {
			return Event{}, fmt.Errorf("invalid %s SessionStart source", name)
		}
		event.Source = *in.Source
	case "UserPromptSubmit":
		if in.Prompt == nil {
			return Event{}, fmt.Errorf("%s UserPromptSubmit requires prompt", name)
		}
		event.Prompt = *in.Prompt
	case "SessionEnd":
		if in.Reason == nil || !validEndReason(name, *in.Reason) {
			return Event{}, fmt.Errorf("invalid %s SessionEnd reason", name)
		}
		event.Reason = *in.Reason
	default:
		return Event{}, fmt.Errorf("unsupported %s hook event %q", name, event.Name)
	}
	return event, nil
}

func validStartSource(name Name, source string) bool {
	switch source {
	case "startup", "resume", "clear", "compact":
		return true
	case "fork":
		return name == Claude
	default:
		return false
	}
}

func validEndReason(name Name, reason string) bool {
	if name == Codex {
		return reason == "other"
	}
	switch reason {
	case "clear", "resume", "logout", "prompt_input_exit", "other":
		return true
	default:
		return false
	}
}

type claudeAdapter struct{}

func (claudeAdapter) Name() Name { return Claude }

func (claudeAdapter) SettingsPath() (string, error) {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, "settings.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "settings.json"), nil
}

func (claudeAdapter) Handler(executable, event string) map[string]any {
	timeout := 30
	if event == "SessionEnd" {
		timeout = 3
	}
	return map[string]any{
		"type":    "command",
		"command": executable,
		"args":    []string{"hooks", "dispatch", "--claude", "--owner=" + ownerTag},
		"timeout": timeout,
	}
}

func (claudeAdapter) OwnsHandler(raw json.RawMessage) bool {
	var h struct {
		Type string   `json:"type"`
		Args []string `json:"args"`
	}
	if json.Unmarshal(raw, &h) != nil || h.Type != "command" || len(h.Args) != 4 {
		return false
	}
	return h.Args[0] == "hooks" && h.Args[1] == "dispatch" && h.Args[2] == "--claude" && h.Args[3] == "--owner="+ownerTag
}

func (claudeAdapter) DecodeEvent(r io.Reader) (Event, error) { return decodeEvent(r, Claude) }

func (claudeAdapter) EncodeContext(w io.Writer, event, context string) error {
	return encodeContext(w, event, context)
}

func (claudeAdapter) CapsuleMaxBytes() int {
	// Claude caps additionalContext at 10,000 characters and replaces a longer
	// value with a file preview; bytes bound characters, with a margin.
	return 9800
}

type codexAdapter struct{}

func (codexAdapter) Name() Name { return Codex }

func (codexAdapter) SettingsPath() (string, error) {
	dir := os.Getenv("CODEX_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".codex")
	}
	return filepath.Join(dir, "hooks.json"), nil
}

func (codexAdapter) Handler(executable, event string) map[string]any {
	timeout := 30
	if event == "SessionEnd" {
		timeout = 3
	}
	dispatch := " hooks dispatch --codex --owner=" + ownerTag
	h := map[string]any{
		"type":    "command",
		"command": shellQuote(executable) + dispatch,
		"timeout": timeout,
	}
	if command, ok := encodedPowerShellCommand("& " + powershellQuote(executable) + dispatch); ok {
		h["commandWindows"] = command
	}
	if event != "SessionEnd" {
		// A capsule is already bounded by nine-tails. Ask Codex to pass the
		// complete hook context rather than replacing a large capsule with a
		// file preview.
		h["additionalContextLimit"] = 0
	}
	return h
}

func (codexAdapter) OwnsHandler(raw json.RawMessage) bool {
	var h struct {
		Type           string `json:"type"`
		Command        string `json:"command"`
		CommandWindows string `json:"commandWindows"`
	}
	if json.Unmarshal(raw, &h) != nil || h.Type != "command" {
		return false
	}
	dispatch := " hooks dispatch --codex --owner=" + ownerTag
	if strings.HasSuffix(h.Command, dispatch) {
		return true
	}
	script, ok := decodePowerShellScript(h.CommandWindows)
	return ok && strings.HasSuffix(script, dispatch)
}

func (codexAdapter) DecodeEvent(r io.Reader) (Event, error) { return decodeEvent(r, Codex) }

func (codexAdapter) EncodeContext(w io.Writer, event, context string) error {
	return encodeContext(w, event, context)
}

func (codexAdapter) CapsuleMaxBytes() int {
	// Cached Markdown is JSON-escaped into a run marker capped at 1 MiB:
	// 140 KiB stays below it even under Go JSON's worst-case six-byte escape.
	return 140 * 1024
}

func encodeContext(w io.Writer, event, context string) error {
	// These camelCase keys belong to the external harness wire schema. Core
	// nine-tails JSON remains snake_case.
	out := struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}{}
	out.HookSpecificOutput.HookEventName = event
	out.HookSpecificOutput.AdditionalContext = context
	return json.NewEncoder(w).Encode(out)
}

// shellQuote returns one POSIX-shell word. Codex command hooks currently use
// a command string rather than Claude's argv form.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

func powershellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// encodedPowerShellCommand is safe when Codex invokes commandWindows through
// either cmd.exe or PowerShell. -EncodedCommand takes UTF-16LE, and its base64
// alphabet needs no shell-specific quoting.
func encodedPowerShellCommand(script string) (string, bool) {
	program, ok := powershellExecutable()
	if !ok {
		return "", false
	}
	units := utf16.Encode([]rune(script))
	b := make([]byte, len(units)*2)
	for i, unit := range units {
		b[2*i] = byte(unit)
		b[2*i+1] = byte(unit >> 8)
	}
	args := []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-EncodedCommand", base64.StdEncoding.EncodeToString(b)}
	return program + " " + strings.Join(args, " "), true
}

func decodePowerShellScript(command string) (string, bool) {
	fields := strings.Fields(command)
	for i := 0; i+1 < len(fields); i++ {
		if !strings.EqualFold(fields[i], "-EncodedCommand") {
			continue
		}
		b, err := base64.StdEncoding.DecodeString(fields[i+1])
		if err != nil || len(b)%2 != 0 {
			return "", false
		}
		units := make([]uint16, len(b)/2)
		for i := range units {
			units[i] = uint16(b[2*i]) | uint16(b[2*i+1])<<8
		}
		return string(utf16.Decode(units)), true
	}
	return "", false
}
