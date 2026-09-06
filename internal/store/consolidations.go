package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

const consolidationSchema = `
CREATE TABLE IF NOT EXISTS consolidations (
    record_id TEXT PRIMARY KEY REFERENCES records(id),
    reason    TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS consolidation_sources (
    record_id TEXT NOT NULL REFERENCES consolidations(record_id),
    source_id TEXT PRIMARY KEY REFERENCES records(id),
    ordinal   INTEGER NOT NULL,
    UNIQUE(record_id, ordinal)
);
`

func initializeConsolidations(q Querier) error {
	_, err := q.Exec(consolidationSchema)
	return err
}

// ConsolidateRequest supplies complete replacement wording for two or more
// active, equally scoped guidance or recall records. Context and Sources accept
// canonical IDs or local @ references. Empty Kind infers the common source kind.
type ConsolidateRequest struct {
	Context string
	Sources []string
	Body    string
	Reason  string
	Kind    string
}

// ConsolidationSource is the exact immediate predecessor, not a redirection to
// its latest version. Inspecting Ref can follow subsequent replacements.
type ConsolidationSource struct {
	RecordEnvelope `yaml:",inline"`
	Ref            string `json:"ref" yaml:"ref"`
}

type Consolidation struct {
	Reason  string                `json:"reason" yaml:"reason"`
	Sources []ConsolidationSource `json:"sources" yaml:"sources"`
}

type ConsolidationResult struct {
	RecordEnvelope `yaml:",inline"`
	Ref            string         `json:"ref" yaml:"ref"`
	Consolidation  *Consolidation `json:"consolidation" yaml:"consolidation"`
}

// GetConsolidation returns stored intent and immediate historical sources in
// caller order, or nil when this record was not created by consolidation. Source
// bodies remain immutable; their mechanical status can change. Origin receipt
// IDs remain recorded, but ordinary context retention still applies.
func GetConsolidation(q Querier, id string) (*Consolidation, error) {
	c := &Consolidation{Sources: []ConsolidationSource{}}
	err := q.QueryRow(`SELECT reason FROM consolidations WHERE record_id = ?`, id).Scan(&c.Reason)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	rows, err := q.Query(`SELECT source_id FROM consolidation_sources WHERE record_id = ? ORDER BY ordinal`, id)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var source string
		if err := rows.Scan(&source); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, source)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, source := range ids {
		r, err := GetRecord(q, source)
		if err != nil {
			return nil, err
		}
		ref, err := Reference(q, source)
		if err != nil {
			return nil, err
		}
		c.Sources = append(c.Sources, ConsolidationSource{RecordEnvelope: r.Envelope(), Ref: ref})
	}
	return c, nil
}

func consolidationID(q Querier, value string) (string, error) {
	if strings.HasPrefix(value, "@") {
		return ResolveReference(q, value)
	}
	if !IsID(value) {
		return "", fmt.Errorf("%w: expected a canonical ID or local @ reference, got %q", ErrInvalid, value)
	}
	return value, nil
}

// Consolidate validates and commits the entire many-to-one replacement in one
// transaction. Source IDs are compare-and-swap expectations: inactive inputs
// fail instead of silently following their replacements. No model is invoked.
func (s *Store) Consolidate(req ConsolidateRequest) (*ConsolidationResult, error) {
	if len(req.Sources) < 2 {
		return nil, fmt.Errorf("%w: consolidation requires at least two distinct sources", ErrInvalid)
	}
	if strings.TrimSpace(req.Reason) == "" {
		return nil, fmt.Errorf("%w: consolidation requires a nonempty reason", ErrInvalid)
	}
	if strings.TrimSpace(req.Body) == "" {
		return nil, fmt.Errorf("%w: consolidation requires complete nonempty replacement text", ErrInvalid)
	}
	if err := ValidateBody(req.Reason); err != nil {
		return nil, err
	}
	if err := ValidateBody(req.Body); err != nil {
		return nil, err
	}
	if req.Kind == "brief-item" || (req.Kind != "" && strings.TrimSpace(req.Kind) == "") {
		return nil, fmt.Errorf("%w: consolidation kind must be an ordinary guidance or recall kind, not %q", ErrInvalid, req.Kind)
	}
	var result *ConsolidationResult
	err := s.Tx(func(tx *sql.Tx) error {
		contextID, err := consolidationID(tx, req.Context)
		if err != nil {
			return err
		}
		if !strings.HasPrefix(contextID, "ctx_") {
			return fmt.Errorf("%w: consolidation requires a context receipt, not %s", ErrInvalid, req.Context)
		}
		ctx, err := GetContext(tx, contextID)
		if err != nil {
			return err
		}
		seen := map[string]bool{}
		var sources []*Record
		kind := req.Kind
		lane := ""
		for _, value := range req.Sources {
			id, err := consolidationID(tx, value)
			if err != nil {
				return err
			}
			if strings.HasPrefix(id, "ctx_") || strings.HasPrefix(id, "gen_") {
				return fmt.Errorf("%w: consolidation source must be a guidance or recall record, not %s", ErrInvalid, value)
			}
			if seen[id] {
				return fmt.Errorf("%w: duplicate consolidation source %s", ErrInvalid, value)
			}
			seen[id] = true
			r, err := GetRecord(tx, id)
			if err != nil {
				return err
			}
			if r.Agent != ctx.Agent || (r.Lane != "guidance" && r.Lane != "recall") || r.Kind == "brief-item" {
				return fmt.Errorf("%w: source %s must be ordinary guidance or recall owned by context agent %s", ErrInvalid, value, ctx.Agent)
			}
			if r.Status != "active" {
				latest, err := LatestSuccessor(tx, id)
				if err != nil {
					return err
				}
				ref, err := Reference(tx, latest)
				if err != nil {
					return err
				}
				return fmt.Errorf("%w: source %s is not active; inspect %s before consolidating current %s", ErrConflict, value, ref, r.Lane)
			}
			if len(sources) == 0 {
				lane = r.Lane
				if kind == "" {
					kind = r.Kind
				}
			} else {
				if r.Lane != lane {
					return fmt.Errorf("%w: consolidation sources must belong to the same lane", ErrInvalid)
				}
				if !sameMetadata(sources[0].Meta, r.Meta) {
					return fmt.Errorf("%w: consolidation sources must have identical metadata value sets; correct scope separately", ErrInvalid)
				}
				if req.Kind == "" && r.Kind != kind {
					return fmt.Errorf("%w: sources have different kinds; choose the replacement kind with --kind", ErrInvalid)
				}
			}
			sources = append(sources, r)
		}
		// Check dependencies before retiring any source or introducing new edges.
		// Existing superseded-by accounting can depend on the current chain tip.
		if lane == "guidance" {
			for _, source := range sources {
				if _, err := InvalidateGenerationForGuidance(tx, ctx.Agent, source.ID); err != nil {
					return err
				}
			}
		}
		rec, err := InsertRecord(tx, NewRecord{Agent: ctx.Agent, Lane: lane, Kind: kind,
			Body: req.Body, OriginContext: ctx.ID, Meta: sources[0].Meta.Clone()})
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO consolidations(record_id, reason) VALUES (?, ?)`, rec.ID, req.Reason); err != nil {
			return err
		}
		for i, source := range sources {
			res, err := tx.Exec(`UPDATE records SET status = 'superseded' WHERE id = ? AND status = 'active'`, source.ID)
			if err != nil {
				return err
			}
			if n, err := res.RowsAffected(); err != nil {
				return err
			} else if n != 1 {
				return fmt.Errorf("%w: consolidation source %s is no longer active", ErrConflict, source.ID)
			}
			if _, err := tx.Exec(`INSERT INTO consolidation_sources(record_id, source_id, ordinal) VALUES (?, ?, ?)`, rec.ID, source.ID, i); err != nil {
				return err
			}
		}
		audit, err := GetConsolidation(tx, rec.ID)
		if err != nil {
			return err
		}
		ref, err := Reference(tx, rec.ID)
		if err != nil {
			return err
		}
		result = &ConsolidationResult{RecordEnvelope: rec.Envelope(), Ref: ref, Consolidation: audit}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
