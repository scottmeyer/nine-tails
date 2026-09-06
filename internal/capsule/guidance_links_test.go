package capsule

import (
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/scottmeyer/nine-tails/internal/store"
)

func TestSharedGuidanceHonorsSourceLifecycleWithoutRenderingSourceBrief(t *testing.T) {
	s := setup(t)
	insert(t, s, store.NewRecord{Agent: "role", Lane: "definition", Kind: "agent-base", Name: "base", Body: "Role."})
	old := insert(t, s, store.NewRecord{Agent: "source", Lane: "guidance", Kind: "note", Body: "Deploy immediately."})
	current := insert(t, s, store.NewRecord{Agent: "source", Lane: "guidance", Kind: "note", Body: "Wait for approval.", Meta: store.Meta{"repo-id": {"current"}}})
	represented := insert(t, s, store.NewRecord{Agent: "source", Lane: "guidance", Kind: "note", Body: "Raw represented source remains visible."})
	insert(t, s, store.NewRecord{Agent: "role", Lane: "definition", Kind: "guidance-link", Name: "source", Body: "source"})
	if err := s.Tx(func(tx *sql.Tx) error {
		_, _, err := store.InstallGeneration(tx, "source", "", []store.NewItem{{Key: "source-brief", Body: "Never expose this source brief.", Sources: []string{represented.ID}}}, []store.BriefInput{
			{EntryID: old.ID, Disposition: "superseded-by", Coverage: "unknown", Successor: current.ID},
			{EntryID: current.ID, Disposition: "deferred", Coverage: "unknown"},
			{EntryID: represented.ID, Disposition: "represented", Coverage: "unknown"},
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Tx(func(tx *sql.Tx) error {
		var err error
		current, err = store.ReplaceRecord(tx, current.ID, store.NewRecord{Agent: "source", Lane: "guidance", Kind: "note", Meta: current.Meta.Clone()})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		repo             string
		wantOld, wantNew bool
	}{
		{"current", false, true},
		{"other", true, false},
	} {
		c, err := Load(s, Request{Agent: "role", Meta: store.Meta{"repo-id": {tc.repo}}})
		if err != nil || strings.Contains(c.Instructions, old.Body) != tc.wantOld || strings.Contains(c.Instructions, current.Body) != tc.wantNew || !strings.Contains(c.Instructions, represented.Body) || strings.Contains(c.Instructions, "Never expose this source brief.") {
			t.Fatalf("repo %s projection: %v\n%s", tc.repo, err, c.Instructions)
		}
	}
	if err := s.Tx(func(tx *sql.Tx) error { _, err := store.RetireRecord(tx, current.ID, "", ""); return err }); err != nil {
		t.Fatal(err)
	}
	c, err := Load(s, Request{Agent: "role", Meta: store.Meta{"repo-id": {"current"}}})
	if err != nil || !strings.Contains(c.Instructions, old.Body) {
		t.Fatalf("unusable successor did not restore source: %v\n%s", err, c.Instructions)
	}
}

func TestSharedGuidanceRespectsByteCeilingAndDoesNotReadSourceIdentity(t *testing.T) {
	s := setup(t)
	insert(t, s, store.NewRecord{Agent: "role", Lane: "definition", Kind: "agent-base", Name: "base", Body: "Role."})
	insert(t, s, store.NewRecord{Agent: "source", Lane: "definition", Kind: "agent-base", Name: "base", Body: "Secret source identity."})
	baseline, err := Load(s, Request{Agent: "role"})
	if err != nil {
		t.Fatal(err)
	}
	shared := insert(t, s, store.NewRecord{Agent: "source", Lane: "guidance", Kind: "note", Body: strings.Repeat("shared rule ", 400)})
	insert(t, s, store.NewRecord{Agent: "role", Lane: "definition", Kind: "guidance-link", Name: "source", Body: "source"})
	_, err = Load(s, Request{Agent: "role", MaxBytes: len(baseline.Markdown) + 100})
	var tooLarge *TooLargeError
	if !errors.As(err, &tooLarge) {
		t.Fatalf("linked guidance bypassed byte ceiling: %v", err)
	}
	var count int
	if err := s.DB.QueryRow("SELECT COUNT(*) FROM contexts").Scan(&count); err != nil || count != 1 {
		t.Fatalf("oversized receipt committed: count=%d error=%v", count, err)
	}
	c, err := Load(s, Request{Agent: "role"})
	if err != nil || len(c.GuidanceLinks) != 1 || c.GuidanceLinks[0].GuidanceID != shared.ID || strings.Contains(c.Instructions, "Secret source identity.") || !strings.Contains(c.Instructions, "Shared guidance retains its labeled source owner") {
		t.Fatalf("shared guidance projection: %#v %v\n%s", c.GuidanceLinks, err, c.Instructions)
	}
}
