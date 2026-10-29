package recovery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/MooseGooseConsulting/entire-history-recovery/internal/reconcile"
	cp "github.com/entireio/cli/cmd/entire/cli/checkpoint"
	"github.com/entireio/cli/cmd/entire/cli/checkpoint/id"
	remotegit "github.com/entireio/cli/cmd/entire/cli/checkpoint/remote"
	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/entireio/cli/cmd/entire/cli/settings"
	"github.com/entireio/cli/cmd/entire/cli/strategy"
	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/filemode"
	"github.com/go-git/go-git/v6/plumbing/object"
)

// StorageAssessment proves coverage only against the locally enumerated refs
// and the configured Entire checkpoint read targets. It says nothing about
// cloud/service ingestion or indexes.
type StorageAssessment struct {
	Coverage                  reconcile.Assessment
	LocalRefsComplete         bool
	ConfiguredRemotesComplete bool
	RemoteTargets             int
	CheckpointTrees           int
}

// AssessCodexStorage enumerates both Entire Git storage layouts directly,
// failing on malformed or unreadable entries. It queries every configured
// checkpoint read target strictly and hydrates transcript bodies only for
// sessions whose ID matches candidate.SessionID.
func AssessCodexStorage(ctx context.Context, repoRoot string, repo *git.Repository, candidate reconcile.Candidate) (StorageAssessment, error) {
	result := StorageAssessment{}
	root, err := filepath.Abs(repoRoot)
	if err != nil {
		return result, err
	}
	ctx = settings.WithWorktreeRoot(ctx, root)
	local, err := readStrictRepository(ctx, root, repo, nil, candidate)
	if err != nil {
		return result, fmt.Errorf("enumerate local Entire storage: %w", err)
	}
	result.LocalRefsComplete = true
	result.CheckpointTrees = local.count
	records := local.records

	targets, err := configuredTargets(ctx, root)
	if err != nil {
		return result, fmt.Errorf("resolve configured checkpoint read targets: %w", err)
	}
	result.RemoteTargets = len(targets)
	if len(targets) > 0 {
		for i, target := range targets {
			remoteRoot, remoteRepo, openErr := fetchRemoteInventory(ctx, root, target)
			if openErr != nil {
				return result, fmt.Errorf("enumerate configured checkpoint target %d: %w", i+1, openErr)
			}
			remoteData, readErr := readStrictRepository(ctx, remoteRoot, remoteRepo, nil, candidate)
			_ = remoteRepo.Close()
			_ = os.RemoveAll(remoteRoot)
			if readErr != nil {
				return result, fmt.Errorf("read configured checkpoint target %d: %w", i+1, readErr)
			}
			result.CheckpointTrees += remoteData.count
			records = append(records, remoteData.records...)
		}
		result.ConfiguredRemotesComplete = true
	}
	assessment, err := reconcile.AssessExact(candidate, records)
	if err != nil {
		return result, err
	}
	result.Coverage = assessment
	return result, nil
}

type strictRepoData struct {
	records []reconcile.Record
	count   int
}
type strictCheckpoint struct {
	id       id.CheckpointID
	treeHash plumbing.Hash
}

const configuredInventoryTimeout = 90 * time.Second

func readStrictRepository(ctx context.Context, root string, repo *git.Repository, remotes []string, candidate reconcile.Candidate) (strictRepoData, error) {
	var out strictRepoData
	checkpoints, err := enumerateCheckpointTrees(ctx, repo, remotes)
	if err != nil {
		return out, err
	}
	storeCtx := settings.WithWorktreeRoot(ctx, root)
	localRefs := cp.DefaultV1Refs().PrimaryAsLocalRead()
	stores, err := cp.Open(storeCtx, repo, cp.OpenOptions{ReadRemotes: remotes, Refs: &localRefs})
	if err != nil {
		return out, fmt.Errorf("open read-only checkpoint store: %w", err)
	}
	for _, checkpoint := range checkpoints {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		summary, err := stores.Persistent.Read(ctx, checkpoint.id)
		if err != nil {
			return out, fmt.Errorf("read checkpoint summary: %w", err)
		}
		if summary == nil || summary.CheckpointID != checkpoint.id || len(summary.Sessions) == 0 {
			return out, errors.New("checkpoint summary is missing, malformed, or empty")
		}
		for sessionIndex := range summary.Sessions {
			meta, err := stores.Persistent.ReadSessionMetadata(ctx, checkpoint.id, sessionIndex)
			if err != nil {
				return out, fmt.Errorf("read checkpoint session metadata: %w", err)
			}
			if meta == nil || meta.SessionID == "" || meta.Agent == "" {
				return out, errors.New("checkpoint session metadata lacks identity")
			}
			record := reconcile.Record{CheckpointID: checkpoint.id, SessionIndex: sessionIndex, Content: cp.SessionContent{Metadata: *meta}}
			if meta.SessionID == candidate.SessionID && string(meta.Agent) == candidate.Agent {
				content, err := stores.Persistent.ReadSessionContent(ctx, checkpoint.id, sessionIndex)
				if err != nil {
					return out, fmt.Errorf("read matching session transcript: %w", err)
				}
				if content == nil || content.Metadata.SessionID != meta.SessionID || content.Metadata.Agent != meta.Agent || len(content.Transcript) == 0 {
					return out, errors.New("matching checkpoint session transcript is incomplete")
				}
				if bytes.Equal(content.Transcript, candidate.Transcript) {
					// Assessment only compares the sanitized transcript. Reuse the
					// caller's bytes so repeated historical copies do not accumulate
					// one transcript allocation per checkpoint.
					record.Content = cp.SessionContent{Metadata: *meta, Transcript: candidate.Transcript}
				}
			} else if meta.SessionID == candidate.SessionID {
				// Preserve the identity as a conflict marker without hydrating its
				// potentially large body. AssessExact sees the same session ID and
				// holds when its agent differs or content is unavailable.
			}
			out.records = append(out.records, record)
		}
		out.count++
	}
	return out, nil
}

func enumerateCheckpointTrees(ctx context.Context, repo *git.Repository, readRemotes []string) ([]strictCheckpoint, error) {
	refs, err := repo.References()
	if err != nil {
		return nil, fmt.Errorf("list Git refs: %w", err)
	}
	defer refs.Close()
	branchNames := map[plumbing.ReferenceName]bool{plumbing.NewBranchReferenceName(paths.MetadataBranchName): true}
	for _, remote := range readRemotes {
		branchNames[plumbing.NewRemoteReferenceName(remote, paths.MetadataBranchName)] = true
	}
	var checkpoints []strictCheckpoint
	seenTree := make(map[id.CheckpointID]plumbing.Hash)
	err = refs.ForEach(func(ref *plumbing.Reference) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := ref.Name()
		if strings.HasPrefix(name.String(), cp.CheckpointRefPrefix) {
			checkpointID, ok := cp.ParseRef(name)
			if !ok {
				return errors.New("malformed ref in Entire checkpoint namespace")
			}
			commit, err := repo.CommitObject(ref.Hash())
			if err != nil {
				return fmt.Errorf("read checkpoint ref target: %w", err)
			}
			tree, err := commit.Tree()
			if err != nil {
				return fmt.Errorf("read checkpoint ref tree: %w", err)
			}
			if err := addStrictCheckpoint(&checkpoints, seenTree, checkpointID, tree.Hash, tree); err != nil {
				return err
			}
			return nil
		}
		if !branchNames[name] {
			return nil
		}
		commit, err := repo.CommitObject(ref.Hash())
		if err != nil {
			return fmt.Errorf("read metadata branch target: %w", err)
		}
		rootTree, err := commit.Tree()
		if err != nil {
			return fmt.Errorf("read metadata branch tree: %w", err)
		}
		return walkStrictBranch(ctx, repo, rootTree, &checkpoints, seenTree)
	})
	if err != nil {
		return nil, fmt.Errorf("enumerate Entire refs: %w", err)
	}
	return checkpoints, nil
}

func addStrictCheckpoint(out *[]strictCheckpoint, seen map[id.CheckpointID]plumbing.Hash, checkpointID id.CheckpointID, hash plumbing.Hash, tree *object.Tree) error {
	if previous, ok := seen[checkpointID]; ok {
		if previous != hash {
			return errors.New("conflicting trees share an Entire checkpoint ID")
		}
		return nil
	}
	file, err := tree.File(paths.MetadataFileName)
	if err != nil {
		return errors.New("checkpoint tree has no root metadata")
	}
	bytes, err := file.Contents()
	if err != nil {
		return fmt.Errorf("read checkpoint root metadata: %w", err)
	}
	var summary cp.CheckpointSummary
	if err := json.Unmarshal([]byte(bytes), &summary); err != nil {
		return fmt.Errorf("parse checkpoint root metadata: %w", err)
	}
	if summary.CheckpointID != checkpointID || len(summary.Sessions) == 0 {
		return errors.New("checkpoint root metadata identity or session list is invalid")
	}
	seen[checkpointID] = hash
	*out = append(*out, strictCheckpoint{id: checkpointID, treeHash: hash})
	return nil
}

func walkStrictBranch(ctx context.Context, repo *git.Repository, root *object.Tree, out *[]strictCheckpoint, seen map[id.CheckpointID]plumbing.Hash) error {
	for _, shard := range root.Entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if shard.Mode != filemode.Dir {
			continue
		}
		if len(shard.Name) != 2 {
			continue
		} // ancillary root entries are not checkpoint shards
		bucket, err := repo.TreeObject(shard.Hash)
		if err != nil {
			return fmt.Errorf("read Entire checkpoint shard: %w", err)
		}
		for _, entry := range bucket.Entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			if entry.Mode != filemode.Dir {
				return errors.New("unexpected file in Entire checkpoint shard")
			}
			checkpointID, err := id.NewCheckpointID(shard.Name + entry.Name)
			if err != nil {
				return fmt.Errorf("malformed checkpoint directory: %w", err)
			}
			tree, err := repo.TreeObject(entry.Hash)
			if err != nil {
				return fmt.Errorf("read checkpoint tree: %w", err)
			}
			if err := addStrictCheckpoint(out, seen, checkpointID, entry.Hash, tree); err != nil {
				return err
			}
		}
	}
	return nil
}

func configuredTargets(ctx context.Context, root string) ([]string, error) {
	loaded, err := settings.Load(settings.WithWorktreeRoot(ctx, root))
	if err != nil {
		return nil, err
	}
	if loaded.HasCheckpointRemoteKey() {
		if loaded.GetCheckpointRemote() == nil {
			return nil, errors.New("checkpoint_remote is configured but cannot be resolved")
		}
		resolution := strategy.CheckpointReadRemotesWithElection(ctx)
		if resolution.ElectionErr != nil {
			return nil, errors.New("checkpoint read-remote election failed")
		}
		dedicated, err := remotegit.ReadsDedicatedStore(ctx, resolution.ElectedName)
		if err != nil || !dedicated {
			return nil, errors.New("configured checkpoint_remote is not a confirmed read target")
		}
		url, err := remotegit.FetchURL(ctx, remotegit.FetchURLOptions{WorktreeRoot: root, LeadReadRemote: resolution.ElectedName})
		if err != nil {
			return nil, errors.New("configured checkpoint_remote URL cannot be resolved")
		}
		return []string{url}, nil
	}
	resolution := strategy.CheckpointReadRemotesWithElection(ctx)
	if resolution.ElectionErr != nil {
		return nil, errors.New("checkpoint read-remote election failed")
	}
	var targets []string
	seen := make(map[string]bool)
	for _, name := range resolution.Candidates {
		url, err := remotegit.FetchURL(ctx, remotegit.FetchURLOptions{WorktreeRoot: root, LeadReadRemote: name})
		if err != nil {
			return nil, fmt.Errorf("resolve configured read target: %w", err)
		}
		if !seen[url] {
			seen[url] = true
			targets = append(targets, url)
		}
	}
	return targets, nil
}

func fetchRemoteInventory(ctx context.Context, worktreeRoot, target string) (string, *git.Repository, error) {
	ctx, cancel := context.WithTimeout(ctx, configuredInventoryTimeout)
	defer cancel()
	listing, err := remotegit.LsRemoteInDir(ctx, worktreeRoot, target, cp.CheckpointRefPrefix+"*", "refs/heads/"+paths.MetadataBranchName)
	if err != nil {
		return "", nil, errors.New("configured checkpoint ref listing failed")
	}
	refspecs, err := strictRemoteRefspecs(listing)
	if err != nil {
		return "", nil, err
	}
	temp, err := os.MkdirTemp("", "entire-import-inventory-")
	if err != nil {
		return "", nil, err
	}
	repo, err := git.PlainInit(temp, true)
	if err != nil {
		_ = os.RemoveAll(temp)
		return "", nil, err
	}
	if len(refspecs) > 0 {
		_, err = remotegit.Fetch(ctx, remotegit.FetchOptions{Remote: target, RefSpecs: refspecs, NoTags: true, NoFilter: true, Dir: temp, ExtraArgs: []string{"--no-write-fetch-head"}})
		if err != nil {
			_ = repo.Close()
			_ = os.RemoveAll(temp)
			return "", nil, errors.New("configured checkpoint objects could not be fetched")
		}
		for _, pair := range strings.Split(strings.TrimSpace(string(listing)), "\n") {
			fields := strings.Fields(pair)
			if len(fields) != 2 {
				continue
			}
			ref, err := repo.Reference(plumbing.ReferenceName(fields[1]), true)
			if err != nil || ref.Hash().String() != fields[0] {
				_ = repo.Close()
				_ = os.RemoveAll(temp)
				return "", nil, errors.New("checkpoint refs changed during strict inventory")
			}
		}
	}
	return temp, repo, nil
}

func strictRemoteRefspecs(listing []byte) ([]string, error) {
	var refspecs []string
	seen := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimSpace(string(listing)), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return nil, errors.New("malformed configured checkpoint ref listing")
		}
		name := fields[1]
		if strings.HasPrefix(name, cp.CheckpointRefPrefix) {
			if _, ok := cp.ParseRef(plumbing.ReferenceName(name)); !ok {
				return nil, errors.New("malformed configured checkpoint ref")
			}
		} else if name != "refs/heads/"+paths.MetadataBranchName {
			if strings.HasPrefix(name, "refs/heads/entire/checkpoints/") {
				return nil, errors.New("malformed configured metadata branch ref")
			}
			continue
		}
		if !plumbing.NewHash(fields[0]).IsZero() && !seen[name] {
			seen[name] = true
			refspecs = append(refspecs, "+"+name+":"+name)
		} else if plumbing.NewHash(fields[0]).IsZero() {
			return nil, errors.New("invalid object ID in configured checkpoint ref listing")
		}
	}
	return refspecs, nil
}
