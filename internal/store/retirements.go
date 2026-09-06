package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// Retirement records why an active record was deliberately removed from
// future projections. It does not rewrite or erase the retired record.
// Context is an immutable identifier, not a foreign key: receipt GC may
// collect that receipt without deleting the reason for the decision.
type Retirement struct {
	Context    string `json:"context" yaml:"context"`
	ContextRef string `json:"context_ref" yaml:"context_ref"`
	Reason     string `json:"reason" yaml:"reason"`
	CreatedAt  string `json:"created_at" yaml:"created_at"`
}

func initializeRetirements(tx Querier) error {
	_, err := tx.Exec(`CREATE TABLE IF NOT EXISTS record_retirements (
		record_id TEXT PRIMARY KEY REFERENCES records(id),
		context_id TEXT NOT NULL,
		reason TEXT NOT NULL,
		created_at TEXT NOT NULL
	)`)
	return err
}

func GetRetirement(q Querier, id string) (*Retirement, error) {
	var r Retirement
	err := q.QueryRow(`SELECT context_id, reason, created_at FROM record_retirements WHERE record_id = ?`, id).Scan(&r.Context, &r.Reason, &r.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r.ContextRef, err = Reference(q, r.Context)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// RetireRecord must run inside Tx. Empty context and reason preserve the
// legacy disable operation; an audited retirement requires both. A failed
// validation cannot retire a record, invalidate its brief, or leave an audit.
func RetireRecord(tx Querier, id, contextID, reason string) (*Record, error) {
	audited := contextID != "" || reason != ""
	if audited {
		if contextID == "" || strings.TrimSpace(reason) == "" {
			return nil, fmt.Errorf("%w: retirement requires both context and a nonempty reason", ErrInvalid)
		}
		if err := ValidateBody(reason); err != nil {
			return nil, err
		}
	}
	r, err := GetRecord(tx, id)
	if err != nil {
		return nil, err
	}
	switch {
	case r.Kind == "brief-item":
		return nil, fmt.Errorf("%w: %s is a brief item; compile a new generation instead", ErrInvalid, id)
	case r.Lane == "signal":
		return nil, fmt.Errorf("%w: %s is a signal; use signal ack", ErrInvalid, id)
	case r.Status != "active":
		return nil, fmt.Errorf("%w: %s is %s, not active", ErrConflict, id, r.Status)
	}
	if audited {
		ctx, err := GetContext(tx, contextID)
		if err != nil {
			return nil, err
		}
		if ctx.Agent != r.Agent {
			return nil, fmt.Errorf("%w: context belongs to %s, not %s", ErrInvalid, ctx.Agent, r.Agent)
		}
	}
	if r.Lane == "guidance" {
		if _, err := InvalidateGenerationForGuidance(tx, r.Agent, r.ID); err != nil {
			return nil, err
		}
	}
	if err := SetStatus(tx, id, "disabled"); err != nil {
		return nil, err
	}
	if audited {
		if _, err := tx.Exec(`INSERT INTO record_retirements (record_id, context_id, reason, created_at) VALUES (?, ?, ?, ?)`, id, contextID, reason, Now()); err != nil {
			return nil, err
		}
	}
	r.Status = "disabled"
	return r, nil
}
