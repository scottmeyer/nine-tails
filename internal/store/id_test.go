package store

import (
	"regexp"
	"testing"
	"time"
)

func TestNewIDShapeAndUniqueness(t *testing.T) {
	shape := regexp.MustCompile(`^rec_[0-9A-HJKMNP-TV-Z]{26}$`)
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		id, err := NewID("rec")
		if err != nil {
			t.Fatal(err)
		}
		if !shape.MatchString(id) {
			t.Fatalf("id %q is not prefix_ULID", id)
		}
		if seen[id] {
			t.Fatalf("id %q repeated", id)
		}
		seen[id] = true
		if !IsID(id) {
			t.Fatalf("IsID rejects %q", id)
		}
	}
}

func TestNewIDTimePartFollowsClock(t *testing.T) {
	old := Clock
	defer func() { Clock = old }()
	Clock = func() time.Time { return time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC) }
	a, _ := NewID("ctx")
	b, _ := NewID("ctx")
	if a[:14] != b[:14] {
		t.Fatalf("same clock should share the time prefix: %s vs %s", a, b)
	}
	if a == b {
		t.Fatalf("random part must differ: %s", a)
	}
	Clock = func() time.Time { return time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC) }
	c, _ := NewID("ctx")
	if c[:14] == a[:14] {
		t.Fatalf("a later clock should change the time prefix: %s vs %s", a, c)
	}
	// Sequential ids from older stores stay recognizable.
	for _, s := range []string{"rec_41", "ctx_72", a} {
		if !IsID(s) {
			t.Fatalf("IsID should accept %q", s)
		}
	}
	for _, s := range []string{"rec_", "Rec_1", "rec_ab", "rec-1", "builder"} {
		if IsID(s) {
			t.Fatalf("IsID should reject %q", s)
		}
	}
}
