package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func referenceRecord(t *testing.T, s *Store, input NewRecord) *Record {
	t.Helper()
	var record *Record
	if err := s.Tx(func(tx *sql.Tx) error {
		var err error
		record, err = InsertRecord(tx, input)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return record
}

func requireReference(t *testing.T, q Querier, id, want string) {
	t.Helper()
	ref, err := Reference(q, id)
	if err != nil || ref != want {
		t.Fatalf("Reference(%s) = %q, %v; want %s", id, ref, err, want)
	}
	canonical, err := ResolveReference(q, ref)
	if err != nil || canonical != id {
		t.Fatalf("ResolveReference(%s) = %q, %v; want %s", ref, canonical, err, id)
	}
}

func TestReferenceReservationAndRollback(t *testing.T) {
	s := openTest(t)
	if err := s.Tx(func(tx *sql.Tx) error {
		ref, err := EnsureReference(tx, "ctx_10")
		if err != nil || ref != "@1" {
			t.Fatalf("reservation = %q, %v", ref, err)
		}
		if err := CreateContextWithID(tx, "ctx_10", "builder", "", "Create a capsule", 100, nil, nil); err != nil {
			return err
		}
		requireReference(t, tx, "ctx_10", "@1")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	stop := errors.New("failed capsule")
	if err := s.Tx(func(tx *sql.Tx) error {
		if _, err := EnsureReference(tx, "ctx_11"); err != nil {
			return err
		}
		return stop
	}); !errors.Is(err, stop) {
		t.Fatal(err)
	}
	if _, err := Reference(s.DB, "ctx_11"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rolled-back reservation remains: %v", err)
	}
	r := referenceRecord(t, s, NewRecord{Agent: "builder", Lane: "guidance", Kind: "note", Body: "Keep evidence."})
	requireReference(t, s.DB, r.ID, "@2")
	if _, err := EnsureReference(s.DB, "not-an-id"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid canonical identifier: %v", err)
	}
}

func TestReferenceSurvivesCollectedContextAndCannotRebind(t *testing.T) {
	s := openTest(t)
	if err := s.Tx(func(tx *sql.Tx) error {
		return CreateContextWithID(tx, "ctx_1", "builder", "", "Old work", 100, nil, nil)
	}); err != nil {
		t.Fatal(err)
	}
	requireReference(t, s.DB, "ctx_1", "@1")
	ids, err := GCContexts(s, time.Now().Add(time.Hour), false)
	if err != nil || !reflect.DeepEqual(ids, []string{"ctx_1"}) {
		t.Fatalf("GC = %v, %v", ids, err)
	}
	requireReference(t, s.DB, "ctx_1", "@1")
	if _, err := GetContext(s.DB, "ctx_1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("collected context still live: %v", err)
	}
	r := referenceRecord(t, s, NewRecord{Agent: "builder", Lane: "guidance", Kind: "note", Body: "New work."})
	requireReference(t, s.DB, r.ID, "@2")
	for _, statement := range []string{
		`DELETE FROM reference_aliases WHERE number = 1`,
		`UPDATE reference_aliases SET entity_id = 'ctx_99' WHERE number = 1`,
	} {
		if _, err := s.DB.Exec(statement); err == nil {
			t.Fatalf("append-only registry accepted %s", statement)
		}
	}
	listed, err := ListReferences(s.DB, ReferenceFilter{})
	if err != nil || len(listed) != 1 || listed[0].ID != r.ID {
		t.Fatalf("live listing includes tombstone: %+v, %v", listed, err)
	}
}

func TestResolveReferenceRequiresExactPositiveInteger(t *testing.T) {
	s := openTest(t)
	for _, ref := range []string{"", "@", "1", "@0", "@01", "@-1", "@+1", "@1.0", "@1x", " @1", "@1\n", "@١", "@9223372036854775808"} {
		if _, err := ResolveReference(s.DB, ref); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q: expected ErrInvalid, got %v", ref, err)
		}
	}
	if _, err := ResolveReference(s.DB, "@9223372036854775807"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("valid absent handle: %v", err)
	}
}

func TestReferenceMigrationBackfillsOnceAndSupportsOldWriters(t *testing.T) {
	home := t.TempDir()
	old, err := sql.Open("sqlite", filepath.Join(home, "nine-tails.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(schema + `
PRAGMA user_version = 4;
INSERT INTO records(id,agent,lane,kind,body,created_at) VALUES ('rec_1','builder','guidance','note','Old note','2020-01-01T00:00:00Z');
INSERT INTO contexts(id,agent,task,estimated_tokens,created_at) VALUES ('ctx_1','builder','Old receipt',100,'2020-01-02T00:00:00Z');
INSERT INTO brief_generations(id,agent,created_at,status) VALUES ('gen_1','builder','2020-01-03T00:00:00Z','active');
`); err != nil {
		t.Fatal(err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	requireReference(t, s.DB, "rec_1", "@1")
	requireReference(t, s.DB, "ctx_1", "@2")
	requireReference(t, s.DB, "gen_1", "@3")
	var version int
	if err := s.DB.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != userVersion {
		t.Fatalf("migration version: %d, %v; want %d", version, err, userVersion)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(home)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var sequence int
	if err := s.DB.QueryRow(`SELECT seq FROM sqlite_sequence WHERE name = 'reference_aliases'`).Scan(&sequence); err != nil || sequence != 3 {
		t.Fatalf("reopen reallocated backfill rows: sequence %d, %v", sequence, err)
	}
	// These are the existing v4 SQL writes, with no new helper call.
	if _, err := s.DB.Exec(`
INSERT INTO records(id,agent,lane,kind,body,created_at) VALUES ('sig_2','builder','signal','signal','New signal','2020-01-04T00:00:00Z');
INSERT INTO contexts(id,agent,estimated_tokens,created_at) VALUES ('ctx_2','builder',100,'2020-01-05T00:00:00Z');
INSERT INTO brief_generations(id,agent,created_at,status) VALUES ('gen_2','builder','2020-01-06T00:00:00Z','staged');
`); err != nil {
		t.Fatal(err)
	}
	requireReference(t, s.DB, "sig_2", "@4")
	requireReference(t, s.DB, "ctx_2", "@5")
	requireReference(t, s.DB, "gen_2", "@6")
}

func TestReferenceInventoryFiltersBeforeLimitAndIncludesMetadata(t *testing.T) {
	s := openTest(t)
	project := Meta{"repo-id": {"soccer-chess"}, "phase": {"build", "review"}}
	r := referenceRecord(t, s, NewRecord{Agent: "builder", Lane: "guidance", Kind: "note", Body: "Readable\n  advice " + strings.Repeat("x", 170) + " 100% accurate", Meta: project})
	_ = referenceRecord(t, s, NewRecord{Agent: "builder", Lane: "signal", Kind: "signal", Body: "An unscoped event"})
	if err := s.Tx(func(tx *sql.Tx) error {
		if err := CreateContextWithID(tx, "ctx_3", "builder", "", "Project review", 100, project, nil); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO brief_generations(id,agent,created_at,status) VALUES ('gen_4','builder',?,'staged')`, Now())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"records", "contexts", "brief_generations"} {
		if _, err := s.DB.Exec(`UPDATE ` + table + ` SET created_at = '2020-01-01T00:00:00Z'`); err != nil {
			t.Fatal(err)
		}
	}
	views, err := ListReferences(s.DB, ReferenceFilter{Meta: Meta{"repo-id": {"soccer-chess"}}, Limit: 1})
	if err != nil || len(views) != 1 || views[0].ID != "ctx_3" || !reflect.DeepEqual(views[0].Meta, project) {
		t.Fatalf("scoped limit: %+v, %v", views, err)
	}
	views, err = ListReferences(s.DB, ReferenceFilter{Kind: "record", Agent: "builder", Query: "100%", Meta: project})
	if err != nil || len(views) != 1 || views[0].ID != r.ID || views[0].Label != r.Body {
		t.Fatalf("record/query filter or label: %+v, %v", views, err)
	}
	views, err = ListReferences(s.DB, ReferenceFilter{Meta: Meta{"repo-id": {"missing"}}})
	if err != nil || len(views) != 0 {
		t.Fatalf("absent scope incorrectly matches: %+v, %v", views, err)
	}
	for _, kind := range []string{"context", "signal", "record", "generation"} {
		views, err := ListReferences(s.DB, ReferenceFilter{Kind: kind})
		if err != nil || len(views) != 1 || views[0].Kind != kind {
			t.Fatalf("%s inventory: %+v, %v", kind, views, err)
		}
	}
}

func TestCanonicalRecordCopiesReceiveDestinationLocalReferences(t *testing.T) {
	source, destination := openTest(t), openTest(t)
	r := referenceRecord(t, source, NewRecord{Agent: "builder", Lane: "guidance", Kind: "note", Body: "Portable guidance"})
	requireReference(t, source.DB, r.ID, "@1")
	_ = referenceRecord(t, destination, NewRecord{Agent: "builder", Lane: "guidance", Kind: "note", Body: "Already here"})
	copy := referenceRecord(t, destination, NewRecord{ID: r.ID, Agent: r.Agent, Lane: r.Lane, Kind: r.Kind, Body: r.Body})
	if copy.ID != r.ID {
		t.Fatalf("import changed identity: %s", copy.ID)
	}
	requireReference(t, destination.DB, r.ID, "@2")
	requireReference(t, source.DB, r.ID, "@1")
}

func TestSignalReferencesShowFullSubjectAndEffectiveDelivery(t *testing.T) {
	s := openTest(t)
	now := Clock()
	longSubject := "Review the passing lane " + strings.Repeat("carefully ", 30)
	var expiredID string
	for i, wanted := range []string{"pending", "leased", "pending", "acknowledged"} {
		if err := s.Tx(func(tx *sql.Tx) error {
			sig, _, err := CreateSignal(tx, "game.engineer", "The full event body is not its subject.", Meta{"subject": {longSubject}, "case": {string(rune('a' + i))}}, now, "", "")
			if err != nil {
				return err
			}
			state, lease := wanted, ""
			if i == 1 {
				lease = FormatTime(now.Add(time.Hour))
			}
			if i == 2 {
				state, lease, expiredID = "leased", FormatTime(now.Add(-time.Hour)), sig.Record.ID
			}
			_, err = tx.Exec(`UPDATE signal_delivery SET state = ?, leased_until = ? WHERE record_id = ?`, state, nullable(lease), sig.Record.ID)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	for i, wanted := range []string{"pending", "leased", "pending", "acknowledged"} {
		views, err := ListReferences(s.DB, ReferenceFilter{Kind: "signal", Query: "passing lane", Meta: Meta{"case": {string(rune('a' + i))}}})
		if err != nil || len(views) != 1 || views[0].Status != wanted || views[0].Label != longSubject {
			t.Fatalf("signal %d: %+v, %v; want full subject and %s", i, views, err, wanted)
		}
	}
	delivery, err := GetDelivery(s.DB, expiredID)
	if err != nil || delivery.State != "leased" {
		t.Fatalf("inventory mutated expired delivery: %+v, %v", delivery, err)
	}
}
