package store

// MarkWeight decodes historical mark strength for inspection only.
func MarkWeight(m string) int {
	switch m {
	case "+", "+++", "+++++":
		return len(m)
	case "-", "---", "-----":
		return -len(m)
	}
	return 0
}

// Tally summarizes legacy feedback retained for historical inspection. Renders
// includes all receipts; Closes counts only old receipts with a stored mark.
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
