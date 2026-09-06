package store

import (
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func recallIndex(t *testing.T, s *Store, req RecallIndexRequest) []RecallIndexEntry {
	t.Helper()
	var entries []RecallIndexEntry
	if err := WalkRecallIndex(s.DB, req, func(entry RecallIndexEntry) (bool, error) {
		entries = append(entries, entry)
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	return entries
}

func TestRecallLibraryEligibilityMatchesCapsuleConflictRule(t *testing.T) {
	s := openTest(t)
	var records []*Record
	for _, meta := range []Meta{
		nil, {}, {"repo-id": {"a"}}, {"repo-id": {"b"}}, {"repo-id": {"a", "b"}},
		{"repo-id": {"a"}, "phase": {"review"}}, {"repo-id": {"a"}, "phase": {"write"}}, {"subject": {"capsule"}},
	} {
		records = append(records, mustInsert(t, s, NewRecord{Agent: "a", Lane: "recall", Kind: "memory", Body: "memory", Meta: meta}))
	}
	other := mustInsert(t, s, NewRecord{Agent: "b", Lane: "recall", Kind: "memory", Body: "other agent"})
	note := mustInsert(t, s, NewRecord{Agent: "a", Lane: "guidance", Kind: "note", Body: "instruction"})
	disabled := mustInsert(t, s, NewRecord{Agent: "a", Lane: "recall", Kind: "memory", Body: "disabled"})
	if err := SetStatus(s.DB, disabled.ID, "disabled"); err != nil {
		t.Fatal(err)
	}
	for _, meta := range []Meta{nil, {"repo-id": {"a"}}, {"repo-id": {"a", "b"}}, {"repo-id": {"a"}, "phase": {"review"}}, {"repo-id": {}}, {"repo-id": {"different"}}} {
		want := map[string]bool{}
		for _, record := range records {
			if !Conflicts(record.Meta, meta) {
				want[record.ID] = true
			}
		}
		count, err := CountEligibleRecall(s.DB, "a", meta)
		if err != nil || count != len(want) {
			t.Fatalf("count for %v = %d, %v; want %d", meta, count, err, len(want))
		}
		entries := recallIndex(t, s, RecallIndexRequest{Agent: "a", Meta: meta})
		if len(entries) != len(want) {
			t.Fatalf("page eligibility differs from count: %v", entries)
		}
		for _, entry := range entries {
			if !want[entry.ID] || entry.ID == other.ID || entry.ID == note.ID || entry.ID == disabled.ID {
				t.Fatalf("ineligible library entry: %+v", entry)
			}
		}
	}
}

func TestRecallLibraryLiveCursorDoesNotForwardOrRepeat(t *testing.T) {
	s := openTest(t)
	priorClock := Clock
	t.Cleanup(func() { Clock = priorClock })
	stamp := time.Date(2026, 9, 6, 14, 0, 0, 0, time.UTC)
	Clock = func() time.Time { return stamp }
	one := mustInsert(t, s, NewRecord{Agent: "a", Lane: "recall", Kind: "memory", Body: "oldest"})
	stamp = stamp.Add(time.Hour)
	two := mustInsert(t, s, NewRecord{Agent: "a", Lane: "recall", Kind: "memory", Body: "older tie"})
	three := mustInsert(t, s, NewRecord{Agent: "a", Lane: "recall", Kind: "memory", Body: "newer tie"})
	entries := recallIndex(t, s, RecallIndexRequest{Agent: "a"})
	var ids []string
	for _, entry := range entries {
		ids = append(ids, entry.ID)
	}
	if !reflect.DeepEqual(ids, []string{three.ID, two.ID, one.ID}) {
		t.Fatalf("index order: %v", ids)
	}
	var first RecallIndexEntry
	if err := WalkRecallIndex(s.DB, RecallIndexRequest{Agent: "a"}, func(entry RecallIndexEntry) (bool, error) { first = entry; return false, nil }); err != nil {
		t.Fatal(err)
	}
	if first.ID != three.ID {
		t.Fatalf("visitor did not stop at first entry: %+v", first)
	}
	entries = recallIndex(t, s, RecallIndexRequest{Agent: "a", After: first.Ref})
	if len(entries) != 2 || entries[0].ID != two.ID || entries[1].ID != one.ID {
		t.Fatalf("cursor page repeats/skips: %+v", entries)
	}
	if err := s.Tx(func(tx *sql.Tx) error {
		if _, err := ReplaceRecord(tx, three.ID, NewRecord{Agent: "a", Lane: "recall", Kind: "memory", Body: "new replacement"}); err != nil {
			return err
		}
		return SetStatus(tx, two.ID, "disabled")
	}); err != nil {
		t.Fatal(err)
	}
	mustInsert(t, s, NewRecord{Agent: "a", Lane: "recall", Kind: "memory", Body: "another newer memory"})
	entries = recallIndex(t, s, RecallIndexRequest{Agent: "a", After: first.Ref})
	if len(entries) != 1 || entries[0].ID != one.ID {
		t.Fatalf("live cursor followed replacement or exposed newer inserts: %+v", entries)
	}
	if err := SetStatus(s.DB, three.ID, "disabled"); err != nil {
		t.Fatal(err)
	}
	entries = recallIndex(t, s, RecallIndexRequest{Agent: "a", After: three.ID})
	if len(entries) != 1 || entries[0].ID != one.ID {
		t.Fatalf("disabled cursor lost position: %+v", entries)
	}
}

func TestRecallLibraryQueryIsUnicodeSubstringBeforePaging(t *testing.T) {
	s := openTest(t)
	wants := []*Record{
		mustInsert(t, s, NewRecord{Agent: "a", Lane: "recall", Kind: "memory", Body: "CAFÉ observation"}),
		mustInsert(t, s, NewRecord{Agent: "a", Lane: "recall", Kind: "memory", Name: "CAFÉ label", Body: "named"}),
		mustInsert(t, s, NewRecord{Agent: "a", Lane: "recall", Kind: "memory", Body: "tagged", Meta: Meta{"subject": {"CAFÉ notes"}}}),
	}
	mustInsert(t, s, NewRecord{Agent: "a", Lane: "recall", Kind: "CAFÉ", Body: "kind is not searched"})
	for range 20 {
		mustInsert(t, s, NewRecord{Agent: "a", Lane: "recall", Kind: "memory", Body: "newer unrelated material"})
	}
	mustInsert(t, s, NewRecord{Agent: "a", Lane: "recall", Kind: "memory", Body: "CAFÉ outside scope", Meta: Meta{"repo-id": {"other"}}})
	entries := recallIndex(t, s, RecallIndexRequest{Agent: "a", Query: "café", Meta: Meta{"repo-id": {"current"}}})
	if len(entries) != len(wants) {
		t.Fatalf("Unicode query: %+v", entries)
	}
	for i, entry := range entries {
		if entry.ID != wants[len(wants)-i-1].ID {
			t.Fatalf("query reordered matches: %+v", entries)
		}
	}
	var first string
	if err := WalkRecallIndex(s.DB, RecallIndexRequest{Agent: "a", Query: "CAFÉ", Meta: Meta{"repo-id": {"current"}}}, func(entry RecallIndexEntry) (bool, error) { first = entry.ID; return false, nil }); err != nil {
		t.Fatal(err)
	}
	if first != wants[2].ID {
		t.Fatal("filters ran after visitor limit")
	}
	literal := mustInsert(t, s, NewRecord{Agent: "a", Lane: "recall", Kind: "memory", Body: "literal %_ characters"})
	entries = recallIndex(t, s, RecallIndexRequest{Agent: "a", Query: "%_"})
	if len(entries) != 1 || entries[0].ID != literal.ID {
		t.Fatalf("SQL wildcard syntax leaked into literal query: %+v", entries)
	}
}

func TestRecallLibraryBoundedPreviewAndStreamCleanup(t *testing.T) {
	s := openTest(t)
	body := "# Original observation\n\n" + strings.Repeat("Readable historical detail. ", 50000) + "CAFÉ suffix match"
	large := mustInsert(t, s, NewRecord{Agent: "a", Lane: "recall", Kind: strings.Repeat("種", 1000), Name: strings.Repeat("名", 1000), Body: body})
	entries := recallIndex(t, s, RecallIndexRequest{Agent: "a", Query: "café suffix"})
	if len(entries) != 1 {
		t.Fatalf("query did not search beyond prefix: %+v", entries)
	}
	entry := entries[0]
	if entry.ID != large.ID || !entry.Truncated || utf8.RuneCountInString(entry.Excerpt) > 160 || utf8.RuneCountInString(entry.Kind) > 80 || utf8.RuneCountInString(entry.Name) > 80 || !strings.HasSuffix(entry.Excerpt, " …") || strings.Contains(entry.Excerpt, "CAFÉ suffix") {
		t.Fatalf("unbounded or misleading index entry: %+v", entry)
	}
	failure := errors.New("caller serialization failed")
	err := WalkRecallIndex(s.DB, RecallIndexRequest{Agent: "a"}, func(RecallIndexEntry) (bool, error) { return false, failure })
	if !errors.Is(err, failure) {
		t.Fatalf("visitor failure lost: %v", err)
	}
	// A subsequent query can use the single connection: both early exits and
	// callback failures release their streaming rows.
	if count, err := CountEligibleRecall(s.DB, "a", nil); err != nil || count != 1 {
		t.Fatalf("stream left rows open: %d, %v", count, err)
	}
	full, err := GetRecord(s.DB, large.ID)
	if err != nil || full.Body != body {
		t.Fatal("index rewrote original body")
	}
	for _, text := range []string{"short", strings.Repeat("界", 500), strings.Repeat("word ", 40), strings.Repeat(" ", 161), strings.Repeat("café ", 40)} {
		prefix := []rune(text)
		if len(prefix) > 161 {
			prefix = prefix[:161]
		}
		excerpt, truncated := libraryExcerpt(string(prefix))
		if utf8.RuneCountInString(excerpt) > 160 || !utf8.ValidString(excerpt) || truncated != (utf8.RuneCountInString(text) > 160) {
			t.Fatalf("bad preview %q, %v", excerpt, truncated)
		}
	}
	// A NUL is valid UTF-8. It must not make SQLite's prefix projection stop
	// early and label the rest of the original memory as already shown.
	nul := mustInsert(t, s, NewRecord{Agent: "nul", Lane: "recall", Kind: "memory", Body: "before\x00after " + strings.Repeat("word ", 100)})
	nulEntries := recallIndex(t, s, RecallIndexRequest{Agent: "nul"})
	if len(nulEntries) != 1 || nulEntries[0].ID != nul.ID || !nulEntries[0].Truncated || !strings.Contains(nulEntries[0].Excerpt, "after") {
		t.Fatalf("NUL hid the rest of a memory: %+v", nulEntries)
	}
	// SQL's bounded byte window may cut a multibyte rune after the 161 runes
	// we need. A valid original body must still produce a valid short preview.
	mustInsert(t, s, NewRecord{Agent: "unicode", Lane: "recall", Kind: "memory", Body: "abc" + strings.Repeat("🙂 ", 300)})
	unicodeEntries := recallIndex(t, s, RecallIndexRequest{Agent: "unicode"})
	if len(unicodeEntries) != 1 || !utf8.ValidString(unicodeEntries[0].Excerpt) || !unicodeEntries[0].Truncated {
		t.Fatalf("bounded bytes damaged Unicode: %+v", unicodeEntries)
	}
}

func TestRecallLibraryCursorAndInputValidation(t *testing.T) {
	s := openTest(t)
	other := mustInsert(t, s, NewRecord{Agent: "b", Lane: "recall", Kind: "memory", Body: "foreign"})
	note := mustInsert(t, s, NewRecord{Agent: "a", Lane: "guidance", Kind: "note", Body: "instruction"})
	otherRef, _ := Reference(s.DB, other.ID)
	for _, tc := range []struct {
		after string
		err   error
	}{
		{other.ID, ErrInvalid}, {otherRef, ErrInvalid}, {note.ID, ErrInvalid}, {"ctx_123", ErrInvalid}, {"@01", ErrInvalid}, {"@999999", ErrNotFound}, {"rec_MISSING", ErrNotFound}, {"bad cursor", ErrInvalid},
	} {
		err := WalkRecallIndex(s.DB, RecallIndexRequest{Agent: "a", After: tc.after}, func(RecallIndexEntry) (bool, error) { t.Fatal("invalid cursor visited a record"); return false, nil })
		if !errors.Is(err, tc.err) {
			t.Errorf("cursor %q: %v; want %v", tc.after, err, tc.err)
		}
	}
	if _, err := CountEligibleRecall(s.DB, "bad name", nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid agent: %v", err)
	}
	if _, err := CountEligibleRecall(s.DB, "a", Meta{"bad key": {"x"}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid meta: %v", err)
	}
	if err := WalkRecallIndex(s.DB, RecallIndexRequest{Agent: "a"}, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("nil visitor: %v", err)
	}
}
