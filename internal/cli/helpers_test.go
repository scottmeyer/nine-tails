package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReadBodyRequiresUTF8Text(t *testing.T) {
	invalid := []byte{'o', 'k', 0xff, '\n'}
	if _, err := ReadBody(nil, true, bytes.NewReader(invalid), false); err == nil || CodeOf(err) != ExitInvalid || !strings.Contains(err.Error(), "UTF-8") {
		t.Fatalf("ReadBody invalid stdin error = %v (code %d), want exit 2 mentioning UTF-8", err, CodeOf(err))
	}

	badArg := string([]byte{'x', 0xff})
	if _, err := ReadBody([]string{badArg}, false, strings.NewReader(""), false); err == nil || CodeOf(err) != ExitInvalid || !strings.Contains(err.Error(), "UTF-8") {
		t.Fatalf("ReadBody invalid argument error = %v (code %d), want exit 2 mentioning UTF-8", err, CodeOf(err))
	}

	got, err := ReadBody(nil, true, strings.NewReader("ユニコード\n"), false)
	if err != nil || got != "ユニコード" {
		t.Fatalf("ReadBody valid UTF-8 = %q, %v", got, err)
	}
}

func TestLoadConfigDefaultsOverlayAndValidation(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		cfg, err := LoadConfig(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if cfg.CompileAdviceTokens != 4000 || cfg.SignalExcerptChars != 300 || cfg.Compiler.Timeout != "300s" || cfg.Compiler.Argv == nil {
			t.Fatalf("unexpected defaults: %+v", cfg)
		}
	})

	t.Run("partial overlay keeps nested defaults", func(t *testing.T) {
		dir := t.TempDir()
		writeConfig(t, dir, "compile_advice_tokens: 1600\ncompiler:\n  argv: [sh]\n")
		cfg, err := LoadConfig(dir)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.CompileAdviceTokens != 1600 || cfg.Compiler.Timeout != "300s" || len(cfg.Compiler.Argv) != 1 || cfg.Compiler.Argv[0] != "sh" {
			t.Fatalf("overlay lost defaults: %+v", cfg)
		}
	})

	cases := []struct {
		name string
		body string
		want string
	}{
		{"negative advice threshold", "compile_advice_tokens: -1\n", "compile_advice_tokens"},
		{"non-positive excerpt", "signal_excerpt_chars: 0\n", "signal_excerpt_chars"},
		{"non-positive state cap", "state_max_bytes: -1\n", "state_max_bytes"},
		{"non-positive retention", "context_retention_days: 0\n", "context_retention_days"},
		{"bad compiler timeout", "compiler:\n  timeout: soon\n", "compiler.timeout"},
		{"non-positive compiler timeout", "compiler:\n  timeout: 0s\n", "compiler.timeout"},
		{"empty compiler argument", "compiler:\n  argv: [claude, '']\n", "compiler.argv[1]"},
		{"second document", "default_budget: 1600\n---\ndefault_budget: 1700\n", "exactly one"},
		{"malformed second document", "default_budget: 1600\n---\nnot: [valid\n", "config.yaml"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeConfig(t, dir, tc.body)
			_, err := LoadConfig(dir)
			if err == nil || CodeOf(err) != ExitInvalid || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("LoadConfig error = %v (code %d), want exit 2 containing %q", err, CodeOf(err), tc.want)
			}
		})
	}

	t.Run("zero advice threshold turns the advice off", func(t *testing.T) {
		dir := t.TempDir()
		writeConfig(t, dir, "compile_advice_tokens: 0\n")
		if _, err := LoadConfig(dir); err != nil {
			t.Fatalf("zero threshold should be valid: %v", err)
		}
	})
}

func writeConfig(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestParseAtStrictRelativeGrammar(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	valid := []struct {
		input string
		want  time.Time
	}{
		{"", now},
		{"+0s", now},
		{"+15s", now.Add(15 * time.Second)},
		{"+2m", now.Add(2 * time.Minute)},
		{"+3h", now.Add(3 * time.Hour)},
		{"+1d", now.Add(24 * time.Hour)},
		{"2026-09-04T08:30:00-05:00", time.Date(2026, 9, 4, 13, 30, 0, 0, time.UTC)},
	}
	for _, tc := range valid {
		t.Run("valid "+tc.input, func(t *testing.T) {
			got, err := ParseAt(tc.input, now)
			if err != nil {
				t.Fatal(err)
			}
			if !got.Equal(tc.want) {
				t.Fatalf("ParseAt(%q) = %s, want %s", tc.input, got, tc.want)
			}
		})
	}

	for _, input := range []string{
		"+1.5h", "+1ms", "+-1h", "+1H", "+h", "+ 1h", "+18446744073709551616s", "+9223372036854775807d", "yesterday",
	} {
		t.Run("invalid "+input, func(t *testing.T) {
			_, err := ParseAt(input, now)
			if err == nil || CodeOf(err) != ExitInvalid {
				t.Fatalf("ParseAt(%q) error = %v (code %d), want exit 2", input, err, CodeOf(err))
			}
		})
	}
}

func TestParseDurationRemainsGeneral(t *testing.T) {
	cases := []struct {
		input string
		want  time.Duration
	}{
		{"1.5h", 90 * time.Minute},
		{"250ms", 250 * time.Millisecond},
		{"1.5d", 36 * time.Hour},
	}
	for _, tc := range cases {
		got, err := ParseDuration(tc.input)
		if err != nil {
			t.Fatalf("ParseDuration(%q): %v", tc.input, err)
		}
		if got != tc.want {
			t.Fatalf("ParseDuration(%q) = %s, want %s", tc.input, got, tc.want)
		}
	}
}
