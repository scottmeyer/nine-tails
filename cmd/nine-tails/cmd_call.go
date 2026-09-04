package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/scottmeyer/nine-tails/internal/cli"
	"github.com/scottmeyer/nine-tails/internal/store"
	"github.com/scottmeyer/nine-tails/internal/tool"
)

func newCallCmd(a *app) *cobra.Command {
	var context, agent, input string
	var stdin bool
	c := &cobra.Command{
		Use:   "call [--context ctx | --agent A] <tool> [--input JSON | --stdin]",
		Short: "Invoke a named executable tool",
		Long: `Resolve <tool> for the calling agent (--agent, else the agent of --context,
else the shared namespace): the agent's own definition first, then a shared
one it is allowed to see. Each {{ placeholder }} in exec.argv becomes one
whole argument from the JSON input object (default {}); nothing is split,
interpolated or shell-quoted, and -- is never added. The tool's stdout and
stderr stream through untouched; when it fails, the nine-tails summary line
follows its output and its exit status is returned verbatim. A tool that
cannot start or times out is exit 5. SIGINT, SIGTERM or SIGHUP received while
the tool runs is forwarded to the tool's process group and nine-tails exits
128+signal. The tool sees NINE_TAILS_HOME, NINE_TAILS_AGENT and, with
--context, NINE_TAILS_CONTEXT.

Inspect tool definitions before calling with
  nine-tails inspect <agent> --include tools --format yaml
or inspect one immutable tool_... record id directly. A ctx_... value is a
context receipt id used with --context, not a tool record id.

The tool inherits nine-tails' current working directory (cwd), so invoke call
from the worktree or directory the tool should act on. Literal artifacts/...
argv entries are resolved under NINE_TAILS_HOME instead.`,
		Example: `  nine-tails inspect pr-review --include tools --format yaml
  nine-tails call --context ctx_72 complete-pr-diff --input '{"repo":"acme/payments","pr":1842}'`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			agentGiven := cmd.Flags().Changed("agent")
			contextGiven := cmd.Flags().Changed("context")
			if agentGiven && agent == "" {
				return cli.Invalid("--agent must not be empty")
			}
			if contextGiven && context == "" {
				return cli.Invalid("--context must not be empty")
			}
			name := args[0]
			inputGiven := cmd.Flags().Changed("input")
			if stdin && inputGiven {
				return cli.Invalid("give the input either with --input or via --stdin, not both")
			}
			if err := store.ValidName("tool", name); err != nil {
				return err
			}
			if agent != "" {
				if err := store.ValidAgentName(agent); err != nil {
					return err
				}
			}
			raw := []byte(input)
			if stdin {
				b, err := io.ReadAll(a.stdin)
				if err != nil {
					return cli.Invalid("read stdin: %v", err)
				}
				raw = b
			}
			if (stdin || inputGiven) && len(bytes.TrimSpace(raw)) == 0 {
				return cli.Invalid("input must be exactly one JSON object")
			}
			in, err := tool.DecodeInput(raw)
			if err != nil {
				return cli.Invalid("%v", err)
			}
			if err := a.open(); err != nil {
				return err
			}
			var ctx *store.Context
			if context != "" {
				ctx, err = store.GetContext(a.st.DB, context)
				if err != nil {
					return err
				}
				if agent != "" && agent != ctx.Agent {
					return cli.Invalid("%s belongs to %s, not %s", context, ctx.Agent, agent)
				}
				agent = ctx.Agent
			}
			if agent == "" {
				agent = "shared"
			}
			rec, err := store.Resolve(a.st.DB, agent, "tool", name)
			if err != nil {
				return err
			}
			// A context-scoped call applies the same metadata conflict rule as
			// capsule tool discovery. Resolution happens first so an owned name
			// continues to shadow a shared name even when the owned definition is
			// inapplicable to this particular context.
			if ctx != nil && store.Conflicts(rec.Meta, ctx.Meta) {
				return cli.NotFound("tool %s is not applicable to context %s", name, context)
			}
			def, err := tool.Parse(rec.Body)
			if err != nil {
				return cli.Errorf(cli.ExitStore, "%s (%s) has a corrupt body: %v\n  repair it with: nine-tails put %s --lane definition --kind tool --name %s --stdin", rec.ID, name, err, rec.Agent, name)
			}
			if err := def.ValidateInput(in); err != nil {
				return cli.Invalid("%s: %v", name, err)
			}
			env := map[string]string{"NINE_TAILS_HOME": a.home, "NINE_TAILS_AGENT": agent}
			if context != "" {
				env["NINE_TAILS_CONTEXT"] = context
			}
			// Both streams belong to the tool and pass through untouched. When
			// the tool fails, the summary line printed by run follows its output.
			err = def.Run(tool.Call{Home: a.home, Input: in, Env: env, Stdout: a.stdout, Stderr: a.stderr})
			var ee *tool.ExitError
			var interrupted *tool.Interrupted
			switch {
			case err == nil:
				return nil
			case errors.As(err, &interrupted):
				return cli.Errorf(interrupted.ExitCode(), "%s (%s) %v", name, rec.ID, interrupted)
			case errors.Is(err, tool.ErrStart):
				return cli.ToolFailed("%s (%s): %v", name, rec.ID, err)
			case errors.As(err, &ee):
				if ee.Code < 0 {
					return cli.ToolFailed("%s (%s) was terminated by a signal", name, rec.ID)
				}
				return &cli.ExitError{Code: ee.Code, Msg: fmt.Sprintf("%s exited with status %d", name, ee.Code)}
			}
			return cli.ToolFailed("%s (%s): %v", name, rec.ID, err)
		},
	}
	c.Flags().StringVar(&context, "context", "", "calling context receipt id (ctx_..., not a tool record id); supplies the agent and NINE_TAILS_CONTEXT")
	c.Flags().StringVar(&agent, "agent", "", "calling agent (default: the context's agent, else shared)")
	c.Flags().StringVar(&input, "input", "", "JSON object of inputs (default {})")
	c.Flags().BoolVar(&stdin, "stdin", false, "read the JSON input object from stdin")
	return c
}
