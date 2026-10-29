package recovery

import (
	"context"
	"crypto/sha256"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/agentimport"
	"github.com/entireio/cli/cmd/entire/cli/checkpoint"
	"github.com/entireio/cli/cmd/entire/cli/settings"
	git "github.com/go-git/go-git/v6"
)

func destinationHead(t *testing.T, root string) string {
	t.Helper()
	repo, err := git.PlainOpen(root)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	head, err := repo.Head()
	if err != nil {
		t.Fatal(err)
	}
	return head.Hash().String()
}

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
{"timestamp":"2020-01-01T00:00:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"fixture-turn"}}
{"timestamp":"2020-01-01T00:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture question"}]}}
{"timestamp":"2020-01-01T00:00:01Z","type":"turn_context","payload":{"turn_id":"fixture-turn","model":"fixture-model"}}
{"timestamp":"2020-01-01T00:00:01Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":10,"cached_input_tokens":0,"output_tokens":5,"total_tokens":15}}}}
{"timestamp":"2020-01-01T00:00:01Z","type":"response_item","payload":{"type":"reasoning","encrypted_content":"synthetic-ciphertext-marker"}}
{"timestamp":"2020-01-01T00:00:02Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"fixture answer"}]}}
{"timestamp":"2020-01-01T00:00:03Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"fixture-turn"}}
`
	if err := os.WriteFile(source, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(source, old, old); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	anchor := destinationHead(t, root)
	preview, err := ImportCodex(ctx, root, source, anchor, true)
	if err != nil || preview.SessionsScanned != 1 || preview.TurnsImported != 1 {
		t.Fatalf("preview: %+v, %v", preview, err)
	}
	result, err := ImportCodex(ctx, root, source, anchor, false)
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
	if data.Metadata.CreatedAt.Year() != 2020 || data.Metadata.CheckpointTranscriptStart != 2 {
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
	again, err := ImportCodex(ctx, root, source, anchor, false)
	if err != nil || again.TurnsImported != 0 || again.TurnsSkipped != 1 {
		t.Fatalf("repeat: %+v, %v", again, err)
	}
	after, err := os.ReadFile(source)
	if err != nil || string(after) != contents {
		t.Fatal("source changed")
	}
}

func TestImportRequiresExplicitDisplayAnchor(t *testing.T) {
	if _, err := ImportCodex(context.Background(), t.TempDir(), "unused", "", true); err == nil {
		t.Fatal("missing anchor accepted")
	}
}

func TestImportedCheckpointSurvivesIsolatedGitRemoteAndFreshClone(t *testing.T) {
	for _, backend := range []string{"git-branch", "git-refs"} {
		t.Run(backend, func(t *testing.T) { testCheckpointRemote(t, backend) })
	}
}

func testCheckpointRemote(t *testing.T, backend string) {
	t.Setenv("ENTIRE_CHECKPOINTS_PRIMARY", backend)
	base := t.TempDir()
	remote := filepath.Join(base, "remote.git")
	work := filepath.Join(base, "work")
	clone := filepath.Join(base, "clone")
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run(base, "init", "--quiet", "--bare", remote)
	run(base, "clone", "--quiet", remote, work)
	run(work, "commit", "--quiet", "--allow-empty", "-m", "fixture")
	run(work, "push", "--quiet", "origin", "HEAD")
	source := filepath.Join(base, "session.jsonl")
	contents := `{"timestamp":"2020-01-01T00:00:00Z","type":"session_meta","payload":{"id":"remote-fixture-123","cwd":"/missing/old"}}
{"timestamp":"2020-01-01T00:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture prompt"}]}}
`
	if err := os.WriteFile(source, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := ImportCodex(context.Background(), work, source, destinationHead(t, work), false)
	if err != nil || result.TurnsImported != 1 {
		t.Fatalf("import: %+v: %v", result, err)
	}
	// Explicit pushes here exercise only isolated Git transport, not Entire's
	// production sync, queue, remote discovery, or server verification.
	if backend == "git-branch" {
		run(work, "push", "--quiet", "origin", "refs/heads/entire/checkpoints/v1:refs/heads/entire/checkpoints/v1")
	} else {
		run(work, "push", "--quiet", "origin", "refs/entire/checkpoints/*:refs/entire/checkpoints/*")
	}
	run(base, "clone", "--quiet", remote, clone)
	if backend == "git-refs" {
		run(clone, "fetch", "--quiet", "origin", "+refs/entire/checkpoints/*:refs/entire/checkpoints/*")
	}
	repo, err := git.PlainOpen(clone)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	ctx := settings.WithWorktreeRoot(context.Background(), clone)
	stores, err := checkpoint.Open(ctx, repo, checkpoint.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	items, err := stores.Persistent.List(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("fresh clone checkpoint list: %d, %v", len(items), err)
	}
	data, err := stores.Persistent.ReadSessionContent(ctx, items[0].CheckpointID, 0)
	if err != nil || data.Metadata.SessionID != "remote-fixture-123" || !strings.Contains(string(data.Transcript), "fixture prompt") {
		t.Fatal("fresh clone did not read imported session")
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
	anchor := destinationHead(t, root)
	result, err := ImportCodex(context.Background(), root, source, anchor, false)
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
	second, err := ImportCodex(context.Background(), root, source, anchor, false)
	if err != nil || second.TurnsImported != 0 || second.TurnsSkipped != result.TurnsImported {
		t.Fatal("repeat import was not idempotent")
	}
	after, err := os.ReadFile(source)
	if err != nil || sha256.Sum256(after) != firstHash {
		t.Fatal("source changed")
	}
	t.Logf("local round trip: %d turns; agent recorded; model recorded for %d turns", result.TurnsImported, models)
	for _, adapter := range agentimport.All() {
		if adapter.Name() != "codex" {
			continue
		}
		turns, err := adapter.SplitTurns(agentimport.SessionFile{}, before)
		if err != nil {
			t.Fatal("could not analyze native prompt evidence")
		}
		evidence := CompareNativePrompts(before, turns)
		t.Logf("import units: %d; native completed user messages: %d; text overlaps: %d", evidence.ImporterUnits, evidence.NativeMessages, evidence.TextMatches)
	}
}

// TestPrivateCodexCohortProcessIsolated imports a private manifest in a child
// process so Windows releases upstream git handles before the parent removes it.
func TestPrivateCodexCohortProcessIsolated(t *testing.T) {
	manifest := os.Getenv("RECOVERY_TEST_CODEX_MANIFEST")
	if manifest == "" {
		t.Skip("no private cohort manifest selected")
	}
	if os.Getenv("RECOVERY_COHORT_CHILD") != "" {
		runPrivateCohortChild(t, manifest, os.Getenv("RECOVERY_COHORT_DEST"), os.Getenv("RECOVERY_COHORT_MODE"))
		return
	}
	paths, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal("cannot read private cohort manifest")
	}
	count := 0
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(paths)), "\n") {
		p := strings.TrimSuffix(strings.TrimSpace(line), "\r")
		if p == "" {
			continue
		}
		if seen[p] {
			t.Fatal("cohort repeats source path")
		}
		seen[p] = true
		count++
	}
	if count < 10 || count > 20 {
		t.Fatalf("cohort size outside 10-20: %d", count)
	}
	root := t.TempDir()
	for _, mode := range []string{"initial", "retry"} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestPrivateCodexCohortProcessIsolated$")
		cmd.Env = append(os.Environ(), "RECOVERY_COHORT_CHILD=1", "RECOVERY_COHORT_DEST="+root, "RECOVERY_COHORT_MODE="+mode)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("private cohort %s process failed: %v: %s", mode, err, out)
		}
	}
}

func runPrivateCohortChild(t *testing.T, manifest, root, mode string) {
	t.Helper()
	paths, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal("cannot read cohort manifest")
	}
	var sources []string
	for _, line := range strings.Split(strings.TrimSpace(string(paths)), "\n") {
		p := strings.TrimSuffix(strings.TrimSpace(line), "\r")
		if p != "" {
			sources = append(sources, p)
		}
	}
	if mode == "initial" {
		if err := os.MkdirAll(root, 0700); err != nil {
			t.Fatal("create private destination")
		}
		for _, args := range [][]string{{"init", "--quiet"}, {"-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--quiet", "--allow-empty", "-m", "private cohort destination"}} {
			cmd := exec.Command("git", args...)
			cmd.Dir = root
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("prepare destination: %v: %s", err, out)
			}
		}
	}
	anchor := destinationHead(t, root)
	total, models := 0, 0
	sessionIDs := map[string]bool{}
	hashes := make([][32]byte, len(sources))
	want := make([]int, len(sources))
	for i, source := range sources {
		before, err := os.ReadFile(source)
		if err != nil {
			t.Fatalf("source %d unavailable", i)
		}
		hashes[i] = sha256.Sum256(before)
		id, err := validateCodexSnapshot(before)
		if err != nil {
			t.Fatalf("source %d invalid", i)
		}
		if sessionIDs[id] {
			t.Fatalf("duplicate session identity at item %d", i)
		}
		sessionIDs[id] = true
		result, err := ImportCodex(context.Background(), root, source, anchor, mode == "retry")
		if err != nil {
			t.Fatalf("source %d import failed", i)
		}
		if mode == "initial" {
			if result.SessionsScanned != 1 || result.TurnsImported == 0 {
				t.Fatalf("source %d not imported", i)
			}
			want[i] = result.TurnsImported
			total += result.TurnsImported
		} else if result.TurnsImported != 0 || result.TurnsSkipped == 0 {
			t.Fatalf("source %d retry duplicated or skipped nothing", i)
		}
	}
	repo, err := git.PlainOpen(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx := settings.WithWorktreeRoot(context.Background(), root)
	stores, err := checkpoint.Open(ctx, repo, checkpoint.OpenOptions{})
	if err != nil {
		_ = repo.Close()
		t.Fatal(err)
	}
	items, err := stores.Persistent.List(ctx)
	if err != nil || len(items) == 0 {
		_ = repo.Close()
		t.Fatal("checkpoint readback unavailable")
	}
	seenIDs := map[string]bool{}
	for _, item := range items {
		c, err := stores.Persistent.ReadSessionContent(ctx, item.CheckpointID, 0)
		if err != nil || c.Metadata.SessionID == "" || string(c.Metadata.Agent) != "Codex" || len(c.Transcript) == 0 {
			_ = repo.Close()
			t.Fatal("checkpoint metadata/content unavailable")
		}
		seenIDs[c.Metadata.SessionID] = true
		if c.Metadata.Model != "" {
			models++
		}
	}
	if len(seenIDs) != len(sources) {
		_ = repo.Close()
		t.Fatal("not all source sessions read back")
	}
	if err := repo.Close(); err != nil {
		t.Fatal("close reader")
	}
	countPath := filepath.Join(root, "cohort-checkpoint-count")
	if mode == "initial" {
		if err := os.WriteFile(countPath, []byte(strconv.Itoa(len(items))), 0600); err != nil {
			t.Fatal("record local checkpoint count")
		}
	} else {
		wantBytes, err := os.ReadFile(countPath)
		if err != nil {
			t.Fatal("initial checkpoint count unavailable")
		}
		wantCount, err := strconv.Atoi(strings.TrimSpace(string(wantBytes)))
		if err != nil || len(items) != wantCount {
			t.Fatal("retry changed the checkpoint count")
		}
	}
	for i, source := range sources {
		after, err := os.ReadFile(source)
		if err != nil || sha256.Sum256(after) != hashes[i] {
			t.Fatalf("source %d changed", i)
		}
	}
	t.Logf("private cohort %s passed: %d sessions, %d stored checkpoints, %d model-bearing records; original sources unchanged", mode, len(sources), len(items), models)
}

func TestCompareNativePromptsCountsMultiplicity(t *testing.T) {
	turns := []agentimport.Turn{{Prompt: "same"}, {Prompt: "same"}, {Prompt: "other"}}
	data := []byte(`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","content":[{"text":"same"}]}}}
{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","content":[{"text":"unknown"}]}}}`)
	got := CompareNativePrompts(data, turns)
	if got.ImporterUnits != 3 || got.NativeMessages != 2 || got.TextMatches != 1 || got.UnmatchedUnits != 2 || got.UnmatchedNativeMessages != 1 {
		t.Fatalf("unexpected evidence counts: %+v", got)
	}
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

func TestSingleTurnModelWithoutNativeCorrelationRemainsUnknown(t *testing.T) {
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
	if err != nil || len(turns) != 1 || turns[0].Model != "" {
		t.Fatal("unverified model was attributed")
	}
}

func TestNativeTaskIDsCorrelateModelsAcrossEventOrders(t *testing.T) {
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
{"type":"event_msg","payload":{"type":"task_started","turn_id":"a"}}
{"type":"turn_context","payload":{"turn_id":"a","model":"model-a"}}
{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"one"}]}}
{"type":"event_msg","payload":{"type":"task_complete","turn_id":"a"}}
{"type":"event_msg","payload":{"type":"task_started","turn_id":"b"}}
{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"two"}]}}
{"type":"turn_context","payload":{"turn_id":"b","model":"model-b"}}
{"type":"event_msg","payload":{"type":"task_complete","turn_id":"b"}}
{"type":"event_msg","payload":{"type":"task_started","turn_id":"c"}}
{"type":"turn_context","payload":{"turn_id":"wrong","model":"model-wrong"}}
{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"three"}]}}
{"type":"event_msg","payload":{"type":"task_complete","turn_id":"c"}}
`
	turns, err := (fixedSource{Importer: adapter}).SplitTurns(agentimport.SessionFile{SessionID: "test"}, []byte(input))
	if err != nil || len(turns) != 3 {
		t.Fatalf("turns %d: %v", len(turns), err)
	}
	if turns[0].Model != "model-a" || turns[1].Model != "model-b" || turns[2].Model != "" {
		t.Fatalf("native task model correlation wrong")
	}
}

func TestNativeOverlappingTasksRemainUnattributed(t *testing.T) {
	var adapter agentimport.Importer
	for _, candidate := range agentimport.All() {
		if candidate.Name() == "codex" {
			adapter = candidate
			break
		}
	}
	input := `{"type":"session_meta","payload":{"id":"test","cwd":"/old"}}
{"type":"event_msg","payload":{"type":"task_started","turn_id":"a"}}
{"type":"event_msg","payload":{"type":"task_started","turn_id":"b"}}
{"type":"turn_context","payload":{"turn_id":"b","model":"model-b"}}
{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"one"}]}}
{"type":"event_msg","payload":{"type":"task_complete","turn_id":"b"}}
`
	turns, err := (fixedSource{Importer: adapter}).SplitTurns(agentimport.SessionFile{SessionID: "test"}, []byte(input))
	if err != nil || len(turns) != 1 || turns[0].Model != "" {
		t.Fatal("overlapping task was attributed")
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
	result, err := ImportCodex(context.Background(), root, source, destinationHead(t, root), true)
	if err != nil || result.SessionsScanned != 1 || result.TurnsImported != 0 {
		t.Fatalf("header-only preview: %+v, %v", result, err)
	}
}
