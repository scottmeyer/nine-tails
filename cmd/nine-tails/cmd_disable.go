package main

import (
	"database/sql"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/scottmeyer/nine-tails/internal/cli"
	"github.com/scottmeyer/nine-tails/internal/store"
)

// newDisableCmd retires a record: the spec's third status (§8.2), which no
// command produced until tools started coming and going.
func newDisableCmd(a *app) *cobra.Command {
	var format string
	c := &cobra.Command{
		Use:   "disable <id>",
		Short: "Retire an active record without deleting it",
		Long: `Set a record's status to disabled. A disabled record is never loaded,
called or compiled, its name is free for a new definition, and it stays
visible to inspect --all. Nothing is deleted or edited. Brief items belong
to their generation (compile a new one) and signals are acknowledged
(signal ack), so neither can be disabled. Prints the id.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateRecordFormat(format); err != nil {
				return err
			}
			id := args[0]
			if !cli.IsID(id) {
				return cli.Invalid("disable wants a record id, not %q", id)
			}
			if err := a.open(); err != nil {
				return err
			}
			var rec *store.Record
			err := a.st.Tx(func(tx *sql.Tx) error {
				r, err := store.GetRecord(tx, id)
				if err != nil {
					return err
				}
				switch {
				case r.Kind == "brief-item":
					return cli.Invalid("%s is a brief item; compile a new generation instead", id)
				case r.Lane == "signal":
					return cli.Invalid("%s is a signal; use signal ack", id)
				case r.Status != "active":
					return fmt.Errorf("%w: %s is %s, not active", store.ErrConflict, id, r.Status)
				}
				if err := store.SetStatus(tx, id, "disabled"); err != nil {
					return err
				}
				r.Status = "disabled"
				rec = r
				return nil
			})
			if err != nil {
				return err
			}
			return a.printRecord(format, rec)
		},
	}
	c.Flags().StringVar(&format, "format", "id", "id|json|yaml")
	return c
}
