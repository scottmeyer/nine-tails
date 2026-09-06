package store

import (
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func consolidationContext(t *testing.T, s *Store, agent string, rendered []ContextRecord) *Context {
	t.Helper()
	var ctx *Context
	if err := s.Tx(func(tx *sql.Tx) error {
		var err error
		ctx, err = CreateContext(tx, agent, "", "consolidate related guidance", 100, Meta{"repo-id": {"ambient"}}, rendered)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return ctx
}

func TestConsolidatePreservesScopeHistoryAndSuccessors(t *testing.T) {
	s := openTest(t)
	origin := consolidationContext(t, s, "a", nil)
	first := mustInsert(t, s, NewRecord{Agent: "a", Lane: "guidance", Kind: "prefer", Body: "First wording", OriginContext: origin.ID, Meta: Meta{"repo-id": {"one", "two"}}})
	second := mustInsert(t, s, NewRecord{Agent: "a", Lane: "guidance", Kind: "prefer", Body: "Related wording", Meta: Meta{"repo-id": {"two", "one"}}})
	rendered := []ContextRecord{{RecordID: first.ID, Section: "recent", Ordinal: 0}, {RecordID: second.ID, Section: "recent", Ordinal: 1}}
	ctx := consolidationContext(t, s, "a", rendered)
	contextRef, _ := Reference(s.DB, ctx.ID)
	firstRef, _ := Reference(s.DB, first.ID)
	merged, err := s.Consolidate(ConsolidateRequest{Context: contextRef, Sources: []string{firstRef, second.ID}, Body: "One complete instruction", Reason: "The sources describe one rule"})
	if err != nil {
		t.Fatal(err)
	}
	if merged.Kind != "prefer" || merged.Lane != "guidance" || merged.Agent != "a" || merged.Status != "active" || merged.Supersedes != nil || merged.Name != nil || *merged.OriginContext != ctx.ID || !reflect.DeepEqual(merged.Meta, first.Meta) {
		t.Fatalf("wrong consolidated envelope: %+v", merged)
	}
	if merged.Ref == "" || merged.Consolidation.Reason != "The sources describe one rule" || len(merged.Consolidation.Sources) != 2 {
		t.Fatalf("missing audit: %+v", merged)
	}
	for i, want := range []*Record{first, second} {
		source := merged.Consolidation.Sources[i]
		want.Status = "superseded"
		if !reflect.DeepEqual(source.RecordEnvelope, want.Envelope()) || source.Ref == "" {
			t.Fatalf("source history changed: %+v, want %+v", source, want)
		}
		latest, err := LatestSuccessor(s.DB, want.ID)
		if err != nil || latest != merged.ID {
			t.Fatalf("latest from %s: %s, %v", want.ID, latest, err)
		}
	}
	gotContext, err := GetContext(s.DB, ctx.ID)
	if err != nil || !reflect.DeepEqual(gotContext.Rendered, rendered) {
		t.Fatalf("consolidation rewrote source receipt: %+v, %v", gotContext, err)
	}
	if _, err := ActiveGeneration(s.DB, "a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("uncompiled agent gained a generation: %v", err)
	}
	third := mustInsert(t, s, NewRecord{Agent: "a", Lane: "guidance", Kind: "prefer", Body: "Another related rule", Meta: first.Meta})
	mergedAgain, err := s.Consolidate(ConsolidateRequest{Context: ctx.ID, Sources: []string{merged.ID, third.ID}, Body: "Expanded instruction", Reason: "Add a related condition"})
	if err != nil {
		t.Fatal(err)
	}
	var latest *Record
	if err := s.Tx(func(tx *sql.Tx) error {
		var err error
		latest, err = ReplaceRecord(tx, mergedAgain.ID, NewRecord{Agent: "a", Lane: "guidance", Kind: "prefer", Body: "Corrected instruction", Meta: first.Meta})
		if err != nil {
			return err
		}
		_, err = RetireRecord(tx, latest.ID, ctx.ID, "No longer wanted")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{first.ID, second.ID, merged.ID, third.ID, mergedAgain.ID} {
		got, err := LatestSuccessor(s.DB, id)
		if err != nil || got != latest.ID {
			t.Errorf("latest from %s: %s, %v; want %s", id, got, err, latest.ID)
		}
	}
	audit, err := GetConsolidation(s.DB, mergedAgain.ID)
	if err != nil || len(audit.Sources) != 2 || audit.Sources[0].ID != merged.ID || audit.Sources[0].Body != "One complete instruction" {
		t.Fatalf("immediate source history was flattened or overwritten: %+v, %v", audit, err)
	}
	if ordinary, err := GetConsolidation(s.DB, latest.ID); err != nil || ordinary != nil {
		t.Fatalf("ordinary replacement acquired synthetic consolidation: %+v, %v", ordinary, err)
	}
	// The audit retains origin identifiers and bodies, not perpetual receipt
	// pins. Existing retention can collect inactive source origins normally.
	if _, err := GCContexts(s, Clock().Add(time.Hour), false); err != nil {
		t.Fatal(err)
	}
	if _, err := GetContext(s.DB, origin.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("consolidation unexpectedly pinned old origin: %v", err)
	}
	audit, err = GetConsolidation(s.DB, merged.ID)
	if err != nil || *audit.Sources[0].OriginContext != origin.ID || audit.Sources[0].Body != "First wording" {
		t.Fatalf("receipt collection destroyed source evidence: %+v, %v", audit, err)
	}
}

func TestConcurrentConsolidationHasOneWinner(t *testing.T) {
	s := openTest(t)
	other, err := Open(s.Home)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	ctx := consolidationContext(t, s, "a", nil)
	one := mustInsert(t, s, NewRecord{Agent: "a", Lane: "guidance", Kind: "note", Body: "one"})
	two := mustInsert(t, s, NewRecord{Agent: "a", Lane: "guidance", Kind: "note", Body: "two"})
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, conn := range []*Store{s, other} {
		go func(conn *Store) {
			<-start
			_, err := conn.Consolidate(ConsolidateRequest{Context: ctx.ID, Sources: []string{one.ID, two.ID}, Body: "merged", Reason: "same rule"})
			results <- err
		}(conn)
	}
	close(start)
	var successes, conflicts int
	for range 2 {
		err := <-results
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrConflict):
			conflicts++
		default:
			t.Fatalf("unexpected concurrent result: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent consolidation: %d successes, %d conflicts", successes, conflicts)
	}
	var audits int
	if err := s.DB.QueryRow(`SELECT count(*) FROM consolidations`).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("expected one durable consolidation: %d, %v", audits, err)
	}
}

func TestConsolidateRejectsInvalidSourcesAtomically(t *testing.T) {
	for _, name := range []string{"one source", "duplicate alias", "wrong owner", "recall", "brief", "state", "different scope", "different kinds", "disabled", "missing", "wrong context type", "missing context", "missing reason", "blank body", "brief kind", "invalid utf8"} {
		t.Run(name, func(t *testing.T) {
			s := openTest(t)
			ctx := consolidationContext(t, s, "a", nil)
			first := mustInsert(t, s, NewRecord{Agent: "a", Lane: "guidance", Kind: "note", Body: "one", Meta: Meta{"repo-id": {"a"}}})
			nr := NewRecord{Agent: "a", Lane: "guidance", Kind: "note", Body: "two", Meta: first.Meta}
			switch name {
			case "wrong owner":
				nr.Agent = "b"
			case "recall":
				nr.Lane = "recall"
			case "brief":
				nr.Kind = "brief-item"
			case "state":
				nr.Lane, nr.Kind = "state", "working-state"
			case "different scope":
				nr.Meta = Meta{"repo-id": {"b"}}
			case "different kinds":
				nr.Kind = "avoid"
			}
			second := mustInsert(t, s, nr)
			req := ConsolidateRequest{Context: ctx.ID, Sources: []string{first.ID, second.ID}, Body: "combined", Reason: "same instruction"}
			wantErr := ErrInvalid
			switch name {
			case "one source":
				req.Sources = req.Sources[:1]
			case "duplicate alias":
				ref, _ := Reference(s.DB, first.ID)
				req.Sources[1] = ref
			case "disabled":
				if err := SetStatus(s.DB, second.ID, "disabled"); err != nil {
					t.Fatal(err)
				}
				wantErr = ErrConflict
			case "missing":
				req.Sources[1] = "rec_999999"
				wantErr = ErrNotFound
			case "wrong context type":
				req.Context = first.ID
			case "missing context":
				req.Context = "ctx_999999"
				wantErr = ErrNotFound
			case "missing reason":
				req.Reason = "  \n"
			case "blank body":
				req.Body = " \n"
			case "brief kind":
				req.Kind = "brief-item"
			case "invalid utf8":
				req.Reason = string([]byte{255})
			}
			var before int
			if err := s.DB.QueryRow(`SELECT count(*) FROM records`).Scan(&before); err != nil {
				t.Fatal(err)
			}
			result, err := s.Consolidate(req)
			if !errors.Is(err, wantErr) || result != nil {
				t.Fatalf("got %+v, %v; want %v", result, err, wantErr)
			}
			var after, audits int
			if err := s.DB.QueryRow(`SELECT count(*) FROM records`).Scan(&after); err != nil {
				t.Fatal(err)
			}
			if err := s.DB.QueryRow(`SELECT count(*) FROM consolidations`).Scan(&audits); err != nil {
				t.Fatal(err)
			}
			got, err := GetRecord(s.DB, first.ID)
			if err != nil || got.Status != "active" || after != before || audits != 0 {
				t.Fatalf("failed operation mutated store: %+v, %v, counts %d -> %d, audits %d", got, err, before, after, audits)
			}
		})
	}
}

func TestConsolidateMixedKindsAndStaleSource(t *testing.T) {
	s := openTest(t)
	ctx := consolidationContext(t, s, "a", nil)
	first := mustInsert(t, s, NewRecord{Agent: "a", Lane: "guidance", Kind: "prefer", Body: "one"})
	second := mustInsert(t, s, NewRecord{Agent: "a", Lane: "guidance", Kind: "avoid", Body: "two"})
	req := ConsolidateRequest{Context: ctx.ID, Sources: []string{first.ID, second.ID}, Body: "one reconciled policy", Reason: "positive and negative forms", Kind: "policy"}
	merged, err := s.Consolidate(req)
	if err != nil || merged.Kind != "policy" {
		t.Fatalf("explicit open kind rejected: %+v, %v", merged, err)
	}
	_, err = s.Consolidate(req)
	if !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "inspect "+merged.Ref) {
		t.Fatalf("stale source needs actionable current ref: %v", err)
	}
	err = s.Tx(func(tx *sql.Tx) error {
		_, err := ReplaceRecord(tx, second.ID, NewRecord{Agent: "a", Lane: "guidance", Kind: "note", Body: "wrong target"})
		return err
	})
	if !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "inspect "+merged.Ref) {
		t.Fatalf("ordinary correction must find consolidation successor: %v", err)
	}
}

func TestConsolidateInvalidatesDependentBriefAndRollsBack(t *testing.T) {
	for _, mode := range []string{"represented", "superseded-by", "unrelated", "failure"} {
		t.Run(mode, func(t *testing.T) {
			s := openTest(t)
			ctx := consolidationContext(t, s, "a", nil)
			first := mustInsert(t, s, NewRecord{Agent: "a", Lane: "guidance", Kind: "note", Body: "one"})
			second := mustInsert(t, s, NewRecord{Agent: "a", Lane: "guidance", Kind: "note", Body: "two"})
			other := mustInsert(t, s, NewRecord{Agent: "a", Lane: "guidance", Kind: "note", Body: "other"})
			source := first.ID
			if mode == "unrelated" {
				source = other.ID
			}
			items := []NewItem{{Key: "policy", Body: "old compiled text", Sources: []string{source}}}
			inputs := []BriefInput{{EntryID: source, Disposition: "represented", Coverage: "unknown"}}
			if mode == "superseded-by" {
				items = nil
				inputs = []BriefInput{{EntryID: other.ID, Disposition: "superseded-by", Coverage: "unknown", Successor: first.ID}}
			}
			var gen *Generation
			if err := s.Tx(func(tx *sql.Tx) error {
				var err error
				gen, _, err = InstallGeneration(tx, "a", "", items, inputs)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			var beforeRefs int
			if err := s.DB.QueryRow(`SELECT count(*) FROM reference_aliases`).Scan(&beforeRefs); err != nil {
				t.Fatal(err)
			}
			if mode == "failure" {
				// Fail on the second source edge, after invalidation, successor
				// insertion and source status changes; everything must roll back.
				if _, err := s.DB.Exec(`CREATE TRIGGER reject_second_source BEFORE INSERT ON consolidation_sources
					WHEN NEW.ordinal = 1 BEGIN SELECT RAISE(ABORT, 'injected failure'); END`); err != nil {
					t.Fatal(err)
				}
			}
			merged, err := s.Consolidate(ConsolidateRequest{Context: ctx.ID, Sources: []string{first.ID, second.ID}, Body: "combined", Reason: "same rule"})
			if mode == "failure" && err == nil {
				t.Fatal("injected failure ignored")
			}
			if mode != "failure" && err != nil {
				t.Fatal(err)
			}
			current, err := ActiveGeneration(s.DB, "a")
			if err != nil {
				t.Fatal(err)
			}
			if mode == "unrelated" || mode == "failure" {
				if current.ID != gen.ID {
					t.Fatalf("unrelated or failed operation changed generation: %+v", current)
				}
			} else {
				if current.Parent != gen.ID {
					t.Fatalf("dependent generation retained: %+v", current)
				}
				gotItems, err := GenerationItems(s.DB, current.ID)
				if err != nil || len(gotItems) != 0 {
					t.Fatalf("stale brief survived: %+v, %v", gotItems, err)
				}
				gotInputs, err := GenerationInputs(s.DB, current.ID)
				if err != nil || len(gotInputs) != 0 {
					t.Fatalf("stale accounting survived: %+v, %v", gotInputs, err)
				}
			}
			if mode == "failure" {
				for _, id := range []string{first.ID, second.ID} {
					r, err := GetRecord(s.DB, id)
					if err != nil || r.Status != "active" {
						t.Fatalf("failure retired %s: %+v, %v", id, r, err)
					}
				}
				var audits, refs int
				if err := s.DB.QueryRow(`SELECT count(*) FROM consolidations`).Scan(&audits); err != nil {
					t.Fatal(err)
				}
				if err := s.DB.QueryRow(`SELECT count(*) FROM reference_aliases`).Scan(&refs); err != nil {
					t.Fatal(err)
				}
				if audits != 0 || refs != beforeRefs || merged != nil {
					t.Fatalf("failure left audit or alias residue: audits %d refs %d/%d result %+v", audits, refs, beforeRefs, merged)
				}
			}
		})
	}
}

func TestLatestSuccessorRejectsConsolidationCyclesAndBranches(t *testing.T) {
	for _, mode := range []string{"cycle", "branch"} {
		t.Run(mode, func(t *testing.T) {
			s := openTest(t)
			ctx := consolidationContext(t, s, "a", nil)
			one := mustInsert(t, s, NewRecord{Agent: "a", Lane: "guidance", Kind: "note", Body: "one"})
			two := mustInsert(t, s, NewRecord{Agent: "a", Lane: "guidance", Kind: "note", Body: "two"})
			merged, err := s.Consolidate(ConsolidateRequest{Context: ctx.ID, Sources: []string{one.ID, two.ID}, Body: "combined", Reason: "same rule"})
			if err != nil {
				t.Fatal(err)
			}
			if mode == "cycle" {
				_, err = s.DB.Exec(`UPDATE records SET supersedes_id = ? WHERE id = ?`, merged.ID, one.ID)
			} else {
				_, err = s.DB.Exec(`UPDATE records SET supersedes_id = ? WHERE id = ?`, one.ID, two.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := LatestSuccessor(s.DB, one.ID); err == nil {
				t.Fatal("corrupt history accepted")
			}
		})
	}
}
