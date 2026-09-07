package store

import (
	"database/sql"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestEpisodeReviewProjectionPreservesNULAndExactSource(t *testing.T) {
	s := openTest(t)
	body := "before\x00after significant evidence"
	name := "topic\x00continued"
	record := mustInsert(t, s, NewRecord{Agent: "a", Lane: "recall", Kind: "memory", Name: name, Body: body})

	projected, err := GetEpisodeReviewRecord(s.DB, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if projected.NamePrefix != name || projected.NameTruncated || projected.BodyPrefix != body || projected.BodyTruncated {
		t.Fatalf("NUL shortened review projection: %+v", projected)
	}
	exact, err := GetRecord(s.DB, record.ID)
	if err != nil || exact.Name != name || exact.Body != body {
		t.Fatalf("projection changed exact source: %+v, %v", exact, err)
	}

	var ctx *Context
	reason := "obsolete\x00because later evidence supersedes it"
	if err := s.Tx(func(tx *sql.Tx) error {
		var err error
		ctx, err = CreateContext(tx, "a", "", "review NUL handling", 1, nil, nil)
		if err != nil {
			return err
		}
		_, err = RetireRecord(tx, record.ID, ctx.ID, reason)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	projectedReason, truncated, err := EpisodeReviewRetirementReason(s.DB, record.ID)
	if err != nil || projectedReason != reason || truncated {
		t.Fatalf("NUL shortened retirement reason: %q, truncated=%v, err=%v", projectedReason, truncated, err)
	}
	retirement, err := GetRetirement(s.DB, record.ID)
	if err != nil || retirement.Reason != reason {
		t.Fatalf("projection changed exact retirement: %+v, %v", retirement, err)
	}
}

func TestEpisodeReviewProjectionTruncatesOnRuneBoundary(t *testing.T) {
	s := openTest(t)
	body := strings.Repeat("🙂", reviewProjectionRunes+1) + "tail"
	record := mustInsert(t, s, NewRecord{Agent: "a", Lane: "recall", Kind: "memory", Body: body})

	projected, err := GetEpisodeReviewRecord(s.DB, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !projected.BodyTruncated || utf8.RuneCountInString(projected.BodyPrefix) != reviewProjectionRunes || !utf8.ValidString(projected.BodyPrefix) {
		t.Fatalf("bad Unicode projection: runes=%d valid=%v truncated=%v", utf8.RuneCountInString(projected.BodyPrefix), utf8.ValidString(projected.BodyPrefix), projected.BodyTruncated)
	}
	exact, err := GetRecord(s.DB, record.ID)
	if err != nil || exact.Body != body {
		t.Fatal("bounded projection changed the exact Unicode body")
	}
}

func TestEpisodeReviewProjectionReportsLongWhitespacePrefix(t *testing.T) {
	s := openTest(t)
	body := strings.Repeat(" ", reviewProjectionRunes+100) + "significant evidence"
	record := mustInsert(t, s, NewRecord{Agent: "a", Lane: "recall", Kind: "memory", Body: body})

	projected, err := GetEpisodeReviewRecord(s.DB, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !projected.BodyTruncated || utf8.RuneCountInString(projected.BodyPrefix) != reviewProjectionRunes {
		t.Fatalf("long whitespace prefix was reported complete: runes=%d truncated=%v", utf8.RuneCountInString(projected.BodyPrefix), projected.BodyTruncated)
	}
	exact, err := GetRecord(s.DB, record.ID)
	if err != nil || exact.Body != body || !strings.HasSuffix(exact.Body, "significant evidence") {
		t.Fatal("bounded projection changed or hid the exact source")
	}
}
