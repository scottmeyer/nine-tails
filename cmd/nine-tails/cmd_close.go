package main

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/scottmeyer/nine-tails/internal/cli"
	"github.com/scottmeyer/nine-tails/internal/store"
)

// Closing a receipt is optional lifecycle bookkeeping, independent of learning.
func newCloseCmd(a *app) *cobra.Command {
	var format string
	c := &cobra.Command{
		Use:    "close <receipt>",
		Short:  "Finish an open receipt (optional bookkeeping)",
		Hidden: true,
		Long: `Close the receipt of one load. Learning is durable as soon as it is saved;
closure is optional and never required before loading an agent again.
Pass the receipt's local reference or canonical context ID. A receipt closes
once. Prints its canonical ID; JSON and YAML return the closed receipt.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateRecordFormat(format); err != nil {
				return err
			}
			id := args[0]
			if !strings.HasPrefix(id, "ctx_") || !cli.IsID(id) {
				return cli.Invalid("close wants a context id, not %q", id)
			}
			if err := a.open(); err != nil {
				return err
			}
			var ctx *store.Context
			err := a.st.Tx(func(tx *sql.Tx) error {
				var err error
				ctx, err = store.CloseReceipt(tx, id)
				return err
			})
			if err != nil {
				return err
			}
			if format == "id" {
				_, err := fmt.Fprintln(a.stdout, ctx.ID)
				return err
			}
			return cli.Write(a.stdout, format, ctx)
		},
	}
	c.Flags().StringVar(&format, "format", "id", "id|json|yaml")
	return c
}
