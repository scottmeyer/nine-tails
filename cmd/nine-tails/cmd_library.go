package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/scottmeyer/nine-tails/internal/cli"
	"github.com/scottmeyer/nine-tails/internal/store"
)

const libraryPageBytes = 4096

type libraryEntry struct {
	store.RecallIndexEntry `yaml:",inline"`
	Inspect                string `json:"inspect" yaml:"inspect"`
}

type libraryNext struct {
	After   string `json:"after" yaml:"after"`
	Inspect string `json:"inspect" yaml:"inspect"`
}

type libraryPage struct {
	Agent      string         `json:"agent" yaml:"agent"`
	Order      string         `json:"order" yaml:"order"`
	Context    string         `json:"context,omitempty" yaml:"context,omitempty"`
	ContextRef string         `json:"context_ref,omitempty" yaml:"context_ref,omitempty"`
	Query      string         `json:"query" yaml:"query"`
	Entries    []libraryEntry `json:"entries" yaml:"entries"`
	Next       *libraryNext   `json:"next" yaml:"next"`
}

// Validate raw page syntax before the inherited hook resolves local handles.
// Existence, ownership and resolved record kinds still require the store.
func validateLibraryArguments(args []string, contextID, query, after, format string) error {
	if len(args) == 0 && contextID == "" {
		return cli.Invalid("--page requires an agent or --context")
	}
	if len(args) != 0 {
		if store.IsID(args[0]) || strings.HasPrefix(args[0], "@") {
			return cli.Invalid("--page wants an agent or --context, not a record ID")
		}
		if err := store.ValidAgentName(args[0]); err != nil {
			return err
		}
	}
	if format != "json" && format != "yaml" {
		return cli.Invalid("unknown format %q (json|yaml)", format)
	}
	if strings.HasPrefix(contextID, "@") {
		if err := store.ValidateReference(contextID); err != nil {
			return err
		}
	} else if contextID != "" && (!strings.HasPrefix(contextID, "ctx_") || !store.IsID(contextID)) {
		return cli.Invalid("--context must identify a context receipt (ctx_ ID or its @N reference)")
	}
	if strings.HasPrefix(after, "@") {
		if err := store.ValidateReference(after); err != nil {
			return err
		}
	} else if after != "" && (!store.IsID(after) || strings.HasPrefix(after, "ctx_") || strings.HasPrefix(after, "gen_")) {
		return fmt.Errorf("%w: recall cursor %q must identify a recall record", store.ErrInvalid, after)
	}
	if !utf8.ValidString(query) {
		return fmt.Errorf("%w: recall query must be valid UTF-8", store.ErrInvalid)
	}
	return nil
}

// Page a live index rather than materializing the entire history. The cursor
// is the last included record, so the row that exceeded the soft target is
// still returned by the next page. Exact inspection opens its complete body.
func (a *app) inspectLibrary(args []string, contextID, query, after, format string) error {
	// A syntactically valid @N can resolve to a non-context entity.
	if contextID != "" && (!strings.HasPrefix(contextID, "ctx_") || !store.IsID(contextID)) {
		return cli.Invalid("--context must identify a context receipt (ctx_ ID or its @N reference)")
	}
	if err := a.open(); err != nil {
		return err
	}
	page := libraryPage{Query: query, Order: "newest-first", Entries: []libraryEntry{}}
	err := a.st.Tx(func(tx *sql.Tx) error {
		meta := store.Meta{}
		if len(args) != 0 {
			page.Agent = args[0]
		}
		if contextID != "" {
			ctx, err := store.GetContext(tx, contextID)
			if err != nil {
				return err
			}
			if page.Agent != "" && page.Agent != ctx.Agent {
				return cli.Invalid("context belongs to %s, not %s", ctx.Agent, page.Agent)
			}
			page.Agent, page.Context, meta = ctx.Agent, ctx.ID, ctx.Meta
			page.ContextRef, err = store.Reference(tx, ctx.ID)
			if err != nil {
				return err
			}
		}
		if err := store.ValidAgentName(page.Agent); err != nil {
			return err
		}
		exists, err := store.AgentExists(tx, page.Agent)
		if err != nil {
			return err
		}
		if !exists {
			return cli.NotFound("no records for agent %q", page.Agent)
		}
		used := 0
		return store.WalkRecallIndex(tx, store.RecallIndexRequest{Agent: page.Agent, Meta: meta, Query: query, After: after}, func(r store.RecallIndexEntry) (bool, error) {
			entry := libraryEntry{RecallIndexEntry: r, Inspect: cli.StoreCommand(a.recipeHome(), "inspect "+r.Ref)}
			encoded, err := json.MarshalIndent(entry, "", "  ")
			if err != nil {
				return false, err
			}
			if len(page.Entries) > 0 && used+len(encoded) > libraryPageBytes {
				last := page.Entries[len(page.Entries)-1].Ref
				command := cli.StoreCommand(a.recipeHome(), "inspect "+page.Agent+" --page")
				if page.ContextRef != "" {
					command += " --context " + page.ContextRef
				}
				if query != "" {
					command += " --query " + quoteLibraryArgument(query)
				}
				command += " --after " + last
				page.Next = &libraryNext{After: last, Inspect: command}
				return false, nil
			}
			page.Entries = append(page.Entries, entry)
			used += len(encoded)
			return true, nil
		})
	})
	if err != nil {
		return err
	}
	return cli.Write(a.stdout, format, page)
}

// Recipe text is inert data. Quote a search phrase as one POSIX-shell argument
// so apostrophes, newlines, dollar signs and backticks never become shell code.
func quoteLibraryArgument(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}
