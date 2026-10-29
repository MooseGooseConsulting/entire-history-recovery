package reconcile

import (
	"context"
	"errors"
	"testing"

	cp "github.com/entireio/cli/api/checkpoint"
	"github.com/entireio/cli/cmd/entire/cli/checkpoint/id"
)

type fixtureReader struct {
	listError, readError error
	reads                []int
}

var fixtureID = id.MustCheckpointID("abcdef123456")

func (r *fixtureReader) List(context.Context) ([]cp.CheckpointInfo, error) {
	return []cp.CheckpointInfo{{CheckpointID: fixtureID, ListedStub: true}}, r.listError
}
func (r *fixtureReader) Read(context.Context, id.CheckpointID) (*cp.CheckpointSummary, error) {
	return &cp.CheckpointSummary{CheckpointID: fixtureID, Sessions: make([]cp.SessionFilePaths, 2)}, nil
}
func (r *fixtureReader) ReadSessionContent(_ context.Context, _ id.CheckpointID, index int) (*cp.SessionContent, error) {
	r.reads = append(r.reads, index)
	if index == 1 && r.readError != nil {
		return nil, r.readError
	}
	session := "unrelated"
	if index == 1 {
		session = "recovered"
	}
	return &cp.SessionContent{Metadata: cp.Metadata{SessionID: session, Agent: "Codex", CheckpointTranscriptStart: 1}, Transcript: []byte("header\nprompt\nanswer\n")}, nil
}

func TestHydrateAllSessionsAndRecognizeLiveCoverage(t *testing.T) {
	reader := &fixtureReader{}
	records, err := ReadVisible(context.Background(), reader)
	if err != nil || len(records) != 2 || len(reader.reads) != 2 || reader.reads[1] != 1 {
		t.Fatalf("all-session inventory: %v", err)
	}
	// The live record has no imported kind and its arbitrary checkpoint ID is
	// unrelated to the importer's deterministic ID; the second session matches.
	candidate := Candidate{SessionID: "recovered", Agent: "Codex", Transcript: []byte("header\nprompt\nanswer\n"), Starts: []int{1}}
	got, err := AssessExact(candidate, records)
	if err != nil || got.Status != "present" || got.Covered != 1 {
		t.Fatalf("live coverage: %+v %v", got, err)
	}
	otherTurn := records[1]
	otherTurn.Content.Metadata.CheckpointTranscriptStart = 2
	got, err = AssessExact(candidate, append(records, otherTurn))
	if err != nil || got.Status != "present" || got.Covered != 1 {
		t.Fatalf("additional existing turn blocked covered candidate: %+v %v", got, err)
	}
	candidate.Transcript = []byte("header\nprompt\nanswer\nnew prompt\n")
	candidate.Starts = []int{1, 3}
	got, err = AssessExact(candidate, records)
	if err != nil || got.Status != "hold" {
		t.Fatalf("different snapshot guessed safe: %+v %v", got, err)
	}
}

func TestInventoryFailuresAndPartialCoverageNeverBecomeAbsence(t *testing.T) {
	for _, reader := range []*fixtureReader{{listError: errors.New("denied")}, {readError: errors.New("missing blob")}} {
		records, err := ReadVisible(context.Background(), reader)
		if err == nil || records != nil {
			t.Fatal("failed inventory returned partial data")
		}
	}
	candidate := Candidate{SessionID: "recovered", Agent: "Codex", Transcript: []byte("header\nprompt\nanswer\nprompt\n"), Starts: []int{1, 3}}
	records := []Record{{Content: cp.SessionContent{Metadata: cp.Metadata{SessionID: "recovered", Agent: "Codex", CheckpointTranscriptStart: 1}, Transcript: candidate.Transcript}}}
	got, err := AssessExact(candidate, records)
	if err != nil || got.Status != "hold" || got.Covered != 1 || got.Uncovered != 1 {
		t.Fatalf("partial: %+v %v", got, err)
	}
	got, err = AssessExact(candidate, nil)
	if err != nil || got.Status != "unseen" {
		t.Fatalf("empty: %+v %v", got, err)
	}
}
