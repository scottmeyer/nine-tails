package main

import (
	"fmt"
	"strings"
	"text/tabwriter"
	"unicode"

	"github.com/scottmeyer/nine-tails/internal/cli"
	"github.com/scottmeyer/nine-tails/internal/store"
	"github.com/spf13/cobra"
)

func newRefsCmd(a *app) *cobra.Command {
	var kind, agent, query, format string
	var meta []string
	var limit int
	c := &cobra.Command{
		Use: "refs", Short: "Find readable references to sessions, signals, and knowledge",
		Long: `List recent identities with stable short references such as @42.
Use a reference wherever a command expects an existing ID: inspect @42,
load coder --context @42, close @42, or note --context @42 --supersedes @17.
The command still checks the expected type and ownership after resolution.

References belong to this user store and never move to another identity.
They are not exported; canonical IDs remain the portable identities.
Use --meta repo-id=soccer-chess to see a project's set of references, or
--kind context / --kind signal to narrow the list. Filters apply before the
limit; --meta requires the listed entity to contain matching values.
JSON/YAML includes canonical IDs and full labels; the table keeps them short.`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if format != "table" && format != "json" && format != "yaml" {
				return cli.Invalid("unknown format %q (table|json|yaml)", format)
			}
			if limit < 1 || limit > 1000 {
				return cli.Invalid("--limit must be between 1 and 1000")
			}
			if kind != "" && kind != "context" && kind != "signal" && kind != "record" && kind != "generation" {
				return cli.Invalid("--kind must be context, signal, record, or generation")
			}
			m, err := metaFlag(meta)
			if err != nil {
				return err
			}
			if err := a.open(); err != nil {
				return err
			}
			rows, err := store.ListReferences(a.st.DB, store.ReferenceFilter{Kind: kind, Agent: agent, Query: query, Meta: m, Limit: limit})
			if err != nil {
				return err
			}
			if format != "table" {
				return cli.Write(a.stdout, format, rows)
			}
			w := tabwriter.NewWriter(a.stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "REF\tKIND\tAGENT\tSTATUS\tPURPOSE")
			for _, r := range rows {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", r.Ref, r.Kind, refLabel(r.Agent), r.Status, refLabel(r.Label))
			}
			return w.Flush()
		},
	}
	c.Flags().StringVar(&kind, "kind", "", "context|signal|record|generation")
	c.Flags().StringVar(&agent, "agent", "", "only this agent")
	c.Flags().StringVar(&query, "query", "", "case-insensitive label search")
	c.Flags().StringArrayVar(&meta, "meta", nil, "require matching metadata key=value (repeatable)")
	c.Flags().IntVar(&limit, "limit", 20, "maximum results after filtering (1..1000)")
	c.Flags().StringVar(&format, "format", "table", "table|json|yaml")
	return c
}

func refLabel(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	r := []rune(strings.Join(strings.Fields(s), " "))
	if len(r) > 100 {
		return string(r[:99]) + "…"
	}
	return string(r)
}

func (a *app) resolveReference(value string) (string, error) {
	if !strings.HasPrefix(value, "@") {
		return value, nil
	}
	if err := store.ValidateReference(value); err != nil {
		return "", err
	}
	if err := a.open(); err != nil {
		return "", err
	}
	return store.ResolveReference(a.st.DB, value)
}

// Only ID-taking slots are rewritten. Lesson bodies, tasks, JSON input,
// metadata, agent names, and state names may contain literal @ text.
func (a *app) resolveCommandReferences(cmd *cobra.Command, args []string) error {
	for _, name := range []string{"context", "supersedes", "expect", "expect-base", "expect-generation"} {
		flag := cmd.Flags().Lookup(name)
		if flag == nil || !strings.HasPrefix(flag.Value.String(), "@") {
			continue
		}
		resolved, err := a.resolveReference(flag.Value.String())
		if err != nil {
			return err
		}
		if err := flag.Value.Set(resolved); err != nil {
			return err
		}
	}
	path := strings.TrimPrefix(cmd.CommandPath(), "nine-tails ")
	switch path {
	case "inspect", "disable", "close", "signal ack", "context pin", "context unpin":
		if len(args) > 0 {
			resolved, err := a.resolveReference(args[0])
			if err != nil {
				return err
			}
			args[0] = resolved
		}
	}
	return nil
}
