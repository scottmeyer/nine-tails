package store

import "fmt"

// CloseReceipt finishes one load without creating feedback or changing any
// historical marks. The caller runs it in a transaction with its other work.
func CloseReceipt(tx Querier, id string) (*Context, error) {
	c, err := GetContext(tx, id)
	if err != nil {
		return nil, err
	}
	if c.ClosedAt != "" {
		return nil, fmt.Errorf("%w: %s was closed at %s", ErrConflict, id, c.ClosedAt)
	}
	now := Now()
	if _, err := tx.Exec(`UPDATE contexts SET closed_at = ? WHERE id = ?`, now, id); err != nil {
		return nil, err
	}
	c.ClosedAt = now
	return c, nil
}
