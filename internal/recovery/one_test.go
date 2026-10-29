package recovery

import (
	"context"
	"crypto/sha256"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/agentimport"
	"github.com/entireio/cli/cmd/entire/cli/checkpoint"
	"github.com/entireio/cli/cmd/entire/cli/settings"
	git "github.com/go-git/go-git/v6"
)

func TestImportOldCodexSessionIntoDifferentRepository(t *testing.T) {
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "--quiet")
	run("commit", "--quiet", "--allow-empty", "-m", "fixture")
	source := filepath.Join(t.TempDir(), "old.jsonl")
	contents := `{"timestamp":"2020-01-01T00:00:00Z","type":"session_meta","payload":{"id":"session-fixture-123","cwd":"/deleted/checkout"}}
{"timestamp":"2020-01-01T00:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture question"}]}}
{"timestamp":"2020-01-01T00:00:01Z","type":"turn_context","payload":{"model":"fixture-model"}}
{"timestamp":"2020-01-01T00:00:01Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":10,"cached_input_tokens":0,"output_tokens":5,"total_tokens":15}}}}
{"timestamp":"2020-01-01T00:00:01Z","type":"response_item","payload":{"type":"reasoning","encrypted_content":"synthetic-ciphertext-marker"}}
{"timestamp":"2020-01-01T00:00:02Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"fixture answer"}]}}
`
	if err := os.WriteFile(source, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(source, old, old); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	preview, err := ImportCodex(ctx, root, source, true)
	if err != nil || preview.SessionsScanned != 1 || preview.TurnsImported != 1 {
		t.Fatalf("preview: %+v, %v", preview, err)
	}
	result, err := ImportCodex(ctx, root, source, false)
	if err != nil || result.SessionsScanned != 1 || result.TurnsImported != 1 {
		t.Fatalf("import: %+v, %v", result, err)
	}
	repo, err := git.PlainOpen(root)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	ctx = settings.WithWorktreeRoot(ctx, root)
	stores, err := checkpoint.Open(ctx, repo, checkpoint.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	items, err := stores.Persistent.List(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("list: %d, %v", len(items), err)
	}
	data, err := stores.Persistent.ReadSessionContent(ctx, items[0].CheckpointID, 0)
	if err != nil {
		t.Fatal(err)
	}
	rootSummary, err := stores.Persistent.Read(ctx, items[0].CheckpointID)
	if err != nil || !rootSummary.Imported || len(rootSummary.LinkedCommits) != 0 {
		t.Fatal("imported checkpoint or attribution state incorrect")
	}
	if data.Metadata.SessionID != "session-fixture-123" || string(data.Metadata.Agent) != "Codex" {
		t.Fatalf("identity: %+v", data.Metadata)
	}
	if data.Metadata.Kind != "imported" || data.Metadata.CommitSHA == "" {
		t.Fatal("import kind or display anchor missing")
	}
	if data.Metadata.CreatedAt.Year() != 2020 || data.Metadata.CheckpointTranscriptStart != 1 {
		t.Fatalf("time or offset: %+v", data.Metadata)
	}
	if data.Metadata.Model != "fixture-model" {
		t.Fatalf("model: %q", data.Metadata.Model)
	}
	if data.Metadata.TokenUsage == nil || data.Metadata.TokenUsage.OutputTokens != 5 {
		t.Fatalf("usage not preserved: %+v", data.Metadata.TokenUsage)
	}
	if strings.Contains(string(data.Transcript), "synthetic-ciphertext-marker") {
		t.Fatal("encrypted Codex payload was not sanitized")
	}
	if !strings.Contains(string(data.Transcript), "fixture question") || !strings.Contains(string(data.Transcript), "fixture answer") {
		t.Fatal("stored transcript lost content")
	}
	again, err := ImportCodex(ctx, root, source, false)
	if err != nil || again.TurnsImported != 0 || again.TurnsSkipped != 1 {
		t.Fatalf("repeat: %+v, %v", again, err)
	}
	after, err := os.ReadFile(source)
	if err != nil || string(after) != contents {
		t.Fatal("source changed")
	}
}

// TestPrivateHistoricalSession is an explicit local-only probe; no transcript
// content is printed or committed, and the temporary repository is removed by
// the test runner. A server-side round trip requires a separate test.
func TestPrivateHistoricalSession(t *testing.T) {
	source := os.Getenv("RECOVERY_TEST_CODEX_SOURCE")
	if source == "" {
		t.Skip("no private session selected")
	}
	before, err := os.ReadFile(source)
	if err != nil {
		t.Fatal("cannot read selected session")
	}
	firstHash := sha256.Sum256(before)
	root := t.TempDir()
	for _, args := range [][]string{{"init", "--quiet"}, {"commit", "--quiet", "--allow-empty", "-m", "local fixture"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid")
		if _, err := cmd.CombinedOutput(); err != nil {
			t.Fatal("could not prepare isolated repository")
		}
	}
	result, err := ImportCodex(context.Background(), root, source, false)
	if err != nil {
		t.Fatal("private session import failed")
	}
	if result.SessionsScanned != 1 || result.TurnsImported == 0 {
		t.Fatal("selected session has no importable turns")
	}
	repo, err := git.PlainOpen(root)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	ctx := settings.WithWorktreeRoot(context.Background(), root)
	stores, err := checkpoint.Open(ctx, repo, checkpoint.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	items, err := stores.Persistent.List(ctx)
	if err != nil || len(items) != result.TurnsImported {
		t.Fatal("checkpoint count does not match imported turns")
	}
	models := 0
	for _, item := range items {
		data, err := stores.Persistent.ReadSessionContent(ctx, item.CheckpointID, 0)
		if err != nil || data.Metadata.SessionID == "" || string(data.Metadata.Agent) != "Codex" || len(data.Transcript) == 0 {
			t.Fatal("session metadata or transcript unavailable")
		}
		if data.Metadata.Model != "" {
			models++
		}
	}
	second, err := ImportCodex(context.Background(), root, source, false)
	if err != nil || second.TurnsImported != 0 || second.TurnsSkipped != result.TurnsImported {
		t.Fatal("repeat import was not idempotent")
	}
	after, err := os.ReadFile(source)
	if err != nil || sha256.Sum256(after) != firstHash {
		t.Fatal("source changed")
	}
	t.Logf("local round trip: %d turns; agent recorded; model recorded for %d turns", result.TurnsImported, models)
}

func TestModelRequiresUnambiguousContextInEachTurn(t *testing.T) {
	var adapter agentimport.Importer
	for _, candidate := range agentimport.All() {
		if candidate.Name() == "codex" {
			adapter = candidate
			break
		}
	}
	if adapter == nil {
		t.Fatal("Codex importer missing")
	}
	input := `{"type":"session_meta","payload":{"id":"test","cwd":"/old"}}
{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"one"}]}}
{"type":"turn_context","payload":{"model":"model-a"}}
{"type":"turn_context","payload":{"model":"model-b"}}
{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"two"}]}}
{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"answer"}]}}
`
	turns, err := (fixedSource{Importer: adapter}).SplitTurns(agentimport.SessionFile{SessionID: "test"}, []byte(input))
	if err != nil || len(turns) != 2 {
		t.Fatalf("turns %d: %v", len(turns), err)
	}
	if turns[0].Model != "" || turns[1].Model != "" {
		t.Fatalf("ambiguous or absent model was populated")
	}
}

func TestModelBeforePromptNeverAssignedToPriorTurn(t *testing.T) {
	var adapter agentimport.Importer
	for _, candidate := range agentimport.All() {
		if candidate.Name() == "codex" {
			adapter = candidate
			break
		}
	}
	if adapter == nil {
		t.Fatal("Codex importer missing")
	}
	input := `{"type":"session_meta","payload":{"id":"test","cwd":"/old"}}
{"type":"turn_context","payload":{"model":"model-a"}}
{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"one"}]}}
{"type":"turn_context","payload":{"model":"model-b"}}
{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"two"}]}}
`
	turns, err := (fixedSource{Importer: adapter}).SplitTurns(agentimport.SessionFile{SessionID: "test"}, []byte(input))
	if err != nil || len(turns) != 2 {
		t.Fatalf("turns %d: %v", len(turns), err)
	}
	if turns[0].Model != "" || turns[1].Model != "" {
		t.Fatal("model assigned without verified turn correlation")
	}
}

func TestSingleTurnModelBeforePrompt(t *testing.T) {
	var adapter agentimport.Importer
	for _, candidate := range agentimport.All() {
		if candidate.Name() == "codex" {
			adapter = candidate
			break
		}
	}
	if adapter == nil {
		t.Fatal("Codex importer missing")
	}
	input := `{"type":"session_meta","payload":{"id":"test","cwd":"/old"}}
{"type":"turn_context","payload":{"model":"model-a"}}
{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"one"}]}}
`
	turns, err := (fixedSource{Importer: adapter}).SplitTurns(agentimport.SessionFile{SessionID: "test"}, []byte(input))
	if err != nil || len(turns) != 1 || turns[0].Model != "model-a" {
		t.Fatalf("single-turn model not preserved")
	}
}

func TestHeaderWithoutNewline(t *testing.T) {
	root := t.TempDir()
	for _, args := range [][]string{{"init", "--quiet"}, {"commit", "--quiet", "--allow-empty", "-m", "fixture"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid")
		if _, err := cmd.CombinedOutput(); err != nil {
			t.Fatal(err)
		}
	}
	source := filepath.Join(t.TempDir(), "header.jsonl")
	if err := os.WriteFile(source, []byte(`{"type":"session_meta","payload":{"id":"valid-header"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := ImportCodex(context.Background(), root, source, true)
	if err != nil || result.SessionsScanned != 1 || result.TurnsImported != 0 {
		t.Fatalf("header-only preview: %+v, %v", result, err)
	}
}
