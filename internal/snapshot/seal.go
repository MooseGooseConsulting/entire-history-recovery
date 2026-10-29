// Package snapshot creates immutable, content-addressed copies of selected
// inactive Codex transcripts before any import operation.
package snapshot

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// MaxSnapshotBytes bounds this experimental recovery path while the upstream
// importer and this validator still materialize transcript data in memory.
// It is a hard limit: inputs above it are rejected, never truncated.
const MaxSnapshotBytes int64 = 64 << 20

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
	return filepath.Join(base, "entire-history-recovery"), nil
}

func EnsurePrivateDir(dir string) error {
	if dir == "" {
		return errors.New("private directory path is required")
	}
	info, err := os.Lstat(dir)
	if err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("private directory must be a real directory")
		}
		return verifyPrivateDirectory(dir)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect private directory: %w", err)
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		return fmt.Errorf("create private directory (parent must exist): %w", err)
	}
	if err := protectNewDirectory(dir); err != nil {
		_ = os.Remove(dir)
		return err
	}
	return verifyPrivateDirectory(dir)
}

// CheckPrivateDir verifies an existing directory without creating it or
// changing its permissions. Import paths use this as a read-only trust gate.
func CheckPrivateDir(dir string) error {
	if dir == "" {
		return errors.New("private directory path is required")
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("inspect private directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("private directory must be a real directory")
	}
	return verifyPrivateDirectory(dir)
}

// SealCodex copies a user-selected inactive native transcript into dir.
// inactive must be an explicit operator assertion. This function detects
// changes during the copy but cannot prove a remote agent process is stopped;
// active sources require their native consistent-export mechanism.
func SealCodex(source, dir string, inactive bool) (Sealed, error) {
	return SealCodexContext(context.Background(), source, dir, inactive)
}

// SealCodexContext is the cancellable form of SealCodex. It retains the
// operator's inactivity assertion and does not claim to prove quiescence.
func SealCodexContext(ctx context.Context, source, dir string, inactive bool) (Sealed, error) {
	if !inactive {
		return Sealed{}, errors.New("source must be confirmed inactive; live writers require a consistent native export")
	}
	if err := ctx.Err(); err != nil {
		return Sealed{}, err
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
	if before.Size() > MaxSnapshotBytes {
		return Sealed{}, fmt.Errorf("source exceeds experimental snapshot limit of %d bytes", MaxSnapshotBytes)
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
	n, err := copyContext(ctx, io.MultiWriter(tmp, h), io.LimitReader(src, MaxSnapshotBytes+1))
	if err != nil {
		tmp.Close()
		return Sealed{}, fmt.Errorf("copy source snapshot: %w", err)
	}
	if n > MaxSnapshotBytes {
		tmp.Close()
		return Sealed{}, fmt.Errorf("source exceeds experimental snapshot limit of %d bytes", MaxSnapshotBytes)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return Sealed{}, fmt.Errorf("flush snapshot: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return Sealed{}, fmt.Errorf("close snapshot: %w", err)
	}
	digest := hex.EncodeToString(h.Sum(nil))
	if err := verifySourceDigest(ctx, src, digest); err != nil {
		return Sealed{}, err
	}
	fdAfter, err := src.Stat()
	if err != nil {
		return Sealed{}, fmt.Errorf("restat opened source: %w", err)
	}
	after, err := os.Stat(source)
	if err != nil {
		return Sealed{}, fmt.Errorf("restat source: %w", err)
	}
	if !os.SameFile(before, fdAfter) || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) || before.Size() != n {
		return Sealed{}, errors.New("source changed during snapshot; discard and retry from a consistent inactive copy")
	}
	data, err := readBounded(tmpName)
	if err != nil {
		return Sealed{}, fmt.Errorf("validate snapshot: %w", err)
	}
	sid, err := validateContext(ctx, data)
	if err != nil {
		return Sealed{}, err
	}
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
		if _, statErr := os.Lstat(final); statErr == nil {
			if verifyErr := Verify(final, digest); verifyErr != nil {
				return Sealed{}, fmt.Errorf("existing content-addressed snapshot is corrupt: %w", verifyErr)
			}
			return Sealed{Path: final, SHA256: digest, SessionID: sid, Bytes: n}, nil
		}
		return Sealed{}, fmt.Errorf("publish snapshot atomically: %w", err)
	}
	// The temporary name is another hard link to this inode. Remove it before
	// making the published inode read-only, especially on Windows.
	if err := os.Remove(tmpName); err != nil {
		_ = os.Remove(final)
		return Sealed{}, fmt.Errorf("remove snapshot staging link: %w", err)
	}
	if err := os.Chmod(final, 0400); err != nil {
		os.Remove(final)
		return Sealed{}, fmt.Errorf("seal snapshot read-only: %w", err)
	}
	return Sealed{Path: final, SHA256: digest, SessionID: sid, Bytes: n}, nil
}

// Verify checks that an apply path still names the exact sealed snapshot.
func Verify(path, digest string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect sealed snapshot: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("sealed snapshot must be a regular file, not a symlink")
	}
	data, err := readBounded(path)
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

func verifySourceDigest(ctx context.Context, src *os.File, expected string) error {
	if _, err := src.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind source for stability check: %w", err)
	}
	h := sha256.New()
	n, err := copyContext(ctx, h, io.LimitReader(src, MaxSnapshotBytes+1))
	if err != nil {
		return fmt.Errorf("rehash opened source: %w", err)
	}
	if n > MaxSnapshotBytes {
		return fmt.Errorf("source exceeds experimental snapshot limit of %d bytes", MaxSnapshotBytes)
	}
	if hex.EncodeToString(h.Sum(nil)) != expected {
		return errors.New("source changed during snapshot; discard and retry from a consistent inactive copy")
	}
	return nil
}

func validate(data []byte) (string, error) {
	return validateContext(context.Background(), data)
}

func readBounded(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxSnapshotBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > MaxSnapshotBytes {
		return nil, fmt.Errorf("snapshot exceeds experimental limit of %d bytes", MaxSnapshotBytes)
	}
	return data, nil
}

func copyContext(ctx context.Context, dst io.Writer, src io.Reader) (int64, error) {
	return io.Copy(dst, contextReader{ctx: ctx, src: src})
}

type contextReader struct {
	ctx context.Context
	src io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.src.Read(p)
	if ctxErr := r.ctx.Err(); ctxErr != nil {
		return n, ctxErr
	}
	return n, err
}

func validateContext(ctx context.Context, data []byte) (string, error) {
	var id string
	for i, rest := 0, data; ; i++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		end := bytes.IndexByte(rest, '\n')
		lineBytes := rest
		if end >= 0 {
			lineBytes = rest[:end]
			rest = rest[end+1:]
		} else {
			rest = nil
		}
		lineBytes = bytes.TrimSpace(lineBytes)
		if len(lineBytes) == 0 && i > 0 {
			if end < 0 {
				break
			}
			continue
		}
		var row struct {
			Type    string          `json:"type"`
			Payload json.RawMessage `json:"payload"`
		}
		if json.Unmarshal(lineBytes, &row) != nil || row.Type == "" || len(row.Payload) < 2 || row.Payload[0] != '{' {
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
		if end < 0 {
			break
		}
	}
	return id, nil
}
