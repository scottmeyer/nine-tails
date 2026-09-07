package store

import (
	"database/sql"
	"errors"
	"fmt"
)

// EpisodeReviewRecord is the bounded record projection needed to render one
// review entry. Full immutable bodies and names remain available through exact
// inspection; a review page never reads them in full merely to make a preview.
type EpisodeReviewRecord struct {
	ID            string
	Agent         string
	Lane          string
	Kind          string
	NamePrefix    string
	NameTruncated bool
	BodyPrefix    string
	BodyTruncated bool
	Status        string
}

const reviewProjectionRunes = 1024

func GetEpisodeReviewRecord(q Querier, id string) (*EpisodeReviewRecord, error) {
	var r EpisodeReviewRecord
	var nameTruncated, bodyTruncated int
	err := q.QueryRow(`SELECT id, agent, lane, kind,
		substr(COALESCE(name, ''), 1, ?), length(COALESCE(name, '')) > ?,
		substr(body, 1, ?), length(body) > ?, status
		FROM records WHERE id = ?`, reviewProjectionRunes, reviewProjectionRunes,
		reviewProjectionRunes, reviewProjectionRunes, id).Scan(
		&r.ID, &r.Agent, &r.Lane, &r.Kind, &r.NamePrefix, &nameTruncated,
		&r.BodyPrefix, &bodyTruncated, &r.Status)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: record %s", ErrNotFound, id)
	}
	if err != nil {
		return nil, err
	}
	r.NameTruncated = nameTruncated != 0
	r.BodyTruncated = bodyTruncated != 0
	return &r, nil
}

// EpisodeReviewRecordIDs returns stable lightweight positions for the two
// live sections. Consolidation sources are deliberately not joined, so one
// consolidated replacement remains one write entry.
func EpisodeReviewRecordIDs(q Querier, contextID, section string) ([]string, error) {
	var query string
	switch section {
	case "write":
		query = `SELECT id FROM records WHERE origin_context_id = ? ORDER BY created_at, rowid`
	case "retirement":
		query = `SELECT record_id FROM record_retirements WHERE context_id = ? ORDER BY created_at, rowid`
	default:
		return nil, fmt.Errorf("%w: unknown episode review section %q", ErrInvalid, section)
	}
	rows, err := q.Query(query, contextID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// EpisodeReviewRetirementReason returns a bounded current inspection preview;
// exact inspection remains the source for the complete reason.
func EpisodeReviewRetirementReason(q Querier, recordID string) (string, bool, error) {
	var reason string
	var truncated int
	err := q.QueryRow(`SELECT substr(reason, 1, ?), length(reason) > ? FROM record_retirements WHERE record_id = ?`,
		reviewProjectionRunes, reviewProjectionRunes, recordID).Scan(&reason, &truncated)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return reason, truncated != 0, err
}
