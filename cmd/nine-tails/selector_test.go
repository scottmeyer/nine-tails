package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A selector script that prints a fixed answer, plus the store it must run against.
func selectorHarness(t *testing.T, script string) (*harness, string) {
	t.Helper()
	h := newHarness(t)
	h.ok("load", "pilot", "--task", "seed", "--meta", "repo-id=acme")
	path := filepath.Join(h.home, "selector.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o700); err != nil {
		t.Fatal(err)
	}
	return h, path
}

func writeSelectorConfig(t *testing.T, h *harness, path, timeout string) {
	t.Helper()
	cfg := fmt.Sprintf("selector:\n  argv: [%q]\n  timeout: %s\n", path, timeout)
	if err := os.WriteFile(filepath.Join(h.home, "config.yaml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSelectorSuppliesRecallAndValidatesIt(t *testing.T) {
	h, path := selectorHarness(t, "cat >/dev/null; echo '{\"recall\": []}'")
	wanted := strings.TrimSpace(h.ok("remember", "pilot", "--meta", "repo-id=acme", "Orientation preface answered the register question without opening documents").out)
	other := strings.TrimSpace(h.ok("remember", "pilot", "--meta", "repo-id=acme", "Clippy rejects needless borrows in the launcher").out)
	foreign := strings.TrimSpace(h.ok("remember", "pilot", "--meta", "repo-id=other", "A memory scoped to another repository").out)
	// Selector echoes the wanted id plus junk that must be filtered: a foreign
	// scope, a nonexistent id and a duplicate.
	script := fmt.Sprintf("cat >/dev/null; echo '{\"recall\": [%q, %q, \"rec_01J00000000000000000000000\", %q]}'", wanted, foreign, wanted)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o700); err != nil {
		t.Fatal(err)
	}
	writeSelectorConfig(t, h, path, "5s")
	r := h.ok("load", "pilot", "--task", "cheapest answer to a status question", "--meta", "repo-id=acme")
	if !strings.Contains(r.out, "Orientation preface") || strings.Contains(r.out, "Clippy rejects") {
		t.Fatalf("selector choice not delivered verbatim; capsule:\n%s", r.out)
	}
	if !strings.Contains(r.err, "recall selected by "+path+": 1 record(s) (2 unusable id(s) ignored)") {
		t.Fatalf("missing selection diagnostic; stderr: %s", r.err)
	}
	_ = other
	// Lexical retrieval would have found the clippy memory for this task; the
	// selector's judgment replaces it only because it named a usable record.
	lexical := h.ok("load", "pilot", "--task", "clippy borrows", "--meta", "repo-id=acme", "--recall", other)
	if !strings.Contains(lexical.out, "Clippy rejects") || strings.Contains(lexical.err, "selector") {
		t.Fatalf("explicit --recall must bypass the selector; stderr: %s", lexical.err)
	}
}

func TestSelectorFailuresFallBackToLexicalRecall(t *testing.T) {
	h, path := selectorHarness(t, "exit 1")
	h.ok("remember", "pilot", "--meta", "repo-id=acme", "Clippy rejects needless borrows in the launcher")
	writeSelectorConfig(t, h, path, "5s")
	r := h.ok("load", "pilot", "--task", "clippy borrows", "--meta", "repo-id=acme")
	if !strings.Contains(r.out, "Clippy rejects") || !strings.Contains(r.err, "failed; using lexical recall") {
		t.Fatalf("failed selector must fall back to lexical recall; stderr: %s\ncapsule: %s", r.err, r.out)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\ncat >/dev/null; echo not-json\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	r = h.ok("load", "pilot", "--task", "clippy borrows", "--meta", "repo-id=acme")
	if !strings.Contains(r.out, "Clippy rejects") || !strings.Contains(r.err, "returned no JSON") {
		t.Fatalf("malformed selector output must fall back; stderr: %s", r.err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\nsleep 3\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeSelectorConfig(t, h, path, "200ms")
	r = h.ok("load", "pilot", "--task", "clippy borrows", "--meta", "repo-id=acme")
	if !strings.Contains(r.out, "Clippy rejects") || !strings.Contains(r.err, "timed out after 200ms; using lexical recall") {
		t.Fatalf("slow selector must fall back; stderr: %s", r.err)
	}
	disabled := h.ok("load", "pilot", "--task", "clippy borrows", "--query", "", "--meta", "repo-id=acme")
	if strings.Contains(disabled.err, "selector") || strings.Contains(disabled.out, "Clippy rejects") {
		t.Fatalf("--query \"\" must disable recall and skip the selector; stderr: %s", disabled.err)
	}
}

func TestSelectorConfigIsValidated(t *testing.T) {
	h := newHarness(t)
	if err := os.WriteFile(filepath.Join(h.home, "config.yaml"), []byte("selector:\n  argv: [\"\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := h.run("load", "pilot", "--task", "seed")
	if r.code == 0 || !strings.Contains(r.err, "selector.argv[0] must not be empty") {
		t.Fatalf("empty argv must be rejected; code %d stderr %s", r.code, r.err)
	}
}
