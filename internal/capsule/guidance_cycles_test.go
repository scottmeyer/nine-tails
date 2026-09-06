package capsule

import (
	"database/sql"
	"strings"
	"testing"

	compiler "github.com/scottmeyer/nine-tails/internal/compile"
	"github.com/scottmeyer/nine-tails/internal/store"
)

func TestSplitGuidanceCoverageAcrossGenerations(t *testing.T) {
	for _, mode := range []string{"drop-one-item", "keep-both-items", "explicitly-merge-items"} {
		t.Run(mode, func(t *testing.T) {
			s := setup(t)
			insert(t, s, store.NewRecord{Agent: "a", Lane: "definition", Kind: "agent-base", Name: "base", Body: "Base."})
			firstClause, secondClause := "Verify the backup before deployment.", "Never publish credentials."
			source := insert(t, s, store.NewRecord{Agent: "a", Lane: "guidance", Kind: "note", Body: firstClause + " " + secondClause})
			if err := s.Tx(func(tx *sql.Tx) error {
				first, _, err := store.InstallGeneration(tx, "a", "", []store.NewItem{
					{Key: "verify", Body: firstClause, Sources: []string{source.ID}},
					{Key: "secrets", Body: secondClause, Sources: []string{source.ID}},
				}, []store.BriefInput{{EntryID: source.ID, Disposition: "represented", Coverage: "unknown"}})
				if err != nil {
					return err
				}
				items := []store.NewItem{{Key: "verify", Body: firstClause}}
				var inputs []store.BriefInput
				switch mode {
				case "keep-both-items":
					items = append(items, store.NewItem{Key: "secrets", Body: secondClause})
				case "explicitly-merge-items":
					items[0].Body, items[0].Sources = source.Body, []string{source.ID}
					inputs = []store.BriefInput{{EntryID: source.ID, Disposition: "represented", Coverage: "unknown"}}
				}
				_, _, err = store.InstallGeneration(tx, "a", first.ID, items, inputs)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			loaded, err := Load(s, Request{Agent: "a"})
			if err != nil {
				t.Fatal(err)
			}
			for _, clause := range []string{firstClause, secondClause} {
				if !strings.Contains(loaded.Instructions, clause) {
					t.Errorf("generation replacement lost %q: %s", clause, loaded.Instructions)
				}
			}
			wantRecent := mode == "drop-one-item"
			renderedSource := false
			for _, id := range loaded.RenderedIDs {
				renderedSource = renderedSource || id == source.ID
			}
			if renderedSource != wantRecent {
				t.Errorf("source receipt membership = %v, want %v", renderedSource, wantRecent)
			}
			input, err := compiler.BuildInput(s.DB, "a")
			if err != nil {
				t.Fatal(err)
			}
			if wantRecent {
				if len(input.Entries) != 1 || input.Entries[0].ID != source.ID || input.Entries[0].Body != source.Body {
					t.Errorf("incomplete representation must restore the complete compiler source: %+v", input.Entries)
				}
			} else if len(input.Entries) != 0 {
				t.Errorf("complete representation unexpectedly restored sources: %+v", input.Entries)
			}
		})
	}
}

func TestCompilerSuccessorFollowsUnchangedRecordReplacements(t *testing.T) {
	for _, replacements := range []int{1, 3} {
		s := setup(t)
		insert(t, s, store.NewRecord{Agent: "a", Lane: "definition", Kind: "agent-base", Name: "base", Body: "Base."})
		old := insert(t, s, store.NewRecord{Agent: "a", Lane: "guidance", Kind: "note", Body: "Deploy each change immediately."})
		current := insert(t, s, store.NewRecord{Agent: "a", Lane: "guidance", Kind: "note", Body: "Wait for explicit deployment approval.", Meta: store.Meta{"repo-id": {"current"}}})
		originalCurrent := current.ID
		var generation *store.Generation
		if err := s.Tx(func(tx *sql.Tx) error {
			var err error
			generation, _, err = store.InstallGeneration(tx, "a", "", nil, []store.BriefInput{
				{EntryID: old.ID, Disposition: "superseded-by", Coverage: "unknown", Successor: current.ID},
				{EntryID: current.ID, Disposition: "deferred", Coverage: "unknown"},
			})
			if err != nil {
				return err
			}
			for i := 0; i < replacements; i++ {
				current, err = store.ReplaceRecord(tx, current.ID, store.NewRecord{Agent: "a", Lane: "guidance", Kind: "note", Meta: current.Meta.Clone()})
				if err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		for _, repo := range []string{"current", "other"} {
			loaded, err := Load(s, Request{Agent: "a", Meta: store.Meta{"repo-id": {repo}}})
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(loaded.Instructions, old.Body) != (repo == "other") || strings.Contains(loaded.Instructions, current.Body) != (repo == "current") {
				t.Errorf("after %d replacements, repo %s has wrong successor projection: %s", replacements, repo, loaded.Instructions)
			}
		}
		active, err := store.ActiveGeneration(s.DB, "a")
		if err != nil || active.ID != generation.ID {
			t.Fatalf("unchanged replacement churned generation: %+v, %v", active, err)
		}
		inputs, err := store.GenerationInputs(s.DB, generation.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, input := range inputs {
			if input.EntryID == old.ID && input.Successor != originalCurrent {
				t.Errorf("historical successor edge changed: %+v", input)
			}
		}
	}
}

func TestCompilerSuccessorFallsBackForUnusableReplacement(t *testing.T) {
	for _, mode := range []string{"ordinary-cycle", "accounting-cycle", "disabled", "malformed"} {
		t.Run(mode, func(t *testing.T) {
			s := setup(t)
			insert(t, s, store.NewRecord{Agent: "a", Lane: "definition", Kind: "agent-base", Name: "base", Body: "Base."})
			old := insert(t, s, store.NewRecord{Agent: "a", Lane: "guidance", Kind: "note", Body: "Keep the original guidance available."})
			successor := insert(t, s, store.NewRecord{Agent: "a", Lane: "guidance", Kind: "note", Body: "Replacement guidance."})
			if err := s.Tx(func(tx *sql.Tx) error {
				inputs := []store.BriefInput{{EntryID: old.ID, Disposition: "superseded-by", Successor: successor.ID}}
				if mode == "accounting-cycle" {
					inputs = append(inputs, store.BriefInput{EntryID: successor.ID, Disposition: "superseded-by", Successor: old.ID})
				}
				if _, _, err := store.InstallGeneration(tx, "a", "", nil, inputs); err != nil {
					return err
				}
				current, err := store.ReplaceRecord(tx, successor.ID, store.NewRecord{Agent: "a", Lane: "guidance", Kind: "note"})
				if err != nil {
					return err
				}
				// Simulate unusable history without the normal write path's
				// generation invalidation; projection must still fail safely.
				switch mode {
				case "ordinary-cycle":
					_, err = tx.Exec("UPDATE records SET supersedes_id = ? WHERE id = ?", current.ID, successor.ID)
				case "disabled":
					_, err = tx.Exec("UPDATE records SET status = 'disabled' WHERE id = ?", current.ID)
				case "malformed":
					_, err = tx.Exec("UPDATE records SET body = ? WHERE id = ?", string([]byte{0xff}), current.ID)
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
			loaded, err := Load(s, Request{Agent: "a"})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(loaded.Instructions, old.Body) {
				t.Errorf("unusable successor hid original guidance: %s", loaded.Instructions)
			}
		})
	}
}
