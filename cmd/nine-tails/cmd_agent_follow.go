package main

import (
	"database/sql"
	"strings"

	"github.com/spf13/cobra"

	"github.com/scottmeyer/nine-tails/internal/cli"
	"github.com/scottmeyer/nine-tails/internal/store"
)

// followTarget resolves the subscriber without ever inferring an ambient
// receipt. It intentionally mirrors stateTarget, but aliases are definitions
// rather than working-state names.
func (a *app) followTarget(target, context string) (agent, alias string, err error) {
	agent, alias, err = followTargetSyntax(target, context)
	if err != nil {
		return "", "", err
	}
	if err := a.open(); err != nil {
		return "", "", err
	}
	if context != "" {
		owner, err := a.contextAgent(context)
		if err != nil {
			return "", "", err
		}
		if agent != "" && owner != agent {
			return "", "", cli.Invalid("%s belongs to %s, not %s", context, owner, agent)
		}
		agent = owner
	}
	return agent, alias, nil
}

// followTargetSyntax is used from Args, before PersistentPreRunE resolves a
// local reference and opens a store. Keep purely mechanical errors ahead of a
// stale @N so callers receive the useful syntax diagnostic.
func followTargetSyntax(target, context string) (agent, alias string, err error) {
	if strings.Contains(target, "/") {
		agent, alias, err = cli.SplitAgentName(target)
		if err != nil {
			return "", "", err
		}
		if err := store.ValidAgentName(agent); err != nil {
			return "", "", err
		}
	} else {
		if context == "" {
			return "", "", cli.Invalid("expected <subscriber>/<alias>, or a bare <alias> with --context")
		}
		alias = target
	}
	if err := store.ValidRecordName("guidance-link", alias); err != nil {
		return "", "", err
	}
	return agent, alias, nil
}

func newAgentFollowCmd(a *app) *cobra.Command {
	var expect, context, format string
	var meta []string
	c := &cobra.Command{
		Use:   "follow [<subscriber>/]<alias> <source-agent> --expect none|<link-id>",
		Short: "Subscribe an agent to another owner's scoped active guidance",
		Long: `Create or replace an immutable definition/guidance-link. Its literal body
is the source owner. On a later load, the subscriber receives eligible active
guidance directly from that source, labeled with its owner and link provenance.
The source's base, state, tools, related agents, recall, and further links are
never loaded. This command never copies or changes source guidance.

--expect none creates safely; otherwise pass the current guidance-link record
id. A bare alias takes its subscriber from --context; an explicit subscriber
must match that receipt. --meta is the complete applicability scope, while
--context records provenance only. Disable the returned link to stop following
it without changing the source.`,
		Example: `  nine-tails agent follow reviewer/team-style maintainer --expect none --meta repo-id=acme
  nine-tails agent follow team-style maintainer --context ctx_72 --expect rec_41`,
		Args: func(cmd *cobra.Command, args []string) error {
			if err := cobra.ExactArgs(2)(cmd, args); err != nil {
				return err
			}
			if err := validateRecordFormat(format); err != nil {
				return err
			}
			if !cmd.Flags().Changed("expect") || (expect != "none" && !store.IsID(expect) && !strings.HasPrefix(expect, "@")) {
				return cli.Invalid("--expect is required: 'none' to create, or the active guidance-link record id")
			}
			if strings.HasPrefix(expect, "@") {
				if err := store.ValidateReference(expect); err != nil {
					return err
				}
			}
			source, err := store.GuidanceLinkTarget(args[1])
			if err != nil {
				return err
			}
			subscriber, _, err := followTargetSyntax(args[0], context)
			if err != nil {
				return err
			}
			if subscriber != "" && subscriber == source {
				return cli.Invalid("guidance link source must not be its subscribing agent")
			}
			if _, err := metaFlag(meta); err != nil {
				return err
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateRecordFormat(format); err != nil {
				return err
			}
			if !cmd.Flags().Changed("expect") || (expect != "none" && !store.IsID(expect)) {
				return cli.Invalid("--expect is required: 'none' to create, or the active guidance-link record id")
			}
			source, err := store.GuidanceLinkTarget(args[1])
			if err != nil {
				return err
			}
			m, err := metaFlag(meta)
			if err != nil {
				return err
			}
			agent, alias, err := a.followTarget(args[0], context)
			if err != nil {
				return err
			}
			if source == agent {
				return cli.Invalid("guidance link source must not be its subscribing agent")
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
				rec, err = store.PutNamed(tx, store.NewRecord{Agent: agent, Lane: "definition", Kind: "guidance-link", Name: alias, Body: source, Meta: m, OriginContext: context}, expect)
				return err
			})
			if err != nil {
				return err
			}
			return a.printRecord(format, rec)
		},
	}
	c.Flags().StringVar(&expect, "expect", "", "required CAS: none or the current guidance-link record id")
	c.Flags().StringVar(&context, "context", "", "originating receipt; supplies or must match the subscribing agent")
	c.Flags().StringArrayVar(&meta, "meta", nil, "complete link applicability scope (repeatable key=value)")
	c.Flags().StringVar(&format, "format", "id", "id|json|yaml")
	return c
}
