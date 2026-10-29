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
		for _, args := range [][]string{{"init", "--quiet"}, {"-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--quiet", "--allow-empty", "-m", "fixture"}} {
			cmd := exec.Command("git", args...)
			cmd.Dir = root
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git fixture: %v %s", err, out)
			}
		}
		cmd := exec.Command("git", "rev-parse", "HEAD")
		cmd.Dir = root
		anchorBytes, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		source := filepath.Join(filepath.Dir(root), "source.jsonl")
		if err := os.WriteFile(source, []byte(integrityHeader+integrityPrompt), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := ImportCodex(context.Background(), root, source, stringTrimSpace(anchorBytes), false); err != nil {
			t.Fatal(err)
		}
		return
	}

	base := t.TempDir()
	root := filepath.Join(base, "destination")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestWindowsImportProcessReleasesRepository$")
	cmd.Env = append(os.Environ(), "RECOVERY_WINDOWS_CHILD_ROOT="+root)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child import: %v: %s", err, out)
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatalf("destination remained locked after importer process exited: %v", err)
	}
}

func stringTrimSpace(b []byte) string { return strings.TrimSpace(string(b)) }
