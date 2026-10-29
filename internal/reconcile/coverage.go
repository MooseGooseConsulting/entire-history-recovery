// Package reconcile compares visible checkpoint coverage without writing it.
// A local inventory is not proof of completeness across remotes or the server.
package reconcile

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	cp "github.com/entireio/cli/api/checkpoint"
	"github.com/entireio/cli/cmd/entire/cli/checkpoint/id"
)

type Reader interface {
	List(context.Context) ([]cp.CheckpointInfo, error)
	Read(context.Context, id.CheckpointID) (*cp.CheckpointSummary, error)
	ReadSessionContent(context.Context, id.CheckpointID, int) (*cp.SessionContent, error)
}

// Record contains private data. Do not serialize inventories into public logs.
type Record struct {
	CheckpointID id.CheckpointID
	SessionIndex int
	Content      cp.SessionContent
}

// ReadVisible hydrates every session in every returned checkpoint, including
// remote names-only stubs. Any failed read invalidates the whole result. The
// caller still has to establish whether List enumerated the required scope.
func ReadVisible(ctx context.Context, reader Reader) ([]Record, error) {
	items, err := reader.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("checkpoint inventory listing failed: %w", err)
	}
	var records []Record
	seen := make(map[id.CheckpointID]bool)
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if seen[item.CheckpointID] {
			continue
		}
		seen[item.CheckpointID] = true
		summary, err := reader.Read(ctx, item.CheckpointID)
		if err != nil {
			return nil, fmt.Errorf("checkpoint inventory hydration failed: %w", err)
		}
		if summary == nil || summary.CheckpointID != item.CheckpointID || len(summary.Sessions) == 0 {
			return nil, errors.New("checkpoint inventory has an invalid or empty summary")
		}
		for index := range summary.Sessions {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			content, err := reader.ReadSessionContent(ctx, item.CheckpointID, index)
			if err != nil {
				return nil, fmt.Errorf("checkpoint session inventory failed: %w", err)
			}
			if content == nil || content.Metadata.SessionID == "" || content.Metadata.Agent == "" || len(content.Transcript) == 0 {
				return nil, errors.New("checkpoint session inventory lacks identity or transcript")
			}
			records = append(records, Record{CheckpointID: item.CheckpointID, SessionIndex: index, Content: *content})
		}
	}
	return records, nil
}

type Candidate struct {
	SessionID string
	Agent     string
	// Transcript must use the SAME sanitized/redacted representation as records.
	Transcript []byte
	// Starts are verified raw-line boundaries, not counts of human prompts.
	Starts []int
}

type Assessment struct {
	// Status is present, unseen, or hold. unseen never authorizes a write:
	// remote/service inventory completeness is a separate prerequisite.
	Status    string `json:"status"`
	Covered   int    `json:"covered"`
	Uncovered int    `json:"uncovered"`
	Reason    string `json:"reason"`
}

// AssessExact recognizes compatible coverage even when checkpoint IDs and kinds
// differ (e.g. live capture). Different snapshots/offset encodings are held for
// event alignment rather than guessed equal, overwritten or declared missing.
func AssessExact(candidate Candidate, records []Record) (Assessment, error) {
	if candidate.SessionID == "" || candidate.Agent == "" || len(candidate.Transcript) == 0 || len(candidate.Starts) == 0 {
		return Assessment{}, errors.New("candidate requires identity, transcript and boundaries")
	}
	starts := make(map[int]bool)
	lineCount := bytes.Count(candidate.Transcript, []byte{'\n'})
	if candidate.Transcript[len(candidate.Transcript)-1] != '\n' {
		lineCount++
	}
	for _, start := range candidate.Starts {
		if start < 0 || start >= lineCount || starts[start] {
			return Assessment{}, errors.New("invalid or repeated candidate boundary")
		}
		starts[start] = true
	}
	covered := make(map[int]bool)
	matched, conflict := false, false
	for _, record := range records {
		meta := record.Content.Metadata
		if meta.SessionID != candidate.SessionID {
			continue
		}
		matched = true
		if string(meta.Agent) != candidate.Agent || !bytes.Equal(record.Content.Transcript, candidate.Transcript) {
			conflict = true
			continue
		}
		if starts[meta.CheckpointTranscriptStart] {
			covered[meta.CheckpointTranscriptStart] = true
		} else {
			conflict = true
		}
	}
	result := Assessment{Covered: len(covered), Uncovered: len(starts) - len(covered)}
	switch {
	case conflict:
		result.Status, result.Reason = "hold", "existing session needs content or boundary reconciliation"
	case result.Uncovered == 0:
		result.Status, result.Reason = "present", "all candidate boundaries have compatible visible records"
	case matched:
		result.Status, result.Reason = "hold", "partial visible coverage requires an append-only writer plan"
	default:
		result.Status, result.Reason = "unseen", "not observed in this inventory; remote and server coverage still required"
	}
	return result, nil
}
