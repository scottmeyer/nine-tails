package main

import (
	"database/sql"
	"strings"

	"github.com/scottmeyer/nine-tails/internal/capsule"
	"github.com/scottmeyer/nine-tails/internal/cli"
	"github.com/scottmeyer/nine-tails/internal/store"
	"github.com/spf13/cobra"
)

// Run before reference resolution, so malformed combinations never open a
// nonexistent home or let a stale local reference hide the useful diagnostic.
func validateRecallCheckArguments(cmd *cobra.Command, args []string, context, query, format string) error {
	for _, flag := range []string{"include", "lane", "kind", "name", "all", "coverage", "lint", "after"} {
		if cmd.Flags().Changed(flag) {
			return cli.Invalid("a recall check cannot be combined with --%s", flag)
		}
	}
	return validateRecallCheckSelectors(args, context, query, format)
}

// Shared by CLI and MCP before either adapter resolves local references.
func validateRecallCheckSelectors(args []string, context, query, format string) error {
	if len(args) != 1 {
		return cli.Invalid("a recall check requires one exact record ID or @ref and --context")
	}
	if format != "json" && format != "yaml" {
		return cli.Invalid("unknown format %q (json|yaml)", format)
	}
	if err := store.ValidateBody(query); err != nil {
		return err
	}
	for _, value := range []string{args[0], context} {
		if strings.HasPrefix(value, "@") {
			if err := store.ValidateReference(value); err != nil {
				return err
			}
		} else if !store.IsID(value) {
			return cli.Invalid("a recall check requires exact record and receipt IDs or @refs")
		}
	}
	if !strings.HasPrefix(context, "@") && !strings.HasPrefix(context, "ctx_") {
		return cli.Invalid("--context requires a context receipt")
	}
	for _, prefix := range []string{"ctx_", "gen_", "lease_"} {
		if strings.HasPrefix(args[0], prefix) {
			return cli.Invalid("a recall check requires a recall record, not %s", args[0])
		}
	}
	return nil
}

func (a *app) inspectRecallCheck(id, contextID string, query *string, format string) error {
	// Local references retain their resource type after resolution.
	if !strings.HasPrefix(contextID, "ctx_") {
		return cli.Invalid("--context requires a context receipt")
	}
	for _, prefix := range []string{"ctx_", "gen_", "lease_"} {
		if strings.HasPrefix(id, prefix) {
			return cli.Invalid("a recall check requires a recall record, not %s", id)
		}
	}
	if err := a.open(); err != nil {
		return err
	}
	var out recordView
	err := a.st.Tx(func(tx *sql.Tx) error {
		ctx, err := store.GetContext(tx, contextID)
		if err != nil {
			return err
		}
		rec, err := store.GetRecord(tx, id)
		if err != nil {
			return err
		}
		check, err := capsule.CheckRecall(tx, rec, ctx, query, a.recipeHome())
		if err != nil {
			return err
		}
		value, _, err := a.inspectByIDFrom(tx, id)
		if err != nil {
			return err
		}
		out = value.(recordView)
		out.RecallCheck = check
		return nil
	})
	if err != nil {
		return err
	}
	return cli.Write(a.stdout, format, out)
}
