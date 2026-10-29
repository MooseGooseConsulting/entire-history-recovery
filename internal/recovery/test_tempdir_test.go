package recovery

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MooseGooseConsulting/entire-history-recovery/internal/snapshot"
)

const testParentRootEnv = "ENTIRE_RECOVERY_TEST_PARENT_ROOT"

// testTempDir normally has the same lifetime as t.TempDir. On Windows the
// package TestMain re-runs the suite in a child process and leaves its files
// under a parent-owned root, so open Git handles are released before removal.
func testTempDir(t *testing.T) string {
	t.Helper()
	if parentRoot := testParentRoot(); parentRoot != "" {
		dir, err := snapshot.CreatePrivateTempDir(parentRoot, "t-")
		if err != nil {
			t.Fatalf("create test directory under parent-owned root: %v", err)
		}
		return filepath.Clean(dir)
	}
	return t.TempDir()
}

func testParentRoot() string {
	return os.Getenv(testParentRootEnv)
}
