package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Context is an immutable receipt of one load (spec §9).
type Context struct {
	ID        string `json:"context_id" yaml:"context_id"`
	Agent     string `json:"agent" yaml:"agent"`
	Parent    string `json:"parent_context,omitempty" yaml:"parent_context,omitempty"`
	Task      string `json:"task,omitempty" yaml:"task,omitempty"`
	Budget    int    `json:"budget" yaml:"budget"`
	CreatedAt string `json:"created_at" yaml:"created_at"`
	Pinned    bool   `json:"pinned" yaml:"pinned"`
	Meta      Meta   `json:"metadata" yaml:"metadata"`
	// Rendered lists emitted records in render order.
	Rendered []ContextRecord `json:"rendered" yaml:"rendered"`
}

// ContextRecord is one emitted record in a receipt.
type ContextRecord struct {
	RecordID string `json:"id" yaml:"id"`
	Section  string `json:"section" yaml:"section"`
	Ordinal  int    `json:"ordinal" yaml:"ordinal"`
}

// RenderedIDs returns just the record IDs in order.
func (c *Context) RenderedIDs() []string {
	out := make([]string, len(c.Rendered))
	for i, r := range c.Rendered {
		out[i] = r.RecordID
	}
	return out
}

// CreateContext persists a receipt. meta must already be fully resolved
// (parent's metadata merged with the explicit metadata). Must run inside Tx.
func CreateContext(tx Querier, agent, parent, task string, budget int, meta Meta, rendered []ContextRecord) (*Context, error) {
	if err := ValidateMeta(meta); err != nil {
		return nil, err
	}
	id, err := NextID(tx, "ctx")
	if err != nil {
		return nil, err
	}
	if err := CreateContextWithID(tx, id, agent, parent, task, budget, meta, rendered); err != nil {
		return nil, err
	}
	if rendered == nil {
		rendered = []ContextRecord{}
	}
	return &Context{ID: id, Agent: agent, Parent: parent, Task: task, Budget: budget, CreatedAt: Now(), Meta: meta.Clone(), Rendered: rendered}, nil
}

// CreateContextWithID persists a receipt under an ID the caller already
// allocated with NextID (so the capsule header can carry it before render).
func CreateContextWithID(tx Querier, id, agent, parent, task string, budget int, meta Meta, rendered []ContextRecord) error {
	if err := ValidateMeta(meta); err != nil {
		return err
	}
	now := Now()
	if _, err := tx.Exec(`INSERT INTO contexts(id, agent, parent_context_id, task, token_budget, created_at, pinned)
		VALUES (?, ?, ?, ?, ?, ?, 0)`, id, agent, nullable(parent), nullable(task), budget, now); err != nil {
		return err
	}
	for _, k := range SortedKeys(meta) {
		for _, v := range meta[k] {
			if _, err := tx.Exec(`INSERT INTO context_metadata(context_id, key, value) VALUES (?, ?, ?)`, id, k, v); err != nil {
				return err
			}
		}
	}
	for _, r := range rendered {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO context_records(context_id, record_id, section, ordinal) VALUES (?, ?, ?, ?)`,
			id, r.RecordID, r.Section, r.Ordinal); err != nil {
			return err
		}
	}
	return nil
}

// GetContext loads a receipt with its metadata and rendered records.
func GetContext(q Querier, id string) (*Context, error) {
	c := &Context{Meta: Meta{}, Rendered: []ContextRecord{}}
	var pinned int
	err := q.QueryRow(`SELECT id, agent, COALESCE(parent_context_id,''), COALESCE(task,''), token_budget, created_at, pinned FROM contexts WHERE id = ?`, id).
		Scan(&c.ID, &c.Agent, &c.Parent, &c.Task, &c.Budget, &c.CreatedAt, &pinned)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: context %s", ErrNotFound, id)
	}
	if err != nil {
		return nil, err
	}
	c.Pinned = pinned != 0
	rows, err := q.Query(`SELECT key, value FROM context_metadata WHERE context_id = ? ORDER BY rowid`, id)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			rows.Close()
			return nil, err
		}
		c.Meta[k] = append(c.Meta[k], v)
	}
	rows.Close()
	rows, err = q.Query(`SELECT record_id, section, ordinal FROM context_records WHERE context_id = ? ORDER BY ordinal`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var cr ContextRecord
		if err := rows.Scan(&cr.RecordID, &cr.Section, &cr.Ordinal); err != nil {
			return nil, err
		}
		c.Rendered = append(c.Rendered, cr)
	}
	return c, rows.Err()
}

// ContextRenderedSet returns the set of record IDs a context emitted.
func ContextRenderedSet(q Querier, id string) (map[string]bool, error) {
	rows, err := q.Query(`SELECT record_id FROM context_records WHERE context_id = ?`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	set := map[string]bool{}
	for rows.Next() {
		var rid string
		if err := rows.Scan(&rid); err != nil {
			return nil, err
		}
		set[rid] = true
	}
	return set, rows.Err()
}

// ListContexts returns complete receipts newest first, optionally filtered by
// agent. A receipt's rendered set is part of its immutable audit record and
// must not disappear merely because it was reached through a list view.
func ListContexts(q Querier, agent string, limit int) ([]*Context, error) {
	query := `SELECT id FROM contexts`
	var args []any
	if agent != "" {
		query += ` WHERE agent = ?`
		args = append(args, agent)
	}
	query += ` ORDER BY created_at DESC, rowid DESC`
	if limit > 0 {
		query += fmt.Sprintf(` LIMIT %d`, limit)
	}
	rows, err := q.Query(query, args...)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]*Context, 0, len(ids))
	for _, id := range ids {
		c, err := GetContext(q, id)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// ContextsRendering returns IDs of contexts that emitted the given record.
func ContextsRendering(q Querier, recordID string) ([]string, error) {
	rows, err := q.Query(`SELECT context_id FROM context_records WHERE record_id = ? ORDER BY rowid`, recordID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// PinContext sets or clears the pinned flag.
func PinContext(q Querier, id string, pinned bool) error {
	p := 0
	if pinned {
		p = 1
	}
	res, err := q.Exec(`UPDATE contexts SET pinned = ? WHERE id = ?`, p, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("%w: context %s", ErrNotFound, id)
	}
	return nil
}

// GCContexts deletes unpinned contexts created before cutoff that no active
// record references as origin (spec §9.2). Returns the deleted IDs. With
// dryRun it only reports. Never touches records or artifacts.
func GCContexts(s *Store, cutoff time.Time, dryRun bool) ([]string, error) {
	var ids []string
	err := s.Tx(func(tx *sql.Tx) error {
		rows, err := tx.Query(`SELECT id FROM contexts c
			WHERE pinned = 0 AND created_at < ?
			AND NOT EXISTS (SELECT 1 FROM records r WHERE r.origin_context_id = c.id AND r.status = 'active')
			ORDER BY rowid`, FormatTime(cutoff))
		if err != nil {
			return err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		rows.Close()
		if dryRun || len(ids) == 0 {
			return nil
		}
		ph := strings.Repeat("?,", len(ids))
		ph = ph[:len(ph)-1]
		args := make([]any, len(ids))
		for i, id := range ids {
			args[i] = id
		}
		for _, stmt := range []string{
			`DELETE FROM context_records WHERE context_id IN (` + ph + `)`,
			`DELETE FROM context_metadata WHERE context_id IN (` + ph + `)`,
			`DELETE FROM contexts WHERE id IN (` + ph + `)`,
		} {
			if _, err := tx.Exec(stmt, args...); err != nil {
				return err
			}
		}
		return nil
	})
	return ids, err
}
