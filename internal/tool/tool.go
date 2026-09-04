// Package tool parses, validates and executes YAML tool definitions
// (spec §13). Only the mechanically required fields are validated; unknown
// keys are preserved for agents to read.
package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Exec is the executable part of a tool body.
type Exec struct {
	Argv    []string       `yaml:"argv" json:"argv"`
	Stdin   string         `yaml:"stdin,omitempty" json:"stdin,omitempty"`     // none | json | text
	Timeout string         `yaml:"timeout,omitempty" json:"timeout,omitempty"` // Go duration, default 60s
	Extra   map[string]any `yaml:",inline" json:"-"`
}

// Input declares one input parameter. Type is informational; only Required
// is enforced.
type Input struct {
	Type     string         `yaml:"type,omitempty" json:"type,omitempty"`
	Required bool           `yaml:"required,omitempty" json:"required,omitempty"`
	Extra    map[string]any `yaml:",inline" json:"-"`
}

// Definition is the parsed tool body. Extra holds unknown top-level keys.
type Definition struct {
	Version     int              `yaml:"version,omitempty" json:"version,omitempty"`
	Description string           `yaml:"description" json:"description"`
	Exec        Exec             `yaml:"exec" json:"exec"`
	Input       map[string]Input `yaml:"input,omitempty" json:"input,omitempty"`
	Output      map[string]any   `yaml:"output,omitempty" json:"output,omitempty"`
	Extra       map[string]any   `yaml:",inline" json:"-"`
}

var placeholderRe = regexp.MustCompile(`^\{\{\s*([a-z0-9_.-]+)\s*\}\}$`)

// Placeholder returns the input name if the argv element is a whole
// placeholder, else "".
func Placeholder(elem string) string {
	if m := placeholderRe.FindStringSubmatch(elem); m != nil {
		return m[1]
	}
	return ""
}

// Decode parses exactly one YAML or JSON tool document without applying the
// mechanical tool validation. tool add uses it before prepending its managed
// artifact path; ordinary callers should use Parse.
func Decode(body string) (*Definition, error) {
	var d Definition
	dec := yaml.NewDecoder(strings.NewReader(body))
	if err := dec.Decode(&d); err != nil {
		return nil, fmt.Errorf("not valid YAML: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err == nil {
		return nil, errors.New("tool body must contain exactly one YAML or JSON document")
	} else if !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("not valid YAML: %w", err)
	}
	return &d, nil
}

// Parse parses and validates a tool body.
func Parse(body string) (*Definition, error) {
	d, err := Decode(body)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(d.Description) == "" {
		return nil, errors.New("description is required")
	}
	if d.Version != 0 && d.Version != 1 {
		return nil, fmt.Errorf("version must be absent or 1 (got %d)", d.Version)
	}
	if len(d.Exec.Argv) == 0 {
		return nil, errors.New("exec.argv must be a non-empty list")
	}
	for _, a := range d.Exec.Argv {
		if a == "" {
			return nil, errors.New("exec.argv must not contain empty strings")
		}
		if err := ValidateArtifactRef(a); err != nil {
			return nil, fmt.Errorf("exec.argv: %w", err)
		}
	}
	switch d.Exec.Stdin {
	case "", "none", "json", "text":
	default:
		return nil, fmt.Errorf("exec.stdin must be none, json or text (got %q)", d.Exec.Stdin)
	}
	if d.Exec.Timeout != "" {
		td, err := time.ParseDuration(d.Exec.Timeout)
		if err != nil {
			return nil, fmt.Errorf("exec.timeout: %v", err)
		}
		if td <= 0 {
			return nil, fmt.Errorf("exec.timeout must be a positive duration (got %s); omit it for the 60s default", d.Exec.Timeout)
		}
	}
	// Placeholders must each be a whole argv element and must be declared.
	for _, a := range d.Exec.Argv {
		if strings.Contains(a, "{{") && Placeholder(a) == "" {
			return nil, fmt.Errorf("argv element %q: a placeholder must be the entire element (nine-tails never splits or interpolates inside an argument)", a)
		}
		if name := Placeholder(a); name != "" {
			if _, ok := d.Input[name]; !ok {
				return nil, fmt.Errorf("argv references {{ %s }} but input.%s is not declared", name, name)
			}
		}
	}
	return d, nil
}

// IsArtifactRef reports whether an argv element is a managed artifact
// reference. Only literal argv elements are inspected; substituted input
// values are never treated as artifact paths.
func IsArtifactRef(s string) bool { return strings.HasPrefix(s, "artifacts/") }

// ValidateArtifactRef rejects managed references that are not clean relative
// paths below artifacts/. Besides keeping bundle reads and writes inside the
// store, this makes call-time path resolution safe on every platform.
// Non-artifact argv elements are outside this convention and are accepted.
func ValidateArtifactRef(s string) error {
	if !IsArtifactRef(s) {
		return nil
	}
	if strings.ContainsRune(s, '\x00') || strings.Contains(s, `\`) || path.Clean(s) != s {
		return fmt.Errorf("artifact path %q must be a clean relative path below artifacts/", s)
	}
	parts := strings.Split(s, "/")
	if len(parts) < 2 || parts[1] == "" {
		return fmt.Errorf("artifact path %q must name a path below artifacts/", s)
	}
	for _, part := range parts[1:] {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("artifact path %q must be a clean relative path below artifacts/", s)
		}
	}
	return nil
}

// Placeholders returns the input names referenced by argv, in order.
func (d *Definition) Placeholders() []string {
	var out []string
	for _, a := range d.Exec.Argv {
		if name := Placeholder(a); name != "" {
			out = append(out, name)
		}
	}
	return out
}

// ValidateInput checks that every required input is present.
func (d *Definition) ValidateInput(in map[string]any) error {
	var missing []string
	for name, decl := range d.Input {
		if _, ok := in[name]; !ok && decl.Required {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("missing required input: %s", strings.Join(missing, ", "))
	}
	return nil
}

// Stringify renders an input value as a single argv element: strings
// verbatim, numbers as their JSON literal, booleans true/false, objects and
// arrays as compact JSON.
func Stringify(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case json.Number:
		return t.String()
	case bool:
		if t {
			return "true"
		}
		return "false"
	case nil:
		return ""
	default:
		b, _ := json.Marshal(t)
		return string(b)
	}
}

// DecodeInput parses a JSON object with UseNumber so numbers keep their
// literal text.
func DecodeInput(raw []byte) (map[string]any, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return map[string]any{}, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("input is not valid JSON: %v", err)
	}
	// Anything after the first value (a second object, a stray brace, junk)
	// means the caller's quoting went wrong; never swallow it silently.
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, errors.New("input must be exactly one JSON object: unexpected data after it")
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, errors.New("input must be a JSON object")
	}
	return m, nil
}

// Call describes one execution.
type Call struct {
	Home   string            // NINE_TAILS_HOME, for resolving artifacts/ paths
	Input  map[string]any    // decoded JSON input (UseNumber)
	Env    map[string]string // extra environment
	Stdout io.Writer
	Stderr io.Writer
}

// ErrStart is returned when the executable could not be started at all
// (as opposed to running and exiting nonzero), or timed out.
var ErrStart = errors.New("could not run tool")

// ExitError carries a tool's nonzero exit status.
type ExitError struct{ Code int }

func (e *ExitError) Error() string { return fmt.Sprintf("tool exited with status %d", e.Code) }

// Argv builds the final argument vector for an input: each placeholder
// element becomes the stringified value, an element whose (optional) input is
// absent is removed, and every literal element beginning with artifacts/
// (in any position, e.g. [/bin/sh, artifacts/tool_2/hi.sh]) is resolved
// against home so a tool runs from any working directory. Substituted input
// values are passed verbatim and never resolved.
func (d *Definition) Argv(in map[string]any, home string) []string {
	argv := make([]string, 0, len(d.Exec.Argv))
	for _, a := range d.Exec.Argv {
		if name := Placeholder(a); name != "" {
			v, ok := in[name]
			if !ok {
				continue
			}
			argv = append(argv, Stringify(v))
			continue
		}
		if home != "" && strings.HasPrefix(a, "artifacts/") {
			a = filepath.Join(home, a)
		}
		argv = append(argv, a)
	}
	return argv
}

// Run executes the tool without a shell, feeding stdin per the declared mode
// and streaming stdout/stderr through.
func (d *Definition) Run(c Call) error {
	if err := d.ValidateInput(c.Input); err != nil {
		return err
	}
	argv := d.Argv(c.Input, c.Home)
	if len(argv) == 0 {
		return fmt.Errorf("%w: argv is empty after substitution", ErrStart)
	}
	timeout := 60 * time.Second
	if t, err := time.ParseDuration(d.Exec.Timeout); err == nil && t > 0 {
		timeout = t
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = os.Environ()
	for k, v := range c.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	switch d.Exec.Stdin {
	case "json":
		raw, _ := json.Marshal(c.Input)
		cmd.Stdin = bytes.NewReader(raw)
	case "text":
		s, _ := c.Input["text"].(string)
		cmd.Stdin = strings.NewReader(s)
	}
	cmd.Stdout = c.Stdout
	cmd.Stderr = c.Stderr
	err := cmd.Run()
	if err == nil {
		return nil
	}
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("%w: timed out after %s", ErrStart, timeout)
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return &ExitError{Code: ee.ExitCode()}
	}
	return fmt.Errorf("%w: %v", ErrStart, err)
}

// MarshalBody renders a definition back to YAML (used by `tool add`).
func MarshalBody(d *Definition) (string, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(d); err != nil {
		return "", err
	}
	_ = enc.Close()
	return buf.String(), nil
}
