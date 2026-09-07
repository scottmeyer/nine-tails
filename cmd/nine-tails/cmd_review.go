package main

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"strings"

	"github.com/scottmeyer/nine-tails/internal/cli"
	"github.com/scottmeyer/nine-tails/internal/store"
)

const (
	reviewEntryBytes   = 8192
	reviewPreviewRunes = 160
)

type reviewContext struct {
	ID        string     `json:"context_id" yaml:"context_id"`
	Ref       string     `json:"ref" yaml:"ref"`
	Agent     string     `json:"agent" yaml:"agent"`
	Parent    string     `json:"parent_context" yaml:"parent_context"`
	Task      string     `json:"task" yaml:"task"`
	CreatedAt string     `json:"created_at" yaml:"created_at"`
	Meta      store.Meta `json:"metadata" yaml:"metadata"`
}

type reviewCounts struct {
	Delivered   int `json:"delivered" yaml:"delivered"`
	Writes      int `json:"writes" yaml:"writes"`
	Retirements int `json:"retirements" yaml:"retirements"`
	Total       int `json:"total" yaml:"total"`
	Returned    int `json:"returned" yaml:"returned"`
	Remaining   int `json:"remaining" yaml:"remaining"`
}

type reviewCurrent struct {
	ID                string `json:"id" yaml:"id"`
	Ref               string `json:"ref" yaml:"ref"`
	Status            string `json:"status" yaml:"status"`
	Relation          string `json:"relation" yaml:"relation"`
	InspectionPreview string `json:"inspection_preview,omitempty" yaml:"inspection_preview,omitempty"`
	PreviewTruncated  bool   `json:"preview_truncated,omitempty" yaml:"preview_truncated,omitempty"`
	Inspect           string `json:"inspect,omitempty" yaml:"inspect,omitempty"`
}

type reviewEntry struct {
	Event             string         `json:"event" yaml:"event"`
	ID                string         `json:"id" yaml:"id"`
	Ref               string         `json:"ref" yaml:"ref"`
	Agent             string         `json:"agent" yaml:"agent"`
	Lane              string         `json:"lane" yaml:"lane"`
	Kind              string         `json:"kind" yaml:"kind"`
	Name              string         `json:"name,omitempty" yaml:"name,omitempty"`
	NameTruncated     bool           `json:"name_truncated,omitempty" yaml:"name_truncated,omitempty"`
	Status            string         `json:"status" yaml:"status"`
	InspectionPreview string         `json:"inspection_preview" yaml:"inspection_preview"`
	PreviewTruncated  bool           `json:"preview_truncated,omitempty" yaml:"preview_truncated,omitempty"`
	Inspect           string         `json:"inspect" yaml:"inspect"`
	Section           string         `json:"section,omitempty" yaml:"section,omitempty"`
	Ordinal           *int           `json:"ordinal,omitempty" yaml:"ordinal,omitempty"`
	Current           *reviewCurrent `json:"current" yaml:"current"`
	ReasonPreview     string         `json:"reason_preview,omitempty" yaml:"reason_preview,omitempty"`
	ReasonTruncated   bool           `json:"reason_truncated,omitempty" yaml:"reason_truncated,omitempty"`
}

type reviewNext struct {
	ReviewAfter string `json:"review_after" yaml:"review_after"`
	Inspect     string `json:"inspect" yaml:"inspect"`
}

type reviewPacket struct {
	Version     int           `json:"version" yaml:"version"`
	Context     reviewContext `json:"context" yaml:"context"`
	Order       []string      `json:"order" yaml:"order"`
	Counts      reviewCounts  `json:"counts" yaml:"counts"`
	Entries     []reviewEntry `json:"entries" yaml:"entries"`
	Next        *reviewNext   `json:"next" yaml:"next"`
	PreviewNote string        `json:"preview_note" yaml:"preview_note"`
	Limit       string        `json:"limit" yaml:"limit"`
}

type reviewCursor struct {
	Version int    `json:"v"`
	Context string `json:"c"`
	Section string `json:"s"`
	ID      string `json:"id"`
}

type reviewEntryKey struct {
	Event   string
	ID      string
	Section string
	Ordinal int
}

func encodeReviewCursor(context string, entry reviewEntry) (string, error) {
	raw, err := json.Marshal(reviewCursor{Version: 1, Context: context, Section: entry.Event, ID: entry.ID})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// decodeReviewCursor validates the complete cursor without consulting a store.
// This keeps malformed review syntax ahead of local-reference resolution.
func decodeReviewCursor(value string) (reviewCursor, error) {
	var cursor reviewCursor
	if value == "" {
		return cursor, cli.Invalid("--review-after must be nonempty when supplied")
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || json.Unmarshal(raw, &cursor) != nil {
		return cursor, cli.Invalid("invalid --review-after cursor")
	}
	if cursor.Version != 1 || !strings.HasPrefix(cursor.Context, "ctx_") || !cli.IsID(cursor.Context) ||
		!cli.IsID(cursor.ID) || (cursor.Section != "delivered" && cursor.Section != "write" && cursor.Section != "retirement") {
		return cursor, cli.Invalid("invalid --review-after cursor")
	}
	return cursor, nil
}

func validateReviewArguments(args []string, review bool, reviewAfter, format string, incompatible []string) error {
	if !review {
		return cli.Invalid("--review-after requires --review")
	}
	if len(args) != 1 {
		return cli.Invalid("--review requires one context receipt target")
	}
	for _, flag := range incompatible {
		return cli.Invalid("--review cannot be combined with --%s", flag)
	}
	target := args[0]
	if strings.HasPrefix(target, "@") {
		if err := store.ValidateReference(target); err != nil {
			return err
		}
	} else if !strings.HasPrefix(target, "ctx_") || !cli.IsID(target) {
		return cli.Invalid("--review target must identify a context receipt (ctx_ ID or its @N reference)")
	}
	if format != "json" && format != "yaml" {
		return cli.Invalid("unknown format %q (json|yaml)", format)
	}
	if reviewAfter != "" {
		if _, err := decodeReviewCursor(reviewAfter); err != nil {
			return err
		}
	}
	return nil
}

func reviewText(value string) (string, bool) {
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) <= reviewPreviewRunes {
		return value, false
	}
	return string(runes[:reviewPreviewRunes-1]) + "…", true
}

func (a *app) reviewEntry(tx store.Querier, event string, rec *store.EpisodeReviewRecord) (reviewEntry, error) {
	ref, err := store.Reference(tx, rec.ID)
	if err != nil {
		return reviewEntry{}, err
	}
	name, nameTruncated := reviewText(rec.NamePrefix)
	preview, previewTruncated := reviewText(rec.BodyPrefix)
	entry := reviewEntry{
		Event: event, ID: rec.ID, Ref: ref, Agent: rec.Agent, Lane: rec.Lane, Kind: rec.Kind,
		Name: name, NameTruncated: nameTruncated || rec.NameTruncated, Status: rec.Status,
		InspectionPreview: preview, PreviewTruncated: previewTruncated || rec.BodyTruncated,
		Inspect: cli.StoreCommand(a.recipeHome(), "inspect "+ref),
	}
	currentID, err := store.LatestSuccessor(tx, rec.ID)
	if err != nil {
		return reviewEntry{}, err
	}
	current := rec
	if currentID != rec.ID {
		current, err = store.GetEpisodeReviewRecord(tx, currentID)
		if err != nil {
			return reviewEntry{}, err
		}
	}
	currentRef, err := store.Reference(tx, currentID)
	if err != nil {
		return reviewEntry{}, err
	}
	relation := "self"
	if currentID != rec.ID {
		relation = "successor"
	} else if current.Status == "disabled" {
		relation = "retired"
	}
	entry.Current = &reviewCurrent{ID: current.ID, Ref: currentRef, Status: current.Status, Relation: relation}
	if currentID != rec.ID {
		entry.Current.InspectionPreview, entry.Current.PreviewTruncated = reviewText(current.BodyPrefix)
		entry.Current.PreviewTruncated = entry.Current.PreviewTruncated || current.BodyTruncated
		entry.Current.Inspect = cli.StoreCommand(a.recipeHome(), "inspect "+currentRef)
	}
	return entry, nil
}

func (a *app) inspectReview(contextID, after, format string) error {
	if !strings.HasPrefix(contextID, "ctx_") || !cli.IsID(contextID) {
		return cli.Invalid("--review target must identify a context receipt (ctx_ ID or its @N reference)")
	}
	if err := a.open(); err != nil {
		return err
	}
	packet := reviewPacket{
		Version: 1, Order: []string{"delivered", "write", "retirement"}, Entries: []reviewEntry{},
		PreviewNote: "inspection_preview is today's bounded view of the immutable record body, not the excerpt delivered by the historical load",
		Limit:       "Recorded delivery and changes do not establish full-text exposure, application, correctness, or improvement; unsaved conversation and tool feedback are absent.",
	}
	err := a.st.Tx(func(tx *sql.Tx) error {
		ctx, err := store.GetContext(tx, contextID)
		if err != nil {
			return err
		}
		contextRef, err := store.Reference(tx, ctx.ID)
		if err != nil {
			return err
		}
		packet.Context = reviewContext{ID: ctx.ID, Ref: contextRef, Agent: ctx.Agent, Parent: ctx.Parent,
			Task: ctx.Task, CreatedAt: ctx.CreatedAt, Meta: ctx.Meta}

		var keys []reviewEntryKey
		for _, rendered := range ctx.Rendered {
			keys = append(keys, reviewEntryKey{Event: "delivered", ID: rendered.RecordID, Section: rendered.Section, Ordinal: rendered.Ordinal})
		}
		packet.Counts.Delivered = len(keys)

		writeIDs, err := store.EpisodeReviewRecordIDs(tx, ctx.ID, "write")
		if err != nil {
			return err
		}
		packet.Counts.Writes = len(writeIDs)
		for _, id := range writeIDs {
			keys = append(keys, reviewEntryKey{Event: "write", ID: id})
		}

		retiredIDs, err := store.EpisodeReviewRecordIDs(tx, ctx.ID, "retirement")
		if err != nil {
			return err
		}
		packet.Counts.Retirements = len(retiredIDs)
		for _, id := range retiredIDs {
			keys = append(keys, reviewEntryKey{Event: "retirement", ID: id})
		}

		packet.Counts.Total = len(keys)
		start := 0
		if after != "" {
			cursor, err := decodeReviewCursor(after)
			if err != nil {
				return err
			}
			if cursor.Context != ctx.ID {
				return cli.Invalid("--review-after cursor belongs to another context")
			}
			found := false
			for i, key := range keys {
				if key.Event == cursor.Section && key.ID == cursor.ID {
					start, found = i+1, true
					break
				}
			}
			if !found {
				return cli.Invalid("--review-after cursor no longer identifies an entry in this review")
			}
		}

		used := 0
		end := start
		for end < len(keys) {
			key := keys[end]
			rec, err := store.GetEpisodeReviewRecord(tx, key.ID)
			if err != nil {
				return err
			}
			entry, err := a.reviewEntry(tx, key.Event, rec)
			if err != nil {
				return err
			}
			if key.Event == "delivered" {
				ordinal := key.Ordinal
				entry.Section, entry.Ordinal = key.Section, &ordinal
			}
			if key.Event == "retirement" {
				reason, sourceTruncated, err := store.EpisodeReviewRetirementReason(tx, key.ID)
				if err != nil {
					return err
				}
				if reason != "" {
					entry.ReasonPreview, entry.ReasonTruncated = reviewText(reason)
					entry.ReasonTruncated = entry.ReasonTruncated || sourceTruncated
				}
			}
			encoded, err := json.Marshal(entry)
			if err != nil {
				return err
			}
			if end > start && used+len(encoded) > reviewEntryBytes {
				break
			}
			packet.Entries = append(packet.Entries, entry)
			used += len(encoded)
			end++
		}
		packet.Counts.Returned = len(packet.Entries)
		packet.Counts.Remaining = len(keys) - end
		if end < len(keys) && len(packet.Entries) > 0 {
			cursor, err := encodeReviewCursor(ctx.ID, packet.Entries[len(packet.Entries)-1])
			if err != nil {
				return err
			}
			command := cli.StoreCommand(a.recipeHome(), "inspect "+contextRef+" --review --review-after "+cli.QuoteArgument(cursor))
			packet.Next = &reviewNext{ReviewAfter: cursor, Inspect: command}
		}
		return nil
	})
	if err != nil {
		return err
	}
	return cli.Write(a.stdout, format, packet)
}
