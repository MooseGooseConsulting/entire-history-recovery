package snapshot

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsurePrivateDirCreatesAndVerifiesOwnedDirectory(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "private")
	if err := EnsurePrivateDir(dir); err != nil {
		t.Fatal(err)
	}
	if err := verifyPrivateDirectory(dir); err != nil {
		t.Fatalf("new private directory rejected: %v", err)
	}
	if err := EnsurePrivateDir(dir); err != nil {
		t.Fatalf("safe existing directory rejected: %v", err)
	}
}

func TestEnsurePrivateDirRejectsUnsafeExistingDirectoryWithoutChangingIt(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "existing")
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsurePrivateDir(dir); err == nil {
		t.Fatal("unsafe existing directory accepted")
	}
	after, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if before.Mode().Perm() != after.Mode().Perm() {
		t.Fatalf("existing permissions changed: %o -> %o", before.Mode().Perm(), after.Mode().Perm())
	}
}
