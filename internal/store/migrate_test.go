package store

import (
	"database/sql"
	"testing"
)

// A v1 store (contexts.token_budget) opens under v2 with the column renamed
// and receipts still writable.
func TestOpenMigratesV1Contexts(t *testing.T) {
	home := t.TempDir()
	s, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`ALTER TABLE contexts RENAME COLUMN estimated_tokens TO token_budget; PRAGMA user_version = 1`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = Open(home)
	if err != nil {
		t.Fatalf("reopen v1 store: %v", err)
	}
	defer s.Close()
	var v int
	if err := s.DB.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil || v != userVersion {
		t.Fatalf("user_version = %d (%v), want %d", v, err, userVersion)
	}
	var ctx *Context
	if err := s.Tx(func(tx *sql.Tx) error {
		var err error
		ctx, err = CreateContext(tx, "a", "", "after migration", 321, Meta{}, nil)
		return err
	}); err != nil {
		t.Fatalf("receipt after migration: %v", err)
	}
	got, err := GetContext(s.DB, ctx.ID)
	if err != nil || got.EstimatedTokens != 321 {
		t.Fatalf("receipt = %v (%v), want estimated_tokens 321", got, err)
	}
}
