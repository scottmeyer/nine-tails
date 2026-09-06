package store

import (
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Readable references are local, append-only aliases. They are not part of
// portable records: import allocates its own canonical IDs and local handles.
// No foreign key points at a live entity, so collecting a context cannot reuse
// its handle for another identity. Triggers also cover older v4 writers.
const referenceSchema = `
CREATE TABLE IF NOT EXISTS reference_aliases (
    number INTEGER PRIMARY KEY AUTOINCREMENT,
    entity_id TEXT UNIQUE NOT NULL
);
CREATE TRIGGER IF NOT EXISTS reference_aliases_immutable_update
BEFORE UPDATE ON reference_aliases BEGIN
    SELECT RAISE(ABORT, 'reference aliases are immutable');
END;
CREATE TRIGGER IF NOT EXISTS reference_aliases_immutable_delete
BEFORE DELETE ON reference_aliases BEGIN
    SELECT RAISE(ABORT, 'reference aliases are append-only');
END;
CREATE TRIGGER IF NOT EXISTS records_allocate_reference
AFTER INSERT ON records BEGIN
    INSERT INTO reference_aliases(entity_id)
    SELECT NEW.id WHERE NOT EXISTS (SELECT 1 FROM reference_aliases WHERE entity_id = NEW.id);
END;
CREATE TRIGGER IF NOT EXISTS contexts_allocate_reference
AFTER INSERT ON contexts BEGIN
    INSERT INTO reference_aliases(entity_id)
    SELECT NEW.id WHERE NOT EXISTS (SELECT 1 FROM reference_aliases WHERE entity_id = NEW.id);
END;
CREATE TRIGGER IF NOT EXISTS generations_allocate_reference
AFTER INSERT ON brief_generations BEGIN
    INSERT INTO reference_aliases(entity_id)
    SELECT NEW.id WHERE NOT EXISTS (SELECT 1 FROM reference_aliases WHERE entity_id = NEW.id);
END;
`

// This additive v4 extension is installed in the existing migration transaction.
// Only its first installation scans old entities; ordinary Open calls do not.
func initializeReferences(q Querier) error {
	var exists int
	if err := q.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'reference_aliases'`).Scan(&exists); err != nil {
		return err
	}
	if _, err := q.Exec(referenceSchema); err != nil {
		return err
	}
	if exists != 0 {
		return nil
	}
	_, err := q.Exec(`INSERT INTO reference_aliases(entity_id)
		SELECT id FROM (
			SELECT id, created_at FROM records UNION ALL
			SELECT id, created_at FROM contexts UNION ALL
			SELECT id, created_at FROM brief_generations
		) GROUP BY id ORDER BY MIN(created_at), id`)
	return err
}

// Reference returns an existing local handle, including a collected entity's
// tombstone. It never creates a semantic record or allocates a new identity.
func Reference(q Querier, id string) (string, error) {
	var number int64
	err := q.QueryRow(`SELECT number FROM reference_aliases WHERE entity_id = ?`, id).Scan(&number)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("%w: reference for %s", ErrNotFound, id)
	}
	if err != nil {
		return "", err
	}
	return "@" + strconv.FormatInt(number, 10), nil
}

// EnsureReference may reserve a handle for a freshly minted canonical ID before
// its entity is inserted. Call it in the same transaction as that insertion so
// a failed load rolls the reservation back. An existing mapping never changes.
func EnsureReference(q Querier, id string) (string, error) {
	if !IsID(id) {
		return "", fmt.Errorf("%w: invalid canonical identifier %q", ErrInvalid, id)
	}
	if _, err := q.Exec(`INSERT INTO reference_aliases(entity_id)
		SELECT ? WHERE NOT EXISTS (SELECT 1 FROM reference_aliases WHERE entity_id = ?)`, id, id); err != nil {
		return "", err
	}
	return Reference(q, id)
}

var referencePattern = regexp.MustCompile(`^@[1-9][0-9]*$`)

func referenceNumber(ref string) (int64, error) {
	if !referencePattern.MatchString(ref) {
		return 0, fmt.Errorf("%w: reference %q must be @ followed by a positive integer", ErrInvalid, ref)
	}
	number, err := strconv.ParseInt(ref[1:], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: reference number is out of range", ErrInvalid)
	}
	return number, nil
}

// ValidateReference checks syntax without opening or changing a store.
func ValidateReference(ref string) error {
	_, err := referenceNumber(ref)
	return err
}

// ResolveReference accepts an exact local handle, including GC tombstones.
// The entity getter still reports not-found for a collected identity.
func ResolveReference(q Querier, ref string) (string, error) {
	number, err := referenceNumber(ref)
	if err != nil {
		return "", err
	}
	var id string
	err = q.QueryRow(`SELECT entity_id FROM reference_aliases WHERE number = ?`, number).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("%w: reference %s", ErrNotFound, ref)
	}
	return id, err
}

// ReferenceFilter selects live entities. Empty fields and a zero Limit mean no
// restriction. Every requested metadata key/value must exist on the entity;
// this is an explicit inventory filter, not load applicability matching.
type ReferenceFilter struct {
	Kind, Agent, Query string
	Meta               Meta
	Limit              int
}

// ReferenceView gives a human-readable inventory without changing envelopes.
type ReferenceView struct {
	Ref       string `json:"ref" yaml:"ref"`
	ID        string `json:"id" yaml:"id"`
	Kind      string `json:"kind" yaml:"kind"`
	Agent     string `json:"agent" yaml:"agent"`
	Label     string `json:"label" yaml:"label"`
	Status    string `json:"status" yaml:"status"`
	CreatedAt string `json:"created_at" yaml:"created_at"`
	Meta      Meta   `json:"meta" yaml:"meta"`
}

const referenceEntities = `WITH entities AS (
    SELECT r.id, CASE WHEN r.lane = 'signal' THEN 'signal' ELSE 'record' END AS kind,
        r.agent, CASE WHEN r.lane = 'signal' THEN COALESCE(
            (SELECT value FROM metadata WHERE record_id = r.id AND key = 'subject' ORDER BY rowid LIMIT 1), r.body)
            ELSE COALESCE(NULLIF(r.name, ''), r.body) END AS label,
        CASE WHEN r.lane = 'signal' THEN COALESCE(d.state, r.status) ELSE r.status END AS status,
        r.created_at, COALESCE(r.name, '') || ' ' || r.body AS search_text,
        COALESCE(d.leased_until, '') AS leased_until
        FROM records r LEFT JOIN signal_delivery d ON d.record_id = r.id
    UNION ALL SELECT id, 'context', agent, COALESCE(task, ''),
        CASE WHEN closed_at IS NULL THEN 'open' ELSE 'closed' END, created_at,
        COALESCE(task, ''), '' FROM contexts
    UNION ALL SELECT id, 'generation', agent, 'Brief generation', status,
        created_at, 'Brief generation', '' FROM brief_generations
), entity_meta AS (
    SELECT record_id AS entity_id, key, value FROM metadata
    UNION ALL SELECT context_id, key, value FROM context_metadata
)
`

// ListReferences lists current entities newest first, using allocation order to
// break timestamp ties. Collected entities retain aliases but have no live row
// to list. Filtering happens before the limit, including scoped metadata.
func ListReferences(q Querier, f ReferenceFilter) ([]ReferenceView, error) {
	if f.Limit < 0 {
		return nil, fmt.Errorf("%w: reference limit must not be negative", ErrInvalid)
	}
	if f.Kind != "" && f.Kind != "context" && f.Kind != "signal" && f.Kind != "record" && f.Kind != "generation" {
		return nil, fmt.Errorf("%w: reference kind %q", ErrInvalid, f.Kind)
	}
	if err := ValidateMeta(f.Meta); err != nil {
		return nil, err
	}
	var where []string
	var args []any
	if f.Kind != "" {
		where, args = append(where, "e.kind = ?"), append(args, f.Kind)
	}
	if f.Agent != "" {
		where, args = append(where, "e.agent = ?"), append(args, f.Agent)
	}
	if f.Query != "" {
		where = append(where, "instr(lower(e.search_text || ' ' || e.label || ' ' || e.id || ' ' || e.agent), lower(?)) > 0")
		args = append(args, f.Query)
	}
	for _, key := range SortedKeys(f.Meta) {
		for _, value := range f.Meta[key] {
			where = append(where, "EXISTS (SELECT 1 FROM entity_meta m WHERE m.entity_id = e.id AND m.key = ? AND m.value = ?)")
			args = append(args, key, value)
		}
	}
	query := referenceEntities + `SELECT a.number, e.id, e.kind, e.agent, e.label, e.status, e.created_at, e.leased_until
		FROM reference_aliases a JOIN entities e ON e.id = a.entity_id`
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY e.created_at DESC, a.number DESC"
	if f.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, f.Limit)
	}
	rows, err := q.Query(query, args...)
	if err != nil {
		return nil, err
	}
	out := []ReferenceView{}
	byID := map[string]int{}
	now := Clock()
	for rows.Next() {
		var view ReferenceView
		var number int64
		var leasedUntil string
		if err := rows.Scan(&number, &view.ID, &view.Kind, &view.Agent, &view.Label, &view.Status, &view.CreatedAt, &leasedUntil); err != nil {
			rows.Close()
			return nil, err
		}
		view.Ref = "@" + strconv.FormatInt(number, 10)
		if view.Kind == "signal" {
			view.Status = DeliveryAsOf(Delivery{State: view.Status, LeasedUntil: leasedUntil}, now).State
		}
		view.Meta = Meta{}
		byID[view.ID] = len(out)
		out = append(out, view)
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(out) == 0 {
		return out, err
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(out)), ",")
	args = nil
	for _, view := range out {
		args = append(args, view.ID)
	}
	rows, err = q.Query(referenceEntities+`SELECT entity_id, key, value FROM entity_meta WHERE entity_id IN (`+placeholders+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, key, value string
		if err := rows.Scan(&id, &key, &value); err != nil {
			return nil, err
		}
		view := &out[byID[id]]
		view.Meta[key] = append(view.Meta[key], value)
	}
	return out, rows.Err()
}
