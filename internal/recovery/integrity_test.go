package recovery

import (
	"context"
	"crypto/sha256"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/agentimport"
	"github.com/entireio/cli/cmd/entire/cli/checkpoint"
	"github.com/entireio/cli/cmd/entire/cli/settings"
	git "github.com/go-git/go-git/v6"
)

func integrityRepo(t *testing.T) string {
	t.Helper()
	root := testTempDir(t)
	for _, args := range [][]string{{"init", "--quiet"}, {"-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--quiet", "--allow-empty", "-m", "fixture"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("fixture: %v %s", err, out)
		}
	}
	return root
}

func TestSourceChangeBetweenValidationAndParsingRejected(t *testing.T) {
	original := []byte(integrityHeader + integrityPrompt)
	digest := sha256.Sum256(original)
	adapter := fixedSource{digest: &digest}
	// The missing underlying importer also proves rejection happens before parsing.
	if _, err := adapter.SplitTurns(agentimport.SessionFile{}, append(original, '\n')); err == nil {
		t.Fatal("changed source accepted")
	}
}

func TestAppendOnlyRecoveryIsHeldWhenTranscriptChanges(t *testing.T) {
	for _, backend := range []string{"git-branch", "git-refs"} {
		t.Run(backend, func(t *testing.T) {
			t.Setenv("ENTIRE_CHECKPOINTS_PRIMARY", backend)
			root := integrityRepo(t)
			source := filepath.Join(testTempDir(t), "session.jsonl")
			original := integrityHeader + integrityPrompt
			if err := os.WriteFile(source, []byte(original), 0600); err != nil {
				t.Fatal(err)
			}
			anchor := destinationHead(t, root)
			first, err := ImportCodex(context.Background(), root, source, anchor, false)
			if err != nil || first.TurnsImported != 1 {
				t.Fatalf("initial: %+v %v", first, err)
			}
			// The candidate now has a different full transcript. The prototype
			// cannot safely distinguish an append from a partial/conflicting
			// historical snapshot, so it must hold without writing.
			if err := os.WriteFile(source, []byte(original+integrityPrompt), 0600); err != nil {
				t.Fatal(err)
			}
			second, err := ImportCodex(context.Background(), root, source, anchor, false)
			if err == nil || second.TurnsImported != 0 {
				t.Fatalf("changed transcript was not held: %+v %v", second, err)
			}
			repo, err := git.PlainOpen(root)
			if err != nil {
				t.Fatal(err)
			}
			stores, err := checkpoint.Open(settings.WithWorktreeRoot(context.Background(), root), repo, checkpoint.OpenOptions{})
			if err != nil {
				_ = repo.Close()
				t.Fatal(err)
			}
			items, err := stores.Persistent.List(context.Background())
			_ = repo.Close()
			if err != nil || len(items) != 1 {
				t.Fatalf("held append changed checkpoint inventory: %d items, %v", len(items), err)
			}
		})
	}
}

func TestSnapshotAcceptsCRLFAndUnknownEventTypes(t *testing.T) {
	input := strings.ReplaceAll(integrityHeader+`{"type":"future_event","payload":{"new_field":true}}`+"\n"+integrityPrompt, "\n", "\r\n")
	id, err := validateCodexSnapshot([]byte(input))
	if err != nil || id != "integrity-fixture" {
		t.Fatalf("valid forward-compatible snapshot rejected: %v", err)
	}
}

func TestChangingCheckpointBackendDoesNotDuplicateSession(t *testing.T) {
	for _, initial := range []string{"git-branch", "git-refs"} {
		t.Run(initial, func(t *testing.T) {
			t.Setenv("ENTIRE_CHECKPOINTS_PRIMARY", initial)
			root := integrityRepo(t)
			source := filepath.Join(testTempDir(t), "session.jsonl")
			if err := os.WriteFile(source, []byte(integrityHeader+integrityPrompt), 0600); err != nil {
				t.Fatal(err)
			}
			anchor := destinationHead(t, root)
			first, err := ImportCodex(context.Background(), root, source, anchor, false)
			if err != nil || first.TurnsImported != 1 {
				t.Fatalf("initial: %+v %v", first, err)
			}
			next := "git-refs"
			if initial == next {
				next = "git-branch"
			}
			t.Setenv("ENTIRE_CHECKPOINTS_PRIMARY", next)
			second, err := ImportCodex(context.Background(), root, source, anchor, false)
			if err != nil || second.TurnsImported != 0 || second.TurnsSkipped != 1 {
				t.Fatalf("backend migration: %+v %v", second, err)
			}
		})
	}
}

const integrityHeader = `{"type":"session_meta","payload":{"id":"integrity-fixture"}}` + "\n"
const integrityPrompt = `{"timestamp":"2020-01-01T00:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture"}]}}` + "\n"

func TestDamagedSessionRejectedBeforeAnyCheckpointWrite(t *testing.T) {
	for name, damage := range map[string]string{
		"truncated_tail": `{"type":"response_item","payload":`,
		"invalid_middle": "not-json\n" + integrityPrompt,
		"null_record":    "null\n",
		"array_record":   "[]\n",
		"missing_type":   `{"payload":{}}`,
		"second_session": integrityHeader,
	} {
		t.Run(name, func(t *testing.T) {
			root := integrityRepo(t)
			source := filepath.Join(testTempDir(t), "session.jsonl")
			content := integrityHeader + integrityPrompt + damage
			if err := os.WriteFile(source, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			for _, dry := range []bool{true, false} {
				result, err := ImportCodex(context.Background(), root, source, destinationHead(t, root), dry)
				if err == nil || result.TurnsImported != 0 {
					t.Fatalf("damaged session accepted: dry=%v result=%+v error=%v", dry, result, err)
				}
			}
			cmd := exec.Command("git", "for-each-ref", "--format=%(refname)")
			cmd.Dir = root
			refs, err := cmd.Output()
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(refs), "entire/") {
				t.Fatal("damaged source created checkpoint refs")
			}
			after, err := os.ReadFile(source)
			if err != nil || string(after) != content {
				t.Fatal("source changed")
			}
		})
	}
}

func TestDryRunDoesNotCreateCheckpointRefs(t *testing.T) {
	root := integrityRepo(t)
	source := filepath.Join(testTempDir(t), "session.jsonl")
	if err := os.WriteFile(source, []byte(integrityHeader+integrityPrompt), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := ImportCodex(context.Background(), root, source, destinationHead(t, root), true)
	if err != nil || result.TurnsImported != 1 {
		t.Fatalf("preview: %+v %v", result, err)
	}
	cmd := exec.Command("git", "for-each-ref", "--format=%(refname)")
	cmd.Dir = root
	refs, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(refs), "entire/") {
		t.Fatal("dry run created checkpoint refs")
	}
}
