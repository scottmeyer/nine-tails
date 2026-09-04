package main

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/scottmeyer/nine-tails/internal/cli"
	"github.com/scottmeyer/nine-tails/internal/store"
)

// validateState checks the mechanical rules for a state body: valid YAML and
// within the byte cap. Nothing semantic is checked (spec §11.4).
func validateState(body string, maxBytes int) error {
	if len(body) > maxBytes {
		return cli.Invalid("state is %d bytes; the cap is %d (state must load losslessly — trim it or raise state_max_bytes in config.yaml)", len(body), maxBytes)
	}
	dec := yaml.NewDecoder(strings.NewReader(body))
	var v any
	if err := dec.Decode(&v); err != nil {
		return cli.Invalid("state is not valid YAML: %v", err)
	}
	var extra any
	if err := dec.Decode(&extra); err == nil {
		return cli.Invalid("state must contain exactly one YAML or JSON document")
	} else if !errors.Is(err, io.EOF) {
		return cli.Invalid("state is not valid YAML: %v", err)
	}
	return nil
}

func newStateCmd(a *app) *cobra.Command {
	c := &cobra.Command{
		Use:   "state",
		Short: "Get or replace an agent's named working state",
		Long: `State is a small YAML snapshot of what is true now. It is loaded directly
into capsules, never compiled, and replaced with compare-and-swap:
  state get  <agent>/<name>
  state put  <agent>/<name> --expect none|<current-id> [--stdin]
  state put  <name> --context ctx_N ...        (agent taken from the context)`,
	}
	c.AddCommand(newStateGetCmd(a), newStatePutCmd(a))
	return commandGroup(c)
}

func newStateGetCmd(a *app) *cobra.Command {
	var format string
	c := &cobra.Command{
		Use:   "get <agent>/<name>",
		Short: "Print the current state document",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			agent, name, err := cli.SplitAgentName(args[0])
			if err != nil {
				return err
			}
			if err := store.ValidAgentName(agent); err != nil {
				return err
			}
			if err := store.ValidRecordName("state", name); err != nil {
				return err
			}
			switch format {
			case "yaml", "", "json", "id":
			default:
				return cli.Invalid("unknown format %q (yaml|json|id)", format)
			}
			if err := a.open(); err != nil {
				return err
			}
			recs, err := store.ListRecords(a.st.DB, store.Filter{Agent: agent, Lane: "state", Kind: "working-state", Name: name})
			if err != nil {
				return err
			}
			if len(recs) == 0 {
				return cli.NotFound("no active state %q for agent %s", name, agent)
			}
			rec := recs[0]
			switch format {
			case "yaml", "":
				// Body verbatim, with the id on stderr so a caller can carry it
				// into --expect without parsing.
				fmt.Fprintf(a.stderr, "nine-tails: %s (use --expect %s to replace)\n", rec.ID, rec.ID)
				_, err := fmt.Fprintln(a.stdout, rec.Body)
				return err
			case "json":
				return cli.WriteJSON(a.stdout, rec)
			case "id":
				_, err := fmt.Fprintln(a.stdout, rec.ID)
				return err
			}
			return nil
		},
	}
	c.Flags().StringVar(&format, "format", "yaml", "yaml (body verbatim)|json (envelope)|id")
	return c
}

func newStatePutCmd(a *app) *cobra.Command {
	var expect, context, format string
	var meta []string
	var stdin bool
	c := &cobra.Command{
		Use:   "put [<agent>/]<name> --expect none|<id> [--] [TEXT]",
		Short: "Replace state with compare-and-swap (--expect none to create)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateRecordFormat(format); err != nil {
				return err
			}
			if !cmd.Flags().Changed("expect") {
				return cli.Invalid("--expect is required: 'none' to create, or the current state id (shown in the capsule heading and by `state get`)")
			}
			if expect != "none" && (!strings.HasPrefix(expect, "state_") || !store.IsID(expect)) {
				return cli.Invalid("--expect must be 'none' or a state id like state_18, got %q", expect)
			}
			if err := a.open(); err != nil {
				return err
			}
			var agent, name string
			if strings.Contains(args[0], "/") {
				var err error
				agent, name, err = cli.SplitAgentName(args[0])
				if err != nil {
					return err
				}
				if context != "" {
					ctxAgent, err := a.contextAgent(context)
					if err != nil {
						return err
					}
					if ctxAgent != agent {
						return cli.Invalid("%s belongs to %s, not %s", context, ctxAgent, agent)
					}
				}
			} else {
				if context == "" {
					return cli.Invalid("expected <agent>/<name>, or a bare <name> with --context")
				}
				ctxAgent, err := a.contextAgent(context)
				if err != nil {
					return err
				}
				agent, name = ctxAgent, args[0]
			}
			if err := store.ValidAgentName(agent); err != nil {
				return err
			}
			if err := store.ValidRecordName("state", name); err != nil {
				return err
			}
			body, err := cli.ReadBody(args[1:], stdin, a.stdin, false)
			if err != nil {
				return err
			}
			if err := validateState(body, a.cfg.StateMaxBytes); err != nil {
				return err
			}
			m, err := metaFlag(meta)
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
				rec, err = store.PutNamed(tx, store.NewRecord{Agent: agent, Lane: "state", Kind: "working-state", Name: name, Body: body, Meta: m, OriginContext: context}, expect)
				return err
			})
			if err != nil {
				if errors.Is(err, store.ErrConflict) {
					return cli.Conflict("%s", strings.TrimPrefix(err.Error(), store.ErrConflict.Error()+": "))
				}
				return err
			}
			return a.printRecord(format, rec)
		},
	}
	c.Flags().StringVar(&expect, "expect", "", "required: 'none' or the id that must currently be active")
	c.Flags().StringVar(&context, "context", "", "originating context id (origin, not scope); supplies the agent for a bare <name>")
	c.Flags().StringArrayVar(&meta, "meta", nil, "applicability metadata key=value (repeatable)")
	c.Flags().BoolVar(&stdin, "stdin", false, "read the YAML from stdin")
	c.Flags().StringVar(&format, "format", "id", "id|json|yaml")
	return c
}
