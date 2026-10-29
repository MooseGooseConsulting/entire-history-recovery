// recover-one seals one selected transcript, then previews or writes local
// checkpoints from that sealed snapshot. It does not sync to Entire's server.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/MooseGooseConsulting/entire-history-recovery/internal/recovery"
	"github.com/MooseGooseConsulting/entire-history-recovery/internal/snapshot"
)

const (
	inventoryWorkerEnv    = "ENTIRE_RECOVERY_IMPORT_WORKER"
	inventoryWorkspaceEnv = "ENTIRE_RECOVERY_IMPORT_WORKSPACE"
	inventoryWorkspaceTag = ".entire-import-inventory-workspace-"
)

func main() {
	if len(os.Args) < 2 {
		fail("usage: recover-one snapshot|import [flags]", 2)
	}
	switch os.Args[1] {
	case "snapshot":
		snapshotCommand(os.Args[2:])
	case "import":
		importCommand(os.Args[2:])
	default:
		fail("expected snapshot or import subcommand", 2)
	}
}

func snapshotCommand(args []string) {
	fs := flag.NewFlagSet("snapshot", flag.ExitOnError)
	source := fs.String("source", "", "selected inactive native Codex JSONL")
	dir := fs.String("dir", "", "private snapshot directory (default: user config directory)")
	inactive := fs.Bool("inactive", false, "assert that no agent is writing this source")
	_ = fs.Parse(args)
	if *source == "" || !*inactive || fs.NArg() != 0 {
		fail("required: snapshot --source PATH --inactive [--dir PRIVATE_DIR]", 2)
	}
	if *dir == "" {
		var err error
		*dir, err = snapshot.EnsureDefaultPrivateDir()
		if err != nil {
			fail(err.Error(), 1)
		}
	} else if err := snapshot.EnsurePrivateDir(*dir); err != nil {
		fail(err.Error(), 1)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	sealed, err := snapshot.SealCodexContext(ctx, *source, *dir, true)
	if err != nil {
		fail(err.Error(), 1)
	}
	_ = json.NewEncoder(os.Stdout).Encode(struct {
		Snapshot string `json:"snapshot"`
		SHA256   string `json:"sha256"`
		Bytes    int64  `json:"bytes"`
	}{sealed.Path, sealed.SHA256, sealed.Bytes})
}

func importCommand(args []string) {
	fs := flag.NewFlagSet("import", flag.ExitOnError)
	repo := fs.String("repo", "", "destination repository checkout")
	source := fs.String("snapshot", "", "path returned by snapshot command")
	anchor := fs.String("anchor", "", "full display-only commit SHA")
	write := fs.Bool("write", false, "write local checkpoints; default is preview")
	_ = fs.Parse(args)
	if *repo == "" || *source == "" || *anchor == "" || fs.NArg() != 0 {
		fail("required: import --snapshot SEALED_PATH --repo PATH --anchor FULL_SHA [--write]", 2)
	}
	base := filepath.Base(*source)
	if filepath.Ext(base) != ".jsonl" {
		fail("import accepts only content-addressed .jsonl snapshots, not raw sources", 2)
	}
	digest := strings.TrimSuffix(base, ".jsonl")
	if len(digest) != 64 {
		fail("snapshot filename does not contain a valid SHA-256", 2)
	}
	if err := snapshot.CheckPrivateDir(filepath.Dir(*source)); err != nil {
		fail(err.Error(), 1)
	}
	if err := snapshot.Verify(*source, digest); err != nil {
		fail(err.Error(), 1)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if os.Getenv(inventoryWorkerEnv) == "1" {
		workspace := os.Getenv(inventoryWorkspaceEnv)
		if workspace == "" {
			fail("import worker inventory workspace is missing", 1)
		}
		ctx, err := recovery.WithInventoryWorkspace(ctx, workspace)
		if err != nil {
			fail(err.Error(), 1)
		}
		writeImportResult(ctx, *repo, *source, *anchor, digest, !*write)
		return
	}
	workspaceParent := filepath.Dir(*source)
	argsForChild := []string{"import", "--repo", *repo, "--snapshot", *source, "--anchor", *anchor}
	if *write {
		argsForChild = append(argsForChild, "--write")
	}
	stdout, stderr, err := runImportWorker(ctx, workspaceParent, argsForChild)
	if err != nil {
		if len(stderr) > 0 {
			fmt.Fprint(os.Stderr, strings.ReplaceAll(string(stderr), workspaceParent, "<private snapshot directory>"))
		}
		fail(err.Error(), 1)
	}
	if _, err := os.Stdout.Write(stdout); err != nil {
		fail(fmt.Sprintf("write import result: %v", err), 1)
	}
}

func writeImportResult(ctx context.Context, repo, source, anchor, digest string, dryRun bool) {
	result, err := recovery.ImportCodexVerified(ctx, repo, source, anchor, digest, dryRun)
	if err != nil {
		fail(err.Error(), 1)
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		fail(err.Error(), 1)
	}
}

func runImportWorker(ctx context.Context, privateParent string, args []string) (stdout, stderr []byte, runErr error) {
	workspace, err := snapshot.CreatePrivateTempDir(privateParent, inventoryWorkspaceTag)
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		cleanupErr := removeOwnedInventoryWorkspace(workspace, privateParent)
		if cleanupErr != nil {
			stdout = nil
			runErr = errors.Join(runErr, fmt.Errorf("clean private inventory workspace: %w", cleanupErr))
		}
	}()

	executable, err := os.Executable()
	if err != nil {
		return nil, nil, fmt.Errorf("locate import worker executable: %w", err)
	}
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Env = withInternalInventoryEnv(os.Environ(), workspace)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err = cmd.Run()
	stdout = bytes.ReplaceAll(outBuf.Bytes(), []byte(workspace), []byte("<private inventory workspace>"))
	stderr = bytes.ReplaceAll(errBuf.Bytes(), []byte(workspace), []byte("<private inventory workspace>"))
	if err != nil {
		return nil, stderr, fmt.Errorf("import worker failed: %w", err)
	}
	return stdout, stderr, nil
}

func withInternalInventoryEnv(env []string, workspace string) []string {
	filtered := make([]string, 0, len(env)+2)
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if key == inventoryWorkerEnv || key == inventoryWorkspaceEnv {
			continue
		}
		filtered = append(filtered, entry)
	}
	return append(filtered, inventoryWorkerEnv+"=1", inventoryWorkspaceEnv+"="+workspace)
}

func removeOwnedInventoryWorkspace(workspace, privateParent string) error {
	parent, err := filepath.Abs(privateParent)
	if err != nil {
		return fmt.Errorf("resolve private snapshot directory: %w", err)
	}
	owned, err := filepath.Abs(workspace)
	if err != nil {
		return fmt.Errorf("resolve inventory workspace: %w", err)
	}
	if filepath.Dir(owned) != parent || !strings.HasPrefix(filepath.Base(owned), inventoryWorkspaceTag) {
		return errors.New("refusing to remove inventory workspace outside its owned snapshot directory")
	}
	if err := snapshot.CheckPrivateDir(parent); err != nil {
		return fmt.Errorf("recheck private snapshot directory: %w", err)
	}
	if err := snapshot.CheckPrivateDir(owned); err != nil {
		return fmt.Errorf("recheck owned inventory workspace: %w", err)
	}
	if err := os.RemoveAll(owned); err != nil {
		return err
	}
	if _, err := os.Lstat(owned); !os.IsNotExist(err) {
		if err == nil {
			return errors.New("inventory workspace remains after removal")
		}
		return fmt.Errorf("verify inventory workspace removal: %w", err)
	}
	return nil
}

func fail(message string, code int) { fmt.Fprintln(os.Stderr, message); os.Exit(code) }
