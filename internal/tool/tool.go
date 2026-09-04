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
	"os/signal"
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
	Stdin   string         `yaml:"stdin,omitempty" json:"stdin,omitempty"`     // none | json | text, default none
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

// Decode parses exactly one YAML or JSON tool document. It validates the
// types and values of mechanical fields that are present, but permits fields
// such as description and exec.argv to be absent so tool add can prepend its
// managed artifact path before applying the complete validation in Parse.
// The version default is materialized for tool add; an absent stdin stays
// absent so Decode followed by MarshalBody preserves what the author wrote.
func Decode(body string) (*Definition, error) {
	dec := yaml.NewDecoder(strings.NewReader(body))
	var doc yaml.Node
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("not valid YAML: %w", err)
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); err == nil {
		return nil, errors.New("tool body must contain exactly one YAML or JSON document")
	} else if !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("not valid YAML: %w", err)
	}

	// Decode once into generic values before the convenience struct. yaml.v3
	// deliberately converts scalars when decoding into typed Go fields (for
	// example, an integer argv element into a string). Mechanical tool fields
	// must retain the types the author actually wrote, so reject such coercions.
	// Generic decoding also resolves YAML aliases and merge keys, making these
	// checks apply to the effective definition.
	var raw any
	if err := doc.Decode(&raw); err != nil {
		return nil, fmt.Errorf("not valid YAML: %w", err)
	}
	root, ok := raw.(map[string]any)
	if !ok {
		return nil, errors.New("tool body must be a YAML or JSON mapping")
	}
	if err := validatePresentFields(root); err != nil {
		return nil, err
	}

	var d Definition
	if err := doc.Decode(&d); err != nil {
		return nil, fmt.Errorf("not valid YAML: %w", err)
	}
	if _, present := root["version"]; !present {
		d.Version = 1
	}
	return &d, nil
}

// validatePresentFields rejects type coercion for every field that affects
// execution. Required fields are checked later by Parse because tool add is
// allowed to supply argv after Decode.
func validatePresentFields(root map[string]any) error {
	if raw, present := root["version"]; present {
		version, ok := raw.(int)
		if !ok || version != 1 {
			return fmt.Errorf("version must be integer 1 when present (got %v)", raw)
		}
	}
	if raw, present := root["description"]; present {
		if _, ok := raw.(string); !ok {
			return errors.New("description must be a string")
		}
	}

	rawExec, present := root["exec"]
	if present {
		execMap, ok := rawExec.(map[string]any)
		if !ok {
			return errors.New("exec must be a mapping")
		}
		if raw, argvPresent := execMap["argv"]; argvPresent {
			argv, ok := raw.([]any)
			if !ok {
				return errors.New("exec.argv must be a list of strings")
			}
			for i, elem := range argv {
				if _, ok := elem.(string); !ok {
					return fmt.Errorf("exec.argv[%d] must be a string", i)
				}
			}
		}
		if raw, stdinPresent := execMap["stdin"]; stdinPresent {
			stdin, ok := raw.(string)
			if !ok {
				return errors.New("exec.stdin must be a string: none, json or text")
			}
			switch stdin {
			case "none", "json", "text":
			default:
				return fmt.Errorf("exec.stdin must be none, json or text (got %q)", stdin)
			}
		}
		if raw, timeoutPresent := execMap["timeout"]; timeoutPresent {
			timeout, ok := raw.(string)
			if !ok {
				return errors.New("exec.timeout must be a positive duration string")
			}
			duration, err := time.ParseDuration(timeout)
			if err != nil {
				return fmt.Errorf("exec.timeout: %v", err)
			}
			if duration <= 0 {
				return fmt.Errorf("exec.timeout must be a positive duration (got %q); omit it for the 60s default", timeout)
			}
		}
	}

	// Input declarations determine whether execution may omit a value, so the
	// declaration shape and required flag are mechanical too. Other declaration
	// fields (such as type) remain informational and are not restricted here.
	if rawInput, present := root["input"]; present {
		input, ok := rawInput.(map[string]any)
		if !ok {
			return errors.New("input must be a mapping")
		}
		for name, rawDecl := range input {
			decl, ok := rawDecl.(map[string]any)
			if !ok {
				return fmt.Errorf("input.%s must be a mapping", name)
			}
			if required, present := decl["required"]; present {
				if _, ok := required.(bool); !ok {
					return fmt.Errorf("input.%s.required must be a boolean", name)
				}
			}
		}
	}
	return nil
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
	if d.Version != 1 {
		return nil, fmt.Errorf("version must be 1 (got %d)", d.Version)
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
	// Decode has already rejected an explicitly empty or null stdin. At this
	// point an empty value therefore means the field was genuinely absent.
	if d.Exec.Stdin == "" {
		d.Exec.Stdin = "none"
	}
	switch d.Exec.Stdin {
	case "none", "json", "text":
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

// toolWaitDelay bounds the extra time Cmd.Wait may spend after cancellation
// on a descendant that inherited one of the command's I/O descriptors. Normal
// descendants are killed with the command where the platform supports it; the
// delay is a backstop for descendants that escape that mechanism.
const toolWaitDelay = 100 * time.Millisecond

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

// Interrupted reports that nine-tails itself received a termination signal
// while the tool ran. Run has already forwarded that signal to the tool's
// process group and waited for the tool; the caller decides how to exit.
type Interrupted struct{ Signal os.Signal }

func (e *Interrupted) Error() string { return fmt.Sprintf("interrupted by %v", e.Signal) }

// ExitCode is the shell convention for a process ended by e.Signal
// (128 + signal number).
func (e *Interrupted) ExitCode() int { return signalExitCode(e.Signal) }

// Run executes the tool without a shell, feeding stdin per the declared mode
// and streaming stdout/stderr through. The tool runs in its own process group
// where the platform has them, so a timeout ends its descendants too. Because
// that group is not the terminal's, a termination signal delivered to
// nine-tails while the tool runs is forwarded to the group and reported as
// *Interrupted; a second signal kills the group outright.
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
	configureProcessTreeCancellation(cmd)
	cmd.WaitDelay = toolWaitDelay
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

	// Subscribe before Start so a signal landing during startup is not lost;
	// the forwarder only acts once the process exists.
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, forwardedSignals...)
	defer signal.Stop(signals)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%w: %v", ErrStart, err)
	}
	done := make(chan struct{})
	forwarded := make(chan os.Signal, 1)
	go func() {
		var first os.Signal
		for {
			select {
			case sig := <-signals:
				if first == nil {
					first = sig
					forwarded <- sig
					forwardSignal(cmd, sig)
				} else {
					_ = terminateProcessTree(cmd)
				}
			case <-done:
				return
			}
		}
	}()
	err := cmd.Wait()
	close(done)
	select {
	case sig := <-forwarded:
		return &Interrupted{Signal: sig}
	default:
	}
	if err == nil {
		return nil
	}
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("%w: timed out after %s", ErrStart, timeout)
	}
	// WaitDelay is an I/O backstop, not a failure of an executable that has
	// already exited successfully. It most commonly fires when a background
	// descendant inherited stdout or stderr. Preserve the direct executable's
	// successful result and clean up its process group where the platform lets
	// us, so that descendant does not continue running after the call returns.
	if errors.Is(err, exec.ErrWaitDelay) && cmd.ProcessState != nil && cmd.ProcessState.Success() {
		_ = terminateProcessTree(cmd)
		return nil
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
