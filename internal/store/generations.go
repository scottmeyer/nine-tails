package store

import (
	"database/sql"
	"errors"
	"fmt"
)

// Generation is one brief generation (spec §12): a cache of compiled items.
type Generation struct {
	ID        string `json:"id" yaml:"id"`
	Agent     string `json:"agent" yaml:"agent"`
	Parent    string `json:"parent,omitempty" yaml:"parent,omitempty"`
	CreatedAt string `json:"created_at" yaml:"created_at"`
	Status    string `json:"status" yaml:"status"` // staged | active | superseded
}

// BriefInput is the accounting row for one input entry of a generation.
type BriefInput struct {
	EntryID     string   `json:"id" yaml:"id"`
	Disposition string   `json:"disposition" yaml:"disposition"` // represented | deferred | superseded-by
	Coverage    string   `json:"coverage" yaml:"coverage"`
	Successor   string   `json:"successor,omitempty" yaml:"successor,omitempty"`
	Items       []string `json:"items,omitempty" yaml:"items,omitempty"` // item record IDs
	Equivalents []string `json:"equivalent_records,omitempty" yaml:"equivalent_records,omitempty"`
}

// ActiveGeneration returns the active generation for agent, or ErrNotFound.
func ActiveGeneration(q Querier, agent string) (*Generation, error) {
	g := &Generation{}
	err := q.QueryRow(`SELECT id, agent, COALESCE(parent_id,''), created_at, status FROM brief_generations WHERE agent = ? AND status = 'active'`, agent).
		Scan(&g.ID, &g.Agent, &g.Parent, &g.CreatedAt, &g.Status)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: no active brief generation for %s", ErrNotFound, agent)
	}
	if err != nil {
		return nil, err
	}
	return g, nil
}

// GetGeneration loads a generation by ID.
func GetGeneration(q Querier, id string) (*Generation, error) {
	g := &Generation{}
	err := q.QueryRow(`SELECT id, agent, COALESCE(parent_id,''), created_at, status FROM brief_generations WHERE id = ?`, id).
		Scan(&g.ID, &g.Agent, &g.Parent, &g.CreatedAt, &g.Status)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: generation %s", ErrNotFound, id)
	}
	return g, err
}

// GenerationItems returns the item records of a generation in ordinal order
// (any status, since old generations keep superseded items).
func GenerationItems(q Querier, genID string) ([]*Record, error) {
	rows, err := q.Query(`SELECT `+recordColsPrefixed("r")+` FROM brief_generation_items gi
		JOIN records r ON r.id = gi.record_id WHERE gi.generation_id = ? ORDER BY gi.ordinal`, genID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Record
	for rows.Next() {
		r, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, loadMeta(q, out)
}

func recordColsPrefixed(p string) string {
	return p + `.id, ` + p + `.agent, ` + p + `.lane, ` + p + `.kind, COALESCE(` + p + `.name, ''), ` + p + `.body, ` + p + `.created_at, COALESCE(` + p + `.origin_context_id, ''), ` + p + `.status, COALESCE(` + p + `.supersedes_id, '')`
}

// GenerationInputs returns the accounting rows for a generation.
func GenerationInputs(q Querier, genID string) ([]BriefInput, error) {
	rows, err := q.Query(`SELECT entry_record_id, disposition, coverage, COALESCE(successor_record_id,'') FROM brief_inputs WHERE generation_id = ? ORDER BY rowid`, genID)
	if err != nil {
		return nil, err
	}
	var out []BriefInput
	for rows.Next() {
		var bi BriefInput
		if err := rows.Scan(&bi.EntryID, &bi.Disposition, &bi.Coverage, &bi.Successor); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, bi)
	}
	rows.Close()
	for i := range out {
		items, err := queryStrings(q, `SELECT item_record_id FROM brief_item_sources WHERE generation_id = ? AND entry_record_id = ? ORDER BY rowid`, genID, out[i].EntryID)
		if err != nil {
			return nil, err
		}
		out[i].Items = items
		eq, err := queryStrings(q, `SELECT equivalent_record_id FROM brief_equivalents WHERE generation_id = ? AND entry_record_id = ? ORDER BY rowid`, genID, out[i].EntryID)
		if err != nil {
			return nil, err
		}
		out[i].Equivalents = eq
	}
	return out, nil
}

// ItemSources returns the source entry IDs for one item in a generation.
func ItemSources(q Querier, genID, itemID string) ([]string, error) {
	return queryStrings(q, `SELECT entry_record_id FROM brief_item_sources WHERE generation_id = ? AND item_record_id = ? ORDER BY rowid`, genID, itemID)
}

// RepresentedEntryIDs returns the set of entry IDs the generation accounts for
// as represented or superseded-by. Deferred entries are NOT included, so they
// keep rendering as recent (spec §11.2).
func RepresentedEntryIDs(q Querier, genID string) (map[string]bool, error) {
	ids, err := queryStrings(q, `SELECT entry_record_id FROM brief_inputs WHERE generation_id = ? AND disposition IN ('represented','superseded-by')`, genID)
	if err != nil {
		return nil, err
	}
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	return set, nil
}

// NewItem is one compiled brief item to install.
type NewItem struct {
	Key     string
	Body    string
	Meta    Meta
	Sources []string // entry record IDs this item represents
}

// InstallGeneration atomically installs a new generation for agent
// (spec §12.4). The caller has already validated the compiler output and
// verified the CAS preconditions inside the same transaction. expectGen is
// the generation being superseded ("" when none). Returns the new generation
// and the created item records in order.
func InstallGeneration(tx Querier, agent, expectGen string, items []NewItem, inputs []BriefInput) (*Generation, []*Record, error) {
	return installGeneration(tx, agent, expectGen, items, inputs, true)
}

// InvalidateGenerationForGuidance installs an empty successor when entryID is
// evidence for the agent's active brief generation. This makes every surviving
// active guidance entry recent again, so disabling one source cannot leave its
// compiled meaning in capsules or compiler input. A superseded-by successor is
// evidence too, including its latest record successor. When the active
// generation does not depend on entryID, this is a no-op.
func InvalidateGenerationForGuidance(tx Querier, agent, entryID string) (*Generation, error) {
	gen, err := ActiveGeneration(tx, agent)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var sourced bool
	if err := tx.QueryRow(`SELECT EXISTS(
		SELECT 1 FROM brief_item_sources src
		JOIN brief_generation_items item
		  ON item.generation_id = src.generation_id AND item.record_id = src.item_record_id
		WHERE src.generation_id = ? AND src.entry_record_id = ?
	)`, gen.ID, entryID).Scan(&sourced); err != nil {
		return nil, err
	}
	if !sourced {
		rows, err := tx.Query(`SELECT successor_record_id FROM brief_inputs
			WHERE generation_id = ? AND disposition = 'superseded-by'
			AND successor_record_id IS NOT NULL`, gen.ID)
		if err != nil {
			return nil, err
		}
		var successors []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			successors = append(successors, id)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
		for _, id := range successors {
			current, err := LatestSuccessor(tx, id)
			if err != nil {
				return nil, err
			}
			if current == entryID {
				sourced = true
				break
			}
		}
	}
	if !sourced {
		return nil, nil
	}
	invalidated, _, err := installGeneration(tx, agent, gen.ID, nil, nil, false)
	return invalidated, err
}

// installGeneration optionally carries prior accounting. Ordinary compiler
// generations do; an empty generation installed to invalidate retired source
// material must not, or superseded-by rows would continue hiding live entries.
func installGeneration(tx Querier, agent, expectGen string, items []NewItem, inputs []BriefInput, carryPrior bool) (*Generation, []*Record, error) {
	genID, err := NewID("gen")
	if err != nil {
		return nil, nil, err
	}
	now := Now()
	if _, err := tx.Exec(`INSERT INTO brief_generations(id, agent, parent_id, created_at, status) VALUES (?, ?, ?, ?, 'staged')`,
		genID, agent, nullable(expectGen), now); err != nil {
		return nil, nil, err
	}
	// Supersede prior generation's items first so the unique name index is
	// free; remember their keys so a re-emitted key links via supersedes.
	priorByKey := map[string]string{}
	priorKeyByID := map[string]string{}
	priorSourcesByKey := map[string][]string{}
	var priorInputs []BriefInput
	if expectGen != "" {
		prior, err := GenerationItems(tx, expectGen)
		if err != nil {
			return nil, nil, err
		}
		for _, p := range prior {
			priorByKey[p.Name] = p.ID
			priorKeyByID[p.ID] = p.Name
			sources, err := ItemSources(tx, expectGen, p.ID)
			if err != nil {
				return nil, nil, err
			}
			priorSourcesByKey[p.Name] = sources
		}
		if carryPrior {
			priorInputs, err = GenerationInputs(tx, expectGen)
			if err != nil {
				return nil, nil, err
			}
		}
		if _, err := tx.Exec(`UPDATE records SET status = 'superseded' WHERE id IN (SELECT record_id FROM brief_generation_items WHERE generation_id = ?) AND status = 'active'`, expectGen); err != nil {
			return nil, nil, err
		}
	}
	currentInputs := make(map[string]bool, len(inputs))
	for _, in := range inputs {
		currentInputs[in.EntryID] = true
	}
	keyToID := map[string]string{}
	var recs []*Record
	for i, it := range items {
		// An orphan brief item with the same key (e.g. imported outside any
		// generation) would collide on the unique name index: supersede it
		// and link to it, exactly like a prior-generation item.
		prev := priorByKey[it.Key]
		if prev == "" {
			if orphan, err := ActiveNamed(tx, agent, "guidance", "brief-item", it.Key); err == nil {
				prev = orphan.ID
				if err := SetStatus(tx, orphan.ID, "superseded"); err != nil {
					return nil, nil, err
				}
			} else if !errors.Is(err, ErrNotFound) {
				return nil, nil, err
			}
		}
		rec, err := InsertRecord(tx, NewRecord{Agent: agent, Lane: "guidance", Kind: "brief-item", Name: it.Key, Body: it.Body, Meta: it.Meta})
		if err != nil {
			return nil, nil, err
		}
		if prev != "" {
			if _, err := tx.Exec(`UPDATE records SET supersedes_id = ? WHERE id = ?`, prev, rec.ID); err != nil {
				return nil, nil, err
			}
			rec.Supersedes = prev
		}
		keyToID[it.Key] = rec.ID
		recs = append(recs, rec)
		if _, err := tx.Exec(`INSERT INTO brief_generation_items(generation_id, record_id, ordinal) VALUES (?, ?, ?)`, genID, rec.ID, i); err != nil {
			return nil, nil, err
		}
		// Reusing a key says the prior item survives this replacement
		// generation. Carry its source relationships mechanically; otherwise a
		// later compile would forget why the item exists and its still-active
		// source entries would incorrectly reappear as recent guidance.
		var sources []string
		for _, src := range priorSourcesByKey[it.Key] {
			if !currentInputs[src] {
				sources = append(sources, src)
			}
		}
		sources = append(sources, it.Sources...)
		for _, src := range sources {
			if _, err := tx.Exec(`INSERT OR IGNORE INTO brief_item_sources(generation_id, item_record_id, entry_record_id) VALUES (?, ?, ?)`, genID, rec.ID, src); err != nil {
				return nil, nil, err
			}
		}
	}
	// Carry old accounting only when its representation still exists. A
	// represented entry whose every item key was dropped is intentionally not
	// copied, so RecentGuidance makes that source visible again. An explicit
	// superseded-by disposition remains effective across cache generations.
	for _, in := range priorInputs {
		if currentInputs[in.EntryID] || in.Disposition == "deferred" {
			continue
		}
		carry := in.Disposition == "superseded-by"
		if in.Disposition == "represented" {
			for _, oldItemID := range in.Items {
				if _, ok := keyToID[priorKeyByID[oldItemID]]; ok {
					carry = true
					break
				}
			}
		}
		if !carry {
			continue
		}
		if _, err := tx.Exec(`INSERT INTO brief_inputs(generation_id, entry_record_id, disposition, coverage, successor_record_id) VALUES (?, ?, ?, ?, ?)`,
			genID, in.EntryID, in.Disposition, in.Coverage, nullable(in.Successor)); err != nil {
			return nil, nil, err
		}
		for _, eq := range in.Equivalents {
			if _, err := tx.Exec(`INSERT OR IGNORE INTO brief_equivalents(generation_id, entry_record_id, equivalent_record_id) VALUES (?, ?, ?)`, genID, in.EntryID, eq); err != nil {
				return nil, nil, err
			}
		}
	}
	for _, in := range inputs {
		if _, err := tx.Exec(`INSERT INTO brief_inputs(generation_id, entry_record_id, disposition, coverage, successor_record_id) VALUES (?, ?, ?, ?, ?)`,
			genID, in.EntryID, in.Disposition, in.Coverage, nullable(in.Successor)); err != nil {
			return nil, nil, err
		}
		for _, eq := range in.Equivalents {
			if _, err := tx.Exec(`INSERT OR IGNORE INTO brief_equivalents(generation_id, entry_record_id, equivalent_record_id) VALUES (?, ?, ?)`, genID, in.EntryID, eq); err != nil {
				return nil, nil, err
			}
		}
	}
	if expectGen != "" {
		res, err := tx.Exec(`UPDATE brief_generations SET status = 'superseded' WHERE id = ? AND status = 'active'`, expectGen)
		if err != nil {
			return nil, nil, err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return nil, nil, fmt.Errorf("%w: generation %s is no longer active", ErrConflict, expectGen)
		}
	}
	if _, err := tx.Exec(`UPDATE brief_generations SET status = 'active' WHERE id = ?`, genID); err != nil {
		return nil, nil, err
	}
	return &Generation{ID: genID, Agent: agent, Parent: expectGen, CreatedAt: now, Status: "active"}, recs, nil
}

// ListGenerations returns all generations for an agent, oldest first.
func ListGenerations(q Querier, agent string) ([]*Generation, error) {
	rows, err := q.Query(`SELECT id, agent, COALESCE(parent_id,''), created_at, status FROM brief_generations WHERE agent = ? ORDER BY rowid`, agent)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Generation
	for rows.Next() {
		g := &Generation{}
		if err := rows.Scan(&g.ID, &g.Agent, &g.Parent, &g.CreatedAt, &g.Status); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// LatestCoverage returns, for each guidance entry of the agent that has been
// accounted for by any generation, its accounting row (with item and
// equivalent ids) from the newest generation that accounted for it.
func LatestCoverage(q Querier, agent string) (map[string]BriefInput, error) {
	rows, err := q.Query(`SELECT bi.generation_id, bi.entry_record_id, bi.disposition, bi.coverage, COALESCE(bi.successor_record_id,'')
		FROM brief_inputs bi JOIN brief_generations g ON g.id = bi.generation_id
		WHERE g.agent = ? ORDER BY g.created_at, g.rowid`, agent)
	if err != nil {
		return nil, err
	}
	type row struct {
		gen string
		bi  BriefInput
	}
	var all []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.gen, &r.bi.EntryID, &r.bi.Disposition, &r.bi.Coverage, &r.bi.Successor); err != nil {
			rows.Close()
			return nil, err
		}
		all = append(all, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	latest := map[string]row{}
	for _, r := range all {
		latest[r.bi.EntryID] = r // later generations overwrite earlier
	}
	out := make(map[string]BriefInput, len(latest))
	for id, r := range latest {
		items, err := queryStrings(q, `SELECT item_record_id FROM brief_item_sources WHERE generation_id = ? AND entry_record_id = ? ORDER BY rowid`, r.gen, id)
		if err != nil {
			return nil, err
		}
		eq, err := queryStrings(q, `SELECT equivalent_record_id FROM brief_equivalents WHERE generation_id = ? AND entry_record_id = ? ORDER BY rowid`, r.gen, id)
		if err != nil {
			return nil, err
		}
		r.bi.Items, r.bi.Equivalents = items, eq
		out[id] = r.bi
	}
	return out, nil
}

func queryStrings(q Querier, query string, args ...any) ([]string, error) {
	rows, err := q.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
