//go:build windows

package recovery

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// This test makes process lifetime an explicit Windows boundary. Some upstream
// go-git/osfs handles can remain open after repository Close; production CLI
// invocations end the process after each destination import. The parent asserts
// that exit has released the destination before removing its .git tree.
func TestWindowsImportProcessReleasesRepository(t *testing.T) {
	if root := os.Getenv("RECOVERY_WINDOWS_CHILD_ROOT"); root != "" {
		if err := os.MkdirAll(root, 0700); err != nil {
			t.Fatal("create fixture destination")
		}
		runHermeticGitFixture(t, root, "init", "--quiet")
		runHermeticGitFixture(t, root, "commit", "--quiet", "--allow-empty", "-m", "fixture")
		anchorBytes := runHermeticGitFixture(t, root, "rev-parse", "HEAD")
		source := filepath.Join(filepath.Dir(root), "source.jsonl")
		if err := os.WriteFile(source, []byte(integrityHeader+integrityPrompt), 0600); err != nil {
			t.Fatal(err)
		}
		refsBefore := string(runHermeticGitFixture(t, root, "for-each-ref", "--format=%(refname) %(objectname)"))
		treeSHA := strings.TrimSpace(string(runHermeticGitFixture(t, root, "rev-parse", "HEAD^{tree}")))
		for _, badAnchor := range []string{"HEAD", "not-a-sha", strings.Repeat("a", 40), treeSHA} {
			for _, preview := range []bool{true, false} {
				result, err := ImportCodex(context.Background(), root, source, badAnchor, preview)
				if err == nil || result.TurnsImported != 0 {
					t.Fatal("invalid display anchor accepted by preview or write")
				}
			}
		}
		if string(runHermeticGitFixture(t, root, "for-each-ref", "--format=%(refname) %(objectname)")) != refsBefore {
			t.Fatal("rejected display anchor changed repository refs")
		}
		if _, err := ImportCodex(context.Background(), root, source, strings.TrimSpace(string(anchorBytes)), false); err != nil {
			t.Fatal(err)
		}
		return
	}

	base := testTempDir(t)
	globalConfig := filepath.Join(base, "empty.gitconfig")
	if err := os.WriteFile(globalConfig, nil, 0600); err != nil {
		t.Fatal("create empty global Git config")
	}
	hooksPath := filepath.Join(base, "empty-hooks")
	if err := os.Mkdir(hooksPath, 0700); err != nil {
		t.Fatal("create empty Git hooks directory")
	}
	root := filepath.Join(base, "destination")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestWindowsImportProcessReleasesRepository$")
	cmd.Env = replaceEnv(os.Environ(), map[string]string{
		"RECOVERY_WINDOWS_CHILD_ROOT":    root,
		"RECOVERY_TEST_EMPTY_GIT_CONFIG": globalConfig,
		"RECOVERY_TEST_EMPTY_GIT_HOOKS":  hooksPath,
	})
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child import: %v: %s", err, out)
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatalf("destination remained locked after importer process exited: %v", err)
	}
}
