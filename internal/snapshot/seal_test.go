package snapshot

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T) (string, []byte) {
	t.Helper()
	data := []byte(`{"type":"session_meta","payload":{"id":"session-fixture"}}` + "\n" + `{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture"}]}}` + "\n")
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path, data
}

func TestSealCodexCopiesExactBytesAndIsIdempotentlyNonOverwriting(t *testing.T) {
	source, original := fixture(t)
	dir := filepath.Join(t.TempDir(), "private")
	snapshot, err := SealCodex(source, dir, true)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(snapshot.Path)
	if err != nil || string(got) != string(original) {
		t.Fatal("snapshot bytes differ from source")
	}
	sum := sha256.Sum256(original)
	if snapshot.SHA256 != hex.EncodeToString(sum[:]) || snapshot.SessionID != "session-fixture" || snapshot.Bytes != int64(len(original)) {
		t.Fatalf("bad snapshot metadata: %+v", snapshot)
	}
	if err := Verify(snapshot.Path, snapshot.SHA256); err != nil {
		t.Fatal(err)
	}
	reused, err := SealCodex(source, dir, true)
	if err != nil || reused.Path != snapshot.Path || reused.SHA256 != snapshot.SHA256 {
		t.Fatal("matching existing snapshot was not safely reused")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".snapshot-") {
			t.Fatal("temporary hard-link name remained after snapshot seal")
		}
	}
	after, err := os.ReadFile(source)
	if err != nil || string(after) != string(original) {
		t.Fatal("source changed")
	}
}

func TestSealRequiresInactiveAssertionAndRejectsDamagedSources(t *testing.T) {
	source, _ := fixture(t)
	dir := filepath.Join(t.TempDir(), "private")
	if _, err := SealCodex(source, dir, false); err == nil {
		t.Fatal("active source accepted")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("refusal created snapshot storage")
	}
	for _, bad := range [][]byte{
		[]byte(`{"type":"session_meta","payload":{"id":"x"}}` + "\n" + `{"type":"response_item","payload":`),
		[]byte(`{"type":"session_meta","payload":{"id":"x"}}` + "\nnot-json\n"),
		[]byte(`{"type":"session_meta","payload":{"id":"x"}}` + "\n" + `{"type":"session_meta","payload":{"id":"y"}}` + "\n"),
	} {
		if err := os.WriteFile(source, bad, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := SealCodex(source, dir, true); err == nil {
			t.Fatal("damaged source accepted")
		}
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return
	}
	if err != nil || len(entries) != 0 {
		t.Fatal("failed snapshot left publishable content")
	}
}

func TestVerifyRejectsTamperedSnapshot(t *testing.T) {
	source, _ := fixture(t)
	snapshot, err := SealCodex(source, filepath.Join(t.TempDir(), "private"), true)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(snapshot.Path, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(snapshot.Path, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Verify(snapshot.Path, snapshot.SHA256); err == nil {
		t.Fatal("tampering accepted")
	}
}

func TestVerifyRejectsSymlinkSnapshot(t *testing.T) {
	source, _ := fixture(t)
	snapshot, err := SealCodex(source, filepath.Join(t.TempDir(), "private"), true)
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(filepath.Dir(snapshot.Path), "linked.jsonl")
	if err := os.Symlink(snapshot.Path, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := Verify(link, snapshot.SHA256); err == nil {
		t.Fatal("snapshot symlink accepted")
	}
}

func TestVerifySourceDigestRejectsSameSizeRewrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.jsonl")
	original := []byte("first version")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteAt([]byte("other version"), 0); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(original)
	if err := verifySourceDigest(file, hex.EncodeToString(digest[:])); err == nil {
		t.Fatal("same-size rewritten source accepted")
	}
}
