// Package cli holds the small shared pieces every command needs: exit codes,
// the error type that carries them, and output helpers. Data goes to stdout,
// diagnostics to stderr, never interactive, never colored.
package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Exit codes (spec §16.4).
const (
	ExitOK       = 0
	ExitInvalid  = 2 // invalid invocation or input
	ExitNotFound = 3 // agent, record, tool, or signal not found
	ExitStore    = 4 // store unavailable or transaction failed
	ExitTool     = 5 // tool or external adapter failed
	// 6 is unused: capsules are never cut for size (DESIGN §7).
	ExitConflict = 7 // compare-and-swap or lease conflict
)

// ExitError is an error that knows its process exit code.
type ExitError struct {
	Code int
	Msg  string
}

func (e *ExitError) Error() string { return e.Msg }

// DetailedError carries diagnostic output that must be printed only after the
// standard nine-tails error summary. External tools and adapters can write to
// stderr before they fail; retaining that output here preserves the CLI's
// invariant that the first diagnostic line always starts with "nine-tails:".
type DetailedError struct {
	Err     error
	Details string
}

func (e *DetailedError) Error() string { return e.Err.Error() }
func (e *DetailedError) Unwrap() error { return e.Err }

// WithDetails attaches deferred stderr to err. Empty diagnostics leave err
// unchanged so callers can use this unconditionally.
func WithDetails(err error, details []byte) error {
	if err == nil || len(details) == 0 {
		return err
	}
	return &DetailedError{Err: err, Details: string(details)}
}

// DetailsOf returns deferred diagnostic output attached anywhere in err's
// unwrap chain.
func DetailsOf(err error) string {
	var de *DetailedError
	if errors.As(err, &de) {
		return de.Details
	}
	return ""
}

// WriteDetails writes diagnostic text as two-space-indented detail lines.
// SplitAfter preserves the adapter's line endings and does not invent a final
// newline when it did not write one.
func WriteDetails(w io.Writer, details string) {
	for _, line := range strings.SplitAfter(details, "\n") {
		if line != "" {
			_, _ = io.WriteString(w, "  "+line)
		}
	}
}

// WriteErrorSummary writes the required prefixed first line and ensures every
// continuation line is visibly a detail. Cobra sometimes returns multiline
// errors whose suggestion lines start with a tab or no indentation at all.
func WriteErrorSummary(w io.Writer, msg string) {
	lines := strings.Split(strings.TrimRight(msg, "\n"), "\n")
	fmt.Fprintf(w, "nine-tails: %s\n", lines[0])
	for _, line := range lines[1:] {
		if strings.HasPrefix(line, "  ") {
			fmt.Fprintln(w, line)
		} else {
			fmt.Fprintln(w, "  "+line)
		}
	}
}

// Errorf builds an ExitError with the given code.
func Errorf(code int, format string, args ...any) error {
	return &ExitError{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// Invalid is Errorf(ExitInvalid, ...).
func Invalid(format string, args ...any) error { return Errorf(ExitInvalid, format, args...) }

// NotFound is Errorf(ExitNotFound, ...).
func NotFound(format string, args ...any) error { return Errorf(ExitNotFound, format, args...) }

// Conflict is Errorf(ExitConflict, ...).
func Conflict(format string, args ...any) error { return Errorf(ExitConflict, format, args...) }

// ToolFailed is Errorf(ExitTool, ...).
func ToolFailed(format string, args ...any) error { return Errorf(ExitTool, format, args...) }

// CodeOf returns the exit code for err: an ExitError's own code, or
// ExitStore for anything else non-nil, or ExitOK for nil.
func CodeOf(err error) int {
	if err == nil {
		return ExitOK
	}
	var ee *ExitError
	if errors.As(err, &ee) {
		return ee.Code
	}
	return ExitStore
}

// Report writes err to stderr as one line, and when jsonOut is true also
// writes {"error": msg, "code": n} to stdout so structured callers can parse
// it. Returns the exit code.
func Report(err error, stderr, stdout io.Writer, jsonOut bool) int {
	code := CodeOf(err)
	if err == nil {
		return code
	}
	WriteErrorSummary(stderr, err.Error())
	if details := DetailsOf(err); details != "" {
		WriteDetails(stderr, details)
	}
	if jsonOut {
		b, _ := json.Marshal(map[string]any{"error": err.Error(), "code": code})
		fmt.Fprintln(stdout, string(b))
	}
	return code
}
