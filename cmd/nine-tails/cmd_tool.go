package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/scottmeyer/nine-tails/internal/cli"
	"github.com/scottmeyer/nine-tails/internal/store"
	"github.com/scottmeyer/nine-tails/internal/tool"
)

func newToolCmd(a *app) *cobra.Command {
	c := &cobra.Command{
		Use:   "tool",
		Short: "Register executable tools backed by immutable script artifacts",
		Long: `A tool is a named definition (lane definition, kind tool) whose YAML body
says how to run an executable. tool add copies a script into the managed
artifact directory and writes the definition in one step:
  tool add <agent> <name> --script PATH --description "..."
  tool add <agent> <name> --script PATH --stdin     (YAML body declaring inputs;
                                                    the artifact path is prepended
                                                    to its exec.argv)
Re-adding a name supersedes the old definition; earlier artifacts are kept.
Call it with ` + "`nine-tails call`" + `.`,
	}
	c.AddCommand(newToolAddCmd(a))
	return commandGroup(c)
}

func newToolAddCmd(a *app) *cobra.Command {
	var script, description, format, context string
	var meta []string
	var stdin bool
	c := &cobra.Command{
		Use:   "add <agent> <name> --script PATH (--description D | --stdin) [--context <receipt>]",
		Short: "Copy a script into the artifact store and register it as a named tool",
		Long: `Copy a script into the managed artifact store and register it as a named
tool. Pass the owning agent's ctx_... receipt with --context when this tool was
created or updated during an episode; it records provenance and must belong to
<agent>. Inspect and review a script before registering it.`,
		Example: `  nine-tails tool add pr-review complete-pr-diff --script ./complete-pr-diff.sh \
    --description "Read the complete PR diff" --context ctx_72`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateRecordFormat(format); err != nil {
				return err
			}
			agent, name := args[0], args[1]
			if script == "" {
				return cli.Invalid("--script PATH is required")
			}
			if stdin && cmd.Flags().Changed("description") {
				return cli.Invalid("give --description or --stdin, not both")
			}
			if !stdin && description == "" {
				return cli.Invalid("--description is required (or --stdin for a full YAML body)")
			}
			if err := store.ValidAgentName(agent); err != nil {
				return err
			}
			if err := store.ValidRecordName("tool", name); err != nil {
				return err
			}
			m, err := metaFlag(meta)
			if err != nil {
				return err
			}
			data, err := os.ReadFile(script)
			if err != nil {
				return cli.Invalid("cannot read --script %s: %v", script, err)
			}
			base := filepath.Base(script)
			if base == "." || base == "/" || base == "" {
				return cli.Invalid("--script %s has no file name", script)
			}
			// The definition body, minus the artifact path that depends on
			// the id allocated inside the transaction.
			def := &tool.Definition{Version: 1, Description: description, Exec: tool.Exec{Stdin: "json"}}
			if stdin {
				body, err := cli.ReadBody(nil, true, a.stdin, false)
				if err != nil {
					return err
				}
				def, err = tool.Decode(body)
				if err != nil {
					return cli.Invalid("tool body: %v", err)
				}
				if def.Version == 0 {
					def.Version = 1
				}
			}
			// Validate the complete definition before allocating an id or touching
			// the artifact tree. The real id only changes this already-valid,
			// managed argv path.
			def.Exec.Argv = append([]string{"artifacts/tool_0/" + base}, def.Exec.Argv...)
			preflight, err := tool.MarshalBody(def)
			if err != nil {
				return cli.Invalid("tool body: %v", err)
			}
			if _, err := tool.Parse(store.NormalizeBody(preflight)); err != nil {
				return cli.Invalid("tool body: %v", err)
			}
			if err := a.open(); err != nil {
				return err
			}
			var rec *store.Record
			var artifactDir string
			err = a.st.Tx(func(tx *sql.Tx) (txErr error) {
				if context != "" {
					ctx, err := store.GetContext(tx, context)
					if err != nil {
						return err
					}
					if ctx.Agent != agent {
						return cli.Invalid("%s belongs to %s, not %s", context, ctx.Agent, agent)
					}
				}
				id, err := store.NewID("tool")
				if err != nil {
					return err
				}
				artifactDir, err = createToolArtifact(a.home, id, base, data)
				if err != nil {
					return cli.Errorf(cli.ExitStore, "copy script: %v", err)
				}
				keepArtifact := false
				defer func() {
					if !keepArtifact {
						if cleanupErr := os.RemoveAll(artifactDir); cleanupErr != nil && txErr == nil {
							txErr = cli.Errorf(cli.ExitStore, "clean artifact dir: %v", cleanupErr)
						}
					}
				}()
				def.Exec.Argv[0] = "artifacts/" + id + "/" + base
				body, err := tool.MarshalBody(def)
				if err != nil {
					return cli.Invalid("tool body: %v", err)
				}
				body = store.NormalizeBody(body)
				if _, err := tool.Parse(body); err != nil {
					return cli.Invalid("tool body: %v", err)
				}
				rec, err = store.PutNamed(tx, store.NewRecord{ID: id, Agent: agent, Lane: "definition", Kind: "tool", Name: name, Body: body, OriginContext: context, Meta: m}, "")
				if err == nil {
					keepArtifact = true
				}
				return err
			})
			if err != nil {
				if artifactDir != "" {
					_ = os.RemoveAll(artifactDir)
				}
				return err
			}
			return a.printRecord(format, rec)
		},
	}
	c.Flags().StringVar(&script, "script", "", "path of the executable to copy into artifacts/<id>/")
	c.Flags().StringVar(&description, "description", "", "one-line description shown in capsules")
	c.Flags().BoolVar(&stdin, "stdin", false, "read a full YAML tool body from stdin instead of --description")
	c.Flags().StringArrayVar(&meta, "meta", nil, "applicability metadata key=value (repeatable)")
	c.Flags().StringVar(&context, "context", "", "originating context receipt id (ctx_...); must belong to <agent>")
	c.Flags().StringVar(&format, "format", "id", "id|json|yaml")
	return c
}

// createToolArtifact creates a fresh, non-symlink record directory and one
// executable file below the store's resolved artifacts root. It refuses a
// pre-existing path instead of following it, and cleans partial creations.
func createToolArtifact(home, id, base string, data []byte) (dir string, err error) {
	homeReal, err := filepath.EvalSymlinks(home)
	if err != nil {
		return "", err
	}
	homeReal, err = filepath.Abs(homeReal)
	if err != nil {
		return "", err
	}
	artifacts := filepath.Join(home, "artifacts")
	artifactsReal, err := filepath.EvalSymlinks(artifacts)
	if err != nil {
		return "", err
	}
	artifactsReal, err = filepath.Abs(artifactsReal)
	if err != nil {
		return "", err
	}
	if !strictDescendant(homeReal, artifactsReal) {
		return "", fmt.Errorf("artifacts directory resolves outside the store")
	}
	dir = filepath.Join(artifacts, id)
	if err := os.Mkdir(dir, 0o755); err != nil {
		return "", fmt.Errorf("create artifact directory: %w", err)
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(dir)
		}
	}()
	dst := filepath.Join(dir, base)
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o755)
	if err != nil {
		return "", err
	}
	_, writeErr := f.Write(data)
	closeErr := f.Close()
	if writeErr != nil {
		return "", writeErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	if err := os.Chmod(dst, 0o755); err != nil {
		return "", err
	}
	keep = true
	return dir, nil
}

func strictDescendant(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == "." || filepath.IsAbs(rel) {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func newAgentCmd(a *app) *cobra.Command {
	c := &cobra.Command{
		Use:   "agent",
		Short: "Advertise related agents in an agent's capsule",
		Long: `A related agent is a name and a one-line description rendered under
"## Available agents" so the loading model knows it may load that agent:
  agent add <agent> <name> --description "..."
This is exactly put <agent> --lane definition --kind related-agent --name <name>.`,
	}
	c.AddCommand(newAgentAddCmd(a))
	return commandGroup(c)
}

func newAgentAddCmd(a *app) *cobra.Command {
	var description, format string
	var meta []string
	c := &cobra.Command{
		Use:   "add <agent> <name> --description D",
		Short: "Register a related agent (supersedes any active one of the same name)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateRecordFormat(format); err != nil {
				return err
			}
			agent, name := args[0], args[1]
			if err := store.ValidAgentName(agent); err != nil {
				return err
			}
			if err := store.ValidRecordName("related-agent", name); err != nil {
				return err
			}
			body := store.NormalizeBody(description)
			if body == "" {
				return cli.Invalid("--description is required")
			}
			m, err := metaFlag(meta)
			if err != nil {
				return err
			}
			if err := a.open(); err != nil {
				return err
			}
			var rec *store.Record
			err = a.st.Tx(func(tx *sql.Tx) error {
				var err error
				rec, err = store.PutNamed(tx, store.NewRecord{Agent: agent, Lane: "definition", Kind: "related-agent", Name: name, Body: body, Meta: m}, "")
				return err
			})
			if err != nil {
				return err
			}
			return a.printRecord(format, rec)
		},
	}
	c.Flags().StringVar(&description, "description", "", "what the related agent does (one line)")
	c.Flags().StringArrayVar(&meta, "meta", nil, "applicability metadata key=value (repeatable)")
	c.Flags().StringVar(&format, "format", "id", "id|json|yaml")
	return c
}
