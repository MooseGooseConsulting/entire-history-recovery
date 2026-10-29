// recover-one seals one selected transcript, then previews or writes local
// checkpoints from that sealed snapshot. It does not sync to Entire's server.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/MooseGooseConsulting/entire-history-recovery/internal/recovery"
	"github.com/MooseGooseConsulting/entire-history-recovery/internal/snapshot"
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
		*dir, err = snapshot.DefaultDir()
		if err != nil {
			fail(err.Error(), 1)
		}
	}
	if err := snapshot.EnsurePrivateDir(*dir); err != nil {
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
	result, err := recovery.ImportCodexVerified(ctx, *repo, *source, *anchor, digest, !*write)
	if err != nil {
		fail(err.Error(), 1)
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		fail(err.Error(), 1)
	}
}

func fail(message string, code int) { fmt.Fprintln(os.Stderr, message); os.Exit(code) }
