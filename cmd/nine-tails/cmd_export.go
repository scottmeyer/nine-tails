package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/scottmeyer/nine-tails/internal/bundle"
	"github.com/scottmeyer/nine-tails/internal/cli"
)

func newExportCmd(a *app) *cobra.Command {
	var include, bundleFile, format string
	var all bool
	c := &cobra.Command{
		Use:   "export <agent>",
		Short: "Export an agent's records as a YAML document or a tar bundle with artifacts",
		Long: `Write {nine_tails_export: 1, agent, records: [envelopes oldest first],
omitted_artifacts: [tool ids]} for an agent. Sections: base, brief, journal
(guidance and recall), state, tools (agent-owned), agents (related agents);
--include picks a subset. Contexts, generations and signals never travel.

Plain YAML cannot carry scripts: tools with an artifacts/ path anywhere in
exec.argv are listed under omitted_artifacts. --bundle FILE.tar writes
manifest.yaml plus artifacts/<id>/<file> and prints the bundle path.
--all keeps superseded records (status as-is).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch format {
			case "yaml", "json":
			default:
				return cli.Invalid("unknown format %q (yaml|json)", format)
			}
			if err := a.open(); err != nil {
				return err
			}
			var inc []string
			if include != "" {
				inc = strings.Split(include, ",")
			}
			doc, err := bundle.Export(a.st.DB, bundle.ExportOptions{Agent: args[0], Include: inc, All: all, WithArtifacts: bundleFile != ""})
			if err != nil {
				return err
			}
			if doc.HasBriefItems() {
				fmt.Fprintln(a.stderr, "nine-tails: brief items travel as plain brief-item records; after import they render only once a compile installs them")
			}
			if bundleFile == "" {
				for _, id := range doc.OmittedArtifacts {
					fmt.Fprintf(a.stderr, "nine-tails: warning: %s references an artifact that plain YAML cannot carry (use --bundle)\n", id)
				}
				return cli.Write(a.stdout, format, doc)
			}
			var buf bytes.Buffer
			if err := bundle.WriteBundle(&buf, a.home, doc); err != nil {
				return cli.Errorf(cli.ExitStore, "build bundle: %v", err)
			}
			for _, id := range doc.OmittedArtifacts {
				fmt.Fprintf(a.stderr, "nine-tails: warning: %s references an artifact that is missing on disk; omitted from the bundle\n", id)
			}
			if err := os.WriteFile(bundleFile, buf.Bytes(), 0o644); err != nil {
				return cli.Errorf(cli.ExitStore, "write bundle: %v", err)
			}
			_, err = fmt.Fprintln(a.stdout, bundleFile)
			return err
		},
	}
	c.Flags().StringVar(&include, "include", "", "comma list of base,brief,journal,state,tools,agents (default: all)")
	c.Flags().StringVar(&bundleFile, "bundle", "", "write a tar bundle (manifest.yaml + artifacts) to this path instead of printing YAML")
	c.Flags().BoolVar(&all, "all", false, "include superseded and disabled records")
	c.Flags().StringVar(&format, "format", "yaml", "yaml|json (ignored with --bundle)")
	return c
}

func newImportCmd(a *app) *cobra.Command {
	var stdin bool
	var format string
	c := &cobra.Command{
		Use:   "import (FILE.yaml | FILE.tar | --stdin)",
		Short: "Import an export document or bundle; every record becomes a new active record",
		Long: `Read a document written by export (YAML, JSON, or a .tar bundle) and write
its records in one transaction. Each record gets a new id, status active, no
supersedes or origin, and imported-from=<old id> in its metadata; bodies are
stored exactly as the document carries them; envelope keys may be snake_case
or kebab-case and a missing lane means recall. Definitions, state and brief
items supersede a same-named active record of the target agent (a brief item
still installed by the live generation is skipped with a warning instead);
guidance and recall records are plain inserts. The document describes one
agent: a record naming another agent is exit 2. Tool artifacts are copied
under the new id and every managed exec.argv path rewritten; a tool whose
artifact the document does not carry (plain YAML) is skipped with a warning
and the active definition kept. Tool bodies, state bodies and metadata keys
are validated as put would, and any failure aborts the whole import (exit 2).
Signal records are skipped with a warning. Prints one new id per line
(json: {ids: {old: new}}).`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch format {
			case "id", "json":
			default:
				return cli.Invalid("unknown format %q (id|json)", format)
			}
			if stdin == (len(args) == 1) {
				return cli.Invalid("give exactly one of FILE or --stdin")
			}
			var data []byte
			var name string
			var err error
			if stdin {
				if data, err = io.ReadAll(a.stdin); err != nil {
					return cli.Invalid("read stdin: %v", err)
				}
			} else {
				name = args[0]
				if data, err = os.ReadFile(name); err != nil {
					return cli.Invalid("cannot read %s: %v", name, err)
				}
			}
			if err := a.open(); err != nil {
				return err
			}
			var doc *bundle.Document
			var arts map[string]bundle.Artifact
			if bundle.IsTar(name, data) {
				doc, arts, err = bundle.ReadBundle(bytes.NewReader(data))
			} else {
				doc, err = bundle.ReadDocument(data)
			}
			if err != nil {
				return err
			}
			warn := func(f string, args ...any) {
				fmt.Fprintf(a.stderr, "nine-tails: warning: "+f+"\n", args...)
			}
			results, err := bundle.Import(a.st, doc, arts, bundle.ImportOptions{StateMaxBytes: a.cfg.StateMaxBytes, Warn: warn})
			if err != nil {
				return err
			}
			if format == "json" {
				ids := map[string]string{}
				for i, r := range results {
					key := r.Old
					if key == "" {
						key = fmt.Sprintf("records[%d]", i)
					}
					ids[key] = r.New
				}
				return cli.WriteJSON(a.stdout, map[string]any{"ids": ids})
			}
			for _, r := range results {
				fmt.Fprintln(a.stdout, r.New)
			}
			return nil
		},
	}
	c.Flags().BoolVar(&stdin, "stdin", false, "read the document (YAML/JSON or tar) from stdin")
	c.Flags().StringVar(&format, "format", "id", "id (one new id per line)|json ({ids: {old: new}})")
	return c
}
