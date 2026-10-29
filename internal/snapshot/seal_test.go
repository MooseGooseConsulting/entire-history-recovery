package snapshot

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
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
	if _, err := SealCodex(source, dir, true); err == nil {
		t.Fatal("existing snapshot path was overwritten")
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
