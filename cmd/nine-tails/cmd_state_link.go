package main

import (
	"database/sql"

	"github.com/spf13/cobra"

	"github.com/scottmeyer/nine-tails/internal/cli"
	"github.com/scottmeyer/nine-tails/internal/store"
)

func newStateLinkCmd(a *app) *cobra.Command {
	var expect, context, format string
	var meta []string
	c := &cobra.Command{
		Use:   "link [<agent>/]<name> <target-agent>/<state-name> --expect none|<link-id>",
		Short: "Surface another agent's current state on future loads",
		Long: `Create or replace an immutable definition/state-link. The first name
belongs to the subscribing agent; a bare name takes its agent from --context.
The target is a literal qualified working-state name. The target may be absent
at creation; a later load reports a missing target without failing.

On load, both the link and target must match the invocation's metadata.
Only the target's current state is read; links are never followed recursively.
The capsule labels its owner and state id as data and records both link and
state ids in the receipt. Target updates appear on the next load automatically.

--expect none creates safely; otherwise pass the current link record id.
As for other definitions, --meta is the complete scope (omitted means
unqualified). --context supplies provenance, never implicit scope. Inspect
or disable the returned link id normally; the target is never changed.`,
		Example: `  nine-tails state link game.engineer/project workshop/soccer-chess --expect none --meta repo-id=soccer-chess
  nine-tails state link project workshop/soccer-chess --context ctx_72 --expect rec_41 --meta repo-id=soccer-chess`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateRecordFormat(format); err != nil {
				return err
			}
			if !cmd.Flags().Changed("expect") || (expect != "none" && !store.IsID(expect)) {
				return cli.Invalid("--expect is required: 'none' to create, or the active state-link record id")
			}
			owner, name, err := store.StateLinkTarget(args[1])
			if err != nil {
				return err
			}
			m, err := metaFlag(meta)
			if err != nil {
				return err
			}
			agent, alias, err := a.stateTarget(args[0], context)
			if err != nil {
				return err
			}
			var rec *store.Record
			err = a.st.Tx(func(tx *sql.Tx) error {
				if context != "" {
					ctx, err := store.GetContext(tx, context)
					if err != nil {
						return err
					}
					if ctx.Agent != agent {
						return cli.Invalid("%s belongs to %s, not %s", context, ctx.Agent, agent)
					}
				}
				var err error
				rec, err = store.PutNamed(tx, store.NewRecord{Agent: agent, Lane: "definition", Kind: "state-link", Name: alias, Body: owner + "/" + name, Meta: m, OriginContext: context}, expect)
				return err
			})
			if err != nil {
				return err
			}
			return a.printRecord(format, rec)
		},
	}
	c.Flags().StringVar(&expect, "expect", "", "required CAS: none or the current state-link record id")
	c.Flags().StringVar(&context, "context", "", "originating receipt; supplies or must match the subscribing agent")
	c.Flags().StringArrayVar(&meta, "meta", nil, "complete link applicability scope (repeatable key=value)")
	c.Flags().StringVar(&format, "format", "id", "id|json|yaml")
	return c
}
