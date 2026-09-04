package main

import (
	"database/sql"

	"github.com/spf13/cobra"

	"github.com/scottmeyer/nine-tails/internal/cli"
	"github.com/scottmeyer/nine-tails/internal/store"
	"github.com/scottmeyer/nine-tails/internal/tool"
)

func newPutCmd(a *app) *cobra.Command {
	var lane, kind, name, expect, context, format string
	var meta []string
	var stdin bool
	c := &cobra.Command{
		Use:   "put <agent> --lane definition|state --kind K --name N [--] [TEXT]",
		Short: "Create an immutable named version and supersede its predecessor",
		Long: `Create a new active named record. If an active record with the same
agent/lane/kind/name exists it is superseded (or, with --expect, only if its id
matches; --expect none requires that none exists). Tool bodies and state
		bodies are validated mechanically. Prints the new record id.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateRecordFormat(format); err != nil {
				return err
			}
			if err := a.open(); err != nil {
				return err
			}
			if lane == "" || kind == "" || name == "" {
				return cli.Invalid("--lane, --kind and --name are required")
			}
			switch lane {
			case "definition", "state":
			case "guidance", "recall":
				return cli.Invalid("use `append` (or note/avoid/prefer/remember) for the %s lane", lane)
			case "signal":
				return cli.Invalid("signals are created with `signal`")
			default:
				return cli.Invalid("unknown lane %q (definition|state)", lane)
			}
			if err := store.ValidAgentName(args[0]); err != nil {
				return err
			}
			if err := store.ValidNamedRecord(lane, kind, name); err != nil {
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
			if lane == "definition" && kind == "tool" {
				if _, err := tool.Parse(body); err != nil {
					return cli.Invalid("tool body: %v", err)
				}
			}
			if lane == "state" {
				if err := validateState(body, a.cfg.StateMaxBytes); err != nil {
					return err
				}
			}
			if context != "" {
				if _, err := store.GetContext(a.st.DB, context); err != nil {
					return err
				}
			}
			var rec *store.Record
			err = a.st.Tx(func(tx *sql.Tx) error {
				if context != "" {
					if _, err := store.GetContext(tx, context); err != nil {
						return err
					}
				}
				var err error
				rec, err = store.PutNamed(tx, store.NewRecord{Agent: args[0], Lane: lane, Kind: kind, Name: name, Body: body, Meta: m, OriginContext: context}, expect)
				return err
			})
			if err != nil {
				return err
			}
			return a.printRecord(format, rec)
		},
	}
	c.Flags().StringVar(&lane, "lane", "", "definition|state")
	c.Flags().StringVar(&kind, "kind", "", "record kind (tool, related-agent, agent-base, working-state, ...)")
	c.Flags().StringVar(&name, "name", "", "mechanical name, unique per agent/lane/kind")
	c.Flags().StringVar(&expect, "expect", "", "compare-and-swap: 'none' or the id that must currently be active")
	c.Flags().StringVar(&context, "context", "", "originating context id")
	c.Flags().StringArrayVar(&meta, "meta", nil, "metadata key=value (repeatable)")
	c.Flags().BoolVar(&stdin, "stdin", false, "read the body from stdin")
	c.Flags().StringVar(&format, "format", "id", "id|json|yaml")
	return c
}
