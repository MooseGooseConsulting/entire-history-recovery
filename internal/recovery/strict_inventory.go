package recovery

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/MooseGooseConsulting/entire-history-recovery/internal/reconcile"
	"github.com/MooseGooseConsulting/entire-history-recovery/internal/snapshot"
	cp "github.com/entireio/cli/cmd/entire/cli/checkpoint"
	"github.com/entireio/cli/cmd/entire/cli/checkpoint/id"
	remotegit "github.com/entireio/cli/cmd/entire/cli/checkpoint/remote"
	"github.com/entireio/cli/cmd/entire/cli/execx"
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

type inventoryWorkspaceContextKey struct{}

// WithInventoryWorkspace validates a private directory and places temporary
// bare repositories beneath it. Remote inventory requires this context; it
// fails closed rather than using the shared system temp directory. The caller
// owns cleanup after every repository handle beneath the workspace is closed.
// The CLI supplies it to a worker subprocess so its parent can clean up after
// the child exits and releases all handles.
func WithInventoryWorkspace(ctx context.Context, workspace string) (context.Context, error) {
	if workspace == "" {
		return nil, errors.New("inventory workspace is required")
	}
	abs, err := filepath.Abs(workspace)
	if err != nil {
		return nil, fmt.Errorf("resolve inventory workspace: %w", err)
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return nil, fmt.Errorf("inspect inventory workspace: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("inventory workspace must be a real directory")
	}
	if err := snapshot.CheckPrivateDir(abs); err != nil {
		return nil, errors.New("inventory workspace must be private")
	}
	return context.WithValue(ctx, inventoryWorkspaceContextKey{}, abs), nil
}

func inventoryWorkspaceFromContext(ctx context.Context) (string, bool) {
	workspace, ok := ctx.Value(inventoryWorkspaceContextKey{}).(string)
	return workspace, ok && workspace != ""
}

// AssessCodexStorage enumerates both Entire Git storage layouts directly,
// failing on malformed or unreadable entries. It queries every configured
// checkpoint read target strictly and hydrates transcript bodies only for
// sessions whose ID matches candidate.SessionID. Configured Git read targets
// require a private caller-owned workspace attached with WithInventoryWorkspace.
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
			closeErr := remoteRepo.Close()
			if readErr != nil || closeErr != nil {
				return result, fmt.Errorf("read configured checkpoint target %d: %w", i+1, errors.Join(readErr, closeErr))
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
		if shard.Mode != filemode.Dir || len(shard.Name) != 2 {
			// Only familiar repository housekeeping files are safely unrelated
			// to checkpoint shards. Unknown entries may represent an unsupported
			// layout, so they cannot establish an exhaustive inventory.
			if shard.Mode == filemode.Regular && (shard.Name == "README.md" || shard.Name == ".gitignore" || shard.Name == ".gitattributes") {
				continue
			}
			return errors.New("unsupported entry at root of Entire checkpoint branch")
		}
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
	workspace, ok := inventoryWorkspaceFromContext(ctx)
	if !ok {
		// Never fall back to os.TempDir: on Windows its inherited ACL may expose
		// fetched checkpoint objects to other users. The caller that created the
		// private workspace owns cleanup after all repository handles are closed.
		return "", nil, errors.New("configured checkpoint inventory requires a private workspace")
	}
	ctx, cancel := context.WithTimeout(ctx, configuredInventoryTimeout)
	defer cancel()
	listing, err := strictLsRemote(ctx, worktreeRoot, target, cp.CheckpointRefPrefix+"*", "refs/heads/"+paths.MetadataBranchName)
	if err != nil {
		return "", nil, errors.New("configured checkpoint ref listing failed")
	}
	refspecs, err := strictRemoteRefspecs(listing)
	if err != nil {
		return "", nil, err
	}
	temp, err := os.MkdirTemp(workspace, "entire-import-inventory-")
	if err != nil {
		return "", nil, err
	}
	repo, err := git.PlainInit(temp, true)
	if err != nil {
		return "", nil, err
	}
	if len(refspecs) > 0 {
		err = fetchStrictObjects(ctx, worktreeRoot, temp, target, refspecs)
		if err != nil {
			_ = repo.Close()
			return "", nil, errors.New("configured checkpoint objects could not be fetched")
		}
		fetched := make(map[string]bool, len(refspecs))
		for _, spec := range refspecs {
			refName, _, _ := strings.Cut(strings.TrimPrefix(spec, "+"), ":")
			fetched[refName] = true
		}
		for _, pair := range strings.Split(strings.TrimSpace(string(listing)), "\n") {
			fields := strings.Fields(pair)
			if len(fields) != 2 || !fetched[fields[1]] {
				continue
			}
			ref, err := repo.Reference(plumbing.ReferenceName(fields[1]), true)
			if err != nil || ref.Hash().String() != fields[0] {
				_ = repo.Close()
				return "", nil, errors.New("checkpoint refs changed during strict inventory")
			}
		}
	}
	return temp, repo, nil
}

// fetchStrictObjects fetches into an isolated bare repository while applying
// the source checkout's local Git transport settings to this command only.
// The upstream remote.Fetch helper inherits process-wide environment and
// cannot accept command-scoped Git configuration, while copying credentials
// into the temporary repository would persist them on disk.
func fetchStrictObjects(ctx context.Context, worktreeRoot, bareDir, target string, refspecs []string) error {
	env, err := strictGitEnv(worktreeRoot, target)
	if err != nil {
		return err
	}
	args := []string{"fetch", "--no-auto-gc", "--no-tags", "--no-write-fetch-head", target}
	args = append(args, refspecs...)
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = bareDir
	cmd.Env = env
	cmd.Stdin = nil
	execx.TerminateOnCancel(cmd)
	if err := cmd.Run(); err != nil {
		// Do not expose transport URLs, helper output, or credentials through
		// error strings; the caller reports a stable generic failure.
		return errors.New("git fetch failed")
	}
	return nil
}

func strictLsRemote(ctx context.Context, worktreeRoot, target string, patterns ...string) ([]byte, error) {
	transportConfig, err := readLocalTransportConfig(worktreeRoot)
	if err != nil {
		return nil, err
	}
	// Run from the checkout so Git loads its local and worktree config itself.
	// Forwarding those same entries through GIT_CONFIG_COUNT would duplicate
	// every multi-valued HTTP header on this command.
	env := sanitizeGitRepositoryEnv(os.Environ())
	env = replaceEnvValue(env, "GIT_TERMINAL_PROMPT", "0")
	env = replaceEnvValue(env, "GCM_INTERACTIVE", "Never")
	env = appendCheckpointTokenIfNeeded(env, target, target+"/info/refs", transportConfig)
	args := append([]string{"ls-remote", target}, patterns...)
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = worktreeRoot
	cmd.Env = env
	cmd.Stdin = nil
	execx.TerminateOnCancel(cmd)
	output, err := cmd.Output()
	if err != nil {
		return nil, errors.New("git remote ref listing failed")
	}
	return output, nil
}

func strictGitEnv(repoRoot, target string) ([]string, error) {
	transportConfig, err := readLocalTransportConfig(repoRoot)
	if err != nil {
		return nil, err
	}
	env := appendGitConfigEnv(sanitizeGitRepositoryEnv(os.Environ()), transportConfig)
	env = replaceEnvValue(env, "GIT_TERMINAL_PROMPT", "0")
	env = replaceEnvValue(env, "GCM_INTERACTIVE", "Never")
	return appendCheckpointTokenIfNeeded(env, target, target, transportConfig), nil
}

func appendCheckpointTokenIfNeeded(env []string, target, requestURL string, transportConfig []gitConfigSetting) []string {
	if token := strings.TrimSpace(os.Getenv(remotegit.CheckpointTokenEnvVar)); token != "" && validCheckpointToken(token) && !hasRepositoryAuthorizationHeader(transportConfig, requestURL) {
		if parsed, parseErr := remotegit.ParseURL(target); parseErr == nil && parsed.Protocol == remotegit.ProtocolHTTPS {
			value := "Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:"+token))
			return appendGitConfigEnv(env, []gitConfigSetting{{key: "http." + target + ".extraheader", value: value}})
		}
	}
	return env
}

func sanitizeGitRepositoryEnv(env []string) []string {
	const keys = "|GIT_DIR|GIT_WORK_TREE|GIT_COMMON_DIR|GIT_INDEX_FILE|GIT_OBJECT_DIRECTORY|GIT_OBJECT_DIRECTORY_RELATIVE|GIT_ALTERNATE_OBJECT_DIRECTORIES|GIT_QUARANTINE_PATH|GIT_SHALLOW_FILE|GIT_REPLACE_REF_BASE|GIT_NAMESPACE|GIT_CEILING_DIRECTORIES|"
	filtered := make([]string, 0, len(env))
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if strings.Contains(keys, "|"+strings.ToUpper(key)+"|") {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

type gitConfigSetting struct {
	key   string
	value string
}

func readLocalTransportConfig(repoRoot string) ([]gitConfigSetting, error) {
	pattern := `^(core\.sshcommand|core\.askpass|core\.gitproxy|credential\..*|http\..*|url\..*\.insteadof|protocol\..*\.allow|ssh\.variant)$`
	cmd := exec.Command("git", "-C", repoRoot, "config", "--local", "--null", "--get-regexp", pattern)
	cmd.Env = sanitizeGitRepositoryEnv(os.Environ())
	output, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 && len(output) == 0 {
			output = nil
		} else {
			return nil, errors.New("could not read repository-local Git transport settings")
		}
	}
	settings, err := parseGitConfigOutput(output)
	if err != nil {
		return nil, err
	}
	worktreeConfig, err := gitWorktreeConfigEnabled(repoRoot)
	if err != nil {
		return nil, err
	}
	if worktreeConfig {
		cmd := exec.Command("git", "-C", repoRoot, "config", "--worktree", "--null", "--get-regexp", pattern)
		output, err := cmd.Output()
		if err != nil {
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 || len(output) != 0 {
				return nil, errors.New("could not read worktree-local Git transport settings")
			}
		}
		worktreeSettings, err := parseGitConfigOutput(output)
		if err != nil {
			return nil, err
		}
		settings = append(settings, worktreeSettings...)
	}
	return settings, nil
}

// hasRepositoryAuthorizationHeader gives an explicit repository-local HTTP
// Authorization header precedence over ENTIRE_CHECKPOINT_TOKEN. Sending both
// produces ambiguous duplicate Authorization headers. Other configured HTTP
// headers remain intact because we do not reset http.extraHeader wholesale.
func hasRepositoryAuthorizationHeader(settings []gitConfigSetting, target string) bool {
	for _, setting := range settings {
		section, generic, ok := httpExtraHeaderSection(setting.key)
		if !ok || (!generic && !httpConfigURLMatches(section, target)) {
			continue
		}
		name, value, ok := strings.Cut(setting.value, ":")
		if ok && strings.EqualFold(strings.TrimSpace(name), "authorization") && strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

func httpExtraHeaderSection(key string) (section string, generic, ok bool) {
	if strings.EqualFold(key, "http.extraheader") {
		return "", true, true
	}
	const prefix, suffix = "http.", ".extraheader"
	if len(key) <= len(prefix)+len(suffix) || !strings.EqualFold(key[:len(prefix)], prefix) || !strings.EqualFold(key[len(key)-len(suffix):], suffix) {
		return "", false, false
	}
	return key[len(prefix) : len(key)-len(suffix)], false, true
}

func httpConfigURLMatches(section, target string) bool {
	configured, err := url.Parse(section)
	if err != nil || configured.Scheme == "" || configured.Hostname() == "" {
		return false
	}
	request, err := url.Parse(target)
	if err != nil || !strings.EqualFold(configured.Scheme, request.Scheme) || request.Hostname() == "" {
		return false
	}
	configuredHost := strings.ToLower(configured.Hostname())
	requestHost := strings.ToLower(request.Hostname())
	if strings.HasPrefix(configuredHost, "*.") {
		suffix := strings.TrimPrefix(configuredHost, "*")
		prefix := strings.TrimSuffix(requestHost, suffix)
		if prefix == requestHost || prefix == "" || strings.Contains(prefix, ".") {
			return false
		}
	} else if configuredHost != requestHost {
		return false
	}
	if effectiveHTTPPort(configured) != effectiveHTTPPort(request) {
		return false
	}
	if configured.User != nil {
		configuredUser := configured.User.Username()
		requestUser := ""
		if request.User != nil {
			requestUser = request.User.Username()
		}
		if configuredUser != requestUser {
			return false
		}
	}
	configuredPath := strings.TrimSuffix(configured.EscapedPath(), "/")
	requestPath := strings.TrimSuffix(request.EscapedPath(), "/")
	if configuredPath != "" && configuredPath != "/" && requestPath != configuredPath && !strings.HasPrefix(requestPath, configuredPath+"/") {
		return false
	}
	return true
}

func effectiveHTTPPort(parsed *url.URL) string {
	if port := parsed.Port(); port != "" {
		return port
	}
	switch strings.ToLower(parsed.Scheme) {
	case "https":
		return "443"
	case "http":
		return "80"
	default:
		return ""
	}
}

func parseGitConfigOutput(output []byte) ([]gitConfigSetting, error) {
	var settings []gitConfigSetting
	for _, record := range bytes.Split(output, []byte{0}) {
		if len(record) == 0 {
			continue
		}
		key, value, ok := bytes.Cut(record, []byte{'\n'})
		if !ok || len(key) == 0 {
			return nil, errors.New("repository-local Git transport settings are malformed")
		}
		settings = append(settings, gitConfigSetting{key: string(key), value: string(value)})
	}
	return settings, nil
}

func gitWorktreeConfigEnabled(repoRoot string) (bool, error) {
	cmd := exec.Command("git", "-C", repoRoot, "config", "--local", "--bool", "--get", "extensions.worktreeConfig")
	cmd.Env = sanitizeGitRepositoryEnv(os.Environ())
	output, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 && len(output) == 0 {
			return false, nil
		}
		return false, errors.New("could not inspect worktree Git configuration")
	}
	return strings.TrimSpace(string(output)) == "true", nil
}

func appendGitConfigEnv(env []string, settings []gitConfigSetting) []string {
	count := 0
	for _, entry := range env {
		if value, ok := strings.CutPrefix(entry, "GIT_CONFIG_COUNT="); ok {
			if n, err := strconv.Atoi(value); err == nil && n > 0 {
				count = n
			}
			break
		}
	}
	filtered := make([]string, 0, len(env)+len(settings)*2)
	for _, entry := range env {
		if strings.HasPrefix(entry, "GIT_CONFIG_COUNT=") {
			continue
		}
		filtered = append(filtered, entry)
	}
	for i, setting := range settings {
		index := count + i
		filtered = append(filtered, fmt.Sprintf("GIT_CONFIG_KEY_%d=%s", index, setting.key))
		filtered = append(filtered, fmt.Sprintf("GIT_CONFIG_VALUE_%d=%s", index, setting.value))
	}
	return append(filtered, fmt.Sprintf("GIT_CONFIG_COUNT=%d", count+len(settings)))
}

func replaceEnvValue(env []string, key, value string) []string {
	prefix := key + "="
	filtered := make([]string, 0, len(env)+1)
	for _, entry := range env {
		entryKey, _, _ := strings.Cut(entry, "=")
		if !strings.EqualFold(entryKey, key) {
			filtered = append(filtered, entry)
		}
	}
	return append(filtered, prefix+value)
}

func validCheckpointToken(token string) bool {
	for _, b := range []byte(token) {
		if b < 0x20 || b == 0x7f {
			return false
		}
	}
	return true
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
