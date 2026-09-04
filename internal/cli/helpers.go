package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// Config is the optional $NINE_TAILS_HOME/config.yaml (DESIGN.md §1).
type Config struct {
	// CompileAdviceTokens: load advises a compile when the capsule's estimated
	// size passes it and something is uncompiled; 0 turns the advice off.
	CompileAdviceTokens  int `yaml:"compile_advice_tokens" json:"compile_advice_tokens"`
	SignalExcerptChars   int `yaml:"signal_excerpt_chars" json:"signal_excerpt_chars"`
	StateMaxBytes        int `yaml:"state_max_bytes" json:"state_max_bytes"`
	ContextRetentionDays int `yaml:"context_retention_days" json:"context_retention_days"`
	Compiler             struct {
		Argv    []string `yaml:"argv" json:"argv"`
		Timeout string   `yaml:"timeout" json:"timeout"`
	} `yaml:"compiler" json:"compiler"`
}

// DefaultConfig returns the built-in defaults.
func DefaultConfig() Config {
	c := Config{CompileAdviceTokens: 4000, SignalExcerptChars: 300, StateMaxBytes: 8192, ContextRetentionDays: 30}
	c.Compiler.Argv = []string{}
	c.Compiler.Timeout = "300s"
	return c
}

// validateConfig checks the mechanical ranges of the capped resources.
// Rejecting bad values at load time keeps every later command on one
// coherent, inspectable policy.
func validateConfig(c Config) error {
	positiveInts := []struct {
		name  string
		value int
	}{
		{"signal_excerpt_chars", c.SignalExcerptChars},
		{"state_max_bytes", c.StateMaxBytes},
		{"context_retention_days", c.ContextRetentionDays},
	}
	for _, v := range positiveInts {
		if v.value <= 0 {
			return fmt.Errorf("%s must be positive (got %d)", v.name, v.value)
		}
	}

	if c.CompileAdviceTokens < 0 {
		return fmt.Errorf("compile_advice_tokens must be zero (off) or positive (got %d)", c.CompileAdviceTokens)
	}

	timeout, err := time.ParseDuration(c.Compiler.Timeout)
	if err != nil || timeout <= 0 {
		return fmt.Errorf("compiler.timeout must be a positive Go duration (got %q)", c.Compiler.Timeout)
	}
	for i, arg := range c.Compiler.Argv {
		if strings.TrimSpace(arg) == "" {
			return fmt.Errorf("compiler.argv[%d] must not be empty", i)
		}
	}
	return nil
}

// LoadConfig reads config.yaml from home, layering it over the defaults.
// A missing file is not an error; a malformed one is (exit 2).
func LoadConfig(home string) (Config, error) {
	c := DefaultConfig()
	b, err := os.ReadFile(filepath.Join(home, "config.yaml"))
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return c, Errorf(ExitStore, "read config: %v", err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	if err := dec.Decode(&c); err != nil && err != io.EOF {
		return c, Invalid("config.yaml: %v", err)
	}
	var extra any
	if err := dec.Decode(&extra); err == nil {
		return c, Invalid("config.yaml: must contain exactly one YAML document")
	} else if err != io.EOF {
		return c, Invalid("config.yaml: %v", err)
	}
	if err := validateConfig(c); err != nil {
		return c, Invalid("config.yaml: %v", err)
	}
	return c, nil
}

// ReadBody returns the body from either the positional text or stdin,
// requiring exactly one of them, with exactly one trailing newline removed.
// An empty body is an error unless allowEmpty.
func ReadBody(positional []string, useStdin bool, stdin io.Reader, allowEmpty bool) (string, error) {
	if useStdin && len(positional) > 0 {
		return "", Invalid("give the text either as an argument or via --stdin, not both")
	}
	var body string
	switch {
	case useStdin:
		b, err := io.ReadAll(stdin)
		if err != nil {
			return "", Invalid("read stdin: %v", err)
		}
		body = string(b)
	case len(positional) == 0:
		if allowEmpty {
			return "", nil
		}
		return "", Invalid("missing text: pass it as an argument (use -- before text that starts with '-') or use --stdin")
	default:
		body = strings.Join(positional, " ")
	}
	if !utf8.ValidString(body) {
		return "", Invalid("body must be valid UTF-8 text")
	}
	body = strings.TrimSuffix(body, "\n")
	if body == "" && !allowEmpty {
		return "", Invalid("body is empty")
	}
	return body, nil
}

var relativeAtRE = regexp.MustCompile(`^\+([0-9]+)([smhd])$`)

// ParseAt parses --at: RFC 3339, or exactly "+<integer><unit>" with unit in
// s, m, h, d. Empty means now. General Go durations remain supported by
// ParseDuration for flags such as --lease and --older-than.
func ParseAt(s string, now time.Time) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return now, nil
	}
	if strings.HasPrefix(s, "+") {
		m := relativeAtRE.FindStringSubmatch(s)
		if m == nil {
			return time.Time{}, Invalid("--at relative time must be +<integer><s|m|h|d>, got %q", s)
		}
		n, err := strconv.ParseUint(m[1], 10, 64)
		if err != nil {
			return time.Time{}, Invalid("--at relative time is too large: %q", s)
		}
		units := map[string]time.Duration{"s": time.Second, "m": time.Minute, "h": time.Hour, "d": 24 * time.Hour}
		unit := units[m[2]]
		const maxDuration = uint64((1 << 63) - 1)
		if n > maxDuration/uint64(unit) {
			return time.Time{}, Invalid("--at relative time is too large: %q", s)
		}
		return now.Add(time.Duration(n) * unit), nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, Invalid("--at must be RFC 3339 (2026-09-04T18:00:00Z) or relative (+5m, +2h, +1d): %v", err)
	}
	return t, nil
}

// ParseDuration is time.ParseDuration plus a "d" (day) unit.
func ParseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if strings.HasSuffix(s, "d") {
		n, err := strconv.ParseFloat(strings.TrimSuffix(s, "d"), 64)
		if err != nil {
			return 0, Invalid("bad duration %q", s)
		}
		return time.Duration(n * 24 * float64(time.Hour)), nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, Invalid("bad duration %q (use 30s, 5m, 2h, 1d)", s)
	}
	return d, nil
}

// SplitAgentName splits "agent/name" for state commands.
func SplitAgentName(s string) (agent, name string, err error) {
	agent, name, ok := strings.Cut(s, "/")
	if !ok || agent == "" || name == "" {
		return "", "", Invalid("expected <agent>/<name>, got %q", s)
	}
	return agent, name, nil
}

// WriteJSON writes v as indented JSON followed by a newline.
func WriteJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// WriteYAML writes v as a YAML document.
func WriteYAML(w io.Writer, v any) error {
	enc := yaml.NewEncoder(w)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		return err
	}
	return enc.Close()
}

// Write dispatches on format: json (default) or yaml.
func Write(w io.Writer, format string, v any) error {
	switch format {
	case "", "json":
		return WriteJSON(w, v)
	case "yaml", "yml":
		return WriteYAML(w, v)
	default:
		return Invalid("unknown format %q (json|yaml)", format)
	}
}

// IsID reports whether s looks like a nine-tails identifier: prefix_ULID, or
// prefix_N from a store created before schema v3.
func IsID(s string) bool {
	i := strings.LastIndex(s, "_")
	if i <= 0 || i == len(s)-1 {
		return false
	}
	for _, ch := range s[:i] {
		if ch < 'a' || ch > 'z' {
			return false
		}
	}
	for _, ch := range s[i+1:] {
		if !(ch >= '0' && ch <= '9' || ch >= 'A' && ch <= 'Z') {
			return false
		}
	}
	return true
}

// Sprintf is fmt.Sprintf re-exported so command files need one import fewer.
func Sprintf(format string, a ...any) string { return fmt.Sprintf(format, a...) }
