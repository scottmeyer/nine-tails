package main

import (
	"fmt"
	"strings"

	"github.com/scottmeyer/nine-tails/internal/cli"
	"github.com/scottmeyer/nine-tails/internal/store"
	"github.com/spf13/cobra"
)

func newConsolidateCmd(a *app) *cobra.Command {
	var req store.ConsolidateRequest
	var stdin bool
	var format string
	c := &cobra.Command{
		Use:   "consolidate [TEXT] --context <receipt> --source <record> --source <record> --reason <reason>",
		Short: "Replace related guidance or recall while preserving its sources",
		Long: `Consolidate at least two active guidance or recall records owned by the context's agent.
Supply the complete replacement text and why these records belong together.
Every source must have exactly the same metadata value sets; the replacement
keeps that scope and inferred lane. Context metadata is provenance, never
replacement scope. Guidance and recall cannot be mixed.

Kinds must agree unless --kind deliberately chooses the replacement kind.
Brief items, state and definitions cannot be consolidated. Inspect a
brief item for its underlying guidance sources first. Sources remain inspectable
and their references navigate to the replacement, including later corrections.
The write is atomic: a stale source rejects the whole operation. Dependent
compiled guidance is invalidated; recall consolidation never changes a brief
generation and is immediately available to recall retrieval and the library.

JSON and YAML return the new envelope, local ref, reason and historical
source envelopes. The default prints only the new canonical record ID.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateRecordFormat(format); err != nil {
				return err
			}
			if req.Context == "" {
				return cli.Invalid("--context is required")
			}
			if cmd.Flags().Changed("kind") && strings.TrimSpace(req.Kind) == "" {
				return cli.Invalid("--kind must be nonempty when supplied")
			}
			body, err := cli.ReadBody(args, stdin, a.stdin, false)
			if err != nil {
				return err
			}
			req.Body = body
			if err := a.open(); err != nil {
				return err
			}
			result, err := a.st.Consolidate(req)
			if err != nil {
				return err
			}
			if format == "id" || format == "" {
				_, err = fmt.Fprintln(a.stdout, result.ID)
				return err
			}
			return cli.Write(a.stdout, format, result)
		},
	}
	c.Flags().StringVar(&req.Context, "context", "", "required originating receipt (ctx_... or @ref); selects the agent")
	c.Flags().StringArrayVar(&req.Sources, "source", nil, "active guidance or recall record ID or @ref (repeat at least twice)")
	c.Flags().StringVar(&req.Reason, "reason", "", "required reason for consolidating these records")
	c.Flags().StringVar(&req.Kind, "kind", "", "replacement kind; required only when source kinds differ")
	c.Flags().BoolVar(&stdin, "stdin", false, "read complete replacement text from stdin")
	c.Flags().StringVar(&format, "format", "id", "id (one line) | json | yaml")
	return c
}
