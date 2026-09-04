package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/scottmeyer/nine-tails/internal/cli"
	"github.com/scottmeyer/nine-tails/internal/store"
)

func newContextCmd(a *app) *cobra.Command {
	c := &cobra.Command{
		Use:   "context",
		Short: "List, pin, or garbage-collect context receipts",
		Long: `A context is an immutable receipt of one load: agent, task, resolved
metadata, estimated size, parent, and the exact record ids emitted. Read one with
` + "`nine-tails inspect ctx_N`" + `.
  context list [--agent A] [--limit N]
  context pin|unpin <ctx-id>
  context gc [--older-than 30d] [--dry-run]`,
	}

	var agent, listFormat string
	var limit int
	list := &cobra.Command{
		Use:   "list",
		Short: "List receipts, newest first",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if agent != "" {
				if err := store.ValidAgentName(agent); err != nil {
					return err
				}
			}
			if limit <= 0 {
				return cli.Invalid("--limit must be positive, got %d", limit)
			}
			switch listFormat {
			case "json", "yaml", "text":
			default:
				return cli.Invalid("unknown format %q (json|yaml|text)", listFormat)
			}
			if err := a.open(); err != nil {
				return err
			}
			ctxs, err := store.ListContexts(a.st.DB, agent, limit)
			if err != nil {
				return err
			}
			if listFormat == "text" {
				for _, c := range ctxs {
					fmt.Fprintf(a.stdout, "%s\t%s\t%s\t%s\n", c.ID, c.Agent, c.CreatedAt, c.Task)
				}
				return nil
			}
			if ctxs == nil {
				ctxs = []*store.Context{}
			}
			return cli.Write(a.stdout, listFormat, ctxs)
		},
	}
	list.Flags().StringVar(&agent, "agent", "", "only this agent")
	list.Flags().IntVar(&limit, "limit", 50, "maximum receipts")
	list.Flags().StringVar(&listFormat, "format", "json", "json|yaml|text")

	pin := func(use string, pinned bool) *cobra.Command {
		return &cobra.Command{
			Use:   use + " <ctx-id>",
			Short: fmt.Sprintf("%s a receipt so garbage collection %s it", use, map[bool]string{true: "keeps", false: "may delete"}[pinned]),
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				if !store.IsID(args[0]) || !strings.HasPrefix(args[0], "ctx_") {
					return cli.Invalid("expected a context id like ctx_9, got %q", args[0])
				}
				if err := a.open(); err != nil {
					return err
				}
				if err := store.PinContext(a.st.DB, args[0], pinned); err != nil {
					return err
				}
				_, err := fmt.Fprintln(a.stdout, args[0])
				return err
			},
		}
	}

	var olderThan, gcFormat string
	var dryRun bool
	gc := &cobra.Command{
		Use:   "gc",
		Short: "Delete old unpinned receipts that no active record references as origin",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			switch gcFormat {
			case "text", "json":
			default:
				return cli.Invalid("unknown format %q (text|json)", gcFormat)
			}
			var explicitRetention time.Duration
			if olderThan != "" {
				d, err := cli.ParseDuration(olderThan)
				if err != nil {
					return err
				}
				if d <= 0 {
					return cli.Invalid("--older-than must be positive, got %q", olderThan)
				}
				explicitRetention = d
			}
			if err := a.open(); err != nil {
				return err
			}
			retention := time.Duration(a.cfg.ContextRetentionDays) * 24 * time.Hour
			if olderThan != "" {
				retention = explicitRetention
			}
			if retention <= 0 {
				return cli.Invalid("context retention must be positive (context_retention_days is %d in config.yaml)", a.cfg.ContextRetentionDays)
			}
			ids, err := store.GCContexts(a.st, a.now().Add(-retention), dryRun)
			if err != nil {
				return err
			}
			if ids == nil {
				ids = []string{}
			}
			if gcFormat == "json" {
				key := "deleted"
				if dryRun {
					key = "would_delete"
				}
				return cli.WriteJSON(a.stdout, map[string]any{key: ids})
			}
			for _, id := range ids {
				fmt.Fprintln(a.stdout, id)
			}
			return nil
		},
	}
	gc.Flags().StringVar(&olderThan, "older-than", "", "age threshold like 30d, 12h (default from config, 30d)")
	gc.Flags().BoolVar(&dryRun, "dry-run", false, "report what would be deleted without deleting")
	gc.Flags().StringVar(&gcFormat, "format", "text", "text (one id per line)|json")

	c.AddCommand(list, pin("pin", true), pin("unpin", false), gc)
	return commandGroup(c)
}
