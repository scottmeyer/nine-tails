package capsule

import (
	"errors"
	"strings"
	"testing"

	"github.com/scottmeyer/nine-tails/internal/store"
)

func TestReferencedStateRespectsTransportCeiling(t *testing.T) {
	s := setup(t)
	insert(t, s, store.NewRecord{Agent: "role", Lane: "definition", Kind: "agent-base", Name: "base", Body: "Role."})
	baseline, err := Load(s, Request{Agent: "role"})
	if err != nil {
		t.Fatal(err)
	}
	insert(t, s, store.NewRecord{Agent: "owner", Lane: "state", Kind: "working-state", Name: "project", Body: "value: " + strings.Repeat("shared fact ", 400)})
	insert(t, s, store.NewRecord{Agent: "role", Lane: "definition", Kind: "state-link", Name: "project", Body: "owner/project"})
	_, err = Load(s, Request{Agent: "role", MaxBytes: len(baseline.Markdown) + 100})
	var tooLarge *TooLargeError
	if !errors.As(err, &tooLarge) {
		t.Fatalf("linked data bypassed byte ceiling: %v", err)
	}
	var count int
	if err := s.DB.QueryRow("SELECT COUNT(*) FROM contexts").Scan(&count); err != nil || count != 1 {
		t.Fatalf("oversized receipt committed: count=%d error=%v", count, err)
	}
}

func TestStateLinkCorruptionIsActionableAndDoesNotBlockLoad(t *testing.T) {
	s := setup(t)
	base := insert(t, s, store.NewRecord{Agent: "role", Lane: "definition", Kind: "agent-base", Name: "base", Body: "Role."})
	link := insert(t, s, store.NewRecord{Agent: "role", Lane: "definition", Kind: "state-link", Name: "project", Body: "owner/project"})
	state := insert(t, s, store.NewRecord{Agent: "owner", Lane: "state", Kind: "working-state", Name: "project", Body: "[invalid YAML"})
	stateRef, _ := store.Reference(s.DB, state.ID)
	c, err := Load(s, Request{Agent: "role"})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.RenderedIDs) != 1 || c.RenderedIDs[0] != base.ID || len(c.Skipped) != 1 || !strings.Contains(c.Skipped[0].Reason, stateRef) || !strings.Contains(c.Markdown, "repair the target state") {
		t.Fatalf("corrupt target: %#v", c)
	}
	for _, body := range []string{"valid: first\n---\n[invalid", "valid: first\n---\nvalid: second", ""} {
		if _, err := s.DB.Exec("UPDATE records SET body=? WHERE id=?", body, state.ID); err != nil {
			t.Fatal(err)
		}
		c, err = Load(s, Request{Agent: "role"})
		if err != nil || len(c.State) != 0 || len(c.Skipped) != 1 {
			t.Fatalf("accepted incomplete/multiple state documents: %#v, %v", c, err)
		}
	}
	if _, err := s.DB.Exec("UPDATE records SET body=? WHERE id=?", strings.Repeat("untrusted malformed body ", 100), link.ID); err != nil {
		t.Fatal(err)
	}
	c, err = Load(s, Request{Agent: "role"})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Skipped) != 1 || !strings.Contains(c.Skipped[0].Reason, "invalid target") || len(c.Skipped[0].Reason) > 400 || strings.Contains(c.Markdown, "untrusted malformed body") {
		t.Fatalf("corrupt link: %#v", c)
	}
}
