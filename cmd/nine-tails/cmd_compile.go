package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/scottmeyer/nine-tails/internal/cli"
	"github.com/scottmeyer/nine-tails/internal/compile"
	"github.com/scottmeyer/nine-tails/internal/store"
)

// errDryRun is returned from inside the install transaction so it rolls back
// after everything (validation, coverage, install, lint) has run.
var errDryRun = errors.New("dry run")

// compileBudget resolves --budget: when the flag was not given, the brief's
// reserved share of a default load (brief_floor × default_budget). An explicit
// --budget 0 is invalid like any other non-positive value (DESIGN §5), so the
// flag's Changed state, not its value, tells the two apart.
func (a *app) compileBudget(cmd *cobra.Command, flag int) (int, error) {
	if cmd.Flags().Changed("budget") {
		if flag <= 0 {
			return 0, cli.Invalid("--budget must be positive, got %d", flag)
		}
		return flag, nil
	}
	b := int(a.cfg.BriefFloor * float64(a.cfg.DefaultBudget))
	if b <= 0 {
		return 0, cli.Invalid("default brief budget brief_floor × default_budget is %d in %s/config.yaml; pass --budget N", b, a.home)
	}
	return b, nil
}

func newCompileInputCmd(a *app) *cobra.Command {
	var budget int
	var format string
	c := &cobra.Command{
		Use:   "compile-input <agent>",
		Short: "Print the document a brief compiler consumes",
		Long: `Assemble everything a compiler needs to produce the next brief generation:
the compiler instructions, the active base, the active generation's items and
every recent guidance entry (with what its origin context rendered), plus the
expect_generation / expect_base ids that "brief put" needs for compare-and-swap.
Feed the output to a model and hand its reply to "brief put --stdin".
The instructions are the built-in default unless an agent named
		"brief-compiler" has a base, in which case that base is used verbatim.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.open(); err != nil {
				return err
			}
			if err := store.ValidAgentName(args[0]); err != nil {
				return err
			}
			b, err := a.compileBudget(cmd, budget)
			if err != nil {
				return err
			}
			in, err := compile.BuildInput(a.st.DB, args[0], b)
			if err != nil {
				return err
			}
			return cli.Write(a.stdout, format, in)
		},
	}
	c.Flags().IntVar(&budget, "budget", 0, "token budget for the brief, > 0 (default brief_floor × default_budget from config)")
	c.Flags().StringVar(&format, "format", "json", "json|yaml")
	return c
}

func newBriefCmd(a *app) *cobra.Command {
	c := &cobra.Command{
		Use:   "brief",
		Short: "Install a compiled brief generation",
		Long: `The brief is a generation of compiled items. "brief put" installs the
compiler's output as the next generation with compare-and-swap:
  compile-input pr-review > in.json        (run a model over it)
  brief put pr-review --expect-generation gen_11 --expect-base base_4 --stdin < out.yaml`,
	}
	c.AddCommand(newBriefPutCmd(a))
	return c
}

func newBriefPutCmd(a *app) *cobra.Command {
	var expectGen, expectBase, format string
	var stdin, dryRun bool
	c := &cobra.Command{
		Use:   "put <agent> --expect-generation gen_N|none --expect-base base_N --stdin",
		Short: "Validate compiler output and install it as the next generation",
		Long: `Read the compiler output (YAML or JSON) from stdin, validate it against the
store (every input entry accounted for, item keys valid, successors and
equivalents real), compute coverage, and install it in one transaction if the
expected generation and base are still active. Source entries are never
consumed. Condition-loss warnings go to stderr and never block the install.
Prints the new generation id; --format json prints {generation, items, warnings}.
		--dry-run runs everything, rolls back, and prints what would be installed.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateBriefFormat(format); err != nil {
				return err
			}
			if err := a.open(); err != nil {
				return err
			}
			agent := args[0]
			if err := store.ValidAgentName(agent); err != nil {
				return err
			}
			if !stdin {
				return cli.Invalid("brief put reads the compiler output from stdin: pass --stdin")
			}
			b, err := io.ReadAll(a.stdin)
			if err != nil {
				return cli.Invalid("read stdin: %v", err)
			}
			exists, err := store.AgentExists(a.st.DB, agent)
			if err != nil {
				return err
			}
			if !exists {
				return cli.NotFound("no records for agent %q", agent)
			}
			out, err := compile.Parse(b)
			if err != nil {
				return err
			}
			res, err := a.installBrief(agent, expectGen, expectBase, out, dryRun)
			if err != nil {
				return err
			}
			return a.printBriefResult(format, res)
		},
	}
	c.Flags().StringVar(&expectGen, "expect-generation", "", "required: 'none' or the active generation id (compile-input's expect_generation)")
	c.Flags().StringVar(&expectBase, "expect-base", "", "required: the active base id (compile-input's expect_base)")
	c.Flags().BoolVar(&stdin, "stdin", false, "read the compiler output from stdin (required)")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "validate, compute coverage and lint, print what would be installed (ids are provisional), write nothing")
	c.Flags().StringVar(&format, "format", "id", "id (one line) | json | yaml")
	return c
}

// installBrief runs the whole install inside one transaction, rolling it
// back for a dry run, and prints lint warnings to stderr.
func (a *app) installBrief(agent, expectGen, expectBase string, out *compile.Output, dryRun bool) (*compile.Result, error) {
	var res *compile.Result
	err := a.st.Tx(func(tx *sql.Tx) error {
		var err error
		res, err = compile.Install(tx, agent, expectGen, expectBase, out)
		if err != nil {
			return err
		}
		if dryRun {
			res.DryRun = true
			return errDryRun
		}
		return nil
	})
	if err != nil && !errors.Is(err, errDryRun) {
		return nil, err
	}
	for _, w := range res.Warnings {
		fmt.Fprintf(a.stderr, "nine-tails: warning: %s\n", w.Message)
	}
	return res, nil
}

func validateBriefFormat(format string) error {
	switch format {
	case "id", "", "json", "yaml":
		return nil
	default:
		return cli.Invalid("unknown format %q (id|json|yaml)", format)
	}
}

func (a *app) printBriefResult(format string, res *compile.Result) error {
	if err := validateBriefFormat(format); err != nil {
		return err
	}
	if res.DryRun {
		// There is no id to print: a dry run always shows the structured plan.
		if format == "id" || format == "" {
			format = "json"
		}
		inputs := res.Inputs
		if inputs == nil {
			inputs = []store.BriefInput{}
		}
		// Inputs belong only to a dry-run plan. Keep a separate output view so
		// an empty plan still says inputs: [], while the exact non-dry result
		// continues to omit the field entirely.
		return cli.Write(a.stdout, format, struct {
			Generation string             `json:"generation" yaml:"generation"`
			Items      []string           `json:"items" yaml:"items"`
			Warnings   []compile.Warning  `json:"warnings" yaml:"warnings"`
			Inputs     []store.BriefInput `json:"inputs" yaml:"inputs"`
			DryRun     bool               `json:"dry_run" yaml:"dry_run"`
		}{Generation: res.Generation, Items: res.Items, Warnings: res.Warnings, Inputs: inputs, DryRun: true})
	}
	res.Inputs = nil
	switch format {
	case "id", "":
		_, err := fmt.Fprintln(a.stdout, res.Generation)
		return err
	case "json", "yaml":
		return cli.Write(a.stdout, format, res)
	}
	return nil
}

func newCompileCmd(a *app) *cobra.Command {
	var budget int
	var compiler, format string
	var dryRun bool
	c := &cobra.Command{
		Use:   "compile <agent> [--budget N] [--compiler \"claude -p\"]",
		Short: "Run the configured compiler over compile-input and install its output",
		Long: `compile-input → compiler → brief put, in one step. The compiler command is
--compiler, else $NINE_TAILS_COMPILER, else compiler.argv in config.yaml; it
receives the compile-input JSON on stdin and must print the output document
(YAML or JSON) on stdout. Its stderr passes through, after the nine-tails error
summary if compilation fails. It is killed after
compiler.timeout (default 300s). The install uses the expect ids from the
compile input, so a generation installed meanwhile makes this exit 7.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateBriefFormat(format); err != nil {
				return err
			}
			if err := a.open(); err != nil {
				return err
			}
			agent := args[0]
			if err := store.ValidAgentName(agent); err != nil {
				return err
			}
			b, err := a.compileBudget(cmd, budget)
			if err != nil {
				return err
			}
			argv, err := a.compilerArgv(compiler)
			if err != nil {
				return err
			}
			timeout, err := cli.ParseDuration(a.cfg.Compiler.Timeout)
			if err != nil {
				return cli.Invalid("config.yaml compiler.timeout: %v", err)
			}
			in, err := compile.BuildInput(a.st.DB, agent, b)
			if err != nil {
				return err
			}
			var doc bytes.Buffer
			if err := cli.WriteJSON(&doc, in); err != nil {
				return err
			}
			raw, diagnostics, err := a.runCompiler(argv, doc.Bytes(), timeout, agent)
			if err != nil {
				return cli.WithDetails(err, diagnostics)
			}
			out, err := compile.Parse(raw)
			if err != nil {
				return cli.WithDetails(err, diagnostics)
			}
			// Spec §12.3: input_entries is echoed unchanged. Recorded as one more
			// problem so the compiler sees it together with everything else.
			compile.CheckEcho(out, in.InputEntries)
			res, err := a.installBrief(agent, in.ExpectGeneration, in.ExpectBase, out, dryRun)
			if err != nil {
				return cli.WithDetails(err, diagnostics)
			}
			if err := a.printBriefResult(format, res); err != nil {
				return cli.WithDetails(err, diagnostics)
			}
			_, _ = a.stderr.Write(diagnostics)
			return nil
		},
	}
	c.Flags().IntVar(&budget, "budget", 0, "token budget for the brief, > 0 (default brief_floor × default_budget from config)")
	c.Flags().StringVar(&compiler, "compiler", "", "compiler command line, split on whitespace (overrides NINE_TAILS_COMPILER and config)")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "run the compiler and validate its output but install nothing")
	c.Flags().StringVar(&format, "format", "id", "id (one line) | json | yaml")
	return c
}

// compilerArgv resolves the compiler command: flag, env, config.
func (a *app) compilerArgv(flag string) ([]string, error) {
	if argv := strings.Fields(flag); len(argv) > 0 {
		return argv, nil
	}
	if argv := strings.Fields(os.Getenv("NINE_TAILS_COMPILER")); len(argv) > 0 {
		return argv, nil
	}
	if len(a.cfg.Compiler.Argv) > 0 {
		return a.cfg.Compiler.Argv, nil
	}
	return nil, cli.Invalid("no compiler configured\n  pass --compiler \"claude -p\", set NINE_TAILS_COMPILER=\"claude -p\", or add to %s/config.yaml:\n    compiler:\n      argv: [\"claude\", \"-p\"]\n      timeout: 300s", a.home)
}

// runCompiler executes argv with input on stdin and returns its stdout and
// buffered stderr. The caller emits stderr after the final outcome is known,
// preserving the required first error line. Cannot start, nonzero exit and
// timeout are all exit 5.
func (a *app) runCompiler(argv []string, input []byte, timeout time.Duration, agent string) ([]byte, []byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdin = bytes.NewReader(input)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Env = append(os.Environ(), "NINE_TAILS_HOME="+a.home, "NINE_TAILS_AGENT="+agent)
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	if err == nil {
		return stdout.Bytes(), stderr.Bytes(), nil
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, stderr.Bytes(), cli.ToolFailed("compiler %s timed out after %s", argv[0], timeout)
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return nil, stderr.Bytes(), cli.ToolFailed("compiler %s exited with status %d", argv[0], ee.ExitCode())
	}
	return nil, stderr.Bytes(), cli.ToolFailed("cannot start compiler %s: %v", argv[0], err)
}
