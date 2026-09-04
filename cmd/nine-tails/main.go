// nine-tails: a small, harness-independent CLI sidecar for persistent agent
// context. See DESIGN.md and lore-sidecar-spec-v0.3.md.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/scottmeyer/nine-tails/internal/cli"
	"github.com/scottmeyer/nine-tails/internal/store"
)

var version = "0.1.0"

// app carries per-invocation state shared by commands.
type app struct {
	stdout   io.Writer
	stderr   io.Writer
	stdin    io.Reader
	now      func() time.Time
	home     string // resolved home; tests set it directly
	homeFlag string // --home, applied only when non-empty
	cfg      cli.Config
	st       *store.Store
}

// open lazily opens the store and config. Commands call it at the start of
// their RunE so that --help and argument errors never touch the database.
func (a *app) open() error {
	if a.st != nil {
		return nil
	}
	store.Clock = a.now
	home := a.home
	if a.homeFlag != "" {
		home = a.homeFlag
	}
	if home == "" {
		h, err := store.HomeDir()
		if err != nil {
			return cli.Errorf(cli.ExitStore, "cannot determine home: %v", err)
		}
		home = h
	}
	cfg, err := cli.LoadConfig(home)
	if err != nil {
		return err
	}
	st, err := store.Open(home)
	if err != nil {
		return cli.Errorf(cli.ExitStore, "open store at %s: %v", home, err)
	}
	a.home, a.cfg, a.st = home, cfg, st
	return nil
}

func (a *app) close() {
	if a.st != nil {
		_ = a.st.Close()
		a.st = nil
	}
}

// newRoot builds the command tree. Every command group lives in its own
// cmd_*.go file and registers through this function so files stay disjoint.
func newRoot(a *app) *cobra.Command {
	root := &cobra.Command{
		Use:           "nine-tails",
		Short:         "persistent agent context sidecar",
		Long:          "Start a session with:\n  nine-tails load pilot --task \"<task>\" --meta repo-id=<repo> --meta harness=<harness>\nThe pilot capsule is the usage guide and the catalog of agents in this store; a fresh store seeds it from the binary.\n\nnine-tails resolves a named agent into a token-bounded context capsule, records corrections, carries small versioned state, exposes named tools, and carries signals into future invocations.\n\nData goes to stdout, diagnostics to stderr. Never interactive. Mutations print the new id on one line; add --format json for the full record.",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetOut(a.stdout)
	root.SetErr(a.stderr)
	root.SetIn(a.stdin)
	root.PersistentFlags().StringVar(&a.homeFlag, "home", "", "override NINE_TAILS_HOME")

	root.AddCommand(
		newLoadCmd(a),
		newAppendCmd(a),
		newNoteCmd(a, "note", "guidance", "note", "Add a guidance note (lane guidance, kind note)"),
		newNoteCmd(a, "avoid", "guidance", "avoid", "Record something the agent should avoid (lane guidance, kind avoid)"),
		newNoteCmd(a, "prefer", "guidance", "prefer", "Record a preference (lane guidance, kind prefer)"),
		newNoteCmd(a, "remember", "recall", "memory", "Record a fact for later recall (lane recall, kind memory; never compiled into the brief)"),
		newBaseCmd(a),
		newInspectCmd(a),
		newPutCmd(a),
		newStateCmd(a),
		newContextCmd(a),
		newAgentsCmd(a),
		newConfigCmd(a),
		newToolCmd(a),
		newCallCmd(a),
		newAgentCmd(a),
		newSignalCmd(a),
		newTickCmd(a),
		newCompileInputCmd(a),
		newBriefCmd(a),
		newCompileCmd(a),
		newExportCmd(a),
		newImportCmd(a),
	)
	return root
}

const commandGroupAnnotation = "nine-tails-command-group"

// commandGroup marks a non-runnable command whose positional arguments must
// name one of its children. Cobra's legacy argument validation accepts unknown
// children for non-root command groups and renders their help successfully.
// Keep the command itself non-runnable so its help and completion behavior stay
// native, then reject those unknown children just before Cobra executes it.
func commandGroup(c *cobra.Command) *cobra.Command {
	if c.Annotations == nil {
		c.Annotations = make(map[string]string)
	}
	c.Annotations[commandGroupAnnotation] = "true"
	return c
}

// validateCommandGroupArgs closes a Cobra legacyArgs gap without changing the
// command tree that Cobra uses for help and shell completion. Unknown flags are
// left to Cobra so its normal flag error takes precedence. Help and --home are
// the only flags a pure group accepts, and neither may hide an unknown child.
func validateCommandGroupArgs(root *cobra.Command, argv []string) error {
	cmd, args, err := root.Find(argv)
	if err != nil || cmd.Annotations[commandGroupAnnotation] != "true" {
		return nil
	}

	var positionals []string
	afterDash := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !afterDash {
			switch {
			case arg == "--":
				afterDash = true
				continue
			case isBooleanFlagSpelling(arg, "help", "h"):
				continue
			case arg == "--home":
				if i+1 == len(args) {
					return nil
				}
				i++
				continue
			case strings.HasPrefix(arg, "--home="):
				continue
			case looksLikeFlag(arg):
				return nil
			}
		}
		positionals = append(positionals, arg)
	}

	for _, arg := range positionals {
		known := false
		for _, child := range cmd.Commands() {
			if arg == child.Name() || child.HasAlias(arg) {
				known = true
				break
			}
		}
		if !known {
			return cobra.NoArgs(cmd, []string{arg})
		}
	}
	return nil
}

func looksLikeFlag(arg string) bool {
	return len(arg) >= 3 && strings.HasPrefix(arg, "--") ||
		len(arg) >= 2 && arg[0] == '-' && arg[1] != '-'
}

// run executes argv against the app and returns the process exit code.
func run(a *app, argv []string) int {
	defer a.close()
	root := newRoot(a)
	root.SetArgs(argv)
	err := validateCommandGroupArgs(root, argv)
	if err == nil {
		err = root.Execute()
	}
	if err == nil {
		return 0
	}
	code := cli.CodeOf(err)
	var ee *cli.ExitError
	if !errors.As(err, &ee) {
		switch {
		case errors.Is(err, store.ErrNotFound):
			code = cli.ExitNotFound
		case errors.Is(err, store.ErrConflict):
			code = cli.ExitConflict
		case errors.Is(err, store.ErrInvalid):
			code = cli.ExitInvalid
		case isUsageError(err):
			code = cli.ExitInvalid
		}
	}
	msg := err.Error()
	if isUsageError(err) && strings.Contains(msg, "unknown shorthand flag") {
		msg += "\n  text starting with '-' needs -- before it, or --stdin"
	}
	cli.WriteErrorSummary(a.stderr, msg)
	if details := cli.DetailsOf(err); details != "" {
		cli.WriteDetails(a.stderr, details)
	}
	if wantsJSON(argv) {
		_ = cli.WriteJSON(a.stdout, map[string]any{"error": err.Error(), "code": code})
	}
	return code
}

func isUsageError(err error) bool {
	s := err.Error()
	for _, p := range []string{"unknown command", "unknown flag", "unknown shorthand", "accepts ", "requires ", "invalid argument", "flag needs an argument", "required flag"} {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}

func wantsJSON(argv []string) bool {
	// call deliberately has no --format flag: every stdout byte belongs to the
	// invoked tool. In particular, an unsupported `call --format json` must not
	// turn its usage error into a root JSON envelope on tool-owned stdout.
	if topLevelCommand(argv) == "call" {
		return false
	}
	format := ""
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		if a == "--" {
			break
		}
		if a == "--format" {
			if i+1 < len(argv) {
				format = argv[i+1]
				i++
			}
			continue
		}
		if strings.HasPrefix(a, "--format=") {
			format = strings.TrimPrefix(a, "--format=")
		}
	}
	return format == "json"
}

func topLevelCommand(argv []string) string {
	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		switch {
		case arg == "--":
			return ""
		case arg == "--home":
			// --home is the only root flag that consumes a value.
			i++
		case strings.HasPrefix(arg, "--home="):
		case isRootBooleanFlag(arg):
		case strings.HasPrefix(arg, "-"):
			// The arity of an unknown root flag is unknowable; leave its usage
			// error in the root namespace rather than guessing at a command.
			return ""
		default:
			return arg
		}
	}
	return ""
}

func isRootBooleanFlag(arg string) bool {
	return isBooleanFlagSpelling(arg, "help", "h") ||
		isBooleanFlagSpelling(arg, "version", "v") ||
		isBooleanShorthandCluster(arg, "hv")
}

func isBooleanFlagSpelling(arg, long, shorthand string) bool {
	if arg == "--"+long || arg == "-"+shorthand {
		return true
	}
	if value, ok := strings.CutPrefix(arg, "--"+long+"="); ok {
		_, err := strconv.ParseBool(value)
		return err == nil
	}
	return isBooleanShorthandCluster(arg, shorthand)
}

func isBooleanShorthandCluster(arg, allowed string) bool {
	if len(arg) < 2 || arg[0] != '-' || arg[1] == '-' {
		return false
	}
	flags := arg[1:]
	if before, value, ok := strings.Cut(flags, "="); ok {
		if _, err := strconv.ParseBool(value); err != nil {
			return false
		}
		flags = before
	}
	if flags == "" {
		return false
	}
	for _, shorthand := range flags {
		if !strings.ContainsRune(allowed, shorthand) {
			return false
		}
	}
	return true
}

func reportStartupError(stdout, stderr io.Writer, argv []string, err error) int {
	cli.WriteErrorSummary(stderr, err.Error())
	if wantsJSON(argv) {
		_ = cli.WriteJSON(stdout, map[string]any{"error": err.Error(), "code": cli.ExitInvalid})
	}
	return cli.ExitInvalid
}

func main() {
	now := time.Now
	if s := os.Getenv("NINE_TAILS_NOW"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			err = cli.Invalid("NINE_TAILS_NOW must be RFC 3339: %v", err)
			os.Exit(reportStartupError(os.Stdout, os.Stderr, os.Args[1:], err))
		}
		now = func() time.Time { return t }
	}
	a := &app{stdout: os.Stdout, stderr: os.Stderr, stdin: os.Stdin, now: now}
	os.Exit(run(a, os.Args[1:]))
}

// ---- small shared helpers for command files ----

// metaFlag parses repeated --meta k=v values.
func metaFlag(vals []string) (store.Meta, error) {
	m, err := store.ParseMeta(vals)
	if err != nil {
		return nil, cli.Invalid("%v", err)
	}
	return m, nil
}

// contextAgent resolves the agent for a --context flag, verifying it exists.
func (a *app) contextAgent(ctxID string) (string, error) {
	c, err := store.GetContext(a.st.DB, ctxID)
	if err != nil {
		return "", err
	}
	return c.Agent, nil
}

func newAgentsCmd(a *app) *cobra.Command {
	var format string
	c := &cobra.Command{
		Use:   "agents",
		Short: "List agents known to this store (one name per line)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.open(); err != nil {
				return err
			}
			names, err := store.Agents(a.st.DB)
			if err != nil {
				return err
			}
			if format == "text" {
				for _, n := range names {
					fmt.Fprintln(a.stdout, n)
				}
				return nil
			}
			type row struct {
				Name    string `json:"name" yaml:"name"`
				HasBase bool   `json:"has_base" yaml:"has_base"`
				Records int    `json:"active_records" yaml:"active_records"`
			}
			rows := []row{}
			for _, n := range names {
				recs, err := store.ListRecords(a.st.DB, store.Filter{Agent: n})
				if err != nil {
					return err
				}
				hasBase := false
				for _, r := range recs {
					if r.Lane == "definition" && r.Kind == "agent-base" {
						hasBase = true
					}
				}
				rows = append(rows, row{Name: n, HasBase: hasBase, Records: len(recs)})
			}
			return cli.Write(a.stdout, format, rows)
		},
	}
	c.Flags().StringVar(&format, "format", "text", "text|json|yaml")
	return c
}

func newConfigCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "config",
		Short: "Print the effective configuration (defaults overlaid with config.yaml)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.open(); err != nil {
				return err
			}
			return cli.WriteJSON(a.stdout, map[string]any{"home": a.home, "config": a.cfg})
		},
	}
}
