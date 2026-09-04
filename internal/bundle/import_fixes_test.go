package bundle

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/scottmeyer/nine-tails/internal/store"
)

func TestImportRejectsRecordsOfAnotherAgent(t *testing.T) {
	s := openStore(t)
	doc := &Document{Version: Version, Agent: "copy", Records: []*store.Record{
		{ID: "rec_3", Agent: "a", Lane: "guidance", Kind: "prefer", Body: "Lead with evidence.", Meta: store.Meta{}},
	}}
	_, err := Import(s, doc, nil, ImportOptions{})
	if !errors.Is(err, store.ErrInvalid) || !strings.Contains(err.Error(), `rec_3: agent "a" does not match the document's agent "copy"`) {
		t.Fatalf("import = %v, want ErrInvalid naming both agents", err)
	}
	for _, agent := range []string{"a", "copy"} {
		if ok, err := store.AgentExists(s.DB, agent); err != nil || ok {
			t.Fatalf("agent %q exists after a rejected import (ok=%v err=%v)", agent, ok, err)
		}
	}
}

func TestImportSkipsToolWhoseArtifactIsAbsent(t *testing.T) {
	s := openStore(t)
	putNamed(t, s, store.NewRecord{Agent: "a", Lane: "definition", Kind: "agent-base", Name: "base", Body: "Base."})
	live := addTool(t, s, "a", "x", "#!/bin/sh\necho hi\n")
	doc, err := Export(s.DB, ExportOptions{Agent: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.OmittedArtifacts) != 1 || doc.OmittedArtifacts[0] != live.ID {
		t.Fatalf("plain export should report the omitted artifact: %v", doc.OmittedArtifacts)
	}
	var warnings []string
	results, err := Import(s, doc, nil, ImportOptions{Warn: func(f string, a ...any) { warnings = append(warnings, fmt.Sprintf(f, a...)) }})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if r.Old == live.ID {
			t.Fatalf("tool without an artifact was imported as %s", r.New)
		}
	}
	wantPrefix := "skipped " + live.ID + ": tool \"x\" references artifacts/" + live.ID + "/x.sh"
	if len(warnings) != 1 || !strings.HasPrefix(warnings[0], wantPrefix) {
		t.Fatalf("warnings = %q, want one starting %q", warnings, wantPrefix)
	}
	cur, err := store.ActiveNamed(s.DB, "a", "definition", "tool", "x")
	if err != nil || cur.ID != live.ID {
		t.Fatalf("active tool after import = %v (%v), want %s untouched", cur, err, live.ID)
	}
}
