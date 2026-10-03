package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setStoreVersion rewrites a store's schema version the way an older binary
// would have left it, so the current binary meets an out-of-date store.
func setStoreVersion(t *testing.T, home string, v int) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(home, "nine-tails.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, stmt := range []string{"DROP TABLE IF EXISTS record_dedupe", fmt.Sprintf("PRAGMA user_version = %d", v)} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
}

func storeVersion(t *testing.T, path string) int {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var v int
	if err := db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestOrdinaryCommandsRefuseAnOlderStoreWithoutChangingIt(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "a", "Base.")
	setStoreVersion(t, h.home, 5)
	for _, args := range [][]string{{"agents"}, {"inspect", "a"}, {"note", "a", "Rule."}, {"load", "a"}} {
		r := h.run(args...)
		if r.code != 4 || !strings.Contains(r.err, "nine-tails migrate") || !strings.Contains(r.err, "schema version 5") {
			t.Fatalf("%v must refuse the older store and name migrate: %+v", args, r)
		}
	}
	if v := storeVersion(t, filepath.Join(h.home, "nine-tails.db")); v != 5 {
		t.Fatalf("a refused command migrated the store to %d", v)
	}
	if matches, _ := filepath.Glob(filepath.Join(h.home, "*.bak")); len(matches) != 0 {
		t.Fatalf("a refused command wrote a backup: %v", matches)
	}
}

func TestMigrateUpgradesOnceWithABackupTheOldBinaryCanOpen(t *testing.T) {
	h := newHarness(t)
	h.ok("base", "a", "Base.")
	before := h.ok("note", "a", "Keep me.").id(t)
	setStoreVersion(t, h.home, 5)
	r := h.ok("migrate")
	backup := filepath.Join(h.home, "nine-tails.db.v5.bak")
	if !strings.Contains(r.out, "from schema 5 to 6") || !strings.Contains(r.out, backup) {
		t.Fatalf("migrate output: %q", r.out)
	}
	if v := storeVersion(t, backup); v != 5 {
		t.Fatalf("backup must keep the old schema, got %d", v)
	}
	if v := storeVersion(t, filepath.Join(h.home, "nine-tails.db")); v != 6 {
		t.Fatalf("store must be current after migrate, got %d", v)
	}
	if body := h.ok("inspect", before, "--format", "json").json(t)["body"]; body != "Keep me." {
		t.Fatalf("migration changed a record: %v", body)
	}
	h.ok("note", "a", "--dedupe-key", "after-migrate", "Works again.")

	again := h.ok("migrate", "--format", "json").json(t)
	if again["migrated"] != false || again["from"] != float64(6) || again["backup"] != "" {
		t.Fatalf("a current store must not migrate again: %v", again)
	}
	if matches, _ := filepath.Glob(filepath.Join(h.home, "*.bak")); len(matches) != 1 {
		t.Fatalf("a no-op migrate must not add backups: %v", matches)
	}

	// A second real migration from the same old version must not overwrite
	// the first backup.
	setStoreVersion(t, h.home, 5)
	h.ok("migrate")
	matches, _ := filepath.Glob(filepath.Join(h.home, "*.bak"))
	if len(matches) != 2 {
		t.Fatalf("second migration must keep the earlier backup: %v", matches)
	}
}

func TestFreshStoreIsCreatedWithoutMigrate(t *testing.T) {
	h := newHarness(t)
	if _, err := os.Stat(filepath.Join(h.home, "nine-tails.db")); !os.IsNotExist(err) {
		t.Fatal("precondition: no store yet")
	}
	h.ok("agents")
	if v := storeVersion(t, filepath.Join(h.home, "nine-tails.db")); v != 6 {
		t.Fatalf("fresh store version %d", v)
	}
	if matches, _ := filepath.Glob(filepath.Join(h.home, "*.bak")); len(matches) != 0 {
		t.Fatalf("creating a store must not write a backup: %v", matches)
	}
}

func TestVersionNamesTheRelease(t *testing.T) {
	h := newHarness(t)
	r := h.ok("--version")
	if !strings.HasPrefix(r.out, "nine-tails version 0.2.0") {
		t.Fatalf("version output: %q", r.out)
	}
}
