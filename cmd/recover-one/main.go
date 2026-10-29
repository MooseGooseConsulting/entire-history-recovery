// recover-one imports an explicitly selected inactive Codex snapshot locally.
// It never discovers sources, pushes checkpoints, or infers repository identity.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"

	"github.com/MooseGooseConsulting/entire-history-recovery/internal/recovery"
)

func main() {
	repo := flag.String("repo", "", "destination checkout; caller must verify source ownership")
	source := flag.String("source", "", "inactive native Codex JSONL snapshot")
	anchor := flag.String("anchor", "", "full commit SHA used only as a display anchor")
	write := flag.Bool("write", false, "write local checkpoints; default previews without writing")
	flag.Parse()
	if *repo == "" || *source == "" || *anchor == "" || flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "required: --repo PATH --source PATH --anchor FULL_SHA [--write]")
		os.Exit(2)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	result, err := recovery.ImportCodex(ctx, *repo, *source, *anchor, !*write)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
