package recovery

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MooseGooseConsulting/entire-history-recovery/internal/reconcile"
	"github.com/entireio/cli/cmd/entire/cli/agentimport"
	"github.com/entireio/cli/cmd/entire/cli/checkpoint"
	"github.com/entireio/cli/cmd/entire/cli/settings"
	"github.com/entireio/cli/redact"
	git "github.com/go-git/go-git/v6"
)

func runStrictGit(t *testing.T, dir string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid", "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return out
}

func configureCheckpointBackend(t *testing.T, root, backend string) {
	t.Helper()
	settingsDir := filepath.Join(root, ".entire")
	if err := os.MkdirAll(settingsDir, 0700); err != nil {
		t.Fatal(err)
	}
	body := `{"enabled":true,"checkpoints":{"primary":{"type":"` + backend + `"}}}`
	if err := os.WriteFile(filepath.Join(settingsDir, "settings.json"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func writeStrictCodexSource(t *testing.T) string {
	t.Helper()
	source := filepath.Join(t.TempDir(), "session.jsonl")
	text := `{"type":"session_meta","payload":{"id":"remote-only-session"}}
{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"remote coverage probe"}]}}
`
	if err := os.WriteFile(source, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	return source
}

func TestStrictConfiguredRemoteFindsRemoteOnlyCheckpointAndRetryIsNoop(t *testing.T) {
	for _, backend := range []string{"git-branch", "git-refs"} {
		t.Run(backend, func(t *testing.T) {
			emptyConfig := filepath.Join(t.TempDir(), "empty.gitconfig")
			if err := os.WriteFile(emptyConfig, nil, 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("GIT_CONFIG_GLOBAL", emptyConfig)
			bare := filepath.Join(t.TempDir(), "remote.git")
			runStrictGit(t, filepath.Dir(bare), "init", "--bare", "--quiet", bare)
			writer := filepath.Join(t.TempDir(), "writer")
			if err := os.MkdirAll(writer, 0700); err != nil {
				t.Fatal(err)
			}
			runStrictGit(t, writer, "init", "--quiet", "-b", "main")
			runStrictGit(t, writer, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false", "commit", "--quiet", "--allow-empty", "-m", "initial")
			runStrictGit(t, writer, "remote", "add", "origin", bare)
			runStrictGit(t, writer, "push", "--quiet", "-u", "origin", "main")
			configureCheckpointBackend(t, writer, backend)
			source := writeStrictCodexSource(t)
			anchor := destinationHead(t, writer)
			imported, err := ImportCodex(context.Background(), writer, source, anchor, false)
			if err != nil || imported.TurnsImported != 1 {
				t.Fatalf("writer import: %+v %v", imported, err)
			}
			pushCheckpointStorage(t, writer, bare, backend)

			reader := filepath.Join(t.TempDir(), "reader")
			runStrictGit(t, filepath.Dir(reader), "clone", "--quiet", "--single-branch", "--branch", "main", bare, reader)
			configureCheckpointBackend(t, reader, backend)
			readerRepo, err := git.PlainOpen(reader)
			if err != nil {
				t.Fatal(err)
			}
			adapter := codexImporter(t)
			full, err := os.ReadFile(source)
			if err != nil {
				_ = readerRepo.Close()
				t.Fatal(err)
			}
			transcript, err := redact.JSONLBytes(checkpoint.SanitizeTranscriptForAgentType(adapter.AgentType(), full))
			if err != nil {
				_ = readerRepo.Close()
				t.Fatal(err)
			}
			turns, err := adapter.SplitTurns(agentimport.SessionFile{Path: source, SessionID: "remote-only-session"}, full)
			if err != nil {
				_ = readerRepo.Close()
				t.Fatal(err)
			}
			starts := make([]int, 0, len(turns))
			for _, turn := range turns {
				starts = append(starts, turn.LineStart)
			}
			proof, err := AssessCodexStorage(settings.WithWorktreeRoot(context.Background(), reader), reader, readerRepo, reconcile.Candidate{
				SessionID: "remote-only-session", Agent: string(adapter.AgentType()), Transcript: transcript.Bytes(), Starts: starts,
			})
			_ = readerRepo.Close()
			if err != nil || proof.Coverage.Status != "present" || !proof.ConfiguredRemotesComplete || proof.RemoteTargets == 0 {
				t.Fatalf("reader did not verify remote-only checkpoint: %+v %v", proof, err)
			}

			retry := filepath.Join(t.TempDir(), "retry")
			runStrictGit(t, filepath.Dir(retry), "clone", "--quiet", "--single-branch", "--branch", "main", bare, retry)
			configureCheckpointBackend(t, retry, backend)
			before := runStrictGit(t, retry, "for-each-ref", "--format=%(refname) %(objectname)")
			retried, err := ImportCodex(context.Background(), retry, source, destinationHead(t, retry), false)
			if err != nil || retried.TurnsImported != 0 || retried.TurnsSkipped != 1 {
				t.Fatalf("remote-only retry: %+v %v", retried, err)
			}
			after := runStrictGit(t, retry, "for-each-ref", "--format=%(refname) %(objectname)")
			if string(before) != string(after) {
				t.Fatal("retry changed local refs")
			}
			if strings.Contains(string(after), "refs/entire/checkpoints") || strings.Contains(string(after), "refs/heads/entire/checkpoints/v1") {
				t.Fatal("retry installed checkpoint refs into the checkout")
			}
		})
	}
}

func TestStrictInventoryHoldsSameSessionWithDifferentAgent(t *testing.T) {
	candidate := reconcile.Candidate{
		SessionID:  "session-1",
		Agent:      "codex",
		Transcript: []byte("candidate\n"),
		Starts:     []int{0},
	}
	assessment, err := reconcile.AssessExact(candidate, []reconcile.Record{{
		CheckpointID: "checkpoint-1",
		SessionIndex: 0,
		Content: checkpoint.SessionContent{Metadata: checkpoint.Metadata{
			SessionID: "session-1",
			Agent:     "claude",
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if assessment.Status != "hold" {
		t.Fatalf("same session under a different agent must hold, got %+v", assessment)
	}
}

func codexImporter(t *testing.T) agentimport.Importer {
	t.Helper()
	for _, imp := range agentimport.All() {
		if imp.Name() == "codex" {
			return imp
		}
	}
	t.Fatal("Codex importer unavailable")
	return nil
}

func pushCheckpointStorage(t *testing.T, writer, bare, backend string) {
	t.Helper()
	if backend == "git-branch" {
		runStrictGit(t, writer, "push", "--quiet", bare, "refs/heads/entire/checkpoints/v1:refs/heads/entire/checkpoints/v1")
		return
	}
	refs := strings.Fields(string(runStrictGit(t, writer, "for-each-ref", "--format=%(refname)", "refs/entire/checkpoints")))
	if len(refs) == 0 {
		t.Fatal("writer has no git-refs checkpoints")
	}
	args := []string{"push", "--quiet", bare}
	for _, ref := range refs {
		args = append(args, ref+":"+ref)
	}
	runStrictGit(t, writer, args...)
}
