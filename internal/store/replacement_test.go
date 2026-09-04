package store

import (
	"database/sql"
	"errors"
	"testing"
)

func TestReplacementInvalidatesSupersededBySuccessor(t *testing.T) {
	s := openTest(t)
	input := mustInsert(t, s, NewRecord{Agent: "a", Lane: "guidance", Kind: "note", Body: "old"})
	successor := mustInsert(t, s, NewRecord{Agent: "a", Lane: "guidance", Kind: "note", Body: "new"})
	var first *Generation
	if err := s.Tx(func(tx *sql.Tx) error {
		var err error
		first, _, err = InstallGeneration(tx, "a", "", nil, []BriefInput{
			{EntryID: input.ID, Disposition: "superseded-by", Coverage: "unknown", Successor: successor.ID},
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Tx(func(tx *sql.Tx) error {
		retag, err := ReplaceRecord(tx, successor.ID, NewRecord{Agent: "a", Lane: "guidance", Kind: "note"})
		if err != nil {
			return err
		}
		_, err = ReplaceRecord(tx, retag.ID, NewRecord{Agent: "a", Lane: "guidance", Kind: "note", Body: "changed"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	gen, err := ActiveGeneration(s.DB, "a")
	if err != nil || gen.Parent != first.ID {
		t.Fatalf("changed accounting successor did not invalidate: %+v, %v", gen, err)
	}
	inputs, err := GenerationInputs(s.DB, gen.ID)
	if err != nil || len(inputs) != 0 {
		t.Fatalf("stale accounting survived: %+v, %v", inputs, err)
	}
}

func TestReplacementInvalidationRollsBackWithFailedInsert(t *testing.T) {
	s := openTest(t)
	source := mustInsert(t, s, NewRecord{Agent: "a", Lane: "guidance", Kind: "note", Body: "old"})
	var first *Generation
	if err := s.Tx(func(tx *sql.Tx) error {
		var err error
		first, _, err = InstallGeneration(tx, "a", "",
			[]NewItem{{Key: "kept", Body: "compiled", Sources: []string{source.ID}}},
			[]BriefInput{{EntryID: source.ID, Disposition: "represented", Coverage: "unknown"}})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	// Fail after invalidation, on the replacement insert itself.
	if _, err := s.DB.Exec(`CREATE TRIGGER reject_replacement BEFORE INSERT ON records
		WHEN NEW.supersedes_id IS NOT NULL BEGIN SELECT RAISE(ABORT, 'injected insert failure'); END`); err != nil {
		t.Fatal(err)
	}
	err := s.Tx(func(tx *sql.Tx) error {
		_, err := ReplaceRecord(tx, source.ID, NewRecord{Agent: "a", Lane: "guidance", Kind: "note", Body: "changed"})
		return err
	})
	if err == nil {
		t.Fatal("expected insert failure")
	}
	gen, err := ActiveGeneration(s.DB, "a")
	if err != nil || gen.ID != first.ID {
		t.Fatalf("failed replacement changed active generation: %+v, %v", gen, err)
	}
	old, err := GetRecord(s.DB, source.ID)
	if err != nil || old.Status != "active" {
		t.Fatalf("failed replacement retired source: %+v, %v", old, err)
	}
	items, err := GenerationItems(s.DB, first.ID)
	if err != nil || len(items) != 1 || items[0].Status != "active" {
		t.Fatalf("failed replacement retired brief: %+v, %v", items, err)
	}
}

func TestReplacementWithoutGeneration(t *testing.T) {
	s := openTest(t)
	// An uncompiled agent needs no synthetic invalidation generation.
	fresh := mustInsert(t, s, NewRecord{Agent: "b", Lane: "guidance", Kind: "note", Body: "fresh"})
	if err := s.Tx(func(tx *sql.Tx) error {
		_, err := ReplaceRecord(tx, fresh.ID, NewRecord{Agent: "b", Lane: "guidance", Kind: "note", Body: "changed"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := ActiveGeneration(s.DB, "b"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("uncompiled agent gained a generation: %v", err)
	}
}
