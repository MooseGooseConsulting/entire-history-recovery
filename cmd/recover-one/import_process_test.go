package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/MooseGooseConsulting/entire-history-recovery/internal/snapshot"
)

func TestImportSubprocessCleansInventoryWorkspace(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "recover-one")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command("go", "build", "-o", binary, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build recover-one: %v: %s", err, out)
	}

	for _, tc := range []struct {
		name      string
		listingOK bool
	}{
		{name: "successful remote inventory", listingOK: true},
		{name: "failed remote listing", listingOK: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			snapshotDir := filepath.Join(root, "private-snapshots")
			if err := snapshot.EnsurePrivateDir(snapshotDir); err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(root, "source.jsonl")
			content := []byte("{\"type\":\"session_meta\",\"payload\":{\"id\":\"process-fixture\"}}\n" +
				"{\"type\":\"response_item\",\"payload\":{\"type\":\"message\",\"role\":\"user\",\"content\":[{\"type\":\"input_text\",\"text\":\"fixture\"}]}}\n")
			if err := os.WriteFile(source, content, 0600); err != nil {
				t.Fatal(err)
			}
			sealed, err := snapshot.SealCodex(source, snapshotDir, true)
			if err != nil {
				t.Fatal(err)
			}

			repo := filepath.Join(root, "repo")
			remote := filepath.Join(root, "remote.git")
			if tc.listingOK {
				runGit(t, root, "init", "--bare", "--quiet", remote)
				writer := filepath.Join(root, "writer")
				writerAnchor := initGitRepo(t, writer)
				writeCmd := exec.Command(binary, "import", "--snapshot", sealed.Path, "--repo", writer, "--anchor", writerAnchor, "--write")
				writeCmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
				if out, err := writeCmd.CombinedOutput(); err != nil {
					t.Fatalf("seed remote checkpoint: %v: %s", err, out)
				}
				assertSnapshotDirContainsOnly(t, snapshotDir, sealed.Path)
				runGit(t, writer, "remote", "add", "origin", remote)
				runGit(t, writer, "push", "--quiet", "origin", "main", "refs/heads/entire/checkpoints/v1:refs/heads/entire/checkpoints/v1")
				if listing := runGit(t, root, "ls-remote", remote, "refs/heads/entire/checkpoints/v1"); len(strings.TrimSpace(string(listing))) == 0 {
					t.Fatal("seeded checkpoint branch is missing from remote")
				}
				runGit(t, root, "clone", "--quiet", "--single-branch", "--branch", "main", remote, repo)
			} else {
				if err := os.Mkdir(repo, 0700); err != nil {
					t.Fatal(err)
				}
				initGitRepoExisting(t, repo)
				runGit(t, repo, "remote", "add", "origin", remote)
			}
			anchor := strings.TrimSpace(string(runGit(t, repo, "rev-parse", "HEAD")))

			cmd := exec.Command(binary, "import", "--snapshot", sealed.Path, "--repo", repo, "--anchor", anchor)
			cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
			stdout, err := cmd.Output()
			if tc.listingOK {
				if err != nil {
					if exit, ok := err.(*exec.ExitError); ok {
						t.Fatalf("import failed: %v: %s", err, exit.Stderr)
					}
					t.Fatal(err)
				}
				var result map[string]any
				if err := json.Unmarshal(stdout, &result); err != nil {
					t.Fatalf("worker output is not a result object: %v", err)
				}
				if result["TurnsSkipped"] != float64(1) || result["TurnsImported"] != float64(0) {
					t.Fatalf("remote-only checkpoint was not recognized as already imported: %v", result)
				}
				if strings.Contains(string(stdout), snapshotDir) {
					t.Fatal("private snapshot path leaked to stdout")
				}
			} else {
				if err == nil {
					t.Fatal("unreachable remote listing unexpectedly succeeded")
				}
				exit, ok := err.(*exec.ExitError)
				if !ok {
					t.Fatalf("command failed without an exit status: %v", err)
				}
				if !strings.Contains(string(exit.Stderr), "configured checkpoint ref listing failed") {
					t.Fatalf("command did not fail at remote ref listing: %v: %s", err, exit.Stderr)
				}
				if len(stdout) != 0 {
					t.Fatalf("failed import emitted success output: %s", stdout)
				}
			}

			assertSnapshotDirContainsOnly(t, snapshotDir, sealed.Path)
		})
	}
}

func initGitRepo(t *testing.T, repo string) string {
	t.Helper()
	if err := os.Mkdir(repo, 0700); err != nil {
		t.Fatal(err)
	}
	initGitRepoExisting(t, repo)
	return strings.TrimSpace(string(runGit(t, repo, "rev-parse", "HEAD")))
}

func initGitRepoExisting(t *testing.T, repo string) {
	t.Helper()
	runGit(t, repo, "init", "--quiet", "-b", "main")
	runGit(t, repo, "config", "user.name", "Fixture")
	runGit(t, repo, "config", "user.email", "fixture@example.invalid")
	runGit(t, repo, "-c", "commit.gpgsign=false", "commit", "--quiet", "--allow-empty", "-m", "initial")
}

func assertSnapshotDirContainsOnly(t *testing.T, snapshotDir, snapshotPath string) {
	t.Helper()
	entries, err := os.ReadDir(snapshotDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(snapshotPath) {
		t.Fatalf("worker inventory workspace was not cleaned: %v", entries)
	}
}

func runGit(t *testing.T, dir string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return out
}
