package main

import (
	"database/sql"
	"strings"

	"github.com/spf13/cobra"

	"github.com/scottmeyer/nine-tails/internal/cli"
	"github.com/scottmeyer/nine-tails/internal/store"
)

// newDisableCmd retires a record: the spec's third status (§8.1), which no
// command produced until tools started coming and going.
func newDisableCmd(a *app) *cobra.Command {
	var format, contextID, reason string
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

For deliberate forgetting, pass --context and --reason together to retain
why the record stopped surfacing. The receipt must belong to its agent.
Inspect the exact record first; age or repeated retrieval is not evidence
that a preference is obsolete. Its original body stays inspectable.

The default format prints the affected id. JSON and YAML print its record
envelope.`,
		Example: `  nine-tails disable rec_01JABC...
  nine-tails inspect rec_01JABC...`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateRecordFormat(format); err != nil {
				return err
			}
			if cmd.Flags().Changed("context") || cmd.Flags().Changed("reason") {
				if contextID == "" || strings.TrimSpace(reason) == "" {
					return cli.Invalid("--context and a nonempty --reason are required together")
				}
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
				var err error
				rec, err = store.RetireRecord(tx, id, contextID, reason)
				return err
			})
			if err != nil {
				return err
			}
			return a.printRecord(format, rec)
		},
	}
	c.Flags().StringVar(&format, "format", "id", "id|json|yaml")
	c.Flags().StringVar(&contextID, "context", "", "receipt of the agent making this retirement (requires --reason)")
	c.Flags().StringVar(&reason, "reason", "", "why the record should stop surfacing (requires --context)")
	return c
}
