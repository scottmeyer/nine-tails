package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/scottmeyer/nine-tails/internal/cli"
	"github.com/scottmeyer/nine-tails/internal/store"
)

// newMigrateCmd is the only command that changes a store's schema. Every
// other command refuses an older store (exit 4) so that running a newer
// binary against a shared or bundled store is never a silent, one-way
// upgrade: an older binary cannot open a newer schema.
func newMigrateCmd(a *app) *cobra.Command {
	var format string
	c := &cobra.Command{
		Use:   "migrate",
		Short: "Upgrade this store's schema to the one this binary uses",
		Long: `Upgrade the store under --home (or NINE_TAILS_HOME) to this binary's schema.
Other commands refuse a store whose schema is older than theirs and print
this command's name; nothing upgrades a store as a side effect.

Before changing anything, the database is copied to nine-tails.db.v<old>.bak
beside itself (a timestamped name when that file already exists). The copy is
what the previous binary can still open: restore it by moving it back into
place. A store that is already current is left untouched and reports no
backup. Record bodies, metadata, receipts and history are never rewritten by
a migration; it adds tables, columns and identifiers.

Stop other writers before migrating a store they share.`,
		Example: `  nine-tails migrate
  nine-tails migrate --home /path/to/store --format json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			switch format {
			case "text", "json", "yaml":
			default:
				return cli.Invalid("unknown format %q (text|json|yaml)", format)
			}
			var m store.Migration
			if err := a.openWith(func(home string) (*store.Store, error) {
				st, result, err := store.Migrate(home)
				m = result
				return st, err
			}); err != nil {
				return err
			}
			migrated := m.From != m.To
			if format == "text" {
				if !migrated {
					_, err := fmt.Fprintf(a.stdout, "store schema %d is current\n", m.To)
					return err
				}
				_, err := fmt.Fprintf(a.stdout, "migrated %s from schema %d to %d; backup %s\n", a.home, m.From, m.To, m.Backup)
				return err
			}
			return cli.Write(a.stdout, format, map[string]any{
				"home": a.home, "from": m.From, "to": m.To, "migrated": migrated, "backup": m.Backup,
			})
		},
	}
	c.Flags().StringVar(&format, "format", "text", "text|json|yaml")
	return c
}
