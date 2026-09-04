package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestInsertAndGet(t *testing.T) {
	s := openTest(t)
	var rec *Record
	err := s.Tx(func(tx *sql.Tx) error {
		var err error
		rec, err = InsertRecord(tx, NewRecord{
			Agent: "pr-review", Lane: "guidance", Kind: "prefer",
			Body: "Lead with evidence.", OriginContext: "ctx_1",
			Meta: Meta{"repo-id": {"my_repo"}, "phase": {"review", "comment"}},
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if rec.ID != "rec_1" {
		t.Errorf("id = %s, want rec_1", rec.ID)
	}
	got, err := GetRecord(s.DB, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Body != "Lead with evidence." || got.Status != "active" || got.OriginContext != "ctx_1" {
		t.Errorf("unexpected record %+v", got)
	}
	if len(got.Meta["phase"]) != 2 || got.Meta["phase"][0] != "review" {
		t.Errorf("meta multimap lost order: %v", got.Meta)
	}
	if _, err := GetRecord(s.DB, "rec_999"); !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestAgentsUsesFirstRecordCreationOrder(t *testing.T) {
	s := openTest(t)
	oldClock := Clock
	t.Cleanup(func() { Clock = oldClock })
	at := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	Clock = func() time.Time { return at }

	for _, agent := range []string{"zebra", "alpha", "middle", "zebra"} {
		if err := s.Tx(func(tx *sql.Tx) error {
			_, err := InsertRecord(tx, NewRecord{Agent: agent, Lane: "recall", Kind: "memory", Body: agent})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Agents(s.DB)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"zebra", "alpha", "middle"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Agents() = %v, want creation order %v", got, want)
	}
}

func TestRecordEnvelopeKeepsExplicitNulls(t *testing.T) {
	r := Record{
		ID: "rec_1", Agent: "a", Lane: "guidance", Kind: "note",
		Body: "body", CreatedAt: "2026-09-04T12:00:00Z", Status: "active",
	}
	check := func(t *testing.T, raw []byte, decode func([]byte, any) error) {
		t.Helper()
		var got map[string]any
		if err := decode(raw, &got); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"name", "origin_context", "supersedes"} {
			v, ok := got[key]
			if !ok || v != nil {
				t.Errorf("%s = %#v, present=%v; want explicit null in %s", key, v, ok, raw)
			}
		}
		if meta, ok := got["meta"].(map[string]any); !ok || len(meta) != 0 {
			t.Errorf("meta = %#v; want empty object in %s", got["meta"], raw)
		}
	}
	js, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	check(t, js, json.Unmarshal)
	ym, err := yaml.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	check(t, ym, yaml.Unmarshal)
}

func TestPrefixes(t *testing.T) {
	s := openTest(t)
	want := map[string]NewRecord{
		"base_1":  {Agent: "a", Lane: "definition", Kind: "agent-base", Name: "base", Body: "x"},
		"state_2": {Agent: "a", Lane: "state", Kind: "working-state", Name: "working", Body: "x: 1"},
		"tool_3":  {Agent: "a", Lane: "definition", Kind: "tool", Name: "t", Body: "x"},
		"rel_4":   {Agent: "a", Lane: "definition", Kind: "related-agent", Name: "b", Body: "x"},
		"sig_5":   {Agent: "a", Lane: "signal", Kind: "signal", Body: "x"},
		"rec_6":   {Agent: "a", Lane: "recall", Kind: "memory", Body: "x"},
		"item_7":  {Agent: "a", Lane: "guidance", Kind: "brief-item", Name: "k", Body: "x"},
	}
	order := []string{"base_1", "state_2", "tool_3", "rel_4", "sig_5", "rec_6", "item_7"}
	for _, id := range order {
		nr := want[id]
		err := s.Tx(func(tx *sql.Tx) error {
			r, err := InsertRecord(tx, nr)
			if err != nil {
				return err
			}
			if r.ID != id {
				t.Errorf("got %s want %s", r.ID, id)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestPutNamedCAS(t *testing.T) {
	s := openTest(t)
	nr := NewRecord{Agent: "a", Lane: "state", Kind: "working-state", Name: "working", Body: "v: 1"}
	var first *Record
	if err := s.Tx(func(tx *sql.Tx) error {
		var err error
		first, err = PutNamed(tx, nr, "none")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	// creating again with none conflicts
	err := s.Tx(func(tx *sql.Tx) error { _, err := PutNamed(tx, nr, "none"); return err })
	if !errors.Is(err, ErrConflict) {
		t.Errorf("want conflict, got %v", err)
	}
	// wrong expect conflicts
	err = s.Tx(func(tx *sql.Tx) error { _, err := PutNamed(tx, nr, "state_999"); return err })
	if !errors.Is(err, ErrConflict) {
		t.Errorf("want conflict, got %v", err)
	}
	// right expect supersedes
	nr.Body = "v: 2"
	var second *Record
	if err := s.Tx(func(tx *sql.Tx) error {
		var err error
		second, err = PutNamed(tx, nr, first.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if second.Supersedes != first.ID {
		t.Errorf("supersedes = %q", second.Supersedes)
	}
	old, _ := GetRecord(s.DB, first.ID)
	if old.Status != "superseded" {
		t.Errorf("old status = %s", old.Status)
	}
	cur, err := ActiveNamed(s.DB, "a", "state", "working-state", "working")
	if err != nil || cur.ID != second.ID || cur.Body != "v: 2" {
		t.Errorf("active = %+v err %v", cur, err)
	}
	// empty expect = last writer wins
	nr.Body = "v: 3"
	if err := s.Tx(func(tx *sql.Tx) error { _, err := PutNamed(tx, nr, ""); return err }); err != nil {
		t.Fatal(err)
	}
	cur, _ = ActiveNamed(s.DB, "a", "state", "working-state", "working")
	if cur.Body != "v: 3" {
		t.Errorf("body = %q", cur.Body)
	}
	// history preserved
	all, _ := ListRecords(s.DB, Filter{Agent: "a", Lane: "state", Status: "*"})
	if len(all) != 3 {
		t.Errorf("want 3 versions, got %d", len(all))
	}
}

func TestResolveShadowing(t *testing.T) {
	s := openTest(t)
	mk := func(agent, name, body string, meta Meta) {
		t.Helper()
		if err := s.Tx(func(tx *sql.Tx) error {
			_, err := InsertRecord(tx, NewRecord{Agent: agent, Lane: "definition", Kind: "tool", Name: name, Body: body, Meta: meta})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	mk("shared", "recall-memory", "shared-impl", nil)
	mk("shared", "restricted", "shared-impl", Meta{"available-to": {"pr-review"}})
	mk("pr-review", "recall-memory", "own-impl", nil)

	r, err := Resolve(s.DB, "pr-review", "tool", "recall-memory")
	if err != nil || r.Body != "own-impl" {
		t.Errorf("own should shadow shared: %+v %v", r, err)
	}
	r, err = Resolve(s.DB, "other", "tool", "recall-memory")
	if err != nil || r.Body != "shared-impl" {
		t.Errorf("other should see shared: %+v %v", r, err)
	}
	if _, err := Resolve(s.DB, "other", "tool", "restricted"); !errors.Is(err, ErrNotFound) {
		t.Errorf("restricted should not be visible to other: %v", err)
	}
	if _, err := Resolve(s.DB, "pr-review", "tool", "restricted"); err != nil {
		t.Errorf("restricted should be visible to pr-review: %v", err)
	}
	if _, err := Resolve(s.DB, "pr-review", "tool", "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("want not found: %v", err)
	}
}

func TestMetaConflictAndOverlap(t *testing.T) {
	ctx := Meta{"repo-id": {"my_repo"}, "language": {"go"}}
	if Conflicts(Meta{"repo-id": {"other"}}, ctx) != true {
		t.Error("disjoint shared key should conflict")
	}
	if Conflicts(Meta{"repo-id": {"other", "my_repo"}}, ctx) {
		t.Error("intersecting sets should not conflict")
	}
	if Conflicts(Meta{"phase": {"review"}}, ctx) {
		t.Error("key on one side only should not conflict")
	}
	if Conflicts(Meta{}, ctx) {
		t.Error("empty meta never conflicts")
	}
	if n := Overlap(Meta{"repo-id": {"my_repo"}, "language": {"go", "rust"}, "x": {"y"}}, ctx); n != 2 {
		t.Errorf("overlap = %d want 2", n)
	}
}

func TestParseMeta(t *testing.T) {
	m, err := ParseMeta([]string{" repo-id = my_repo ", "phase=review", "phase=comment", "k=a=b"})
	if err != nil {
		t.Fatal(err)
	}
	if m.First("repo-id") != "my_repo" || len(m["phase"]) != 2 || m.First("k") != "a=b" {
		t.Errorf("parsed %v", m)
	}
	if _, err := ParseMeta([]string{"novalue"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("want invalid, got %v", err)
	}
	if _, err := ParseMeta([]string{"bad\u2003key=value"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("Unicode whitespace in a key must be invalid, got %v", err)
	}
	invalid := string([]byte{0xff})
	if _, err := ParseMeta([]string{invalid + "=value"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("invalid UTF-8 key must be invalid, got %v", err)
	}
	if _, err := ParseMeta([]string{"key=" + invalid}); !errors.Is(err, ErrInvalid) {
		t.Errorf("invalid UTF-8 value must be invalid, got %v", err)
	}
}

func TestPersistenceRejectsInvalidUTF8BeforeWriting(t *testing.T) {
	s := openTest(t)
	invalid := string([]byte{0xff})

	for _, tc := range []struct {
		name string
		nr   NewRecord
	}{
		{"body", NewRecord{Agent: "a", Lane: "recall", Kind: "memory", Body: "bad" + invalid}},
		{"metadata key", NewRecord{Agent: "a", Lane: "recall", Kind: "memory", Body: "ok", Meta: Meta{invalid: {"value"}}}},
		{"metadata value", NewRecord{Agent: "a", Lane: "recall", Kind: "memory", Body: "ok", Meta: Meta{"key": {invalid}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := s.Tx(func(tx *sql.Tx) error {
				_, err := InsertRecord(tx, tc.nr)
				return err
			})
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("InsertRecord error = %v, want ErrInvalid", err)
			}
		})
	}

	var rec *Record
	if err := s.Tx(func(tx *sql.Tx) error {
		var err error
		rec, err = InsertRecord(tx, NewRecord{Agent: "a", Lane: "recall", Kind: "memory", Body: "valid ✓", Meta: Meta{"language": {"Go"}}})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if rec.ID != "rec_1" {
		t.Fatalf("failed validation consumed an id: first valid id = %s, want rec_1", rec.ID)
	}
}

func TestContextAndDeduplicatedSignalRejectInvalidUTF8(t *testing.T) {
	s := openTest(t)
	invalid := string([]byte{0xff})

	err := s.Tx(func(tx *sql.Tx) error {
		_, err := CreateContext(tx, "a", "", "", 100, Meta{"repo": {invalid}}, nil)
		return err
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("CreateContext error = %v, want ErrInvalid", err)
	}

	if err := s.Tx(func(tx *sql.Tx) error {
		_, _, err := CreateSignal(tx, "a", "valid", Meta{"subject": {"first"}}, time.Unix(0, 0), "same", "")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	err = s.Tx(func(tx *sql.Tx) error {
		_, deduplicated, err := CreateSignal(tx, "a", invalid, Meta{"subject": {"retry"}}, time.Unix(0, 0), "same", "")
		if deduplicated {
			t.Error("invalid retry must not be reported as a successful deduplication")
		}
		return err
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("CreateSignal invalid retry error = %v, want ErrInvalid", err)
	}
}

func TestConcurrentFirstOpen(t *testing.T) {
	home := t.TempDir()
	const callers = 24
	start := make(chan struct{})
	errs := make(chan error, callers)
	ids := make(chan string, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			s, err := Open(home)
			if err != nil {
				errs <- err
				return
			}
			defer s.Close()
			var id string
			err = s.Tx(func(tx *sql.Tx) error {
				r, err := InsertRecord(tx, NewRecord{Agent: fmt.Sprintf("a%d", i), Lane: "recall", Kind: "memory", Body: "x"})
				if err == nil {
					id = r.ID
				}
				return err
			})
			if err != nil {
				errs <- err
				return
			}
			ids <- id
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	close(ids)
	for err := range errs {
		t.Errorf("concurrent first use: %v", err)
	}
	seen := map[string]bool{}
	for id := range ids {
		if seen[id] {
			t.Errorf("duplicate id %s", id)
		}
		seen[id] = true
	}
	if len(seen) != callers {
		t.Fatalf("created %d records, want %d", len(seen), callers)
	}
	s, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var rows, n int
	if err := s.DB.QueryRow(`SELECT COUNT(*), MAX(n) FROM seq`).Scan(&rows, &n); err != nil {
		t.Fatal(err)
	}
	if rows != 1 || n != callers {
		t.Fatalf("sequence rows=%d n=%d, want rows=1 n=%d", rows, n, callers)
	}
}

func TestReservedRecordNames(t *testing.T) {
	for _, what := range []string{"tool", "related-agent", "state", "brief item"} {
		if err := ValidRecordName(what, "base"); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s named base: want ErrInvalid, got %v", what, err)
		}
	}
	if err := ValidRecordName("agent-base", "base"); err != nil {
		t.Errorf("the conventional agent base name must remain valid: %v", err)
	}
	if err := ValidNamedRecord("definition", "agent-base", "base"); err != nil {
		t.Errorf("the conventional base tuple must remain valid: %v", err)
	}
	for _, tc := range [][3]string{{"state", "agent-base", "base"}, {"definition", "agent-base", "other"}} {
		if err := ValidNamedRecord(tc[0], tc[1], tc[2]); !errors.Is(err, ErrInvalid) {
			t.Errorf("tuple %v: want ErrInvalid, got %v", tc, err)
		}
	}
}
