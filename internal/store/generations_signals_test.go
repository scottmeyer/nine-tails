package store

import (
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"
)

func mustInsert(t *testing.T, s *Store, nr NewRecord) *Record {
	t.Helper()
	var rec *Record
	if err := s.Tx(func(tx *sql.Tx) error {
		var err error
		rec, err = InsertRecord(tx, nr)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return rec
}

func TestInstallGenerationCAS(t *testing.T) {
	s := openTest(t)
	e1 := mustInsert(t, s, NewRecord{Agent: "a", Lane: "guidance", Kind: "prefer", Body: "one", Meta: Meta{"repo-id": {"r"}}})
	e2 := mustInsert(t, s, NewRecord{Agent: "a", Lane: "guidance", Kind: "avoid", Body: "two"})
	e3 := mustInsert(t, s, NewRecord{Agent: "a", Lane: "guidance", Kind: "note", Body: "three"})

	if _, err := ActiveGeneration(s.DB, "a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want not found, got %v", err)
	}

	var gen *Generation
	var items []*Record
	err := s.Tx(func(tx *sql.Tx) error {
		var err error
		gen, items, err = InstallGeneration(tx, "a", "",
			[]NewItem{{Key: "k1", Body: "merged", Meta: Meta{"repo-id": {"r"}}, Sources: []string{e1.ID, e2.ID}}},
			[]BriefInput{
				{EntryID: e1.ID, Disposition: "represented", Coverage: "novel", Items: []string{"k1"}},
				{EntryID: e2.ID, Disposition: "represented", Coverage: "unknown", Items: []string{"k1"}, Equivalents: []string{e1.ID}},
				{EntryID: e3.ID, Disposition: "deferred", Coverage: "unknown"},
			})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if gen.Status != "active" || !strings.HasPrefix(gen.ID, "gen_") || len(items) != 1 || !strings.HasPrefix(items[0].ID, "item_") {
		t.Errorf("gen %+v items %+v", gen, items)
	}
	act, err := ActiveGeneration(s.DB, "a")
	if err != nil || act.ID != gen.ID {
		t.Fatalf("active %+v %v", act, err)
	}
	rep, _ := RepresentedEntryIDs(s.DB, gen.ID)
	if !rep[e1.ID] || !rep[e2.ID] || rep[e3.ID] {
		t.Errorf("represented set wrong: %v", rep)
	}
	// source entries stay active (not consumed)
	for _, id := range []string{e1.ID, e2.ID, e3.ID} {
		r, _ := GetRecord(s.DB, id)
		if r.Status != "active" {
			t.Errorf("%s status %s", id, r.Status)
		}
	}
	src, _ := ItemSources(s.DB, gen.ID, items[0].ID)
	if len(src) != 2 {
		t.Errorf("sources %v", src)
	}
	ins, _ := GenerationInputs(s.DB, gen.ID)
	if len(ins) != 3 || len(ins[1].Equivalents) != 1 {
		t.Errorf("inputs %+v", ins)
	}

	// second generation supersedes first; first's items are superseded
	err = s.Tx(func(tx *sql.Tx) error {
		_, _, err := InstallGeneration(tx, "a", gen.ID,
			[]NewItem{{Key: "k1", Body: "merged v2", Sources: []string{e1.ID, e2.ID, e3.ID}}},
			[]BriefInput{
				{EntryID: e1.ID, Disposition: "represented", Coverage: "novel", Items: []string{"k1"}},
				{EntryID: e2.ID, Disposition: "represented", Coverage: "novel", Items: []string{"k1"}},
				{EntryID: e3.ID, Disposition: "represented", Coverage: "novel", Items: []string{"k1"}},
			})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	old, _ := GetGeneration(s.DB, gen.ID)
	if old.Status != "superseded" {
		t.Errorf("old gen status %s", old.Status)
	}
	oldItem, _ := GetRecord(s.DB, items[0].ID)
	if oldItem.Status != "superseded" {
		t.Errorf("old item status %s", oldItem.Status)
	}
	// stale expect fails and writes nothing
	err = s.Tx(func(tx *sql.Tx) error {
		_, _, err := InstallGeneration(tx, "a", gen.ID, nil, nil)
		return err
	})
	if !errors.Is(err, ErrConflict) {
		t.Errorf("want conflict, got %v", err)
	}
	gens, _ := ListGenerations(s.DB, "a")
	if len(gens) != 2 {
		t.Errorf("want 2 generations, got %d", len(gens))
	}
	cov, _ := LatestCoverage(s.DB, "a")
	if cov[e3.ID].Disposition != "represented" {
		t.Errorf("latest coverage for e3: %+v", cov[e3.ID])
	}
}

func TestDroppedBriefItemMakesSourceRecentAgain(t *testing.T) {
	s := openTest(t)
	entry := mustInsert(t, s, NewRecord{Agent: "a", Lane: "guidance", Kind: "prefer", Body: "keep this"})
	var first *Generation
	if err := s.Tx(func(tx *sql.Tx) error {
		var err error
		first, _, err = InstallGeneration(tx, "a", "",
			[]NewItem{{Key: "keep", Body: "Keep this.", Sources: []string{entry.ID}}},
			[]BriefInput{{EntryID: entry.ID, Disposition: "represented", Coverage: "unknown"}})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if recent, err := RecentGuidance(s.DB, "a"); err != nil || len(recent) != 0 {
		t.Fatalf("represented in active generation: recent=%v err=%v", recent, err)
	}
	if err := s.Tx(func(tx *sql.Tx) error {
		_, _, err := InstallGeneration(tx, "a", first.ID, nil, nil)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	recent, err := RecentGuidance(s.DB, "a")
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 1 || recent[0].ID != entry.ID {
		t.Fatalf("source guidance dropped by replacement generation must resurface: %+v", recent)
	}
}

func TestReusedBriefItemCarriesPriorAccounting(t *testing.T) {
	s := openTest(t)
	e1 := mustInsert(t, s, NewRecord{Agent: "a", Lane: "guidance", Kind: "prefer", Body: "one"})
	e2 := mustInsert(t, s, NewRecord{Agent: "a", Lane: "guidance", Kind: "prefer", Body: "two"})
	var first *Generation
	if err := s.Tx(func(tx *sql.Tx) error {
		var err error
		first, _, err = InstallGeneration(tx, "a", "",
			[]NewItem{{Key: "kept", Body: "One.", Sources: []string{e1.ID}}},
			[]BriefInput{{EntryID: e1.ID, Disposition: "represented", Coverage: "unknown"}})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var second *Generation
	var secondItems []*Record
	if err := s.Tx(func(tx *sql.Tx) error {
		var err error
		second, secondItems, err = InstallGeneration(tx, "a", first.ID,
			[]NewItem{{Key: "kept", Body: "One and two.", Sources: []string{e2.ID}}},
			[]BriefInput{{EntryID: e2.ID, Disposition: "represented", Coverage: "unknown"}})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	sources, err := ItemSources(s.DB, second.ID, secondItems[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 2 || sources[0] != e1.ID || sources[1] != e2.ID {
		t.Fatalf("carried sources = %v, want [%s %s]", sources, e1.ID, e2.ID)
	}
	inputs, err := GenerationInputs(s.DB, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs) != 2 || inputs[0].EntryID != e1.ID || inputs[1].EntryID != e2.ID {
		t.Fatalf("carried inputs = %+v", inputs)
	}
	if recent, err := RecentGuidance(s.DB, "a"); err != nil || len(recent) != 0 {
		t.Fatalf("surviving item should keep both sources represented: recent=%v err=%v", recent, err)
	}
}

func TestCurrentAccountingOverridesCarriedItemSources(t *testing.T) {
	s := openTest(t)
	e := mustInsert(t, s, NewRecord{Agent: "a", Lane: "guidance", Kind: "prefer", Body: "source"})
	var g1, g2 *Generation
	var secondItems []*Record
	if err := s.Tx(func(tx *sql.Tx) error {
		var err error
		g1, _, err = InstallGeneration(tx, "a", "", []NewItem{{Key: "kept", Body: "first", Sources: []string{e.ID}}}, []BriefInput{{EntryID: e.ID, Disposition: "represented", Coverage: "novel"}})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Tx(func(tx *sql.Tx) error {
		var err error
		g2, secondItems, err = InstallGeneration(tx, "a", g1.ID, []NewItem{{Key: "kept", Body: "second"}}, []BriefInput{{EntryID: e.ID, Disposition: "deferred", Coverage: "novel"}})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	sources, err := ItemSources(s.DB, g2.ID, secondItems[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 0 {
		t.Fatalf("explicit deferred accounting retained old item sources: %v", sources)
	}
	inputs, err := GenerationInputs(s.DB, g2.ID)
	if err != nil || len(inputs) != 1 || inputs[0].Disposition != "deferred" || len(inputs[0].Items) != 0 {
		t.Fatalf("current accounting was not authoritative: %+v err=%v", inputs, err)
	}
	recent, err := RecentGuidance(s.DB, "a")
	if err != nil || len(recent) != 1 || recent[0].ID != e.ID {
		t.Fatalf("deferred source must be recent: %+v err=%v", recent, err)
	}
}

func TestSignalLifecycle(t *testing.T) {
	s := openTest(t)
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	future := now.Add(time.Hour)

	var sig *Signal
	err := s.Tx(func(tx *sql.Tx) error {
		var err error
		sig, _, err = CreateSignal(tx, "a", "body", Meta{"subject": {"Recheck"}}, future, "k1", "")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	// dedupe
	var dup bool
	var sig2 *Signal
	_ = s.Tx(func(tx *sql.Tx) error {
		var err error
		sig2, dup, err = CreateSignal(tx, "a", "other body", nil, now, "k1", "")
		return err
	})
	if !dup || sig2.Record.ID != sig.Record.ID {
		t.Errorf("dedupe failed: dup=%v id=%s", dup, sig2.Record.ID)
	}
	// not due yet
	due, _ := DueSignals(s.DB, "a", now)
	if len(due) != 0 {
		t.Errorf("should not be due: %d", len(due))
	}
	due, _ = DueSignals(s.DB, "a", future)
	if len(due) != 1 || due[0].Delivery.State != "pending" {
		t.Fatalf("should be due pending: %+v", due)
	}
	// claim
	var claimed []*Signal
	_ = s.Tx(func(tx *sql.Tx) error {
		var err error
		claimed, err = ClaimDue(tx, "", future, 5*time.Minute)
		return err
	})
	if len(claimed) != 1 || claimed[0].Delivery.State != "leased" || claimed[0].Delivery.LeaseToken == "" {
		t.Fatalf("claim: %+v", claimed)
	}
	token := claimed[0].Delivery.LeaseToken
	// second claim while leased gets nothing
	_ = s.Tx(func(tx *sql.Tx) error {
		var err error
		claimed, err = ClaimDue(tx, "", future.Add(time.Minute), 5*time.Minute)
		return err
	})
	if len(claimed) != 0 {
		t.Errorf("should not re-claim a live lease")
	}
	// wrong token
	err = s.Tx(func(tx *sql.Tx) error { return AckSignal(tx, sig.Record.ID, "lease_bogus", future.Add(time.Minute)) })
	if !errors.Is(err, ErrConflict) {
		t.Errorf("want conflict, got %v", err)
	}
	// lease expiry → pending again, claimable
	later := future.Add(10 * time.Minute)
	due, _ = DueSignals(s.DB, "a", later)
	if len(due) != 1 || due[0].Delivery.State != "pending" {
		t.Errorf("expired lease should read as pending: %+v", due[0].Delivery)
	}
	pending, err := PendingSignals(s.DB, "a", later)
	if err != nil || len(pending) != 1 || pending[0].Delivery.State != "pending" || pending[0].Delivery.LeaseToken != "" || pending[0].Delivery.LeasedUntil != "" {
		t.Errorf("inspect view of expired lease should be clean pending state: %+v err=%v", pending, err)
	}
	// ack with the old token after expiry fails
	err = s.Tx(func(tx *sql.Tx) error { return AckSignal(tx, sig.Record.ID, token, later) })
	if !errors.Is(err, ErrConflict) {
		t.Errorf("expired lease ack should conflict, got %v", err)
	}
	// reclaim and ack
	_ = s.Tx(func(tx *sql.Tx) error {
		var err error
		claimed, err = ClaimDue(tx, "a", later, 5*time.Minute)
		return err
	})
	if len(claimed) != 1 {
		t.Fatalf("reclaim failed")
	}
	if err := s.Tx(func(tx *sql.Tx) error { return AckSignal(tx, sig.Record.ID, claimed[0].Delivery.LeaseToken, later) }); err != nil {
		t.Fatal(err)
	}
	due, _ = DueSignals(s.DB, "a", later.Add(time.Hour))
	if len(due) != 0 {
		t.Errorf("acknowledged should not be due")
	}
	// dedupe key is free again after ack
	_ = s.Tx(func(tx *sql.Tx) error {
		var err error
		sig2, dup, err = CreateSignal(tx, "a", "again", nil, now, "k1", "")
		return err
	})
	if dup {
		t.Errorf("acknowledged signal should not block dedupe key")
	}
}

func TestDueSignalsReportsOrphanWithoutDiscardingHealthySignals(t *testing.T) {
	s := openTest(t)
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	var healthy, orphaned *Signal
	if err := s.Tx(func(tx *sql.Tx) error {
		var err error
		healthy, _, err = CreateSignal(tx, "a", "healthy", Meta{"subject": {"Healthy"}}, now, "", "")
		if err != nil {
			return err
		}
		orphaned, _, err = CreateSignal(tx, "a", "orphaned", Meta{"subject": {"Orphaned"}}, now, "", "")
		if err != nil {
			return err
		}
		_, err = tx.Exec(`DELETE FROM records WHERE id = ?`, orphaned.Record.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	due, err := DueSignals(s.DB, "a", now)
	var orphanErr *OrphanedSignalRecordsError
	if !errors.As(err, &orphanErr) || !errors.Is(err, ErrNotFound) {
		t.Fatalf("DueSignals error = %v, want OrphanedSignalRecordsError wrapping ErrNotFound", err)
	}
	if len(orphanErr.RecordIDs) != 1 || orphanErr.RecordIDs[0] != orphaned.Record.ID {
		t.Errorf("orphan IDs = %v, want [%s]", orphanErr.RecordIDs, orphaned.Record.ID)
	}
	if len(due) != 1 || due[0].Record.ID != healthy.Record.ID {
		t.Errorf("healthy due signals = %+v, want only %s", due, healthy.Record.ID)
	}
	if _, err := GetDelivery(s.DB, orphaned.Record.ID); err != nil {
		t.Errorf("DueSignals mutated the orphaned delivery: %v", err)
	}
}

func TestListContextsUsesTimestampRecencyThenRowID(t *testing.T) {
	s := openTest(t)
	oldClock := Clock
	t.Cleanup(func() { Clock = oldClock })
	makeContext := func(at time.Time) *Context {
		Clock = func() time.Time { return at }
		var c *Context
		if err := s.Tx(func(tx *sql.Tx) error {
			var err error
			c, err = CreateContext(tx, "a", "", "", 100, Meta{}, nil)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return c
	}
	newer := makeContext(time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC))
	olderButInsertedLast := makeContext(time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC))
	got, err := ListContexts(s.DB, "a", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != newer.ID || got[1].ID != olderButInsertedLast.ID {
		t.Fatalf("contexts are not timestamp-recency ordered: %+v", got)
	}
}

func TestContextsAndGC(t *testing.T) {
	s := openTest(t)
	var ctx *Context
	err := s.Tx(func(tx *sql.Tx) error {
		var err error
		ctx, err = CreateContext(tx, "a", "", "task", 1000, Meta{"repo-id": {"r"}}, []ContextRecord{{RecordID: "base_1", Section: "base", Ordinal: 0}})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := GetContext(s.DB, ctx.ID)
	if err != nil || got.Task != "task" || got.Meta.First("repo-id") != "r" || len(got.Rendered) != 1 {
		t.Fatalf("got %+v err %v", got, err)
	}
	listed, err := ListContexts(s.DB, "a", 10)
	if err != nil || len(listed) != 1 || len(listed[0].Rendered) != 1 || listed[0].Rendered[0].RecordID != "base_1" {
		t.Fatalf("listed receipt lost rendered records: %+v err %v", listed, err)
	}
	// a record referencing the context protects it from GC
	mustInsert(t, s, NewRecord{Agent: "a", Lane: "guidance", Kind: "note", Body: "x", OriginContext: ctx.ID})
	var orphan *Context
	_ = s.Tx(func(tx *sql.Tx) error {
		var err error
		orphan, err = CreateContext(tx, "a", ctx.ID, "", 1000, Meta{}, nil)
		return err
	})
	deleted, err := GCContexts(s, time.Now().Add(time.Hour), true)
	if err != nil || len(deleted) != 1 || deleted[0] != orphan.ID {
		t.Fatalf("dry run: %v %v", deleted, err)
	}
	if _, err := GetContext(s.DB, orphan.ID); err != nil {
		t.Errorf("dry run must not delete")
	}
	if err := PinContext(s.DB, orphan.ID, true); err != nil {
		t.Fatal(err)
	}
	deleted, _ = GCContexts(s, time.Now().Add(time.Hour), false)
	if len(deleted) != 0 {
		t.Errorf("pinned context deleted: %v", deleted)
	}
	_ = PinContext(s.DB, orphan.ID, false)
	deleted, _ = GCContexts(s, time.Now().Add(time.Hour), false)
	if len(deleted) != 1 {
		t.Errorf("want 1 deleted, got %v", deleted)
	}
	if _, err := GetContext(s.DB, ctx.ID); err != nil {
		t.Errorf("referenced context must survive: %v", err)
	}
}
