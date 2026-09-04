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
  state get  <name> --context ctx_N           (agent taken from the context)
  state put  <agent>/<name> --expect none|<current-id> [--stdin]
  state put  <name> --context ctx_N ...        (agent taken from the context)`,
	}
	c.AddCommand(newStateGetCmd(a), newStatePutCmd(a))
	return commandGroup(c)
}

func newStateGetCmd(a *app) *cobra.Command {
	var format, context string
	c := &cobra.Command{
		Use:   "get [<agent>/]<name> [--context ctx_N]",
		Short: "Print the current state document",
		Long: `With the default --format yaml, write the YAML state body verbatim to
stdout and the active state_... record id to stderr as the compare-and-swap
hint for state put. --format json writes the full record envelope to stdout;
--format id writes only the state record id to stdout. Those alternate formats
do not emit the hint.

A state_... record id belongs in state put --expect. A ctx_... context receipt
id belongs in --context; the two are not interchangeable. A bare name requires
--context to select its agent. An explicit agent must match that receipt's
owner. The read returns current state, not the version seen by the receipt;
context metadata does not filter an explicitly named state.`,
		Example: `  nine-tails state get pr-review/working
  nine-tails state get pr-review/working --format json
  nine-tails state get pr-review/working --format id
  nine-tails state get working --context ctx_72`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch format {
			case "yaml", "", "json", "id":
			default:
				return cli.Invalid("unknown format %q (yaml|json|id)", format)
			}
			agent, name, err := a.stateTarget(args[0], context)
			if err != nil {
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
	c.Flags().StringVar(&format, "format", "yaml", "yaml (body verbatim stdout, CAS id stderr)|json (envelope stdout)|id (state id stdout)")
	c.Flags().StringVar(&context, "context", "", "context receipt id (ctx_...) selecting the agent for a bare name; must match an explicit agent")
	return c
}

// stateTarget validates syntax before opening the store, then resolves an
// explicitly supplied receipt. Never guess an owner from ambient context.
func (a *app) stateTarget(target, context string) (agent, name string, err error) {
	if strings.Contains(target, "/") {
		agent, name, err = cli.SplitAgentName(target)
		if err != nil {
			return "", "", err
		}
		if err := store.ValidAgentName(agent); err != nil {
			return "", "", err
		}
	} else {
		if context == "" {
			return "", "", cli.Invalid("expected <agent>/<name>, or a bare <name> with --context")
		}
		name = target
	}
	if err := store.ValidRecordName("state", name); err != nil {
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
	return agent, name, nil
}

func newStatePutCmd(a *app) *cobra.Command {
	var expect, context, format string
	var meta []string
	var stdin, clearMeta bool
	c := &cobra.Command{
		Use:   "put [<agent>/]<name> --expect none|<state-id> [--] [TEXT]",
		Short: "Replace state with compare-and-swap (--expect none to create)",
		Long: `Replace state with a required compare-and-swap guard. Use --expect none
to create safely when no active state exists, or pass the active state_...
record id reported by state get. State is loaded directly into capsules and
is never compiled.

--context takes a ctx_... context receipt id: it records the origin and
supplies the agent when the target is a bare <name>. A context id is not a
state record id and cannot be used for --expect. By default stdout is the new
state_... record id; --format json or yaml prints its record envelope.

Omitting --meta preserves existing state metadata; new state is unqualified.
--meta replaces the complete metadata set; --clear-meta explicitly removes it.
--context records provenance, never implicit scope.`,
		Example: `  nine-tails state put pr-review/working --expect none "status: ready"
  nine-tails state put working --context ctx_72 --expect state_17 --stdin < state.yml`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateRecordFormat(format); err != nil {
				return err
			}
			if clearMeta && cmd.Flags().Changed("meta") {
				return cli.Invalid("--clear-meta and --meta are mutually exclusive")
			}
			if !cmd.Flags().Changed("expect") {
				return cli.Invalid("--expect is required: 'none' to create, or the current state id (shown in the capsule heading and by `state get`)")
			}
			if expect != "none" && (!strings.HasPrefix(expect, "state_") || !store.IsID(expect)) {
				return cli.Invalid("--expect must be 'none' or a state id like state_18, got %q", expect)
			}
			agent, name, err := a.stateTarget(args[0], context)
			if err != nil {
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
				rec, err = store.PutState(tx, store.NewRecord{Agent: agent, Lane: "state", Kind: "working-state", Name: name, Body: body, Meta: m, OriginContext: context}, expect, !clearMeta && !cmd.Flags().Changed("meta"))
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
	c.Flags().StringVar(&expect, "expect", "", "required CAS: 'none' to create, or the active state record id (state_...)")
	c.Flags().StringVar(&context, "context", "", "originating context receipt id (ctx_..., not a state id); supplies the agent for a bare <name>")
	c.Flags().StringArrayVar(&meta, "meta", nil, "replace all applicability metadata with key=value pairs (repeatable); omitted preserves existing metadata")
	c.Flags().BoolVar(&clearMeta, "clear-meta", false, "explicitly remove all state metadata (mutually exclusive with --meta)")
	c.Flags().BoolVar(&stdin, "stdin", false, "read the YAML from stdin")
	c.Flags().StringVar(&format, "format", "id", "id|json|yaml")
	return c
}
