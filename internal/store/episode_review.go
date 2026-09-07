package store

import (
	"database/sql"
	"errors"
	"fmt"
	"unicode/utf8"
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
const reviewProjectionBytes = (reviewProjectionRunes + 1) * utf8.UTFMax

func episodeReviewPrefix(raw []byte, sourceContinues bool) (string, bool, error) {
	prefix, ok := libraryPrefix(raw, reviewProjectionRunes+1)
	if !ok {
		return "", false, fmt.Errorf("%w: episode review text is invalid UTF-8", ErrInvalid)
	}
	runes := []rune(prefix)
	truncated := sourceContinues || len(runes) > reviewProjectionRunes
	if len(runes) > reviewProjectionRunes {
		runes = runes[:reviewProjectionRunes]
	}
	return string(runes), truncated, nil
}

func GetEpisodeReviewRecord(q Querier, id string) (*EpisodeReviewRecord, error) {
	var r EpisodeReviewRecord
	var nameTruncated, bodyTruncated int
	var rawName, rawBody []byte
	err := q.QueryRow(`SELECT id, agent, lane, kind,
		substr(CAST(COALESCE(name, '') AS BLOB), 1, ?), length(CAST(COALESCE(name, '') AS BLOB)) > ?,
		substr(CAST(body AS BLOB), 1, ?), length(CAST(body AS BLOB)) > ?, status
		FROM records WHERE id = ?`, reviewProjectionBytes, reviewProjectionBytes,
		reviewProjectionBytes, reviewProjectionBytes, id).Scan(
		&r.ID, &r.Agent, &r.Lane, &r.Kind, &rawName, &nameTruncated,
		&rawBody, &bodyTruncated, &r.Status)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: record %s", ErrNotFound, id)
	}
	if err != nil {
		return nil, err
	}
	r.NamePrefix, r.NameTruncated, err = episodeReviewPrefix(rawName, nameTruncated != 0)
	if err != nil {
		return nil, fmt.Errorf("record %s name: %w", id, err)
	}
	r.BodyPrefix, r.BodyTruncated, err = episodeReviewPrefix(rawBody, bodyTruncated != 0)
	if err != nil {
		return nil, fmt.Errorf("record %s body: %w", id, err)
	}
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
	var rawReason []byte
	var truncated int
	err := q.QueryRow(`SELECT substr(CAST(reason AS BLOB), 1, ?), length(CAST(reason AS BLOB)) > ?
		FROM record_retirements WHERE record_id = ?`, reviewProjectionBytes, reviewProjectionBytes, recordID).Scan(&rawReason, &truncated)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	reason, prefixTruncated, err := episodeReviewPrefix(rawReason, truncated != 0)
	if err != nil {
		return "", false, fmt.Errorf("retirement for %s: %w", recordID, err)
	}
	return reason, prefixTruncated, nil
}
