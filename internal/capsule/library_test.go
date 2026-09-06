package capsule

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/scottmeyer/nine-tails/internal/store"
)

func TestLibraryPointerIsScopedDiscoverableDataWithoutRecall(t *testing.T) {
	s := setup(t)
	insert(t, s, store.NewRecord{Agent: "a", Lane: "definition", Kind: "agent-base", Name: "base", Body: "Base."})
	guidance := insert(t, s, store.NewRecord{Agent: "a", Lane: "guidance", Kind: "prefer", Body: "Keep all corrections."})
	empty, err := Load(s, Request{Agent: "a"})
	if err != nil || empty.Library != nil || strings.Contains(empty.Markdown, "Memory library") {
		t.Fatalf("empty recall library should add no pointer: %+v, %v", empty, err)
	}
	global := insert(t, s, store.NewRecord{Agent: "a", Lane: "recall", Kind: "memory", Body: "Unqualified experience."})
	insert(t, s, store.NewRecord{Agent: "a", Lane: "recall", Kind: "memory", Body: "Letters style.", Meta: store.Meta{"repo-id": {"letters"}}})
	insert(t, s, store.NewRecord{Agent: "a", Lane: "recall", Kind: "incident", Body: "Letters incident.", Meta: store.Meta{"repo-id": {"letters"}}})
	insert(t, s, store.NewRecord{Agent: "a", Lane: "recall", Kind: "memory", Body: "Other project.", Meta: store.Meta{"repo-id": {"games"}}})
	insert(t, s, store.NewRecord{Agent: "b", Lane: "recall", Kind: "memory", Body: "Other owner.", Meta: store.Meta{"repo-id": {"letters"}}})
	retired := insert(t, s, store.NewRecord{Agent: "a", Lane: "recall", Kind: "memory", Body: "Retired experience."})
	if _, err := s.DB.Exec("UPDATE records SET status='disabled' WHERE id=?", retired.ID); err != nil {
		t.Fatal(err)
	}
	for _, req := range []Request{
		{Agent: "a", Meta: store.Meta{"repo-id": {"letters"}}},
		{Agent: "a", Task: "letters", Query: strptr(""), Meta: store.Meta{"repo-id": {"letters"}}},
		{Agent: "a", Task: "letters", Recall: []string{}, Meta: store.Meta{"repo-id": {"letters"}}},
	} {
		c, err := Load(s, req)
		if err != nil {
			t.Fatal(err)
		}
		if c.Library == nil || c.Library.Count != 3 || c.Library.Inspect != "nine-tails inspect --page --context "+c.ContextRef {
			t.Fatalf("library did not use the owner, resolved scope and current receipt: %+v", c.Library)
		}
		if len(c.Recall) != 0 || strings.Contains(c.Instructions, "Memory library") || !strings.Contains(c.Instructions, guidance.Body) || !strings.Contains(c.Markdown, "Memory library (data): 3 memories; browse with `"+c.Library.Inspect+"`") {
			t.Fatalf("library pointer changed recall/guidance or was not discoverable: %+v", c)
		}
		receipt, err := store.GetContext(s.DB, c.ContextID)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range receipt.Rendered {
			if entry.Section == "recall" || entry.RecordID == global.ID {
				t.Fatal("library index was falsely recorded as delivered memory")
			}
		}
		body, err := json.Marshal(c)
		if err != nil || !strings.Contains(string(body), `"library":{"count":3,"inspect":"nine-tails inspect --page --context `) {
			t.Fatalf("structured library pointer missing: %s, %v", body, err)
		}
	}
}

func TestLibraryCountIgnoresRetrievalSelectionAndInheritsScope(t *testing.T) {
	s := setup(t)
	insert(t, s, store.NewRecord{Agent: "a", Lane: "definition", Kind: "agent-base", Name: "base", Body: "Base."})
	one := insert(t, s, store.NewRecord{Agent: "a", Lane: "recall", Kind: "memory", Body: "One memory."})
	insert(t, s, store.NewRecord{Agent: "a", Lane: "recall", Kind: "memory", Body: "Letters memory.", Meta: store.Meta{"repo-id": {"letters"}}})
	parent, err := Load(s, Request{Agent: "a", Meta: store.Meta{"repo-id": {"letters"}, "harness": {"test"}}})
	if err != nil {
		t.Fatal(err)
	}
	selected, err := Load(s, Request{Agent: "a", Parent: parent.ContextID, Query: strptr("does not match"), Recall: []string{one.ID}})
	if err != nil || len(selected.Recall) != 1 || selected.Library == nil || selected.Library.Count != 2 {
		t.Fatalf("exact selection narrowed library discovery: %+v, %v", selected, err)
	}
	switched, err := Load(s, Request{Agent: "a", Parent: parent.ContextID, Meta: store.Meta{"repo-id": {"games"}}})
	if err != nil || switched.Library == nil || switched.Library.Count != 1 || !strings.Contains(switched.Markdown, "1 memory;") {
		t.Fatalf("library did not follow replaced project scope: %+v, %v", switched, err)
	}
}
