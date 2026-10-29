// Package recovery prototypes importing a specifically selected Codex session.
// It does not discover repositories, push checkpoints, or verify server data.
package recovery

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/agentimport"
	"github.com/entireio/cli/cmd/entire/cli/settings"
	"github.com/entireio/cli/cmd/entire/cli/strategy"
	git "github.com/go-git/go-git/v6"
)

// fixedSource delegates parsing to Entire but supplies the selected file.
type fixedSource struct {
	agentimport.Importer
	file agentimport.SessionFile
}

func (s fixedSource) Discover(_ string, _ string, _ time.Time, _ []string) ([]agentimport.SessionFile, error) {
	return []agentimport.SessionFile{s.file}, nil
}

// SplitTurns retains Entire's boundaries and uses matching native task IDs to
// associate contexts even when the context appears before the user message.
func (s fixedSource) SplitTurns(file agentimport.SessionFile, full []byte) ([]agentimport.Turn, error) {
	turns, err := s.Importer.SplitTurns(file, full)
	if err != nil {
		return nil, err
	}
	lines := bytes.Split(full, []byte{'\n'})
	type task struct {
		id, model  string
		start, end int
		ambiguous  bool
	}
	var tasks []task
	var active *task
	for i, line := range lines {
		var e struct {
			Type    string `json:"type"`
			Payload struct {
				Type   string `json:"type"`
				TurnID string `json:"turn_id"`
				Model  string `json:"model"`
			} `json:"payload"`
		}
		if json.Unmarshal(line, &e) != nil {
			continue
		}
		if e.Type == "event_msg" && e.Payload.Type == "task_started" {
			// Overlapping starts cannot establish an unambiguous boundary.
			overlapped := active != nil
			if active != nil {
				active.ambiguous = true
				tasks = append(tasks, *active)
			}
			active = &task{id: e.Payload.TurnID, start: i, end: -1, ambiguous: overlapped}
			continue
		}
		if active == nil {
			continue
		}
		if e.Type == "turn_context" && e.Payload.TurnID == active.id && active.id != "" && e.Payload.Model != "" {
			if active.model != "" && active.model != e.Payload.Model {
				active.ambiguous = true
			}
			active.model = e.Payload.Model
		}
		if e.Type == "event_msg" && e.Payload.Type == "task_complete" {
			if e.Payload.TurnID == active.id && active.id != "" {
				active.end = i
			} else {
				active.ambiguous = true
			}
			tasks = append(tasks, *active)
			active = nil
		}
	}
	for i := range turns {
		for _, item := range tasks {
			if !item.ambiguous && item.end >= 0 && item.model != "" && turns[i].LineStart >= item.start && turns[i].LineStart < item.end {
				turns[i].Model = item.model
				break
			}
		}
	}
	if len(turns) != 1 || turns[0].Model != "" || len(tasks) != 0 || active != nil {
		return turns, nil
	}
	// Legacy single-turn sessions may lack task IDs. Only one globally
	// unambiguous model is safe when there is exactly one prompt.
	model, ambiguous := "", false
	for _, line := range lines {
		var event struct {
			Type    string `json:"type"`
			Payload struct {
				Model string `json:"model"`
			} `json:"payload"`
		}
		if json.Unmarshal(line, &event) != nil || event.Type != "turn_context" || event.Payload.Model == "" {
			continue
		}
		if model != "" && model != event.Payload.Model {
			ambiguous = true
			break
		}
		model = event.Payload.Model
	}
	if !ambiguous {
		turns[0].Model = model
	}
	return turns, nil
}

// ImportCodex is a local-only prototype. The caller must establish that the
// selected session belongs to repoRoot before calling it. anchor is Entire's
// display anchor, not commit attribution; the caller must select it explicitly.
// This does not expose a production command or push checkpoint data.
func ImportCodex(ctx context.Context, repoRoot, source, anchor string, dryRun bool) (agentimport.Result, error) {
	var result agentimport.Result
	if anchor == "" {
		return result, errors.New("an explicit import display anchor is required")
	}
	root, err := filepath.Abs(repoRoot)
	if err != nil {
		return result, err
	}
	path, err := filepath.Abs(source)
	if err != nil {
		return result, err
	}
	f, err := os.Open(path)
	if err != nil {
		return result, err
	}
	defer f.Close()
	reader := bufio.NewReader(f)
	line, err := reader.ReadBytes('\n')
	if err != nil && !(errors.Is(err, io.EOF) && len(line) > 0) {
		return result, fmt.Errorf("read Codex session metadata: %w", err)
	}
	var header struct {
		Type    string `json:"type"`
		Payload struct {
			ID string `json:"id"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(line, &header); err != nil || header.Type != "session_meta" || header.Payload.ID == "" {
		return result, errors.New("source has no valid Codex session ID")
	}
	var adapter agentimport.Importer
	for _, candidate := range agentimport.All() {
		if candidate.Name() == "codex" {
			adapter = candidate
			break
		}
	}
	if adapter == nil {
		return result, errors.New("Entire Codex importer unavailable")
	}
	repo, err := git.PlainOpen(root)
	if err != nil {
		return result, fmt.Errorf("open destination repository: %w", err)
	}
	defer repo.Close()
	ctx = settings.WithWorktreeRoot(ctx, root)
	if err := strategy.EnsureRedactionConfigured(ctx); err != nil {
		return result, fmt.Errorf("configure redaction: %w", err)
	}
	return agentimport.Run(ctx, repo, fixedSource{Importer: adapter, file: agentimport.SessionFile{Path: path, SessionID: header.Payload.ID}}, agentimport.Options{
		RepoRoot: root, Now: time.Now(), DryRun: dryRun, LinkCommitSHA: anchor,
	})
}
