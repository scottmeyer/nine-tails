package main

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/scottmeyer/nine-tails/internal/cli"
	"github.com/scottmeyer/nine-tails/internal/store"
)

// newCloseCmd is the run's verdict on what it was shown (DESIGN §18).
func newCloseCmd(a *app) *cobra.Command {
	var format string
	c := &cobra.Command{
		Use:   "close <ctx-id> [<id-or-ordinal>=<mark>]...",
		Short: "Optionally close a receipt, with optional feedback marks",
		Long: `Close the receipt of one load. Plain close needs no marks or reflection;
learning is already durable before closure. If useful, add feedback on
rendered records by id or by their ordinal in the receipt
(` + "`nine-tails inspect ctx_N`" + ` lists both, with an excerpt). Marks:
  + +++ +++++   it applied: nudged me, shaped the work, decisive
  - --- -----   it hindered: cost a detour, misled me, caused a mistake
  X             it is wrong as a statement; write the correction first with
                nine-tails avoid|note --context ctx_N "...", then mark it
  ?             it never came up (the default for anything unlisted)
Marks report usefulness, not verified correctness or compliance. Check
measurable claims before reporting success. Correct a wrong clause even when
another clause in the same record helped; a positive mark must not conceal it.
A receipt closes once. Prints the receipt id.`,
		Args: cobra.MinimumNArgs(1),
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
				c, err := store.GetContext(tx, id)
				if err != nil {
					return err
				}
				byOrdinal := map[int]string{}
				for _, r := range c.Rendered {
					byOrdinal[r.Ordinal] = r.RecordID
				}
				marks := map[string]string{}
				for _, arg := range args[1:] {
					key, mark, ok := strings.Cut(arg, "=")
					if !ok || key == "" || mark == "" {
						return cli.Invalid("want <id-or-ordinal>=<mark>, got %q", arg)
					}
					rid := key
					if n, err := strconv.Atoi(key); err == nil {
						var found bool
						if rid, found = byOrdinal[n]; !found {
							return cli.Invalid("%s has no rendered record at ordinal %d", id, n)
						}
					} else if !cli.IsID(key) {
						return cli.Invalid("%q is neither a record id nor an ordinal", key)
					}
					if _, dup := marks[rid]; dup {
						return cli.Invalid("%s is marked twice", rid)
					}
					marks[rid] = mark
				}
				ctx, err = store.CloseContext(tx, id, marks)
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
