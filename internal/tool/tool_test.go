package tool

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseRejects(t *testing.T) {
	cases := []struct {
		name, body, want string
	}{
		{"placeholder inside element", "description: d\nexec:\n  argv: [echo, \"pr={{ pr }}\"]\ninput:\n  pr: {required: true}\n", "entire element"},
		{"undeclared placeholder", "description: d\nexec:\n  argv: [echo, \"{{ pr }}\"]\n", "input.pr is not declared"},
		{"bad stdin mode", "description: d\nexec:\n  argv: [echo]\n  stdin: yaml\n", "exec.stdin"},
		{"missing description", "exec:\n  argv: [echo]\n", "description is required"},
		{"blank description", "description: \"  \"\nexec:\n  argv: [echo]\n", "description is required"},
		{"version 2", "version: 2\ndescription: d\nexec:\n  argv: [echo]\n", "version"},
		{"empty argv", "description: d\nexec:\n  argv: []\n", "exec.argv"},
		{"no exec", "description: d\n", "exec.argv"},
		{"empty argv element", "description: d\nexec:\n  argv: [echo, \"\"]\n", "empty strings"},
		{"bad timeout", "description: d\nexec:\n  argv: [echo]\n  timeout: soon\n", "exec.timeout"},
		{"negative timeout", "description: d\nexec:\n  argv: [echo]\n  timeout: -5s\n", "positive duration"},
		{"zero timeout", "description: d\nexec:\n  argv: [echo]\n  timeout: 0s\n", "positive duration"},
		{"bare zero timeout", "description: d\nexec:\n  argv: [echo]\n  timeout: 0\n", "positive duration"},
		{"not yaml", "description: [unclosed\n", "not valid YAML"},
		{"second document", "description: d\nexec: {argv: [true]}\n---\nignored: true\n", "exactly one"},
		{"malformed second document", "description: d\nexec: {argv: [true]}\n---\nnot: [valid\n", "not valid YAML"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse(c.body)
			if err == nil {
				t.Fatalf("Parse accepted %q", c.body)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q should mention %q", err.Error(), c.want)
			}
		})
	}
}

func TestParseAcceptsAndRoundTrips(t *testing.T) {
	body := "version: 1\ndescription: Fetch a PR\nexec:\n  argv: [\"artifacts/tool_12/pr.sh\", \"{{ pr }}\", \"{{pr}}\"]\n  stdin: json\n  timeout: 30s\n  adapter: local\n  retry: {count: 2}\ninput:\n  pr: {type: string, required: true, choices: [one, two], hint: preserve-me}\noutput:\n  format: json\nnotes: kept verbatim\n"
	d, err := Parse(body)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := d.Placeholders(); len(got) != 2 || got[0] != "pr" || got[1] != "pr" {
		t.Errorf("placeholders: %v", got)
	}
	if d.Extra["notes"] != "kept verbatim" {
		t.Errorf("unknown top-level key not preserved: %v", d.Extra)
	}
	if d.Exec.Extra["adapter"] != "local" || d.Input["pr"].Extra["hint"] != "preserve-me" {
		t.Errorf("unknown nested keys not parsed: exec=%v input=%v", d.Exec.Extra, d.Input["pr"].Extra)
	}
	out, err := MarshalBody(d)
	if err != nil {
		t.Fatalf("MarshalBody: %v", err)
	}
	d2, err := Parse(out)
	if err != nil {
		t.Fatalf("re-Parse of %q: %v", out, err)
	}
	if d2.Description != d.Description || len(d2.Exec.Argv) != 3 || d2.Exec.Stdin != "json" || d2.Exec.Timeout != "30s" || !d2.Input["pr"].Required || d2.Extra["notes"] != "kept verbatim" || d2.Exec.Extra["adapter"] != "local" || d2.Input["pr"].Extra["hint"] != "preserve-me" {
		t.Errorf("round trip lost data:\n%s", out)
	}
	if !strings.Contains(out, "choices:") || !strings.Contains(out, "retry:") {
		t.Errorf("round trip dropped structured nested extensions:\n%s", out)
	}
	// Absent version and stdin receive their defaults.
	if _, err := Parse("description: d\nexec:\n  argv: [echo]\n"); err != nil {
		t.Errorf("minimal body rejected: %v", err)
	}
}

func TestParseRejectsMechanicalTypeCoercion(t *testing.T) {
	cases := []struct {
		name, body, want string
	}{
		{"null document", "null\n", "mapping"},
		{"sequence document", "[]\n", "mapping"},
		{"scalar document", "tool\n", "mapping"},
		{"version zero", "version: 0\ndescription: d\nexec: {argv: [echo]}\n", "version"},
		{"version null", "version: null\ndescription: d\nexec: {argv: [echo]}\n", "version"},
		{"version quoted", "version: \"1\"\ndescription: d\nexec: {argv: [echo]}\n", "version"},
		{"description number", "description: 7\nexec: {argv: [echo]}\n", "description must be a string"},
		{"description null", "description: null\nexec: {argv: [echo]}\n", "description must be a string"},
		{"exec null", "description: d\nexec: null\n", "exec must be a mapping"},
		{"exec sequence", "description: d\nexec: []\n", "exec must be a mapping"},
		{"argv scalar", "description: d\nexec: {argv: echo}\n", "exec.argv"},
		{"argv null", "description: d\nexec: {argv: null}\n", "exec.argv"},
		{"argv number", "description: d\nexec: {argv: [echo, 7]}\n", "exec.argv[1] must be a string"},
		{"argv boolean", "description: d\nexec: {argv: [echo, true]}\n", "exec.argv[1] must be a string"},
		{"argv null element", "description: d\nexec: {argv: [echo, null]}\n", "exec.argv[1] must be a string"},
		{"stdin empty", "description: d\nexec: {argv: [echo], stdin: \"\"}\n", "exec.stdin"},
		{"stdin null", "description: d\nexec: {argv: [echo], stdin: null}\n", "exec.stdin"},
		{"stdin boolean", "description: d\nexec: {argv: [echo], stdin: false}\n", "exec.stdin"},
		{"timeout empty", "description: d\nexec: {argv: [echo], timeout: \"\"}\n", "exec.timeout"},
		{"timeout null", "description: d\nexec: {argv: [echo], timeout: null}\n", "exec.timeout"},
		{"timeout number", "description: d\nexec: {argv: [echo], timeout: 10}\n", "exec.timeout"},
		{"input null", "description: d\nexec: {argv: [echo]}\ninput: null\n", "input must be a mapping"},
		{"input declaration null", "description: d\nexec: {argv: [echo]}\ninput: {x: null}\n", "input.x must be a mapping"},
		{"required string", "description: d\nexec: {argv: [echo]}\ninput: {x: {required: \"true\"}}\n", "input.x.required must be a boolean"},
		{"required null", "description: d\nexec: {argv: [echo]}\ninput: {x: {required: null}}\n", "input.x.required must be a boolean"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(tc.body)
			if err == nil {
				t.Fatalf("Parse accepted:\n%s", tc.body)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q should mention %q", err, tc.want)
			}
		})
	}
}

func TestDecodeRejectsExplicitInvalidFieldsBeforeToolAddDefaults(t *testing.T) {
	// tool add decodes a partial body before it prepends the managed script.
	// Explicit invalid values must fail here, before omitempty or defaulting can
	// accidentally turn them into absent fields during the later marshal.
	for _, body := range []string{
		"version: 0\ndescription: d\n",
		"version: null\ndescription: d\n",
		"description: d\nexec: {stdin: \"\"}\n",
		"description: d\nexec: {stdin: null}\n",
		"description: d\nexec: {timeout: \"\"}\n",
		"description: d\nexec: {timeout: null}\n",
		"description: d\nexec: {argv: [7]}\n",
		"description: d\nexec: {argv: [true]}\n",
	} {
		if _, err := Decode(body); err == nil {
			t.Errorf("Decode accepted explicit invalid field:\n%s", body)
		}
	}
}

func TestAbsentFieldsReceiveDefaults(t *testing.T) {
	d, err := Parse("description: defaults\nexec:\n  argv: [/bin/echo]\nextension: {kept: true}\n")
	if err != nil {
		t.Fatal(err)
	}
	if d.Version != 1 {
		t.Errorf("absent version default = %d, want 1", d.Version)
	}
	if d.Exec.Stdin != "none" {
		t.Errorf("absent exec.stdin default = %q, want none", d.Exec.Stdin)
	}
	if d.Exec.Timeout != "" {
		t.Errorf("absent exec.timeout should retain the runtime-default sentinel, got %q", d.Exec.Timeout)
	}
	if ext, ok := d.Extra["extension"].(map[string]any); !ok || ext["kept"] != true {
		t.Errorf("unknown key lost while applying defaults: %#v", d.Extra)
	}

	// This is the tool-add --stdin shape: argv is allowed to be absent until
	// the managed script path is prepended, while defaults and unknown keys
	// survive the marshal that constructs the stored body.
	partial, err := Decode("description: partial\nexec:\n  adapter: local\nnotes: preserve-me\n")
	if err != nil {
		t.Fatal(err)
	}
	if partial.Exec.Stdin != "" {
		t.Fatalf("Decode materialized absent exec.stdin as %q", partial.Exec.Stdin)
	}
	partial.Exec.Argv = []string{"artifacts/tool_1/run.sh"}
	stored, err := MarshalBody(partial)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored, "stdin:") {
		t.Fatalf("tool-add round trip materialized absent exec.stdin:\n%s", stored)
	}
	roundTrip, err := Parse(stored)
	if err != nil {
		t.Fatalf("Parse stored partial definition: %v\n%s", err, stored)
	}
	if roundTrip.Version != 1 || roundTrip.Exec.Stdin != "none" || roundTrip.Exec.Extra["adapter"] != "local" || roundTrip.Extra["notes"] != "preserve-me" {
		t.Errorf("defaults or unknown keys lost:\n%s", stored)
	}
}

func TestStrictTypesApplyThroughYAMLMerge(t *testing.T) {
	valid := "description: merged\ncommon: &common\n  argv: [echo]\nexec:\n  <<: *common\n"
	d, err := Parse(valid)
	if err != nil {
		t.Fatalf("valid merged definition rejected: %v", err)
	}
	if len(d.Exec.Argv) != 1 || d.Exec.Argv[0] != "echo" || d.Exec.Stdin != "none" {
		t.Errorf("merged definition/defaults: %#v", d.Exec)
	}

	invalid := "description: merged\ncommon: &common\n  argv: [echo, 7]\nexec:\n  <<: *common\n"
	if _, err := Parse(invalid); err == nil || !strings.Contains(err.Error(), "exec.argv[1]") {
		t.Errorf("numeric merged argv should be rejected, got %v", err)
	}
}

func TestPlaceholder(t *testing.T) {
	for in, want := range map[string]string{"{{ pr }}": "pr", "{{pr}}": "pr", "{{ repo.id-x_1 }}": "repo.id-x_1", "pr={{ pr }}": "", "{{ PR }}": "", "plain": ""} {
		if got := Placeholder(in); got != want {
			t.Errorf("Placeholder(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestArgvSubstitution(t *testing.T) {
	body := "description: d\nexec:\n  argv: [\"artifacts/tool_1/x.sh\", \"{{ pr }}\", \"{{ price }}\", \"{{ flag }}\", \"{{ opt }}\", \"--\", \"{{ obj }}\", \"{{ list }}\", \"{{ s }}\"]\ninput:\n  pr: {type: integer, required: true}\n  price: {type: number}\n  flag: {type: boolean}\n  opt: {type: string}\n  obj: {type: object}\n  list: {type: array}\n  s: {type: string}\n"
	d, err := Parse(body)
	if err != nil {
		t.Fatal(err)
	}
	in, err := DecodeInput([]byte(`{"pr": 1842, "price": 1.50, "flag": true, "obj": {"a": [1, 2.0]}, "list": ["x", 3], "s": "--rm -rf"}`))
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join("/", "home", "nt")
	got := d.Argv(in, home)
	want := []string{filepath.Join(home, "artifacts", "tool_1", "x.sh"), "1842", "1.50", "true", "--", `{"a":[1,2.0]}`, `["x",3]`, "--rm -rf"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("argv:\n got %q\nwant %q", got, want)
	}
	// Absent optional placeholder is removed, not passed as "".
	for _, a := range got {
		if a == "" {
			t.Errorf("empty element in %q", got)
		}
	}
	// An element not under artifacts/ is untouched; artifacts/ with no home stays relative.
	d2, _ := Parse("description: d\nexec:\n  argv: [/bin/echo, artifacts/keep]\n")
	if g := d2.Argv(nil, home); g[0] != "/bin/echo" || g[1] != filepath.Join(home, "artifacts", "keep") {
		t.Errorf("argv: %q", g)
	}
	if g := d.Argv(in, ""); g[0] != "artifacts/tool_1/x.sh" {
		t.Errorf("empty home should leave the path relative: %q", g[0])
	}
}

// DESIGN §9: "Argv elements beginning with artifacts/ resolve relative to
// NINE_TAILS_HOME at call time" — every literal element, not only argv[0],
// so the interpreter-plus-script shape works from any cwd. Values supplied by
// the caller are verbatim and are never resolved.
func TestArgvResolvesArtifactsInAnyPosition(t *testing.T) {
	d, err := Parse("description: d\nexec:\n  argv: [/bin/sh, artifacts/tool_2/hi.sh, \"{{ x }}\", artifacts/tool_2/data.json, ./artifacts/rel, artifactsfoo]\ninput:\n  x: {}\n")
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join("/", "home", "nt")
	got := d.Argv(map[string]any{"x": "artifacts/from-input"}, home)
	want := []string{"/bin/sh", filepath.Join(home, "artifacts", "tool_2", "hi.sh"), "artifacts/from-input", filepath.Join(home, "artifacts", "tool_2", "data.json"), "./artifacts/rel", "artifactsfoo"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("argv:\n got %q\nwant %q", got, want)
	}
}

func TestRunInterpreterShapeFromAnyCwd(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "artifacts", "tool_2")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Not executable on purpose: /bin/sh reads it, it is only an argument.
	if err := os.WriteFile(filepath.Join(dir, "hi.sh"), []byte("echo \"hi $1\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir()) // somewhere with no artifacts/ directory
	d, err := Parse("description: interp\nexec:\n  argv: [/bin/sh, artifacts/tool_2/hi.sh, \"{{ x }}\"]\ninput:\n  x: {}\n")
	if err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	err = d.Run(Call{Home: home, Input: map[string]any{"x": "there"}, Stdout: &out, Stderr: &errb})
	if err != nil {
		t.Fatalf("Run: %v (stderr %q)", err, errb.String())
	}
	if out.String() != "hi there\n" {
		t.Errorf("stdout %q", out.String())
	}
	// Without a home the path stays relative and the shell cannot find it,
	// which is what the reviewer observed from cwd=/.
	out.Reset()
	err = d.Run(Call{Home: "", Input: map[string]any{"x": "there"}, Stdout: &out, Stderr: &errb})
	var ee *ExitError
	if !errors.As(err, &ee) || ee.Code != 127 {
		t.Errorf("unresolved path should fail in the shell with 127, got %v", err)
	}
}

func TestDecodeInput(t *testing.T) {
	m, err := DecodeInput(nil)
	if err != nil || len(m) != 0 {
		t.Errorf("empty input should be {}: %v %v", m, err)
	}
	if _, err := DecodeInput([]byte(`[1]`)); err == nil {
		t.Error("array accepted")
	}
	if _, err := DecodeInput([]byte(`{"a":`)); err == nil {
		t.Error("truncated JSON accepted")
	}
	m, err = DecodeInput([]byte(`{"n": 12345678901234567890}`))
	if err != nil || Stringify(m["n"]) != "12345678901234567890" {
		t.Errorf("big number literal not preserved: %v %v", m, err)
	}
	// Surrounding whitespace is fine.
	m, err = DecodeInput([]byte("  \n {\"v\": \"ok\"} \n\t"))
	if err != nil || m["v"] != "ok" {
		t.Errorf("padded object rejected: %v %v", m, err)
	}
}

// DESIGN §9: --input must be a JSON object. Anything after the first value
// is a quoting mistake by the caller and must be rejected, not swallowed.
func TestDecodeInputRejectsTrailingData(t *testing.T) {
	for _, raw := range []string{
		`{"v":"ok"} trailing garbage`,
		`{"v":"ok"}}`,
		`{"pr":1} {"pr":2}`,
		"{\"v\":\"ok\"} junk\n",
		`{"v":"ok"},`,
		`{} 1`,
		`[1] {}`,
	} {
		m, err := DecodeInput([]byte(raw))
		if err == nil {
			t.Errorf("DecodeInput(%q) accepted: %v", raw, m)
			continue
		}
		if !strings.Contains(err.Error(), "JSON object") {
			t.Errorf("DecodeInput(%q) error %q should mention JSON object", raw, err)
		}
	}
}

func TestValidateInput(t *testing.T) {
	d, _ := Parse("description: d\nexec:\n  argv: [echo, \"{{ pr }}\"]\ninput:\n  zeta: {required: true}\n  pr: {required: true}\n  repo: {required: true}\n  alpha: {required: true}\n  opt: {}\n")
	err := d.ValidateInput(map[string]any{"pr": "1"})
	if err == nil || err.Error() != "missing required input: alpha, repo, zeta" {
		t.Errorf("missing inputs must be deterministic, got %v", err)
	}
	if err := d.ValidateInput(map[string]any{"alpha": "a", "pr": "1", "repo": "r", "zeta": "z"}); err != nil {
		t.Errorf("unexpected: %v", err)
	}
}

func TestParseRejectsEscapingArtifactReferences(t *testing.T) {
	for _, ref := range []string{
		"artifacts/tool_1/../secret",
		"artifacts/tool_1/../../secret",
		"artifacts//tool_1/x.sh",
		"artifacts/tool_1/x.sh/",
		`artifacts/tool_1\..\secret`,
	} {
		body := "description: d\nexec:\n  argv: [/bin/sh, '" + ref + "']\n"
		if _, err := Parse(body); err == nil || !strings.Contains(err.Error(), "artifact path") {
			t.Errorf("Parse accepted escaping or noncanonical ref %q: %v", ref, err)
		}
	}
	if _, err := Parse("description: d\nexec:\n  argv: [/bin/sh, artifacts/tool_1/x.sh, artifacts/keep]\n"); err != nil {
		t.Errorf("clean artifact refs rejected: %v", err)
	}
}

// script writes an executable shell script into a temp dir.
func script(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "tool.sh")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func runDef(t *testing.T, body string, in map[string]any, env map[string]string) (string, string, error) {
	t.Helper()
	d, err := Parse(body)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	var out, errb bytes.Buffer
	err = d.Run(Call{Home: t.TempDir(), Input: in, Env: env, Stdout: &out, Stderr: &errb})
	return out.String(), errb.String(), err
}

func TestRunStdinModes(t *testing.T) {
	p := script(t, "printf 'stdin=%s;arg=%s\\n' \"$(cat)\" \"$1\"\n")
	in, _ := DecodeInput([]byte(`{"text": "hello", "pr": 7}`))
	for mode, want := range map[string]string{
		"json": `stdin={"pr":7,"text":"hello"};arg=7` + "\n",
		"text": "stdin=hello;arg=7\n",
		"none": "stdin=;arg=7\n",
	} {
		body := "description: d\nexec:\n  argv: [" + p + ", \"{{ pr }}\"]\n  stdin: " + mode + "\ninput:\n  pr: {}\n"
		out, errb, err := runDef(t, body, in, nil)
		if err != nil {
			t.Errorf("%s: %v (stderr %q)", mode, err, errb)
		}
		if out != want {
			t.Errorf("%s: got %q want %q", mode, out, want)
		}
	}
	// Only absence selects the default, which is closed stdin.
	out, errb, err := runDef(t, "description: d\nexec:\n  argv: ["+p+", \"{{ pr }}\"]\ninput:\n  pr: {}\n", in, nil)
	if err != nil || errb != "" || out != "stdin=;arg=7\n" {
		t.Errorf("absent stdin mode: out=%q stderr=%q err=%v", out, errb, err)
	}
	// Absent text key with stdin text → empty stdin.
	out, _, err = runDef(t, "description: d\nexec:\n  argv: ["+p+"]\n  stdin: text\n", map[string]any{}, nil)
	if err != nil || out != "stdin=;arg=\n" {
		t.Errorf("text without text key: %q %v", out, err)
	}
}

func TestRunEnvAndStderr(t *testing.T) {
	p := script(t, "echo \"agent=$NINE_TAILS_AGENT home=$NINE_TAILS_HOME\"\necho diag >&2\n")
	out, errb, err := runDef(t, "description: d\nexec:\n  argv: ["+p+"]\n", nil, map[string]string{"NINE_TAILS_AGENT": "a", "NINE_TAILS_HOME": "/h"})
	if err != nil {
		t.Fatal(err)
	}
	if out != "agent=a home=/h\n" || errb != "diag\n" {
		t.Errorf("out=%q err=%q", out, errb)
	}
}

func TestRunExitPassthrough(t *testing.T) {
	p := script(t, "echo partial\nexit 3\n")
	out, _, err := runDef(t, "description: d\nexec:\n  argv: ["+p+"]\n", nil, nil)
	var ee *ExitError
	if !errors.As(err, &ee) || ee.Code != 3 {
		t.Fatalf("want ExitError{3}, got %v", err)
	}
	if errors.Is(err, ErrStart) {
		t.Error("a nonzero exit is not a start failure")
	}
	if out != "partial\n" {
		t.Errorf("stdout before exit should stream through: %q", out)
	}
}

func TestRunMissingRequired(t *testing.T) {
	p := script(t, "true\n")
	_, _, err := runDef(t, "description: d\nexec:\n  argv: ["+p+", \"{{ pr }}\"]\ninput:\n  pr: {required: true}\n", map[string]any{}, nil)
	if err == nil || !strings.Contains(err.Error(), "missing required input: pr") {
		t.Errorf("want missing-input error, got %v", err)
	}
	if errors.Is(err, ErrStart) || errors.As(err, new(*ExitError)) {
		t.Errorf("missing input must not look like a tool failure: %v", err)
	}
}

func TestRunCannotStart(t *testing.T) {
	_, _, err := runDef(t, "description: d\nexec:\n  argv: [/nonexistent/nine-tails-tool]\n", nil, nil)
	if !errors.Is(err, ErrStart) {
		t.Errorf("want ErrStart, got %v", err)
	}
}

func TestRunTimeout(t *testing.T) {
	p := script(t, "exec sleep 1\n")
	start := time.Now()
	_, _, err := runDef(t, "description: d\nexec:\n  argv: ["+p+"]\n  timeout: 100ms\n", nil, nil)
	if !errors.Is(err, ErrStart) {
		t.Errorf("timeout should be ErrStart, got %v", err)
	}
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Errorf("message should say timed out: %v", err)
	}
	if time.Since(start) > 900*time.Millisecond {
		t.Errorf("timeout took %s; the process was not killed", time.Since(start))
	}
}
