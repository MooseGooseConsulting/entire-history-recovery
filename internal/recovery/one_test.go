package recovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/agentimport"
	"github.com/entireio/cli/cmd/entire/cli/checkpoint"
	"github.com/entireio/cli/cmd/entire/cli/checkpoint/id"
	"github.com/entireio/cli/cmd/entire/cli/settings"
	"github.com/entireio/cli/redact"
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

type syntheticCoverageInventory struct {
	info    checkpoint.CheckpointInfo
	summary *checkpoint.CheckpointSummary
	content *checkpoint.SessionContent
	readErr error
}

func (s syntheticCoverageInventory) List(context.Context) ([]checkpoint.CheckpointInfo, error) {
	return []checkpoint.CheckpointInfo{s.info}, nil
}
func (s syntheticCoverageInventory) Read(context.Context, id.CheckpointID) (*checkpoint.CheckpointSummary, error) {
	if s.readErr != nil {
		return nil, s.readErr
	}
	return s.summary, nil
}
func (s syntheticCoverageInventory) ReadSessionContent(context.Context, id.CheckpointID, int) (*checkpoint.SessionContent, error) {
	return s.content, nil
}

func syntheticCodexSource(t *testing.T, root string) (string, string) {
	t.Helper()
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
	contents := `{"timestamp":"2020-01-01T00:00:00Z","type":"session_meta","payload":{"id":"session-coverage-fixture","cwd":"/deleted/checkout"}}
{"timestamp":"2020-01-01T00:00:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"fixture-turn"}}
{"timestamp":"2020-01-01T00:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture question"}]}}
{"timestamp":"2020-01-01T00:00:01Z","type":"turn_context","payload":{"turn_id":"fixture-turn","model":"fixture-model"}}
{"timestamp":"2020-01-01T00:00:02Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"fixture answer"}]}}
{"timestamp":"2020-01-01T00:00:03Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"fixture-turn"}}
`
	source := filepath.Join(testTempDir(t), "source.jsonl")
	if err := os.WriteFile(source, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	return source, destinationHead(t, root)
}

func TestImportSkipsExactCoverageUnderDifferentCheckpointID(t *testing.T) {
	root := testTempDir(t)
	source, anchor := syntheticCodexSource(t, root)
	full, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	adapter := agentimport.All()[0]
	for _, candidate := range agentimport.All() {
		if candidate.Name() == "codex" {
			adapter = candidate
			break
		}
	}
	file := agentimport.SessionFile{Path: source, SessionID: "session-coverage-fixture"}
	turns, err := adapter.SplitTurns(file, full)
	if err != nil || len(turns) != 1 {
		t.Fatalf("split: %d turns, %v", len(turns), err)
	}
	transcript, err := redact.JSONLBytes(checkpoint.SanitizeTranscriptForAgentType(adapter.AgentType(), full))
	if err != nil {
		t.Fatal(err)
	}
	cid := id.MustCheckpointID("abcdef123456") // intentionally unrelated to the importer-derived ID
	inv := syntheticCoverageInventory{
		info:    checkpoint.CheckpointInfo{CheckpointID: cid},
		summary: &checkpoint.CheckpointSummary{CheckpointID: cid, Sessions: []checkpoint.SessionFilePaths{{}}},
		content: &checkpoint.SessionContent{Metadata: checkpoint.Metadata{SessionID: file.SessionID, Agent: adapter.AgentType(), Kind: "live", CheckpointTranscriptStart: turns[0].LineStart}, Transcript: transcript.Bytes()},
	}
	result, err := importCodexWithInventory(withTestInventoryWorkspace(t, context.Background()), root, source, anchor, "", false, inv)
	if err != nil || result.SessionsScanned != 1 || result.TurnsSkipped != 1 || result.TurnsImported != 0 {
		t.Fatalf("duplicate result: %+v, %v", result, err)
	}
	assertNoCheckpointWrites(t, root)
}

func TestImportHoldsDamagedOrPartialCoverageWithoutWriting(t *testing.T) {
	for _, tc := range []struct {
		name    string
		damaged bool
	}{{"damaged inventory", true}, {"partial conflicting coverage", false}} {
		t.Run(tc.name, func(t *testing.T) {
			root := testTempDir(t)
			source, anchor := syntheticCodexSource(t, root)
			full, err := os.ReadFile(source)
			if err != nil {
				t.Fatal(err)
			}
			adapter := agentimport.All()[0]
			for _, candidate := range agentimport.All() {
				if candidate.Name() == "codex" {
					adapter = candidate
					break
				}
			}
			file := agentimport.SessionFile{Path: source, SessionID: "session-coverage-fixture"}
			turns, err := adapter.SplitTurns(file, full)
			if err != nil || len(turns) != 1 {
				t.Fatalf("split: %d turns, %v", len(turns), err)
			}
			cid := id.MustCheckpointID("abcdef123456")
			inv := syntheticCoverageInventory{info: checkpoint.CheckpointInfo{CheckpointID: cid}, summary: &checkpoint.CheckpointSummary{CheckpointID: cid, Sessions: []checkpoint.SessionFilePaths{{}}}}
			if tc.damaged {
				inv.readErr = errors.New("fixture read failure")
			} else {
				inv.content = &checkpoint.SessionContent{Metadata: checkpoint.Metadata{SessionID: file.SessionID, Agent: adapter.AgentType(), CheckpointTranscriptStart: turns[0].LineStart}, Transcript: []byte("different transcript\n")}
			}
			if _, err := importCodexWithInventory(withTestInventoryWorkspace(t, context.Background()), root, source, anchor, "", false, inv); err == nil {
				t.Fatal("unsafe import was not held")
			}
			assertNoCheckpointWrites(t, root)
		})
	}
}

func assertNoCheckpointWrites(t *testing.T, root string) {
	t.Helper()
	repo, err := git.PlainOpen(root)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	ctx := settings.WithWorktreeRoot(withTestInventoryWorkspace(t, context.Background()), root)
	stores, err := checkpoint.Open(ctx, repo, checkpoint.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	items, err := stores.Persistent.List(ctx)
	if err != nil || len(items) != 0 {
		t.Fatalf("checkpoint write occurred: %d items, %v", len(items), err)
	}
}

func TestImportOldCodexSessionIntoDifferentRepository(t *testing.T) {
	root := testTempDir(t)
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
	source := filepath.Join(testTempDir(t), "old.jsonl")
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
	ctx := withTestInventoryWorkspace(t, context.Background())
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
	if _, err := ImportCodex(withTestInventoryWorkspace(t, context.Background()), testTempDir(t), "unused", "", true); err == nil {
		t.Fatal("missing anchor accepted")
	}
}

func TestVerifiedImportRejectsWrongSnapshotHashBeforeCheckpointWrite(t *testing.T) {
	root := testTempDir(t)
	source := filepath.Join(testTempDir(t), "source.jsonl")
	contents := `{"type":"session_meta","payload":{"id":"verified-fixture"}}` + "\n" + `{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture"}]}}` + "\n"
	if err := os.WriteFile(source, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportCodexVerified(withTestInventoryWorkspace(t, context.Background()), root, source, strings.Repeat("0", 40), strings.Repeat("0", 64), false); err == nil {
		t.Fatal("mismatched expected hash accepted")
	}
}

func TestImportedCheckpointSurvivesIsolatedGitRemoteAndFreshClone(t *testing.T) {
	for _, backend := range []string{"git-branch", "git-refs"} {
		t.Run(backend, func(t *testing.T) { testCheckpointRemote(t, backend) })
	}
}

func testCheckpointRemote(t *testing.T, backend string) {
	t.Setenv("ENTIRE_CHECKPOINTS_PRIMARY", backend)
	base := testTempDir(t)
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
	result, err := ImportCodex(withTestInventoryWorkspace(t, context.Background()), work, source, destinationHead(t, work), false)
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
	ctx := settings.WithWorktreeRoot(withTestInventoryWorkspace(t, context.Background()), clone)
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
	root := testTempDir(t)
	for _, args := range [][]string{{"init", "--quiet"}, {"commit", "--quiet", "--allow-empty", "-m", "local fixture"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid")
		if _, err := cmd.CombinedOutput(); err != nil {
			t.Fatal("could not prepare isolated repository")
		}
	}
	anchor := destinationHead(t, root)
	result, err := ImportCodex(withTestInventoryWorkspace(t, context.Background()), root, source, anchor, false)
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
	ctx := settings.WithWorktreeRoot(withTestInventoryWorkspace(t, context.Background()), root)
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
	second, err := ImportCodex(withTestInventoryWorkspace(t, context.Background()), root, source, anchor, false)
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
		runPrivateCohortChild(t, manifest, os.Getenv("RECOVERY_COHORT_MANIFEST_SHA256"), os.Getenv("RECOVERY_COHORT_DEST"), os.Getenv("RECOVERY_COHORT_MODE"))
		return
	}
	paths, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal("cannot read private cohort manifest")
	}
	count := 0
	seen := map[string]bool{}
	var normalized strings.Builder
	for _, line := range strings.Split(strings.TrimSpace(string(paths)), "\n") {
		p := strings.TrimSuffix(strings.TrimSpace(line), "\r")
		if p == "" {
			continue
		}
		if seen[p] {
			t.Fatal("cohort repeats source path")
		}
		contents, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("cohort source %d unavailable", count)
		}
		sessionID, err := validateCodexSnapshot(contents)
		if err != nil {
			t.Fatalf("cohort source %d invalid", count)
		}
		if seen["session:"+sessionID] {
			t.Fatalf("cohort repeats native session identity at item %d", count)
		}
		seen["session:"+sessionID] = true
		seen[p] = true
		normalized.WriteString(p)
		normalized.WriteByte('\n')
		count++
	}
	if count < 10 || count > 20 {
		t.Fatalf("cohort size outside 10-20: %d", count)
	}
	root := testTempDir(t)
	globalConfig := filepath.Join(root, "empty.gitconfig")
	if err := os.WriteFile(globalConfig, nil, 0600); err != nil {
		t.Fatal("create empty global Git config")
	}
	hooksPath := filepath.Join(root, "empty-hooks")
	if err := os.Mkdir(hooksPath, 0700); err != nil {
		t.Fatal("create empty Git hooks directory")
	}
	privateManifest := filepath.Join(root, "sources.manifest")
	manifestBytes := []byte(normalized.String())
	// Keep the file writable so Windows can remove the containing temporary
	// directory after both child processes exit. The child verifies the pinned
	// digest before reading it, which detects accidental manifest changes.
	if err := os.WriteFile(privateManifest, manifestBytes, 0600); err != nil {
		t.Fatal("seal normalized private manifest")
	}
	manifestHash := sha256.Sum256(manifestBytes)
	manifestDigest := hex.EncodeToString(manifestHash[:])
	for _, mode := range []string{"initial", "retry"} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestPrivateCodexCohortProcessIsolated$", "-test.v")
		cmd.Env = replaceEnv(os.Environ(), map[string]string{
			"RECOVERY_COHORT_CHILD":           "1",
			"RECOVERY_TEST_CODEX_MANIFEST":    privateManifest,
			"RECOVERY_COHORT_MANIFEST_SHA256": manifestDigest,
			"RECOVERY_COHORT_DEST":            root,
			"RECOVERY_COHORT_MODE":            mode,
			"RECOVERY_TEST_EMPTY_GIT_CONFIG":  globalConfig,
			"RECOVERY_TEST_EMPTY_GIT_HOOKS":   hooksPath,
		})
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("private cohort %s process failed: %v: %s", mode, err, out)
		} else {
			t.Logf("%s", out)
		}
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatalf("parent could not remove the exact cohort temporary directory after child exit: %v", err)
	}
}

func runPrivateCohortChild(t *testing.T, manifest, expectedManifestDigest, root, mode string) {
	t.Helper()
	paths, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal("cannot read cohort manifest")
	}
	digest := sha256.Sum256(paths)
	if hex.EncodeToString(digest[:]) != expectedManifestDigest {
		t.Fatal("private manifest changed after parent validation")
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
		runHermeticGitFixture(t, root, "init", "--quiet")
		runHermeticGitFixture(t, root, "commit", "--quiet", "--allow-empty", "-m", "private cohort destination")
	}
	anchor := destinationHead(t, root)
	total, models := 0, 0
	sessionIDs := map[string]bool{}
	expectedContent := map[string][32]byte{}
	expectedBoundaries := map[string]map[int]string{}
	var adapter agentimport.Importer
	for _, available := range agentimport.All() {
		if available.Name() == "codex" {
			adapter = available
		}
	}
	if adapter == nil {
		t.Fatal("Codex parser missing")
	}
	hashes := make([][32]byte, len(sources))
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
		turns, err := (fixedSource{Importer: adapter}).SplitTurns(agentimport.SessionFile{SessionID: id}, before)
		if err != nil {
			t.Fatalf("source %d boundaries unavailable", i)
		}
		expectedBoundaries[id] = map[int]string{}
		for _, turn := range turns {
			if _, duplicate := expectedBoundaries[id][turn.LineStart]; duplicate {
				t.Fatal("source parser repeated a boundary")
			}
			expectedBoundaries[id][turn.LineStart] = turn.Model
		}
		result, err := ImportCodex(withTestInventoryWorkspace(t, context.Background()), root, source, anchor, false)
		if err != nil {
			t.Fatalf("source %d import failed", i)
		}
		if mode == "initial" {
			if result.SessionsScanned != 1 || result.TurnsImported == 0 {
				t.Fatalf("source %d not imported", i)
			}
			total += result.TurnsImported
		} else if result.TurnsImported != 0 || result.TurnsSkipped != len(turns) {
			t.Fatalf("source %d retry duplicated or skipped nothing", i)
		}
		redacted, err := redact.JSONLBytes(checkpoint.SanitizeTranscriptForAgentType("Codex", before))
		if err != nil {
			t.Fatalf("source %d comparison redaction failed", i)
		}
		expectedContent[id] = sha256.Sum256(redacted.Bytes())
	}
	repo, err := git.PlainOpen(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx := settings.WithWorktreeRoot(withTestInventoryWorkspace(t, context.Background()), root)
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
	if mode == "initial" && len(items) != total {
		t.Fatal("stored count does not match successful imports")
	}
	seenIDs := map[string]bool{}
	for _, item := range items {
		c, err := stores.Persistent.ReadSessionContent(ctx, item.CheckpointID, 0)
		if err != nil || c.Metadata.SessionID == "" || string(c.Metadata.Agent) != "Codex" || len(c.Transcript) == 0 {
			_ = repo.Close()
			t.Fatal("checkpoint metadata/content unavailable")
		}
		seenIDs[c.Metadata.SessionID] = true
		if !sessionIDs[c.Metadata.SessionID] || sha256.Sum256(c.Transcript) != expectedContent[c.Metadata.SessionID] {
			t.Fatal("stored transcript does not match expected sanitized source")
		}
		boundaries := expectedBoundaries[c.Metadata.SessionID]
		expectedModel, exists := boundaries[c.Metadata.CheckpointTranscriptStart]
		if !exists {
			t.Fatal("unexpected or duplicate stored boundary")
		}
		delete(boundaries, c.Metadata.CheckpointTranscriptStart)
		if c.Metadata.Model != expectedModel {
			t.Fatal("stored model does not match native task evidence, including unknown values")
		}
		if c.Metadata.Model != "" {
			models++
		}
	}
	if len(seenIDs) != len(sources) {
		_ = repo.Close()
		t.Fatal("not all source sessions read back")
	}
	for _, boundaries := range expectedBoundaries {
		if len(boundaries) != 0 {
			t.Fatal("source boundary missing from readback")
		}
	}
	if err := repo.Close(); err != nil {
		t.Fatal("close reader")
	}
	countPath := filepath.Join(root, "cohort-checkpoint-count")
	refsCommand := exec.Command("git", "for-each-ref", "--format=%(refname) %(objectname)")
	refsCommand.Dir = root
	refs, err := refsCommand.Output()
	if err != nil {
		t.Fatal("cannot verify checkpoint ref stability")
	}
	refsPath := filepath.Join(root, "cohort-checkpoint-refs")
	if mode == "initial" {
		if err := os.WriteFile(refsPath, refs, 0600); err != nil {
			t.Fatal("cannot record checkpoint refs")
		}
		if err := os.WriteFile(countPath, []byte(strconv.Itoa(len(items))), 0600); err != nil {
			t.Fatal("record local checkpoint count")
		}
	} else {
		priorRefs, err := os.ReadFile(refsPath)
		if err != nil || string(priorRefs) != string(refs) {
			t.Fatal("retry changed checkpoint refs")
		}
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
	root := testTempDir(t)
	for _, args := range [][]string{{"init", "--quiet"}, {"commit", "--quiet", "--allow-empty", "-m", "fixture"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid")
		if _, err := cmd.CombinedOutput(); err != nil {
			t.Fatal(err)
		}
	}
	source := filepath.Join(testTempDir(t), "header.jsonl")
	if err := os.WriteFile(source, []byte(`{"type":"session_meta","payload":{"id":"valid-header"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := ImportCodex(withTestInventoryWorkspace(t, context.Background()), root, source, destinationHead(t, root), true)
	if err != nil || result.SessionsScanned != 1 || result.TurnsImported != 0 {
		t.Fatalf("header-only preview: %+v, %v", result, err)
	}
}
