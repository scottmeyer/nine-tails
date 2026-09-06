package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/scottmeyer/nine-tails/internal/capsule"
	"github.com/scottmeyer/nine-tails/internal/cli"
	"github.com/scottmeyer/nine-tails/internal/starter"
	"github.com/scottmeyer/nine-tails/internal/store"
)

func newLoadCmd(a *app) *cobra.Command {
	var agent, task, query, ctx, format string
	var meta []string
	c := &cobra.Command{
		Use:   "load [<agent> | --agent NAME]",
		Short: "Resolve an agent into a context capsule and record a receipt",
		Long: `Assemble base instructions, current state, the active brief, recent
adjustments, tools, related agents, relevant recall and due signals into one
capsule. Guidance is never cut for size: the capsule reports its size and how many
adjustments are uncompiled, and past the configured threshold it advises a
compile on stderr. Every load persists an immutable context receipt listing
the exact record ids emitted. Its opaque ctx_... id appears as
[nine-tails-context=...] in the capsule and is the only kind of id accepted by
later --context flags; record ids and state CAS ids are not contexts. Pass
--context to inherit a parent's metadata and link the new receipt.
Explicit --meta keys replace inherited values for those keys; unspecified
keys still inherit. Repeat --meta with the same key to select multiple values.

The --task value is stored on that receipt and retrieves up to three matching
recall excerpts. Use a concise, non-sensitive purpose and keep the complete
task in the calling harness conversation. --query overrides recall retrieval;
--query "" disables it. Retrieval never removes explicit guidance.`,
		Example: `  nine-tails load pilot --task "Review this change" --meta repo-id=acme --meta harness=my-harness
  nine-tails load reviewer --task "Review this change" --context <pilot-context-id>`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("agent") && agent == "" {
				return cli.Invalid("--agent must not be empty")
			}
			if len(args) == 1 {
				if agent != "" && agent != args[0] {
					return cli.Invalid("conflicting agent selection: positional %q and --agent %q", args[0], agent)
				}
				agent = args[0]
			}
			if agent == "" {
				return cli.Invalid("missing agent: pass <agent> or --agent NAME")
			}
			if err := a.open(); err != nil {
				return err
			}
			if err := store.ValidAgentName(agent); err != nil {
				return err
			}
			// pilot is the entry agent (DESIGN §1.2): loading it on a store that
			// lacks it seeds pilot and reflector from the documents embedded in
			// the binary, so one binary bootstraps any store. Existing agents,
			// whoever made them, are never touched.
			if agent == "pilot" {
				seeded, err := starter.Seed(a.st, a.cfg.StateMaxBytes)
				if err != nil {
					return err
				}
				if len(seeded) > 0 {
					fmt.Fprintf(a.stderr, "nine-tails: seeded %s from the built-in starter (ordinary agents; edit with nine-tails base <agent>)\n", strings.Join(seeded, " and "))
				}
			}
			m, err := metaFlag(meta)
			if err != nil {
				return err
			}
			switch format {
			case "md", "markdown", "", "json", "yaml":
			default:
				return cli.Invalid("unknown format %q (md|json|yaml)", format)
			}
			var recallQuery *string
			if cmd.Flags().Changed("query") {
				recallQuery = &query
			}
			cp, err := capsule.Load(a.st, capsule.Request{Agent: agent, Task: task, Query: recallQuery, Parent: ctx, Meta: m, SignalExcerptChars: a.cfg.SignalExcerptChars, Now: a.now()})
			if err != nil {
				return err
			}
			for _, sk := range cp.Skipped {
				fmt.Fprintf(a.stderr, "nine-tails: skipped %s: %s\n", sk.ID, sk.Reason)
			}
			// Size is advice, never enforcement (DESIGN §7): when the capsule has
			// grown past the configured threshold and a compile would shrink it,
			// say so. The pilot guide tells the model what to do with the advice.
			if limit := a.cfg.CompileAdviceTokens; limit > 0 && cp.EstimatedTokens > limit && cp.UncompiledAdjustments > 0 {
				fmt.Fprintf(a.stderr, "nine-tails: capsule is %d estimated tokens with %d uncompiled adjustments; optional condensation: `nine-tails compile %s`\n", cp.EstimatedTokens, cp.UncompiledAdjustments, agent)
			}
			switch format {
			case "json":
				return cli.WriteJSON(a.stdout, cp)
			case "yaml":
				return cli.WriteYAML(a.stdout, cp)
			}
			_, err = a.stdout.Write([]byte(cp.Markdown))
			return err
		},
	}
	c.Flags().StringVar(&agent, "agent", "", "agent name (alternative to the positional name)")
	c.Flags().StringVar(&task, "task", "", "concise non-sensitive purpose stored on the receipt; the caller retains the full task")
	c.Flags().StringVar(&query, "query", "", "recall search override (default: --task; explicitly empty disables recall)")
	c.Flags().StringVar(&ctx, "context", "", "parent context receipt id (ctx_...); inherit its metadata")
	c.Flags().StringArrayVar(&meta, "meta", nil, "ambient key=value; supplied keys replace inherited values (repeatable)")
	c.Flags().StringVar(&format, "format", "md", "md|json|yaml")
	return c
}
