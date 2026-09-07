package capsule

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scottmeyer/nine-tails/internal/cli"
	"github.com/scottmeyer/nine-tails/internal/store"
	"github.com/scottmeyer/nine-tails/internal/tokens"
)

func TestCommandBindingCountsBeforeTransportAndPreservesAuthoredText(t *testing.T) {
	s := setup(t)
	body := "Keep this authored example unchanged: `nine-tails inspect historical-example`."
	base := insert(t, s, store.NewRecord{Agent: "a", Lane: "definition", Kind: "agent-base", Name: "base", Body: body})
	plain, err := Load(s, Request{Agent: "a"})
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(t.TempDir(), "memory's `$HOME`")
	bound, err := Load(s, Request{Agent: "a", CommandHome: home})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(bound.Instructions, body) ||
		!strings.Contains(bound.Instructions, cli.InlineCode(cli.StoreCommand(home, "refs"))) ||
		!strings.Contains(bound.Instructions, cli.InlineCode(cli.StoreCommand(home, "inspect <episode-receipt> --review"))) {
		t.Fatalf("authored body changed or command quoting missing: %s", bound.Instructions)
	}
	if bound.EstimatedTokens != tokens.Estimate(bound.Markdown) || len(bound.Markdown) <= len(plain.Markdown) {
		t.Fatal("binding must be included in size accounting")
	}
	stored, err := store.GetContext(s.DB, bound.ContextID)
	if err != nil || stored.EstimatedTokens != bound.EstimatedTokens {
		t.Fatalf("receipt size mismatch: %+v, %v", stored, err)
	}
	before := rowCounts(t, s)
	_, err = Load(s, Request{Agent: "a", CommandHome: home, MaxBytes: len(plain.Markdown)})
	var tooLarge *TooLargeError
	if !errors.As(err, &tooLarge) {
		t.Fatalf("bound capsule should exceed the plain capsule ceiling: %v", err)
	}
	if after := rowCounts(t, s); after != before {
		t.Fatalf("failed transport left receipt/reference rows: before %v after %v", before, after)
	}
	current, err := store.GetRecord(s.DB, base.ID)
	if err != nil || current.Body != body {
		t.Fatalf("runtime binding changed durable knowledge: %+v %v", current, err)
	}
}

func rowCounts(t *testing.T, s *store.Store) [3]int {
	t.Helper()
	var counts [3]int
	for i, table := range []string{"contexts", "context_records", "reference_aliases"} {
		if err := s.DB.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&counts[i]); err != nil {
			t.Fatal(err)
		}
	}
	return counts
}
