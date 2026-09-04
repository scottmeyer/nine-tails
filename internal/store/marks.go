package store

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
)

// Marks are what a run says each rendered record was worth (DESIGN §18):
// + +++ +++++ it applied (nudged, shaped, decisive); - --- ----- it
// hindered (detour, misled, caused a mistake); X it is wrong as a statement
// and the run wrote the correction; ? it never came up, the default.
var validMarks = map[string]bool{"+": true, "+++": true, "+++++": true, "-": true, "---": true, "-----": true, "X": true, "?": true}

// ValidMark reports whether s is one of the eight marks.
func ValidMark(s string) bool { return validMarks[s] }

// MarkWeight is the strength of a mark: +1, +3, +5 for applied, -1, -3, -5
// for hindered, 0 for ? and X.
func MarkWeight(m string) int {
	switch m {
	case "+", "+++", "+++++":
		return len(m)
	case "-", "---", "-----":
		return -len(m)
	}
	return 0
}

// CloseContext closes receipt id with marks keyed by rendered record id.
// Every rendered record gets exactly one mark; unlisted ones get ?. A
// receipt closes once. An X needs a guidance record written against this
// context first, so a kill always carries its correction.
func CloseContext(tx Querier, id string, marks map[string]string) (*Context, error) {
	c, err := GetContext(tx, id)
	if err != nil {
		return nil, err
	}
	if c.ClosedAt != "" {
		return nil, fmt.Errorf("%w: %s was closed at %s", ErrConflict, id, c.ClosedAt)
	}
	rendered := map[string]bool{}
	for _, r := range c.Rendered {
		rendered[r.RecordID] = true
	}
	for rid, m := range marks {
		if !rendered[rid] {
			return nil, fmt.Errorf("%w: %s did not render %s", ErrInvalid, id, rid)
		}
		if !ValidMark(m) {
			return nil, fmt.Errorf("%w: mark %q for %s is not one of + +++ +++++ - --- ----- X ?", ErrInvalid, m, rid)
		}
		if m == "X" {
			var one int
			err := tx.QueryRow(`SELECT 1 FROM records WHERE origin_context_id = ? AND lane = 'guidance' LIMIT 1`, id).Scan(&one)
			if errors.Is(err, sql.ErrNoRows) {
				return nil, fmt.Errorf("%w: X on %s needs its correction first: nine-tails avoid|note --context %s \"...\"", ErrInvalid, rid, id)
			}
			if err != nil {
				return nil, err
			}
		}
	}
	now := Now()
	c.Marks = map[string]string{}
	for _, r := range c.Rendered {
		m := marks[r.RecordID]
		if m == "" {
			m = "?"
		}
		if _, err := tx.Exec(`INSERT INTO context_marks(context_id, record_id, mark, created_at) VALUES (?, ?, ?, ?)`, id, r.RecordID, m, now); err != nil {
			return nil, err
		}
		c.Marks[r.RecordID] = m
	}
	if _, err := tx.Exec(`UPDATE contexts SET closed_at = ? WHERE id = ?`, now, id); err != nil {
		return nil, err
	}
	c.ClosedAt = now
	return c, nil
}

// Tally is what practice says about one record: how many receipts rendered
// it, how many of those closed, and how the closes marked it.
type Tally struct {
	Renders     int    `json:"renders" yaml:"renders"`
	Closes      int    `json:"closes" yaml:"closes"`
	Plus        int    `json:"plus" yaml:"plus"`
	Minus       int    `json:"minus" yaml:"minus"`
	Unknown     int    `json:"unknown" yaml:"unknown"`
	Wrong       int    `json:"wrong" yaml:"wrong"`
	PlusWeight  int    `json:"plus_weight" yaml:"plus_weight"`
	MinusWeight int    `json:"minus_weight" yaml:"minus_weight"`
	LastApplied string `json:"last_applied,omitempty" yaml:"last_applied,omitempty"`
}

// TallyRecord computes the tally for one record.
func TallyRecord(q Querier, recordID string) (*Tally, error) {
	t := &Tally{}
	if err := q.QueryRow(`SELECT COUNT(*) FROM context_records WHERE record_id = ?`, recordID).Scan(&t.Renders); err != nil {
		return nil, err
	}
	rows, err := q.Query(`SELECT mark, created_at FROM context_marks WHERE record_id = ? ORDER BY created_at, rowid`, recordID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var m, at string
		if err := rows.Scan(&m, &at); err != nil {
			return nil, err
		}
		t.Closes++
		switch w := MarkWeight(m); {
		case w > 0:
			t.Plus++
			t.PlusWeight += w
			t.LastApplied = at
		case w < 0:
			t.Minus++
			t.MinusWeight -= w
		case m == "X":
			t.Wrong++
		default:
			t.Unknown++
		}
	}
	return t, rows.Err()
}

// ContextsMarking returns the receipts that gave recordID the given mark,
// oldest first.
func ContextsMarking(q Querier, recordID, mark string) ([]string, error) {
	return queryStrings(q, `SELECT context_id FROM context_marks WHERE record_id = ? AND mark = ? ORDER BY created_at, rowid`, recordID, mark)
}

// ContextsHindering returns the receipts that gave recordID any hindered
// mark, oldest first.
func ContextsHindering(q Querier, recordID string) ([]string, error) {
	ids, err := queryStrings(q, `SELECT context_id FROM context_marks WHERE record_id = ? AND mark IN ('-', '---', '-----') ORDER BY created_at, rowid`, recordID)
	if err != nil {
		return nil, err
	}
	sort.Strings(ids)
	return ids, nil
}
