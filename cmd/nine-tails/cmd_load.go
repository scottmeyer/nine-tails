package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/scottmeyer/nine-tails/internal/capsule"
	"github.com/scottmeyer/nine-tails/internal/cli"
	"github.com/scottmeyer/nine-tails/internal/store"
)

func newLoadCmd(a *app) *cobra.Command {
	var task, ctx, format string
	var meta []string
	var budget int
	c := &cobra.Command{
		Use:   "load <agent>",
		Short: "Resolve an agent into a context capsule and record a receipt",
		Long: `Assemble base instructions, current state, the active brief, recent
adjustments, tools, related agents and due signals into one capsule that fits
--budget tokens. Every load persists an immutable context receipt listing the
exact record ids emitted; its id appears in the capsule as
[nine-tails-context=ctx_N]. Pass --context to inherit a parent's metadata.
Anything omitted for budget is summarized on stderr (md) or in "truncated" (json).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.open(); err != nil {
				return err
			}
			if err := store.ValidAgentName(args[0]); err != nil {
				return err
			}
			m, err := metaFlag(meta)
			if err != nil {
				return err
			}
			if !cmd.Flags().Changed("budget") {
				budget = a.cfg.DefaultBudget
			}
			if budget <= 0 {
				return cli.Invalid("--budget must be positive")
			}
			switch format {
			case "md", "markdown", "", "json", "yaml":
			default:
				return cli.Invalid("unknown format %q (md|json|yaml)", format)
			}
			pol := capsule.Policy{BriefFloor: a.cfg.BriefFloor, RecentCap: a.cfg.RecentCap, ToolsCap: a.cfg.ToolsCap,
				SignalsCap: a.cfg.SignalsCap, SignalExcerptChars: a.cfg.SignalExcerptChars}
			cp, err := capsule.Load(a.st, capsule.Request{Agent: args[0], Task: task, Parent: ctx, Meta: m, Budget: budget, Policy: pol, Now: a.now()})
			if err != nil {
				if capsule.IsBudgetError(err) {
					// The capsule layer wraps its private sentinel as "budget: ..."
					// so errors.Is can classify it. That implementation detail is not
					// part of the command's byte-exact diagnostic contract.
					return cli.Budget("%s", strings.TrimPrefix(err.Error(), "budget: "))
				}
				return err
			}
			for _, sk := range cp.Skipped {
				fmt.Fprintf(a.stderr, "nine-tails: skipped %s: %s\n", sk.ID, sk.Reason)
			}
			switch format {
			case "json":
				return cli.WriteJSON(a.stdout, cp)
			case "yaml":
				return cli.WriteYAML(a.stdout, cp)
			}
			if s := cp.TruncationSummary(); s != "" {
				fmt.Fprintf(a.stderr, "nine-tails: %s\n", s)
			}
			_, err = a.stdout.Write([]byte(cp.Markdown))
			return err
		},
	}
	c.Flags().StringVar(&task, "task", "", "what this invocation is for (recorded on the receipt)")
	c.Flags().StringVar(&ctx, "context", "", "parent context id to inherit metadata from")
	c.Flags().StringArrayVar(&meta, "meta", nil, "ambient metadata key=value (repeatable)")
	c.Flags().IntVar(&budget, "budget", 0, "token budget for the capsule (default from config, 2000)")
	c.Flags().StringVar(&format, "format", "md", "md|json|yaml")
	return c
}
