package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/scottmeyer/nine-tails/internal/cli"
)

// harness runs the CLI in-process against a temp home. Every test gets a
// fresh store. Use h.run("load", "x") and assert on code/out/err.
type harness struct {
	t    *testing.T
	home string
	now  time.Time
}

type result struct {
	code int
	out  string
	err  string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	return &harness{t: t, home: t.TempDir(), now: time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)}
}

func (h *harness) command(tail string) string { return cli.StoreCommand(h.home, tail) }

func (h *harness) runIn(stdin string, args ...string) result {
	h.t.Helper()
	var out, errb bytes.Buffer
	a := &app{stdout: &out, stderr: &errb, stdin: strings.NewReader(stdin), now: func() time.Time { return h.now }, home: h.home}
	code := run(a, args)
	return result{code: code, out: out.String(), err: errb.String()}
}

func (h *harness) run(args ...string) result { return h.runIn("", args...) }

// ok runs and fails the test on a nonzero exit.
func (h *harness) ok(args ...string) result {
	h.t.Helper()
	r := h.run(args...)
	if r.code != 0 {
		h.t.Fatalf("nine-tails %s: exit %d\nstdout: %s\nstderr: %s", strings.Join(args, " "), r.code, r.out, r.err)
	}
	return r
}

func (h *harness) okIn(stdin string, args ...string) result {
	h.t.Helper()
	r := h.runIn(stdin, args...)
	if r.code != 0 {
		h.t.Fatalf("nine-tails %s: exit %d\nstdout: %s\nstderr: %s", strings.Join(args, " "), r.code, r.out, r.err)
	}
	return r
}

// id returns the id from a --format id result (or the "id" field of JSON).
func (r result) id(t *testing.T) string {
	t.Helper()
	s := strings.TrimSpace(r.out)
	if strings.HasPrefix(s, "{") {
		var m map[string]any
		if err := json.Unmarshal([]byte(s), &m); err != nil {
			t.Fatalf("not json: %s", s)
		}
		if id, ok := m["id"].(string); ok {
			return id
		}
		if id, ok := m["context_id"].(string); ok {
			return id
		}
		t.Fatalf("no id in %s", s)
	}
	return s
}

func (r result) json(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(r.out), &m); err != nil {
		t.Fatalf("not json: %v\n%s", err, r.out)
	}
	return m
}

func contextID(t *testing.T, md string) string {
	t.Helper()
	const marker = "[nine-tails-context="
	i := strings.Index(md, marker)
	if i < 0 {
		t.Fatalf("no context marker in capsule:\n%s", md)
	}
	rest := md[i+len(marker):]
	return rest[:strings.Index(rest, "]")]
}

func localRef(t *testing.T, h *harness, id string) string {
	t.Helper()
	return h.ok("inspect", id, "--format", "json").json(t)["ref"].(string)
}

func TestSmokeBaseAndLoad(t *testing.T) {
	h := newHarness(t)
	r := h.run("load", "nobody")
	if r.code != 3 {
		t.Errorf("missing agent should be exit 3, got %d: %s", r.code, r.err)
	}
	h.ok("base", "pr-review", "--meta", "title=PR Review Agent", "Review proposed changes for correctness.")
	r = h.ok("load", "pr-review", "--task", "Review PR 1", "--meta", "repo-id=my_repo")
	if !strings.HasPrefix(r.out, "# PR Review Agent\n\n[nine-tails-context=ctx_") {
		t.Errorf("capsule:\n%s", r.out)
	}
	ctx := contextID(t, r.out)
	h.ok("prefer", "pr-review", "--context", ctx, "Lead with evidence.")
	r = h.ok("load", "pr-review")
	if !strings.Contains(r.out, "(prefer) Lead with evidence.") {
		t.Errorf("correction should appear on next load:\n%s", r.out)
	}
	// origin recorded, scope not inherited
	r = h.ok("inspect", "pr-review", "--lane", "guidance", "--format", "json")
	if !strings.Contains(r.out, `"origin_context": "`+ctx+`"`) {
		t.Errorf("origin missing:\n%s", r.out)
	}
	if strings.Contains(r.out, "my_repo") {
		t.Errorf("ambient metadata must not become scope:\n%s", r.out)
	}
}

func TestErrorsGoToStderrAndJSON(t *testing.T) {
	h := newHarness(t)
	r := h.run("inspect", "nobody", "--format", "json")
	if r.code != 3 || !strings.HasPrefix(r.err, "nine-tails: ") || !strings.Contains(r.out, `"code": 3`) {
		t.Errorf("code=%d out=%q err=%q", r.code, r.out, r.err)
	}
	r = h.run("bogus-command")
	if r.code != 2 {
		t.Errorf("unknown command should be exit 2, got %d", r.code)
	}
	r = h.run("lod")
	if r.code != 2 || !strings.Contains(r.err, "Did you mean this?") {
		t.Fatalf("near command should include Cobra suggestion: code=%d stderr=%q", r.code, r.err)
	}
	lines := strings.Split(strings.TrimSuffix(r.err, "\n"), "\n")
	for i, line := range lines[1:] {
		if !strings.HasPrefix(line, "  ") {
			t.Errorf("suggestion detail line %d is not indented: %q", i+2, line)
		}
	}
	r = h.run("note", "a")
	if r.code != 2 {
		t.Errorf("missing text should be exit 2, got %d: %s", r.code, r.err)
	}
}

func TestContextAndStdinAcceptsOnlyMatchingExplicitAgent(t *testing.T) {
	for _, command := range [][]string{{"note"}, {"avoid"}, {"prefer"}, {"remember"}, {"append", "--lane", "guidance"}, {"append", "--lane", "recall"}} {
		t.Run(strings.Join(command, "-"), func(t *testing.T) {
			h := newHarness(t)
			h.ok("base", "a", "Base.")
			h.ok("base", "b", "Other.")
			ctx := contextID(t, h.ok("load", "a").out)
			body := "Lead with evidence."
			args := append(append([]string{}, command...), "a", "--context", localRef(t, h, ctx), "--stdin", "--meta", "repo-id=original", "--format", "json")
			rec := h.okIn(body, args...).json(t)
			if rec["agent"] != "a" || rec["origin_context"] != ctx || rec["body"] != body {
				t.Fatalf("matching explicit agent changed stdin/origin semantics: %#v", rec)
			}
			correctionArgs := append(append([]string{}, command...), "a", "--context", ctx, "--stdin", "--supersedes", rec["id"].(string), "--format", "json")
			corrected := h.okIn("Corrected body.", correctionArgs...).json(t)
			if corrected["body"] != "Corrected body." || corrected["origin_context"] != ctx || !reflect.DeepEqual(corrected["meta"], map[string]any{"repo-id": []any{"original"}}) {
				t.Fatalf("matching-agent stdin correction changed body, origin, or omitted scope: %#v", corrected)
			}

			before := h.ok("inspect", "a", "--format", "json").out
			for _, bad := range [][]string{{"b"}, {"a", "not-text"}} {
				badArgs := append(append([]string{}, command...), bad...)
				badArgs = append(badArgs, "--context", ctx, "--stdin")
				r := h.runIn("must not persist", badArgs...)
				if r.code != 2 {
					t.Fatalf("%v accepted: %#v", bad, r)
				}
			}
			if after := h.ok("inspect", "a", "--format", "json").out; after != before {
				t.Fatalf("rejected context/stdin invocation wrote a record:\nbefore=%s\nafter=%s", before, after)
			}
		})
	}
}

func TestContextAndStdinExtraArgsPreflightBeforeLocalReferenceResolution(t *testing.T) {
	for _, invalidConfig := range []bool{false, true} {
		t.Run(fmt.Sprintf("invalid_config_%t", invalidConfig), func(t *testing.T) {
			h := newHarness(t)
			if invalidConfig {
				if err := os.WriteFile(filepath.Join(h.home, "config.yaml"), []byte("not: [valid"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			r := h.runIn("must not persist", "note", "a", "not-text", "--context", "@123", "--stdin")
			if r.code != 2 || !strings.Contains(r.err, "at most one matching") || strings.Contains(r.err, "config") || strings.Contains(r.err, "reference") {
				t.Fatalf("extra args were masked by local-reference resolution: %#v", r)
			}
			if _, err := os.Stat(filepath.Join(h.home, "nine-tails.db")); !os.IsNotExist(err) {
				t.Fatalf("syntax preflight opened the store: %v", err)
			}
			if !invalidConfig {
				entries, err := os.ReadDir(h.home)
				if err != nil || len(entries) != 0 {
					t.Fatalf("syntax preflight touched absent home: %v %v", entries, err)
				}
			}
		})
	}
}

func TestContextTextAfterDashKeepsLeadingDash(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "a", "Base.")
	ctx := contextID(t, h.ok("load", "a").out)
	rec := h.ok("note", "a", "--context", ctx, "--format", "json", "--", "-leading-text").json(t)
	if rec["agent"] != "a" || rec["origin_context"] != ctx || rec["body"] != "-leading-text" {
		t.Fatalf("context text after -- changed: %#v", rec)
	}
}

func TestStateCAS(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "a", "Base.")
	r := h.run("state", "put", "a/working", "status: new")
	if r.code != 2 {
		t.Errorf("--expect required: %d %s", r.code, r.err)
	}
	r = h.ok("state", "put", "a/working", "--expect", "none", "--format", "id", "status: new")
	s1 := r.id(t)
	r = h.run("state", "put", "a/working", "--expect", "none", "status: again")
	if r.code != 7 {
		t.Errorf("expect none on existing should be 7, got %d", r.code)
	}
	r = h.run("state", "put", "a/working", "--expect", "state_999", "status: again")
	if r.code != 7 {
		t.Errorf("wrong expect should be 7, got %d", r.code)
	}
	r = h.run("state", "put", "a/working", "--expect", s1, "not: [valid")
	if r.code != 2 {
		t.Errorf("invalid yaml should be 2, got %d", r.code)
	}
	for _, body := range []string{"status: first\n---\nstatus: ignored", "status: first\n---\nnot: [valid"} {
		r = h.runIn(body, "state", "put", "a/working", "--expect", s1, "--stdin")
		if r.code != 2 {
			t.Errorf("multi-document state should be 2, got %d: %s", r.code, r.err)
		}
	}
	r = h.run("state", "put", "a/working", "--expect", s1, strings.Repeat("x", 9000))
	if r.code != 2 {
		t.Errorf("oversize should be 2, got %d", r.code)
	}
	r = h.okIn("status: waiting\nnext: recheck\n", "state", "put", "a/working", "--expect", s1, "--stdin", "--format", "id")
	s2 := r.id(t)
	r = h.ok("state", "get", "a/working")
	if r.out != "status: waiting\nnext: recheck\n" {
		t.Errorf("state get body: %q", r.out)
	}
	if !strings.Contains(r.err, s2) {
		t.Errorf("state get should name the current id on stderr: %q", r.err)
	}
	r = h.ok("load", "a")
	if !strings.Contains(r.out, "## Current state (a/working, "+localRef(t, h, s2)+")\n\n```yaml\nstatus: waiting\nnext: recheck\n```") {
		t.Errorf("state not in capsule:\n%s", r.out)
	}
	r = h.ok("inspect", s1)
	if !strings.Contains(r.out, `"status": "superseded"`) {
		t.Errorf("old version should remain inspectable: %s", r.out)
	}
}
