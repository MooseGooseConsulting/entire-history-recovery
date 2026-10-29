// Package snapshot creates immutable, content-addressed copies of selected
// inactive Codex transcripts before any import operation.
package snapshot

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"time"
)

// Sealed identifies a validated private snapshot. The source pathname is
// intentionally absent: callers should retain provenance in their private
// ledger rather than placing it in logs or public artifacts.
type Sealed struct {
	Path      string
	SHA256    string
	SessionID string
	Bytes     int64
}

func DefaultDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config directory: %w", err)
	}
	return filepath.Join(base, "entire-history-recovery", "snapshots"), nil
}

func EnsurePrivateDir(dir string) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create private directory: %w", err)
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return fmt.Errorf("restrict private directory: %w", err)
	}
	if os.PathSeparator != '\\' {
		return nil
	}
	current, err := user.Current()
	if err != nil || !strings.HasPrefix(current.Uid, "S-") {
		return errors.New("cannot establish current Windows user SID")
	}
	grant := "*" + current.Uid + ":(OI)(CI)F"
	if out, err := exec.Command("icacls", dir, "/inheritance:r", "/grant:r", grant).CombinedOutput(); err != nil {
		return fmt.Errorf("set private Windows ACL: %w: %s", err, strings.TrimSpace(string(out)))
	}
	out, err := exec.Command("icacls", dir).CombinedOutput()
	if err != nil {
		return errors.New("cannot verify private Windows ACL")
	}
	acl := strings.ToLower(string(out))
	for _, broad := range []string{"everyone", "authenticated users", "\\users:", "built-in\\users"} {
		if strings.Contains(acl, broad) {
			return fmt.Errorf("directory ACL includes broad principal %q", broad)
		}
	}
	if !strings.Contains(acl, strings.ToLower(current.Username)) && !strings.Contains(acl, strings.ToLower(current.Uid)) {
		return errors.New("private Windows ACL does not show current owner")
	}
	return nil
}

// SealCodex copies a user-selected inactive native transcript into dir.
// inactive must be an explicit operator assertion. This function detects
// changes during the copy but cannot prove a remote agent process is stopped;
// active sources require their native consistent-export mechanism.
func SealCodex(source, dir string, inactive bool) (Sealed, error) {
	if !inactive {
		return Sealed{}, errors.New("source must be confirmed inactive; live writers require a consistent native export")
	}
	src, err := os.Open(source)
	if err != nil {
		return Sealed{}, fmt.Errorf("open source: %w", err)
	}
	defer src.Close()
	before, err := src.Stat()
	if err != nil {
		return Sealed{}, fmt.Errorf("stat source: %w", err)
	}
	if !before.Mode().IsRegular() {
		return Sealed{}, errors.New("source must be a regular file")
	}
	if err := EnsurePrivateDir(dir); err != nil {
		return Sealed{}, fmt.Errorf("secure snapshot directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".snapshot-*.tmp")
	if err != nil {
		return Sealed{}, fmt.Errorf("create snapshot temporary file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return Sealed{}, fmt.Errorf("restrict temporary snapshot: %w", err)
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), src)
	if err != nil {
		tmp.Close()
		return Sealed{}, fmt.Errorf("copy source snapshot: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return Sealed{}, fmt.Errorf("flush snapshot: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return Sealed{}, fmt.Errorf("close snapshot: %w", err)
	}
	after, err := os.Stat(source)
	if err != nil {
		return Sealed{}, fmt.Errorf("restat source: %w", err)
	}
	if !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) || before.Size() != n {
		return Sealed{}, errors.New("source changed during snapshot; discard and retry from a consistent inactive copy")
	}
	data, err := os.ReadFile(tmpName)
	if err != nil {
		return Sealed{}, fmt.Errorf("validate snapshot: %w", err)
	}
	sid, err := validate(data)
	if err != nil {
		return Sealed{}, err
	}
	digest := hex.EncodeToString(h.Sum(nil))
	if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != digest {
		return Sealed{}, errors.New("snapshot digest verification failed")
	}
	if strings.ContainsAny(sid, `/\\`) {
		return Sealed{}, errors.New("invalid session ID")
	}
	final := filepath.Join(dir, digest+".jsonl")
	// Hard-link publication is atomic and fails if the content-addressed name
	// already exists; never overwrite an earlier sealed snapshot.
	if err := os.Link(tmpName, final); err != nil {
		if _, statErr := os.Stat(final); statErr == nil {
			return Sealed{}, errors.New("snapshot already exists; verify its sealed contents before reuse")
		}
		return Sealed{}, fmt.Errorf("publish snapshot atomically: %w", err)
	}
	if err := os.Chmod(final, 0400); err != nil {
		os.Remove(final)
		return Sealed{}, fmt.Errorf("seal snapshot read-only: %w", err)
	}
	_ = os.Chtimes(final, time.Time{}, time.Time{})
	return Sealed{Path: final, SHA256: digest, SessionID: sid, Bytes: n}, nil
}

// Verify checks that an apply path still names the exact sealed snapshot.
func Verify(path, digest string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read sealed snapshot: %w", err)
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != digest {
		return errors.New("sealed snapshot digest mismatch")
	}
	if _, err := validate(data); err != nil {
		return err
	}
	return nil
}

func validate(data []byte) (string, error) {
	var id string
	for i, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" && i > 0 {
			continue
		}
		var row struct {
			Type    string          `json:"type"`
			Payload json.RawMessage `json:"payload"`
		}
		if json.Unmarshal([]byte(line), &row) != nil || row.Type == "" || len(row.Payload) < 2 || row.Payload[0] != '{' {
			return "", fmt.Errorf("invalid Codex JSONL record at line %d", i+1)
		}
		if i == 0 {
			var meta struct {
				ID string `json:"id"`
			}
			if row.Type != "session_meta" || json.Unmarshal(row.Payload, &meta) != nil || meta.ID == "" {
				return "", errors.New("source has no valid first-line session_meta ID")
			}
			id = meta.ID
		} else if row.Type == "session_meta" {
			return "", fmt.Errorf("second session header at line %d", i+1)
		}
	}
	return id, nil
}
