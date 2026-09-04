// Package starter carries the agents a fresh store is seeded with (DESIGN
// §1.2): pilot, the entry agent whose base is the usage guide, and reflector.
// They are ordinary export documents embedded in the binary, so one binary
// bootstraps any store, and the same files are the template for agent packs.
package starter

import (
	"embed"
	"fmt"
	"io/fs"
	"sort"

	"github.com/scottmeyer/nine-tails/internal/bundle"
	"github.com/scottmeyer/nine-tails/internal/store"
)

//go:embed *.yaml
var documents embed.FS

// Names lists the starter agents in seeding order.
func Names() ([]string, error) {
	entries, err := fs.ReadDir(documents, ".")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		data, err := documents.ReadFile(e.Name())
		if err != nil {
			return nil, err
		}
		doc, err := bundle.ReadDocument(data)
		if err != nil {
			return nil, fmt.Errorf("starter %s: %w", e.Name(), err)
		}
		names = append(names, doc.Agent)
	}
	sort.Strings(names)
	return names, nil
}

// Seed imports every starter agent that does not exist in the store yet and
// returns the names it created. An agent that already exists, whoever made
// it, is never touched.
func Seed(st *store.Store, stateMaxBytes int) ([]string, error) {
	entries, err := fs.ReadDir(documents, ".")
	if err != nil {
		return nil, err
	}
	var seeded []string
	for _, e := range entries {
		data, err := documents.ReadFile(e.Name())
		if err != nil {
			return nil, err
		}
		doc, err := bundle.ReadDocument(data)
		if err != nil {
			return nil, fmt.Errorf("starter %s: %w", e.Name(), err)
		}
		exists, err := store.AgentExists(st.DB, doc.Agent)
		if err != nil {
			return nil, err
		}
		if exists {
			continue
		}
		if _, err := bundle.Import(st, doc, nil, bundle.ImportOptions{StateMaxBytes: stateMaxBytes}); err != nil {
			return nil, fmt.Errorf("seed %s: %w", doc.Agent, err)
		}
		seeded = append(seeded, doc.Agent)
	}
	return seeded, nil
}
