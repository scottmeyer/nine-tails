package main

import (
	"database/sql"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/scottmeyer/nine-tails/internal/cli"
	"github.com/scottmeyer/nine-tails/internal/store"
)

// appendOpts are the flags shared by append and its convenience aliases.
type appendOpts struct {
	meta       []string
	context    string
	stdin      bool
	format     string
	supersedes string
}

func (o *appendOpts) bind(c *cobra.Command) {
	c.Flags().StringArrayVar(&o.meta, "meta", nil, "applicability metadata key=value (repeatable)")
	c.Flags().StringVar(&o.supersedes, "supersedes", "", "replace this active record of the same agent and lane; --meta becomes its exact scope, and without TEXT its body is kept")
	c.Flags().StringVar(&o.context, "context", "", "originating context receipt id (ctx_..., not a record id); also supplies the agent when <agent> is omitted")
	c.Flags().BoolVar(&o.stdin, "stdin", false, "read the body from stdin instead of the argument")
	c.Flags().StringVar(&o.format, "format", "id", "id (one line) | json | yaml")
}

// resolveAgentAndText applies the "--context implies the agent" rule
// (DESIGN §6): with --context, two or more positionals mean the first is the
// agent (which must match); one positional is the text; with --stdin there
// are none. Without --context the first positional is always the agent.
func (a *app) resolveAgentAndText(ctxID string, stdin, textOptional bool, args []string) (agent string, text []string, err error) {
	if ctxID == "" {
		if len(args) == 0 {
			return "", nil, cli.Invalid("missing <agent> (or pass --context)")
		}
		return args[0], args[1:], nil
	}
	if stdin && len(args) > 0 {
		return "", nil, cli.Invalid("with --context and --stdin, do not pass positional arguments")
	}
	ctxAgent, err := a.contextAgent(ctxID)
	if err != nil {
		return "", nil, err
	}
	switch {
	case len(args) == 0 && (stdin || textOptional):
		return ctxAgent, nil, nil
	case len(args) == 0:
		return "", nil, cli.Invalid("missing text: pass it as an argument or use --stdin")
	case len(args) == 1:
		return ctxAgent, args, nil
	default:
		agent, text = args[0], args[1:]
	}
	if agent != ctxAgent {
		return "", nil, cli.Invalid("%s belongs to %s, not %s", ctxID, ctxAgent, agent)
	}
	return agent, text, nil
}

// doAppend inserts a record and prints its id (or envelope).
func (a *app) doAppend(o *appendOpts, lane, kind, name string, args []string) error {
	if err := validateRecordFormat(o.format); err != nil {
		return err
	}
	if err := a.open(); err != nil {
		return err
	}
	agent, text, err := a.resolveAgentAndText(o.context, o.stdin, o.supersedes != "", args)
	if err != nil {
		return err
	}
	if err := store.ValidAgentName(agent); err != nil {
		return err
	}
	if o.supersedes != "" && !cli.IsID(o.supersedes) {
		return cli.Invalid("--supersedes wants a record id, not %q", o.supersedes)
	}
	var body string
	if o.supersedes == "" || o.stdin || len(text) > 0 {
		body, err = cli.ReadBody(text, o.stdin, a.stdin, false)
		if err != nil {
			return err
		}
	}
	meta, err := metaFlag(o.meta)
	if err != nil {
		return err
	}
	var rec *store.Record
	err = a.st.Tx(func(tx *sql.Tx) error {
		if o.context != "" {
			ctx, err := store.GetContext(tx, o.context)
			if err != nil {
				return err
			}
			if ctx.Agent != agent {
				return cli.Invalid("%s belongs to %s, not %s", o.context, ctx.Agent, agent)
			}
		}
		var err error
		nr := store.NewRecord{Agent: agent, Lane: lane, Kind: kind, Name: name, Body: body, OriginContext: o.context, Meta: meta}
		if o.supersedes != "" {
			rec, err = store.ReplaceRecord(tx, o.supersedes, nr)
		} else {
			rec, err = store.InsertRecord(tx, nr)
		}
		return err
	})
	if err != nil {
		return err
	}
	return a.printRecord(o.format, rec)
}

func validateRecordFormat(format string) error {
	switch format {
	case "id", "", "json", "yaml":
		return nil
	default:
		return cli.Invalid("unknown format %q (id|json|yaml)", format)
	}
}

// printRecord prints a mutation result: the id alone (default) or the envelope.
func (a *app) printRecord(format string, rec *store.Record) error {
	if err := validateRecordFormat(format); err != nil {
		return err
	}
	switch format {
	case "id", "":
		_, err := fmt.Fprintln(a.stdout, rec.ID)
		return err
	case "json", "yaml":
		return cli.Write(a.stdout, format, rec)
	}
	return nil
}

func newAppendCmd(a *app) *cobra.Command {
	o := &appendOpts{}
	var lane, kind string
	c := &cobra.Command{
		Use:   "append [<agent>] [--supersedes <record-id>] [--] [TEXT]",
		Short: "Add a generic immutable record to an agent",
		Long: `Add a record. Lanes control mechanical treatment: guidance is rendered as
recent adjustments and compiled into the brief; recall is kept for explicit
lookup. Unknown kinds are allowed. --lane defaults to recall, so unknown
material never silently becomes always-on guidance. Definitions, state and
signals have their own commands (put, base, state put, tool add, agent add,
signal). With --context the <agent> may be omitted.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if lane == "" {
				lane = "recall"
			}
			switch lane {
			case "guidance", "recall":
			case "definition", "state":
				return cli.Invalid("use `put`, `base`, `state put`, `tool add` or `agent add` for the %s lane", lane)
			case "signal":
				return cli.Invalid("use `signal` for the signal lane")
			default:
				return cli.Invalid("unknown lane %q (guidance|recall)", lane)
			}
			if kind == "brief-item" {
				return cli.Invalid("brief items are created only by `brief put`")
			}
			if kind == "" {
				if lane == "guidance" {
					kind = "note"
				} else {
					kind = "memory"
				}
			}
			return a.doAppend(o, lane, kind, "", args)
		},
	}
	o.bind(c)
	c.Flags().StringVar(&lane, "lane", "", "guidance|recall (default recall)")
	c.Flags().StringVar(&kind, "kind", "", "open-ended kind (default note for guidance, memory for recall)")
	return c
}

func newNoteCmd(a *app, verb, lane, kind, short string) *cobra.Command {
	o := &appendOpts{}
	long, example := noteHelp(verb)
	c := &cobra.Command{
		Use:     verb + " [<agent>] [--supersedes <record-id>] [--] [TEXT]",
		Short:   short,
		Long:    long,
		Example: example,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.doAppend(o, lane, kind, "", args)
		},
	}
	o.bind(c)
	return c
}

func noteHelp(verb string) (long, example string) {
	switch verb {
	case "note":
		long, example = `Add general guidance that should affect relevant future capsules
immediately and is eligible for compilation into the brief. Add --meta only
when the guidance is genuinely scoped.

On success, stdout is the new immutable rec_... record id by default. A
ctx_... value passed to --context identifies the originating load receipt,
not the new record.`, `  nine-tails note pr-review "Repository tests run with make test."
  nine-tails note --context ctx_72 --meta repo-id=acme "Generated mocks are not edited directly."
  nine-tails note --context ctx_72 --supersedes rec_41 --meta repo-id=acme`
	case "avoid":
		long, example = `Add guidance describing behavior the agent should avoid. It affects
relevant future capsules immediately and is eligible for compilation into the
brief.

On success, stdout is the new immutable rec_... record id by default. A
ctx_... value passed to --context identifies the originating load receipt,
not the new record.`, `  nine-tails avoid pr-review "Do not edit generated files."
  nine-tails avoid --context ctx_72 "Do not infer behavior without reading the implementation."`
	case "prefer":
		long, example = `Add guidance describing behavior the agent should prefer. It affects
relevant future capsules immediately and is eligible for compilation into the
brief.

On success, stdout is the new immutable rec_... record id by default. A
ctx_... value passed to --context identifies the originating load receipt,
not the new record.`, `  nine-tails prefer pr-review "Lead with evidence and expected impact."
  nine-tails prefer --context ctx_72 "Run focused tests before the full suite."`
	case "remember":
		long, example = `Store a recall fact for explicit retrieval. Recall records are not
loaded into context capsules and are never compiled into the brief. Find them
with inspect --lane recall and, when useful, --query.

On success, stdout is the new immutable rec_... record id by default. A
ctx_... value passed to --context identifies the originating load receipt,
not the new record.`, `  nine-tails remember pr-review "GitHub may omit large patch bodies."
  nine-tails inspect pr-review --lane recall --query "patch bodies" --format json`
	default:
		return "Add an immutable record.", ""
	}
	long += `

Use --supersedes rec_... to replace an active record of the same agent and
lane. With no TEXT or --stdin, it keeps the old body; --meta becomes the exact
new applicability scope. This fixes scope without editing history.`
	return long, example
}

func newBaseCmd(a *app) *cobra.Command {
	var meta []string
	var expect, format string
	var stdin bool
	c := &cobra.Command{
		Use:   "base <agent> [--expect none|<base-id>] [--] [TEXT]",
		Short: "Create or replace an agent's base instructions",
		Long: `Use --expect none for safe creation: the command fails with a conflict
if an active base already exists. Omitting --expect is unconditional: it
creates a new immutable base and supersedes any active base.

Create an agent by giving it base instructions. This is exactly
  put <agent> --lane definition --kind agent-base --name base

For a compare-and-swap replacement, pass the current base_... record id to
--expect. Use --meta title="..." to set the capsule title. By default stdout
is the new base_... record id; --format json or yaml prints its envelope.`,
		Example: `  nine-tails base pr-review --expect none --meta title="PR Review Agent" \
    "Review proposed changes for correctness."
  nine-tails base pr-review --expect base_4 --stdin < base.md`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateRecordFormat(format); err != nil {
				return err
			}
			if err := a.open(); err != nil {
				return err
			}
			if err := store.ValidAgentName(args[0]); err != nil {
				return err
			}
			body, err := cli.ReadBody(args[1:], stdin, a.stdin, false)
			if err != nil {
				return err
			}
			m, err := metaFlag(meta)
			if err != nil {
				return err
			}
			var rec *store.Record
			err = a.st.Tx(func(tx *sql.Tx) error {
				var err error
				rec, err = store.PutNamed(tx, store.NewRecord{Agent: args[0], Lane: "definition", Kind: "agent-base", Name: "base", Body: body, Meta: m}, expect)
				return err
			})
			if err != nil {
				return err
			}
			return a.printRecord(format, rec)
		},
	}
	c.Flags().StringArrayVar(&meta, "meta", nil, "metadata key=value (repeatable); title=... names the capsule")
	c.Flags().StringVar(&expect, "expect", "", "CAS: 'none' for safe creation, or current base record id (base_...); omit to supersede unconditionally")
	c.Flags().BoolVar(&stdin, "stdin", false, "read the body from stdin")
	c.Flags().StringVar(&format, "format", "id", "id|json|yaml")
	return c
}
