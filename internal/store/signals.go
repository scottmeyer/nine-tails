package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Delivery is the mechanical delivery state of a signal (spec §15.3).
type Delivery struct {
	RecordID       string `json:"id" yaml:"id"`
	Agent          string `json:"agent" yaml:"agent"`
	AvailableAt    string `json:"available_at" yaml:"available_at"`
	DedupeKey      string `json:"dedupe_key,omitempty" yaml:"dedupe_key,omitempty"`
	State          string `json:"state" yaml:"state"` // pending | leased | acknowledged
	LeaseToken     string `json:"lease_token,omitempty" yaml:"lease_token,omitempty"`
	LeasedUntil    string `json:"leased_until,omitempty" yaml:"leased_until,omitempty"`
	AcknowledgedAt string `json:"acknowledged_at,omitempty" yaml:"acknowledged_at,omitempty"`
}

// Signal pairs a signal record with its delivery state.
type Signal struct {
	Record   *Record  `json:"record" yaml:"record"`
	Delivery Delivery `json:"delivery" yaml:"delivery"`
}

// OrphanedSignalRecordsError reports due delivery rows whose record side is
// missing. DueSignals still returns every healthy signal alongside this error
// so callers that treat signals as optional can preserve the usable subset.
type OrphanedSignalRecordsError struct {
	RecordIDs []string
}

func (e *OrphanedSignalRecordsError) Error() string {
	return fmt.Sprintf("%v: signal delivery references missing record(s): %s", ErrNotFound, strings.Join(e.RecordIDs, ", "))
}

func (e *OrphanedSignalRecordsError) Unwrap() error { return ErrNotFound }

// CreateSignal inserts a signal record and its delivery row. If dedupeKey is
// non-empty and a nonterminal signal with the same (agent, key) exists, that
// existing signal is returned with deduplicated=true and nothing is written.
// Must run inside Tx.
func CreateSignal(tx Querier, agent, body string, meta Meta, availableAt time.Time, dedupeKey, originContext string) (sig *Signal, deduplicated bool, err error) {
	// Validate before dedupe resolution: malformed retry input must not appear
	// successful merely because a prior signal already owns the key.
	if err := ValidateBody(body); err != nil {
		return nil, false, err
	}
	if err := ValidateMeta(meta); err != nil {
		return nil, false, err
	}
	if dedupeKey != "" {
		var existing string
		err := tx.QueryRow(`SELECT record_id FROM signal_delivery WHERE agent = ? AND dedupe_key = ? AND state != 'acknowledged'`, agent, dedupeKey).Scan(&existing)
		if err == nil {
			s, err := GetSignal(tx, existing)
			return s, true, err
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, false, err
		}
	}
	rec, err := InsertRecord(tx, NewRecord{Agent: agent, Lane: "signal", Kind: "signal", Body: body, Meta: meta, OriginContext: originContext})
	if err != nil {
		return nil, false, err
	}
	d := Delivery{RecordID: rec.ID, Agent: agent, AvailableAt: FormatTime(availableAt), DedupeKey: dedupeKey, State: "pending"}
	if _, err := tx.Exec(`INSERT INTO signal_delivery(record_id, agent, available_at, dedupe_key, state) VALUES (?, ?, ?, ?, 'pending')`,
		rec.ID, agent, d.AvailableAt, nullable(dedupeKey)); err != nil {
		return nil, false, err
	}
	return &Signal{Record: rec, Delivery: d}, false, nil
}

func scanDelivery(sc interface{ Scan(...any) error }) (Delivery, error) {
	var d Delivery
	err := sc.Scan(&d.RecordID, &d.Agent, &d.AvailableAt, &d.DedupeKey, &d.State, &d.LeaseToken, &d.LeasedUntil, &d.AcknowledgedAt)
	return d, err
}

const deliveryCols = `record_id, agent, available_at, COALESCE(dedupe_key,''), state, COALESCE(lease_token,''), COALESCE(leased_until,''), COALESCE(acknowledged_at,'')`

// GetDelivery loads delivery state for a signal.
func GetDelivery(q Querier, id string) (*Delivery, error) {
	d, err := scanDelivery(q.QueryRow(`SELECT `+deliveryCols+` FROM signal_delivery WHERE record_id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: signal %s", ErrNotFound, id)
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// GetSignal loads a signal record with its delivery state.
func GetSignal(q Querier, id string) (*Signal, error) {
	d, err := GetDelivery(q, id)
	if err != nil {
		return nil, err
	}
	rec, err := GetRecord(q, id)
	if err != nil {
		return nil, err
	}
	return &Signal{Record: rec, Delivery: *d}, nil
}

// effectiveState reports the delivery state as of now: a leased signal whose
// lease expired counts as pending.
func effectiveState(d Delivery, now time.Time) string {
	if d.State == "leased" && d.LeasedUntil != "" {
		if until, err := time.Parse(time.RFC3339, d.LeasedUntil); err == nil && !until.After(now) {
			return "pending"
		}
	}
	return d.State
}

// DeliveryAsOf returns a coherent read-only view of delivery at now. Expired
// leases are pending and no longer expose their stale token or deadline.
func DeliveryAsOf(d Delivery, now time.Time) Delivery {
	d.State = effectiveState(d, now)
	if d.State == "pending" {
		d.LeaseToken, d.LeasedUntil = "", ""
	}
	return d
}

// DueSignals returns unacknowledged signals for agent (or all agents when
// agent is "") whose available_at <= now, oldest-available first. Leased
// signals are included with their current (effective) state. When a delivery
// row has lost its record, the healthy subset is returned with an
// OrphanedSignalRecordsError identifying every missing record ID. Read-only.
func DueSignals(q Querier, agent string, now time.Time) ([]*Signal, error) {
	query := `SELECT ` + deliveryCols + ` FROM signal_delivery WHERE state != 'acknowledged' AND available_at <= ?`
	args := []any{FormatTime(now)}
	if agent != "" {
		query += ` AND agent = ?`
		args = append(args, agent)
	}
	query += ` ORDER BY available_at, rowid`
	rows, err := q.Query(query, args...)
	if err != nil {
		return nil, err
	}
	var ds []Delivery
	for rows.Next() {
		d, err := scanDelivery(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		d = DeliveryAsOf(d, now)
		ds = append(ds, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]*Signal, 0, len(ds))
	var orphaned []string
	for _, d := range ds {
		rec, err := GetRecord(q, d.RecordID)
		if errors.Is(err, ErrNotFound) {
			orphaned = append(orphaned, d.RecordID)
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, &Signal{Record: rec, Delivery: d})
	}
	if len(orphaned) > 0 {
		return out, &OrphanedSignalRecordsError{RecordIDs: orphaned}
	}
	return out, nil
}

// PendingSignals returns all unacknowledged signals for an agent regardless of
// availability time (for inspect), oldest first.
func PendingSignals(q Querier, agent string, now time.Time) ([]*Signal, error) {
	rows, err := q.Query(`
		SELECT d.record_id, d.agent, d.available_at, COALESCE(d.dedupe_key,''), d.state,
		       COALESCE(d.lease_token,''), COALESCE(d.leased_until,''), COALESCE(d.acknowledged_at,'')
		FROM signal_delivery AS d
		JOIN records AS r ON r.id = d.record_id
		WHERE d.state != 'acknowledged' AND d.agent = ?
		ORDER BY r.created_at, r.rowid`, agent)
	if err != nil {
		return nil, err
	}
	var ds []Delivery
	for rows.Next() {
		d, err := scanDelivery(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		d = DeliveryAsOf(d, now)
		ds = append(ds, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]*Signal, 0, len(ds))
	for _, d := range ds {
		rec, err := GetRecord(q, d.RecordID)
		if err != nil {
			return nil, err
		}
		out = append(out, &Signal{Record: rec, Delivery: d})
	}
	return out, nil
}

// ClaimDue atomically leases every due pending (or lease-expired) signal for
// agent (or all agents when "") for the given duration. Must run inside Tx.
func ClaimDue(tx Querier, agent string, now time.Time, lease time.Duration) ([]*Signal, error) {
	due, err := DueSignals(tx, agent, now)
	if err != nil {
		return nil, err
	}
	until := FormatTime(now.Add(lease))
	var out []*Signal
	for _, s := range due {
		if s.Delivery.State != "pending" {
			continue // currently leased by someone else
		}
		token, err := NextID(tx, "lease")
		if err != nil {
			return nil, err
		}
		if _, err := tx.Exec(`UPDATE signal_delivery SET state = 'leased', lease_token = ?, leased_until = ? WHERE record_id = ?`, token, until, s.Record.ID); err != nil {
			return nil, err
		}
		s.Delivery.State = "leased"
		s.Delivery.LeaseToken = token
		s.Delivery.LeasedUntil = until
		out = append(out, s)
	}
	return out, nil
}

// AckSignal acknowledges a leased signal using its lease token (spec §15.3).
// Wrong token, expired lease, or a non-leased signal → ErrConflict.
func AckSignal(tx Querier, id, token string, now time.Time) error {
	d, err := GetDelivery(tx, id)
	if err != nil {
		return err
	}
	if d.State == "acknowledged" {
		return fmt.Errorf("%w: signal %s is already acknowledged", ErrConflict, id)
	}
	if effectiveState(*d, now) != "leased" {
		return fmt.Errorf("%w: signal %s is not leased (state %s)", ErrConflict, id, d.State)
	}
	if d.LeaseToken != token {
		return fmt.Errorf("%w: lease token does not match for signal %s", ErrConflict, id)
	}
	_, err = tx.Exec(`UPDATE signal_delivery SET state = 'acknowledged', acknowledged_at = ? WHERE record_id = ?`, FormatTime(now), id)
	return err
}
