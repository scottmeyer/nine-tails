package capsule

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/scottmeyer/nine-tails/internal/store"
)

func TestProtocolRecipesFollowSurfacedCapabilities(t *testing.T) {
	s := setup(t)
	base := insert(t, s, store.NewRecord{Agent: "role", Lane: "definition", Kind: "agent-base", Name: "base", Body: "Preserve this role's purpose."})
	insert(t, s, store.NewRecord{Agent: "role", Lane: "state", Kind: "working-state", Name: "foreign", Body: "phase: scoped out", Meta: store.Meta{"repo-id": {"other"}}})
	insert(t, s, store.NewRecord{Agent: "role", Lane: "definition", Kind: "tool", Name: "broken", Body: "not a tool"})
	load := func() *Capsule {
		t.Helper()
		c, err := Load(s, Request{Agent: "role", Meta: store.Meta{"repo-id": {"project"}}})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	empty := load()
	for _, absent := range []string{"nine-tails state get", "nine-tails state put", "Inspect advertised tools", "nine-tails close"} {
		if strings.Contains(empty.Instructions, absent) {
			t.Fatalf("unavailable capability has a recipe: %s", absent)
		}
	}
	if len(empty.RenderedIDs) != 1 || empty.RenderedIDs[0] != base.ID {
		t.Fatal("scope or corrupt tool changed rendered set")
	}
	state := insert(t, s, store.NewRecord{Agent: "owner", Lane: "state", Kind: "working-state", Name: "project", Body: "phase: shared-current", Meta: store.Meta{"repo-id": {"project"}}})
	link := insert(t, s, store.NewRecord{Agent: "role", Lane: "definition", Kind: "state-link", Name: "project", Body: "owner/project"})
	tool := insert(t, s, store.NewRecord{Agent: "role", Lane: "definition", Kind: "tool", Name: "check", Body: "description: Check the work\nexec:\n  argv: [echo, checked]"})
	c := load()
	for _, present := range []string{
		"State: `nine-tails state get <owner>/<name>`",
		"nine-tails state put role/<name> --context " + c.ContextRef + " --expect <ref|none> --stdin",
		"Inspect advertised tools before calling them.",
	} {
		if !strings.Contains(c.Instructions, present) {
			t.Fatalf("surfaced capability has no recipe: %s", present)
		}
	}
	if strings.Contains(c.Instructions, "shared-current") || !strings.Contains(c.Markdown, "shared-current") || !strings.HasPrefix(c.Markdown, c.Instructions) {
		t.Fatal("referenced state crossed the instruction boundary")
	}
	if !strings.Contains(c.Instructions, base.Body) {
		t.Fatal("protocol compacted the role base")
	}
	if len(c.State) != 1 || c.State[0].ID != state.ID || c.State[0].Agent != "owner" || len(c.StateLinks) != 1 || c.StateLinks[0].ID != link.ID {
		t.Fatal("capability discovery changed selected state")
	}
	allowed := map[string]bool{base.ID: true, state.ID: true, link.ID: true, tool.ID: true}
	receipt, err := store.GetContext(s.DB, c.ContextID)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.RenderedIDs) != len(allowed) || len(receipt.Rendered) != len(allowed) {
		t.Fatal("wrong receipt cardinality")
	}
	for i, r := range receipt.Rendered {
		if !allowed[r.RecordID] || c.RenderedIDs[i] != r.RecordID || strings.HasPrefix(r.RecordID, "@") {
			t.Fatal("display refs changed canonical receipt lineage")
		}
	}
	allowed[c.ContextID] = true
	for _, ref := range regexp.MustCompile(`@[1-9][0-9]*`).FindAllString(c.Markdown, -1) {
		id, err := store.ResolveReference(s.DB, ref)
		if err != nil || !allowed[id] {
			t.Fatalf("displayed ref %s does not identify delivered context: %s, %v", ref, id, err)
		}
	}
	for _, id := range []string{state.ID, link.ID, tool.ID} {
		if strings.Contains(c.Markdown, id) {
			t.Fatalf("rendered handle stayed opaque: %s", id)
		}
	}
}

func TestNeverExistingOrphanHasNoInventedReference(t *testing.T) {
	s := setup(t)
	base := insert(t, s, store.NewRecord{Agent: "role", Lane: "definition", Kind: "agent-base", Name: "base", Body: "Role."})
	const orphan = "sig_NEVEREXISTED"
	if _, err := s.DB.Exec("INSERT INTO signal_delivery(record_id, agent, available_at, state) VALUES(?, ?, ?, 'pending')", orphan, "role", "2000-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Reference(s.DB, orphan); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unexpected orphan reference: %v", err)
	}
	c, err := Load(s, Request{Agent: "role"})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Skipped) != 1 || c.Skipped[0].ID != orphan || c.Skipped[0].Ref != "" || len(c.RenderedIDs) != 1 || c.RenderedIDs[0] != base.ID {
		t.Fatalf("orphan changed load: %#v", c)
	}
	if _, err := store.Reference(s.DB, orphan); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("load invented orphan reference: %v", err)
	}
}
