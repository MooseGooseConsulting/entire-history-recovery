//go:build !windows

package snapshot

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureDefaultPrivateDirCreatesMissingConfigParent(t *testing.T) {
	config := filepath.Join(t.TempDir(), "not-created-yet", "config")
	t.Setenv("XDG_CONFIG_HOME", config)
	dir, err := EnsureDefaultPrivateDir()
	if err != nil {
		t.Fatal(err)
	}
	if dir != filepath.Join(config, "entire-history-recovery") {
		t.Fatalf("unexpected default directory: %s", dir)
	}
	for _, path := range []string{filepath.Dir(config), config, dir} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("missing created config path %s: %v", path, err)
		}
		if info.Mode().Perm() != 0700 {
			t.Fatalf("new directory %s has mode %04o, want 0700", path, info.Mode().Perm())
		}
	}
}

func TestEnsureDefaultPrivateDirLeavesExistingConfigPermissionsUnchanged(t *testing.T) {
	config := filepath.Join(t.TempDir(), "config")
	if err := os.Mkdir(config, 0751); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", config)
	if _, err := EnsureDefaultPrivateDir(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(config)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0751 {
		t.Fatalf("existing config permissions changed to %04o, want 0751", got)
	}
}
