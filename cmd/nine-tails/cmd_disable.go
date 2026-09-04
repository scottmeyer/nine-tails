package main

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/scottmeyer/nine-tails/internal/cli"
	"github.com/scottmeyer/nine-tails/internal/store"
)

// newDisableCmd retires a record: the spec's third status (§8.1), which no
// command produced until tools started coming and going.
func newDisableCmd(a *app) *cobra.Command {
	var format string
	c := &cobra.Command{
		Use:   "disable <id>",
		Short: "Retire an active record without deleting it",
		Long: `Set a record's status to disabled. A disabled record is never loaded,
called or compiled, its name is free for a new definition, and it stays
visible by id and in an agent's inspect --all history. No semantic content or
history is deleted or rewritten. Brief items belong to their generation
(compile a new one) and signals are acknowledged (signal ack), so neither can
be disabled.

Pass an immutable record id such as rec_..., base_..., or tool_..., not a
ctx_... context receipt. Use --supersedes on a writing command when replacing
a record; use disable only when it should have no successor. Disabling guidance
already represented in the active brief invalidates that compiled cache so no
blended item can retain the retired meaning.

The default format prints the affected id. JSON and YAML print its record
envelope.`,
		Example: `  nine-tails disable rec_01JABC...
  nine-tails inspect rec_01JABC...`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateRecordFormat(format); err != nil {
				return err
			}
			id := args[0]
			if strings.HasPrefix(id, "ctx_") {
				return cli.Invalid("disable wants a record id, not context receipt %s", id)
			}
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
				if r.Lane == "guidance" {
					if _, err := store.InvalidateGenerationForGuidance(tx, r.Agent, r.ID); err != nil {
						return err
					}
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
