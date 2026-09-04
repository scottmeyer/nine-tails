package capsule

import (
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/scottmeyer/nine-tails/internal/store"
)

// Nothing eligible is ever cut: a burst of recent guidance renders whole
// beside the whole brief, and the capsule reports what a compile would fold.
func TestLoadRendersEverythingAndCountsUncompiled(t *testing.T) {
	s := setup(t)
	insert(t, s, store.NewRecord{Agent: "a", Lane: "definition", Kind: "agent-base", Name: "base", Body: "Base."})
	var items []store.NewItem
	for i := 0; i < 10; i++ {
		items = append(items, store.NewItem{Key: "k" + string(rune('a'+i)), Body: strings.Repeat("brief item text ", 3)})
	}
	if err := s.Tx(func(tx *sql.Tx) error {
		_, _, err := store.InstallGeneration(tx, "a", "", items, nil)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		insert(t, s, store.NewRecord{Agent: "a", Lane: "guidance", Kind: "note", Body: strings.Repeat("recent note ", 4)})
	}
	c, err := Load(s, Request{Agent: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if brief, recent := strings.Count(c.Markdown, "- brief item"), strings.Count(c.Markdown, "(note) recent note"); brief != 10 || recent != 20 {
		t.Fatalf("brief=%d recent=%d, want 10 and 20:\n%s", brief, recent, c.Markdown)
	}
	if c.UncompiledAdjustments != 20 || len(c.RenderedIDs) != 31 || c.EstimatedTokens <= 0 {
		t.Fatalf("uncompiled=%d rendered=%d estimated=%d", c.UncompiledAdjustments, len(c.RenderedIDs), c.EstimatedTokens)
	}
	receipt, err := store.GetContext(s.DB, c.ContextID)
	if err != nil || receipt.EstimatedTokens != c.EstimatedTokens {
		t.Fatalf("receipt size = %v (%v), want %d", receipt, err, c.EstimatedTokens)
	}
}

// A transport ceiling is all or nothing: a capsule over it is not recorded,
// so no receipt claims the model saw what the harness could not deliver.
func TestMaxBytesIsAllOrNothing(t *testing.T) {
	s := setup(t)
	insert(t, s, store.NewRecord{Agent: "a", Lane: "definition", Kind: "agent-base", Name: "base", Body: "Base."})
	insert(t, s, store.NewRecord{Agent: "a", Lane: "guidance", Kind: "note", Body: "Some guidance that makes the capsule longer than ten bytes."})
	fits, err := Load(s, Request{Agent: "a", MaxBytes: 100000})
	if err != nil {
		t.Fatal(err)
	}
	_, err = Load(s, Request{Agent: "a", MaxBytes: 10})
	var tooLarge *TooLargeError
	if !errors.As(err, &tooLarge) || tooLarge.Max != 10 || tooLarge.Bytes <= 10 {
		t.Fatalf("over-ceiling load = %v, want *TooLargeError", err)
	}
	ctxs, err := store.ListContexts(s.DB, "a", 10)
	if err != nil || len(ctxs) != 1 || ctxs[0].ID != fits.ContextID {
		t.Fatalf("receipts after a rejected load = %v (%v); want only %s", ctxs, err, fits.ContextID)
	}
}
