// Package recovery prototypes importing a specifically selected Codex session.
// It does not discover repositories, push checkpoints, or verify server data.
package recovery

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/MooseGooseConsulting/entire-history-recovery/internal/reconcile"
	"github.com/entireio/cli/cmd/entire/cli/agentimport"
	cp "github.com/entireio/cli/cmd/entire/cli/checkpoint"
	"github.com/entireio/cli/cmd/entire/cli/settings"
	"github.com/entireio/cli/cmd/entire/cli/strategy"
	"github.com/entireio/cli/redact"
	git "github.com/go-git/go-git/v6"
)

// fixedSource delegates parsing to Entire but supplies the selected file.
type fixedSource struct {
	agentimport.Importer
	file   agentimport.SessionFile
	digest *[32]byte
}

func (s fixedSource) Discover(_ string, _ string, _ time.Time, _ []string) ([]agentimport.SessionFile, error) {
	return []agentimport.SessionFile{s.file}, nil
}

// SplitTurns retains Entire's boundaries and uses matching native task IDs to
// associate contexts even when the context appears before the user message.
func (s fixedSource) SplitTurns(file agentimport.SessionFile, full []byte) ([]agentimport.Turn, error) {
	if s.digest != nil && sha256.Sum256(full) != *s.digest {
		return nil, errors.New("source changed after validation; select an inactive snapshot and retry")
	}
	if _, err := validateCodexSnapshot(full); err != nil {
		return nil, err
	}
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
	return turns, nil
}

// ImportCodex is a local-only prototype. The caller must establish that the
// selected session belongs to repoRoot before calling it. anchor is Entire's
// display anchor, not commit attribution; the caller must select it explicitly.
// This does not expose a production command or push checkpoint data.
// ImportCodex imports a selected Codex session after reconciling local and
// configured checkpoint storage. If configured Git read targets exist, ctx
// must carry a private workspace via WithInventoryWorkspace; inventory fails
// closed without one.
func ImportCodex(ctx context.Context, repoRoot, source, anchor string, dryRun bool) (agentimport.Result, error) {
	return importCodex(ctx, repoRoot, source, anchor, "", dryRun)
}

// ImportCodexVerified imports only when source still has the SHA-256 verified
// by the caller. It also pins that digest across agentimport.Run's reread.
// Like ImportCodex, configured Git read targets require a private workspace.
func ImportCodexVerified(ctx context.Context, repoRoot, source, anchor, expectedSHA256 string, dryRun bool) (agentimport.Result, error) {
	if len(expectedSHA256) != 64 {
		return agentimport.Result{}, errors.New("expected snapshot SHA-256 is required")
	}
	return importCodex(ctx, repoRoot, source, anchor, expectedSHA256, dryRun)
}

func importCodex(ctx context.Context, repoRoot, source, anchor, expectedSHA256 string, dryRun bool) (agentimport.Result, error) {
	return importCodexWithInventory(ctx, repoRoot, source, anchor, expectedSHA256, dryRun, nil)
}

// importCodexWithInventory performs a local exact-coverage preflight before
// delegating to Entire's importer. The reader seam is private and exists so
// tests can prove that damaged inventories fail closed before any write.
func importCodexWithInventory(ctx context.Context, repoRoot, source, anchor, expectedSHA256 string, dryRun bool, inventory reconcile.Reader) (agentimport.Result, error) {
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
	full, err := os.ReadFile(path)
	if err != nil {
		return result, err
	}
	sessionID, err := validateCodexSnapshot(full)
	if err != nil {
		return result, err
	}
	digest := sha256.Sum256(full)
	if expectedSHA256 != "" && fmt.Sprintf("%x", digest) != expectedSHA256 {
		return result, errors.New("snapshot changed after verification")
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
	anchor, err = validateDisplayAnchor(repo, anchor)
	if err != nil {
		return result, err
	}
	ctx = settings.WithWorktreeRoot(ctx, root)
	if err := strategy.EnsureRedactionConfigured(ctx); err != nil {
		return result, fmt.Errorf("configure redaction: %w", err)
	}
	var present bool
	var turnCount int
	if inventory != nil {
		present, turnCount, err = preflightExactCoverage(ctx, inventory, adapter, agentimport.SessionFile{Path: path, SessionID: sessionID}, full)
	} else {
		present, turnCount, err = preflightConfiguredStorage(ctx, root, repo, adapter, agentimport.SessionFile{Path: path, SessionID: sessionID}, full)
	}
	if err != nil {
		return result, err
	}
	if present {
		return agentimport.Result{SessionsScanned: 1, TurnsSkipped: turnCount}, nil
	}
	return agentimport.Run(ctx, repo, fixedSource{Importer: adapter, file: agentimport.SessionFile{Path: path, SessionID: sessionID}, digest: &digest}, agentimport.Options{
		RepoRoot: root, Now: time.Now(), DryRun: dryRun, LinkCommitSHA: anchor,
	})
}

func preflightConfiguredStorage(ctx context.Context, root string, repo *git.Repository, adapter agentimport.Importer, file agentimport.SessionFile, full []byte) (bool, int, error) {
	turns, err := adapter.SplitTurns(file, full)
	if err != nil {
		return false, 0, fmt.Errorf("split %s session %s for coverage: %w", adapter.Name(), file.SessionID, err)
	}
	if len(turns) == 0 {
		return false, 0, nil
	}
	transcript, err := redact.JSONLBytes(cp.SanitizeTranscriptForAgentType(adapter.AgentType(), full))
	if err != nil {
		return false, 0, fmt.Errorf("prepare %s transcript for coverage: %w", file.SessionID, err)
	}
	starts := make([]int, 0, len(turns))
	for _, turn := range turns {
		starts = append(starts, turn.LineStart)
	}
	proof, err := AssessCodexStorage(ctx, root, repo, reconcile.Candidate{
		SessionID:  file.SessionID,
		Agent:      string(adapter.AgentType()),
		Transcript: transcript.Bytes(),
		Starts:     starts,
	})
	if err != nil {
		return false, 0, fmt.Errorf("assess configured Entire storage: %w", err)
	}
	if proof.Coverage.Status == "hold" {
		return false, 0, fmt.Errorf("checkpoint coverage requires reconciliation; refusing import (%s)", proof.Coverage.Reason)
	}
	if proof.Coverage.Status == "present" {
		return true, len(turns), nil
	}
	// unseen is only a Git storage result. It is not evidence that the Entire
	// service has ingested or indexed all checkpoint data; this remains a local
	// recovery prototype, not global absence proof.
	return false, 0, nil
}

// preflightExactCoverage suppresses duplicate imports only when every parsed
// turn boundary is already represented by a byte-identical sanitized and
// redacted transcript in this locally visible checkpoint inventory. Partial
// or conflicting matches are held for later reconciliation. An unseen result
// permits the importer to proceed only within this local prototype; it does
// not establish remote/server completeness.
func preflightExactCoverage(ctx context.Context, reader reconcile.Reader, adapter agentimport.Importer, file agentimport.SessionFile, full []byte) (bool, int, error) {
	turns, err := adapter.SplitTurns(file, full)
	if err != nil {
		return false, 0, fmt.Errorf("split %s session %s for coverage: %w", adapter.Name(), file.SessionID, err)
	}
	if len(turns) == 0 {
		return false, 0, nil
	}
	transcript, err := redact.JSONLBytes(cp.SanitizeTranscriptForAgentType(adapter.AgentType(), full))
	if err != nil {
		return false, 0, fmt.Errorf("prepare %s transcript for coverage: %w", file.SessionID, err)
	}
	records, err := reconcile.ReadVisible(ctx, reader)
	if err != nil {
		return false, 0, fmt.Errorf("read local checkpoint inventory: %w", err)
	}
	starts := make([]int, 0, len(turns))
	for _, turn := range turns {
		starts = append(starts, turn.LineStart)
	}
	assessment, err := reconcile.AssessExact(reconcile.Candidate{
		SessionID:  file.SessionID,
		Agent:      string(adapter.AgentType()),
		Transcript: transcript.Bytes(),
		Starts:     starts,
	}, records)
	if err != nil {
		return false, 0, fmt.Errorf("assess local checkpoint coverage: %w", err)
	}
	if assessment.Status == "hold" {
		return false, 0, fmt.Errorf("local checkpoint coverage requires reconciliation; refusing import (%s)", assessment.Reason)
	}
	if assessment.Status == "present" {
		return true, len(turns), nil
	}
	return false, 0, nil
}

// Reject damaged or concatenated sessions before the upstream parser can skip
// unreadable records. Unknown event types remain valid for forward compatibility.
// Errors expose line numbers, never transcript contents.
func validateCodexSnapshot(full []byte) (string, error) {
	var sessionID string
	for i, raw := range bytes.Split(full, []byte{'\n'}) {
		line := bytes.TrimSpace(raw)
		if len(line) == 0 && i > 0 {
			continue
		}
		var record struct {
			Type    string          `json:"type"`
			Payload json.RawMessage `json:"payload"`
		}
		if json.Unmarshal(line, &record) != nil || record.Type == "" || len(record.Payload) == 0 || record.Payload[0] != '{' {
			return "", fmt.Errorf("invalid Codex record at line %d", i+1)
		}
		if i == 0 {
			var meta struct {
				ID string `json:"id"`
			}
			if record.Type != "session_meta" || json.Unmarshal(record.Payload, &meta) != nil || meta.ID == "" {
				return "", errors.New("source has no valid Codex session ID")
			}
			sessionID = meta.ID
		} else if record.Type == "session_meta" {
			return "", fmt.Errorf("additional session header at line %d; split concatenated sessions before importing", i+1)
		}
	}
	return sessionID, nil
}
