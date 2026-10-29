package recovery

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MooseGooseConsulting/entire-history-recovery/internal/reconcile"
	"github.com/MooseGooseConsulting/entire-history-recovery/internal/snapshot"
	"github.com/entireio/cli/cmd/entire/cli/agentimport"
	"github.com/entireio/cli/cmd/entire/cli/checkpoint"
	"github.com/entireio/cli/cmd/entire/cli/checkpoint/id"
	remotegit "github.com/entireio/cli/cmd/entire/cli/checkpoint/remote"
	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/entireio/cli/cmd/entire/cli/settings"
	"github.com/entireio/cli/redact"
	git "github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/filemode"
	"github.com/go-git/go-git/v6/plumbing/object"
)

func TestStrictBranchRejectsUnknownRootLayout(t *testing.T) {
	for _, entry := range []object.TreeEntry{
		{Name: "unexpected-layout", Mode: filemode.Dir},
		{Name: "ab", Mode: filemode.Regular},
	} {
		tree := &object.Tree{Entries: []object.TreeEntry{entry}}
		if err := walkStrictBranch(context.Background(), nil, tree, nil, map[id.CheckpointID]plumbing.Hash{}); err == nil {
			t.Fatal("unrecognized checkpoint layout was treated as complete")
		}
	}
	tree := &object.Tree{Entries: []object.TreeEntry{{Name: "README.md", Mode: filemode.Regular}}}
	if err := walkStrictBranch(context.Background(), nil, tree, nil, map[id.CheckpointID]plumbing.Hash{}); err != nil {
		t.Fatalf("known housekeeping file rejected: %v", err)
	}
}

func TestWithInventoryWorkspaceRequiresPrivateDirectory(t *testing.T) {
	parent := testTempDir(t)
	private := filepath.Join(parent, "private")
	if err := snapshot.EnsurePrivateDir(private); err != nil {
		t.Fatal(err)
	}
	if _, err := WithInventoryWorkspace(context.Background(), private); err != nil {
		t.Fatalf("private workspace rejected: %v", err)
	}

	shared := filepath.Join(parent, "shared")
	if err := os.Mkdir(shared, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := WithInventoryWorkspace(context.Background(), shared); err == nil || err.Error() != "inventory workspace must be private" {
		t.Fatalf("shared workspace was not rejected with a safe error: %v", err)
	}
}

func runStrictGit(t *testing.T, dir string, args ...string) []byte {
	t.Helper()
	return runStrictGitWithGlobalConfig(t, dir, "", args...)
}

func runStrictGitWithGlobalConfig(t *testing.T, dir, globalConfig string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = strictFixtureGitEnv(t)
	if globalConfig != "" {
		cmd.Env = replaceEnvValue(cmd.Env, "GIT_CONFIG_GLOBAL", globalConfig)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return out
}

// strictFixtureGitEnv keeps fixture Git commands deterministic without using
// the developer's helpers, hooks, templates, askpass programs, or config.
// Production inventory commands use a separate environment that preserves
// cached credential helpers.
func strictFixtureGitEnv(t *testing.T) []string {
	t.Helper()
	root := t.TempDir()
	emptyGlobal := filepath.Join(root, "empty.gitconfig")
	if err := os.WriteFile(emptyGlobal, nil, 0600); err != nil {
		t.Fatal(err)
	}
	emptyTemplates := filepath.Join(root, "templates")
	emptyHooks := filepath.Join(root, "hooks")
	for _, dir := range []string{emptyTemplates, emptyHooks} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	env := sanitizeGitRepositoryEnv(os.Environ())
	filtered := make([]string, 0, len(env))
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		upper := strings.ToUpper(key)
		switch upper {
		case "GIT_CONFIG", "GIT_ASKPASS", "SSH_ASKPASS", "GIT_TEMPLATE_DIR",
			"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "FTP_PROXY",
			"NO_PROXY":
			continue
		}
		if strings.HasPrefix(upper, "GIT_CONFIG_") {
			continue
		}
		filtered = append(filtered, entry)
	}
	env = filtered
	for _, pair := range [][2]string{
		{"GIT_CONFIG_NOSYSTEM", "1"}, {"GIT_CONFIG_GLOBAL", emptyGlobal},
		{"GIT_TEMPLATE_DIR", emptyTemplates}, {"GIT_TERMINAL_PROMPT", "0"},
		{"no_proxy", "localhost,127.0.0.1,::1"},
		{"GCM_INTERACTIVE", "Never"}, {"GIT_AUTHOR_NAME", "Fixture"},
		{"GIT_AUTHOR_EMAIL", "fixture@example.invalid"}, {"GIT_COMMITTER_NAME", "Fixture"},
		{"GIT_COMMITTER_EMAIL", "fixture@example.invalid"},
	} {
		env = replaceEnvValue(env, pair[0], pair[1])
	}
	return appendGitConfigEnv(env, []gitConfigSetting{{key: "credential.helper", value: ""}, {key: "core.hooksPath", value: emptyHooks}})
}

func useStrictInventoryTemp(t *testing.T) string {
	t.Helper()
	base := filepath.Join(testTempDir(t), "inventory-temp")
	if err := os.MkdirAll(base, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMP", base)
	t.Setenv("TEMP", base)
	t.Setenv("TMPDIR", base)
	return base
}

func withTestInventoryWorkspace(t *testing.T, ctx context.Context) context.Context {
	t.Helper()
	workspace := filepath.Join(testTempDir(t), "private-inventory")
	if err := snapshot.EnsurePrivateDir(workspace); err != nil {
		t.Fatal(err)
	}
	ctx, err := WithInventoryWorkspace(ctx, workspace)
	if err != nil {
		t.Fatal(err)
	}
	return ctx
}

func assertNoStrictInventoryTemp(t *testing.T, base string) {
	t.Helper()
	entries, err := os.ReadDir(base)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("temporary inventory repositories remain: %d", len(entries))
	}
}

func configureCheckpointBackend(t *testing.T, root, backend string) {
	t.Helper()
	settingsDir := filepath.Join(root, ".entire")
	if err := os.MkdirAll(settingsDir, 0700); err != nil {
		t.Fatal(err)
	}
	body := `{"enabled":true,"checkpoints":{"primary":{"type":"` + backend + `"}}}`
	if err := os.WriteFile(filepath.Join(settingsDir, "settings.json"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func writeStrictCodexSource(t *testing.T) string {
	t.Helper()
	source := filepath.Join(testTempDir(t), "session.jsonl")
	text := `{"type":"session_meta","payload":{"id":"remote-only-session"}}
{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"remote coverage probe"}]}}
`
	if err := os.WriteFile(source, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	return source
}

func TestStrictConfiguredRemoteFindsRemoteOnlyCheckpointAndRetryIsNoop(t *testing.T) {
	for _, backend := range []string{"git-branch", "git-refs"} {
		t.Run(backend, func(t *testing.T) {
			emptyConfig := filepath.Join(testTempDir(t), "empty.gitconfig")
			useStrictInventoryTemp(t)
			if err := os.WriteFile(emptyConfig, nil, 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("GIT_CONFIG_GLOBAL", emptyConfig)
			bare := filepath.Join(testTempDir(t), "remote.git")
			runStrictGit(t, filepath.Dir(bare), "init", "--bare", "--quiet", bare)
			writer := filepath.Join(testTempDir(t), "writer")
			if err := os.MkdirAll(writer, 0700); err != nil {
				t.Fatal(err)
			}
			runStrictGit(t, writer, "init", "--quiet", "-b", "main")
			runStrictGit(t, writer, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false", "commit", "--quiet", "--allow-empty", "-m", "initial")
			runStrictGit(t, writer, "remote", "add", "origin", bare)
			runStrictGit(t, writer, "push", "--quiet", "-u", "origin", "main")
			configureCheckpointBackend(t, writer, backend)
			source := writeStrictCodexSource(t)
			anchor := destinationHead(t, writer)
			imported, err := ImportCodex(withTestInventoryWorkspace(t, context.Background()), writer, source, anchor, false)
			if err != nil || imported.TurnsImported != 1 {
				t.Fatalf("writer import: %+v %v", imported, err)
			}
			pushCheckpointStorage(t, writer, bare, backend)

			reader := filepath.Join(testTempDir(t), "reader")
			runStrictGit(t, filepath.Dir(reader), "clone", "--quiet", "--single-branch", "--branch", "main", bare, reader)
			configureCheckpointBackend(t, reader, backend)
			readerRepo, err := git.PlainOpen(reader)
			if err != nil {
				t.Fatal(err)
			}
			adapter := codexImporter(t)
			full, err := os.ReadFile(source)
			if err != nil {
				_ = readerRepo.Close()
				t.Fatal(err)
			}
			transcript, err := redact.JSONLBytes(checkpoint.SanitizeTranscriptForAgentType(adapter.AgentType(), full))
			if err != nil {
				_ = readerRepo.Close()
				t.Fatal(err)
			}
			turns, err := adapter.SplitTurns(agentimport.SessionFile{Path: source, SessionID: "remote-only-session"}, full)
			if err != nil {
				_ = readerRepo.Close()
				t.Fatal(err)
			}
			starts := make([]int, 0, len(turns))
			for _, turn := range turns {
				starts = append(starts, turn.LineStart)
			}
			proofCtx := withTestInventoryWorkspace(t, settings.WithWorktreeRoot(context.Background(), reader))
			proof, err := AssessCodexStorage(proofCtx, reader, readerRepo, reconcile.Candidate{
				SessionID: "remote-only-session", Agent: string(adapter.AgentType()), Transcript: transcript.Bytes(), Starts: starts,
			})
			_ = readerRepo.Close()
			if err != nil || proof.Coverage.Status != "present" || !proof.ConfiguredRemotesComplete || proof.RemoteTargets == 0 {
				t.Fatalf("reader did not verify remote-only checkpoint: %+v %v", proof, err)
			}

			retry := filepath.Join(testTempDir(t), "retry")
			runStrictGit(t, filepath.Dir(retry), "clone", "--quiet", "--single-branch", "--branch", "main", bare, retry)
			configureCheckpointBackend(t, retry, backend)
			before := runStrictGit(t, retry, "for-each-ref", "--format=%(refname) %(objectname)")
			retried, err := ImportCodex(withTestInventoryWorkspace(t, context.Background()), retry, source, destinationHead(t, retry), false)
			if err != nil || retried.TurnsImported != 0 || retried.TurnsSkipped != 1 {
				t.Fatalf("remote-only retry: %+v %v", retried, err)
			}
			after := runStrictGit(t, retry, "for-each-ref", "--format=%(refname) %(objectname)")
			if string(before) != string(after) {
				t.Fatal("retry changed local refs")
			}
			if strings.Contains(string(after), "refs/entire/checkpoints") || strings.Contains(string(after), "refs/heads/entire/checkpoints/v1") {
				t.Fatal("retry installed checkpoint refs into the checkout")
			}
		})
	}
}

func TestStrictInventoryHoldsSameSessionWithDifferentAgent(t *testing.T) {
	candidate := reconcile.Candidate{
		SessionID:  "session-1",
		Agent:      "codex",
		Transcript: []byte("candidate\n"),
		Starts:     []int{0},
	}
	assessment, err := reconcile.AssessExact(candidate, []reconcile.Record{{
		CheckpointID: "checkpoint-1",
		SessionIndex: 0,
		Content: checkpoint.SessionContent{Metadata: checkpoint.Metadata{
			SessionID: "session-1",
			Agent:     "claude",
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if assessment.Status != "hold" {
		t.Fatalf("same session under a different agent must hold, got %+v", assessment)
	}
}

func TestStrictDedicatedCheckpointRemoteFindsRemoteOnlyCheckpoint(t *testing.T) {
	for _, backend := range []string{"git-branch", "git-refs"} {
		t.Run(backend, func(t *testing.T) {
			// The dedicated target resolves to a GitHub-shaped URL from Entire's
			// settings, but the fixture must never be able to contact the network.
			// The exact URL rewrite below routes that target to a second local bare
			// repository; GIT_ALLOW_PROTOCOL rejects any unrewritten network URL.
			t.Setenv("GIT_ALLOW_PROTOCOL", "file")
			useStrictInventoryTemp(t)
			globalConfig := filepath.Join(testTempDir(t), "gitconfig")
			if err := os.WriteFile(globalConfig, nil, 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("GIT_CONFIG_GLOBAL", globalConfig)
			projectBare := filepath.Join(testTempDir(t), "project.git")
			checkpointBare := filepath.Join(testTempDir(t), "checkpoints.git")
			runStrictGit(t, filepath.Dir(projectBare), "init", "--bare", "--quiet", projectBare)
			runStrictGit(t, filepath.Dir(checkpointBare), "init", "--bare", "--quiet", checkpointBare)

			writer := filepath.Join(testTempDir(t), "writer")
			if err := os.MkdirAll(writer, 0700); err != nil {
				t.Fatal(err)
			}
			runStrictGit(t, writer, "init", "--quiet", "-b", "main")
			runStrictGit(t, writer, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false", "commit", "--quiet", "--allow-empty", "-m", "initial")
			configureCheckpointBackend(t, writer, backend)
			source := writeStrictCodexSource(t)
			if imported, err := ImportCodex(withTestInventoryWorkspace(t, context.Background()), writer, source, destinationHead(t, writer), false); err != nil || imported.TurnsImported != 1 {
				t.Fatalf("writer import: %+v %v", imported, err)
			}
			runStrictGit(t, writer, "remote", "add", "origin", projectBare)
			runStrictGit(t, writer, "push", "--quiet", "-u", "origin", "main")
			pushCheckpointStorage(t, writer, checkpointBare, backend)

			// Preserve a GitHub-shaped project identity for Entire's ownership
			// check. Only the dedicated checkpoint URL is rewritten; the file-only
			// protocol policy blocks any attempted network fallback to origin.
			checkpointURL := "https://github.com/MooseGooseConsulting/checkpoints.git"
			slashPath := filepath.ToSlash(checkpointBare)
			fileURL := "file:///" + slashPath
			if strings.HasPrefix(slashPath, "/") {
				fileURL = "file://" + slashPath
			}
			configLine := "[url \"" + fileURL + "\"]\n\tinsteadOf = " + checkpointURL + "\n"
			if err := os.WriteFile(globalConfig, []byte(configLine), 0600); err != nil {
				t.Fatal(err)
			}

			reader := filepath.Join(testTempDir(t), "reader")
			runStrictGit(t, filepath.Dir(reader), "clone", "--quiet", "--single-branch", "--branch", "main", projectBare, reader)
			runStrictGit(t, reader, "remote", "set-url", "origin", "https://github.com/MooseGooseConsulting/project.git")
			configureCheckpointBackend(t, reader, backend)
			if err := os.WriteFile(filepath.Join(reader, ".entire", "settings.local.json"), []byte(`{"strategy_options":{"checkpoint_remote":{"provider":"github","repo":"MooseGooseConsulting/checkpoints"}}}`), 0600); err != nil {
				t.Fatal(err)
			}
			if listing := runStrictGitWithGlobalConfig(t, reader, globalConfig, "ls-remote", checkpointURL); len(strings.TrimSpace(string(listing))) == 0 {
				t.Fatal("dedicated checkpoint remote rewrite did not reach the populated bare repository")
			}

			readerRepo, err := git.PlainOpen(reader)
			if err != nil {
				t.Fatal(err)
			}
			adapter := codexImporter(t)
			full, err := os.ReadFile(source)
			if err != nil {
				_ = readerRepo.Close()
				t.Fatal(err)
			}
			transcript, err := redact.JSONLBytes(checkpoint.SanitizeTranscriptForAgentType(adapter.AgentType(), full))
			if err != nil {
				_ = readerRepo.Close()
				t.Fatal(err)
			}
			turns, err := adapter.SplitTurns(agentimport.SessionFile{Path: source, SessionID: "remote-only-session"}, full)
			if err != nil {
				_ = readerRepo.Close()
				t.Fatal(err)
			}
			starts := make([]int, 0, len(turns))
			for _, turn := range turns {
				starts = append(starts, turn.LineStart)
			}
			proofCtx := withTestInventoryWorkspace(t, context.Background())
			proof, err := AssessCodexStorage(proofCtx, reader, readerRepo, reconcile.Candidate{
				SessionID: "remote-only-session", Agent: string(adapter.AgentType()), Transcript: transcript.Bytes(), Starts: starts,
			})
			_ = readerRepo.Close()
			if err != nil || proof.Coverage.Status != "present" || !proof.ConfiguredRemotesComplete || proof.RemoteTargets != 1 {
				t.Fatalf("dedicated checkpoint_remote was not inventoried: %+v %v", proof, err)
			}
		})
	}
}

func TestStrictRemoteInventoryRequiresPrivateWorkspace(t *testing.T) {
	tempBase := useStrictInventoryTemp(t)
	_, repo, err := fetchRemoteInventory(context.Background(), testTempDir(t), filepath.Join(testTempDir(t), "missing.git"))
	if err == nil || err.Error() != "configured checkpoint inventory requires a private workspace" || repo != nil {
		if repo != nil {
			_ = repo.Close()
		}
		t.Fatalf("inventory without a private workspace was not rejected safely: %v", err)
	}
	assertNoStrictInventoryTemp(t, tempBase)
}

func TestStrictRemoteInventoryListingFailureCreatesNoRepository(t *testing.T) {
	ctx := withTestInventoryWorkspace(t, context.Background())
	workspace, _ := inventoryWorkspaceFromContext(ctx)
	_, repo, err := fetchRemoteInventory(ctx, testTempDir(t), filepath.Join(testTempDir(t), "missing.git"))
	if err == nil || repo != nil {
		if repo != nil {
			_ = repo.Close()
		}
		t.Fatal("unreachable configured target unexpectedly opened")
	}
	assertNoStrictInventoryTemp(t, workspace)
}

func TestStrictRemoteInventoryFetchUsesRepositoryHTTPAuthAndIsolatedGitPaths(t *testing.T) {
	setStrictInventoryProcessEnv(t)
	inventoryCtx := withTestInventoryWorkspace(t, context.Background())
	workspace, _ := inventoryWorkspaceFromContext(inventoryCtx)
	remote := filepath.Join(testTempDir(t), "checkpoint-store.git")
	writer := filepath.Join(testTempDir(t), "writer")
	configRepo := filepath.Join(testTempDir(t), "project")
	runStrictGit(t, filepath.Dir(remote), "init", "--bare", "--quiet", remote)
	if err := os.MkdirAll(writer, 0700); err != nil {
		t.Fatal(err)
	}
	runStrictGit(t, writer, "init", "--quiet", "-b", "main")
	runStrictGit(t, writer, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false", "commit", "--quiet", "--allow-empty", "-m", "metadata")
	runStrictGit(t, writer, "push", "--quiet", remote, "HEAD:refs/heads/entire/checkpoints/v1")
	if err := os.MkdirAll(configRepo, 0700); err != nil {
		t.Fatal(err)
	}
	runStrictGit(t, configRepo, "init", "--quiet")
	const localAuthorization = "Basic repository-credential"
	const checkpointToken = "inventory-checkpoint-token"
	tokenAuthorization := "Basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:"+checkpointToken))
	var expectedAuthorization atomic.Value
	expectedAuthorization.Store(localAuthorization)
	var acceptedRequests atomic.Int32
	var rejectedRequests atomic.Int32
	var observedAuthValues atomic.Int32
	var observedLocalAuth atomic.Bool
	var observedExtraHeader atomic.Bool
	fileServer := http.StripPrefix("/org/checkpoint-store.git/", http.FileServer(http.Dir(remote)))
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		values := r.Header.Values("Authorization")
		observedAuthValues.Store(int32(len(values)))
		observedLocalAuth.Store(len(values) > 0 && values[0] == localAuthorization)
		observedExtraHeader.Store(r.Header.Get("X-Fixture") == "retained")
		if len(values) != 1 || values[0] != expectedAuthorization.Load().(string) || r.Header.Get("X-Fixture") != "retained" {
			rejectedRequests.Add(1)
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		acceptedRequests.Add(1)
		// This fixture uses Git's dumb HTTP transport. Ignore the smart service
		// query so Git falls back to the advertised static refs and objects.
		r.URL.RawQuery = ""
		r.URL.ForceQuery = false
		fileServer.ServeHTTP(w, r)
	}))
	defer server.Close()
	target := server.URL + "/org/checkpoint-store.git"
	runStrictGit(t, remote, "update-server-info")
	runStrictGit(t, configRepo, "config", "--local", "http."+target+".extraHeader", "X-Fixture: retained")
	runStrictGit(t, configRepo, "config", "--local", "--add", "http."+target+".extraHeader", "Authorization: "+localAuthorization)
	t.Setenv("GIT_SSL_NO_VERIFY", "1")
	emptyGlobalConfig := filepath.Join(t.TempDir(), "empty.gitconfig")
	if err := os.WriteFile(emptyGlobalConfig, nil, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", emptyGlobalConfig)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	transportConfig, err := readLocalTransportConfig(configRepo)
	if err != nil {
		t.Fatal("could not read source transport settings")
	}
	configCmd := exec.Command("git", "config", "--get-urlmatch", "http.extraheader", target+"/info/refs")
	configCmd.Env = appendGitConfigEnv(strictFixtureGitEnv(t), transportConfig)
	configOutput, err := configCmd.Output()
	if err != nil || strings.TrimSpace(string(configOutput)) != "Authorization: "+localAuthorization {
		t.Fatal("URL-matched repository Authorization header was not found by Git")
	}
	// --get-urlmatch reports one effective value for this multivalue setting;
	// the authenticated request below checks both headers and rejects duplicates.
	if !hasRepositoryAuthorizationHeader(transportConfig, target+"/info/refs") {
		t.Fatal("repository Authorization header was not recognized for token precedence")
	}
	expectedHash := strings.TrimSpace(string(runStrictGit(t, writer, "rev-parse", "HEAD")))

	// Poison the ambient Git repository and object locations. Neither config
	// discovery nor the isolated fetch may follow these inherited paths.
	poisonRepo := filepath.Join(t.TempDir(), "poison.git")
	runStrictGit(t, filepath.Dir(poisonRepo), "init", "--bare", "--quiet", poisonRepo)
	poisonObjects := filepath.Join(t.TempDir(), "poison-objects")
	if err := os.MkdirAll(poisonObjects, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_DIR", poisonRepo)
	t.Setenv("GIT_WORK_TREE", t.TempDir())
	t.Setenv("GIT_COMMON_DIR", poisonRepo)
	t.Setenv("GIT_INDEX_FILE", filepath.Join(poisonRepo, "index"))
	t.Setenv("GIT_OBJECT_DIRECTORY", poisonObjects)
	t.Setenv("GIT_ALTERNATE_OBJECT_DIRECTORIES", poisonObjects)
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "credential.helper")
	t.Setenv("GIT_CONFIG_VALUE_0", "")
	t.Setenv(remotegit.CheckpointTokenEnvVar, checkpointToken)
	bareRoot, fetched, err := fetchRemoteInventory(inventoryCtx, configRepo, target)
	if err != nil {
		t.Fatalf("strict inventory fetch failed (accepted=%d rejected=%d auth_values=%d local_auth=%t extra_header=%t)", acceptedRequests.Load(), rejectedRequests.Load(), observedAuthValues.Load(), observedLocalAuth.Load(), observedExtraHeader.Load())
	}
	ref, err := fetched.Reference(plumbing.NewBranchReferenceName(paths.MetadataBranchName), true)
	if err != nil || ref == nil {
		_ = fetched.Close()
		t.Fatal("transport-configured fetch did not install the checkpoint metadata ref")
	}
	if ref.Hash().String() != expectedHash {
		_ = fetched.Close()
		t.Fatal("fetched checkpoint ref did not resolve to the source object")
	}
	if _, err := fetched.CommitObject(ref.Hash()); err != nil {
		_ = fetched.Close()
		t.Fatal("fetched checkpoint object was unreadable")
	}
	_ = fetched.Close()
	if entries, err := os.ReadDir(poisonObjects); err != nil || len(entries) != 0 {
		t.Fatal("Git wrote fetched objects into the poisoned object directory")
	}
	if filepath.Dir(bareRoot) != workspace {
		t.Fatal("inventory repository was created outside the private workspace")
	}

	// With only an empty URL-scoped Authorization value and a credential scoped
	// to another host, the Entire token is used while X-Fixture remains present.
	headerKey := "http." + target + ".extraHeader"
	runStrictGitWithoutRepositoryEnv(t, configRepo, "config", "--local", "--unset-all", headerKey)
	runStrictGitWithoutRepositoryEnv(t, configRepo, "config", "--local", headerKey, "Authorization: ")
	runStrictGitWithoutRepositoryEnv(t, configRepo, "config", "--local", "--add", headerKey, "X-Fixture: retained")
	runStrictGitWithoutRepositoryEnv(t, configRepo, "config", "--local", "http.https://unrelated.example.invalid/repo.extraHeader", "Authorization: Basic unrelated-credential")
	runStrictGitWithoutRepositoryEnv(t, configRepo, "config", "--local", "http."+server.URL+"/org/other-repo.extraHeader", "Authorization: Basic different-path-credential")
	expectedAuthorization.Store(tokenAuthorization)
	transportConfig, err = readLocalTransportConfig(configRepo)
	if err != nil || hasRepositoryAuthorizationHeader(transportConfig, target+"/info/refs") {
		t.Fatal("unrelated or empty Authorization header blocked the checkpoint token")
	}
	tokenEnv, err := strictGitEnv(configRepo, target)
	if err != nil {
		t.Fatal("could not prepare token authentication environment")
	}
	tokenSettingPresent := false
	for _, entry := range tokenEnv {
		if key, value, ok := strings.Cut(entry, "="); ok && strings.HasPrefix(key, "GIT_CONFIG_VALUE_") && value == "Authorization: "+tokenAuthorization {
			tokenSettingPresent = true
		}
	}
	configCmd = exec.Command("git", "config", "--get-urlmatch", "http.extraheader", target+"/info/refs")
	configCmd.Dir = configRepo
	configCmd.Env = tokenEnv
	configOutput, err = configCmd.Output()
	if err != nil || strings.TrimSpace(string(configOutput)) != "Authorization: "+tokenAuthorization {
		got := strings.TrimSpace(string(configOutput))
		parsed, _ := remotegit.ParseURL(target)
		t.Fatalf("Entire token header precedence failed (env_token=%t process_token=%t valid_token=%t protocol_https=%t local_auth=%t retained_other=%t token=%t)", tokenSettingPresent, os.Getenv(remotegit.CheckpointTokenEnvVar) == checkpointToken, validCheckpointToken(checkpointToken), parsed != nil && parsed.Protocol == remotegit.ProtocolHTTPS, got == localAuthorization, got == "X-Fixture: retained", got == "Authorization: "+tokenAuthorization)
	}
	bareRoot, fetched, err = fetchRemoteInventory(inventoryCtx, configRepo, target)
	if err != nil {
		t.Fatalf("strict inventory token fetch failed (accepted=%d rejected=%d auth_values=%d local_auth=%t extra_header=%t)", acceptedRequests.Load(), rejectedRequests.Load(), observedAuthValues.Load(), observedLocalAuth.Load(), observedExtraHeader.Load())
	}
	ref, err = fetched.Reference(plumbing.NewBranchReferenceName(paths.MetadataBranchName), true)
	if err != nil || ref == nil || ref.Hash().String() != expectedHash {
		_ = fetched.Close()
		t.Fatal("token-authenticated fetch did not retrieve the expected checkpoint object")
	}
	if _, err := fetched.CommitObject(ref.Hash()); err != nil {
		_ = fetched.Close()
		t.Fatal("token-authenticated checkpoint object was unreadable")
	}
	_ = fetched.Close()
	if entries, err := os.ReadDir(poisonObjects); err != nil || len(entries) != 0 {
		t.Fatal("token-authenticated fetch wrote into the poisoned object directory")
	}
	if filepath.Dir(bareRoot) != workspace {
		t.Fatal("token inventory repository was created outside the private workspace")
	}
}

// Strict inventory subprocess environments are derived from os.Environ, so
// isolate process-level Git auth/config and proxy settings as well as fixture
// helper environments. t.Setenv restores the caller's environment afterward.
func setStrictInventoryProcessEnv(t *testing.T) {
	t.Helper()
	emptyGlobal := filepath.Join(t.TempDir(), "empty-process.gitconfig")
	if err := os.WriteFile(emptyGlobal, nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{
		{"GIT_CONFIG_NOSYSTEM", "1"}, {"GIT_CONFIG_GLOBAL", emptyGlobal},
		{"GIT_CONFIG_COUNT", "1"}, {"GIT_CONFIG_KEY_0", "credential.helper"},
		{"GIT_CONFIG_VALUE_0", ""}, {"GIT_ASKPASS", ""}, {"SSH_ASKPASS", ""},
		{"SSH_ASKPASS_REQUIRE", "never"}, {"GIT_TERMINAL_PROMPT", "0"},
		{"GCM_INTERACTIVE", "Never"}, {"no_proxy", "localhost,127.0.0.1,::1"},
		{"NO_PROXY", "localhost,127.0.0.1,::1"},
	} {
		t.Setenv(pair[0], pair[1])
	}
	for _, key := range []string{
		"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy",
		"ALL_PROXY", "all_proxy", "FTP_PROXY", "ftp_proxy",
	} {
		t.Setenv(key, "")
	}
}

func TestHTTPAuthorizationScopeMatchingAgreesWithGit(t *testing.T) {
	cases := []struct {
		name    string
		section string
		target  string
	}{
		{name: "path prefix", section: "https://example.invalid/org/repo", target: "https://example.invalid/org/repo/info/refs"},
		{name: "path boundary", section: "https://example.invalid/org/rep", target: "https://example.invalid/org/repo/info/refs"},
		{name: "other host", section: "https://other.invalid/org/repo", target: "https://example.invalid/org/repo/info/refs"},
		{name: "other scheme", section: "http://example.invalid/org/repo", target: "https://example.invalid/org/repo/info/refs"},
		{name: "wildcard subdomain", section: "https://*.example.invalid/org/repo", target: "https://git.example.invalid/org/repo/info/refs"},
		{name: "wildcard one level only", section: "https://*.example.invalid/org/repo", target: "https://deep.git.example.invalid/org/repo/info/refs"},
		{name: "wildcard does not match apex", section: "https://*.example.invalid/org/repo", target: "https://example.invalid/org/repo/info/refs"},
		{name: "matching port", section: "https://example.invalid:8443/org/repo", target: "https://example.invalid:8443/org/repo/info/refs"},
		{name: "different port", section: "https://example.invalid:8443/org/repo", target: "https://example.invalid:9443/org/repo/info/refs"},
		{name: "default HTTPS port", section: "https://example.invalid:443/org/repo", target: "https://example.invalid/org/repo/info/refs"},
		{name: "default HTTP port", section: "http://example.invalid:80/org/repo", target: "http://example.invalid/org/repo/info/refs"},
		{name: "nondefault port is distinct", section: "https://example.invalid/org/repo", target: "https://example.invalid:8443/org/repo/info/refs"},
		{name: "password ignored", section: "https://alice:old@example.invalid/org/repo", target: "https://alice:new@example.invalid/org/repo/info/refs"},
		{name: "username matters", section: "https://alice:old@example.invalid/org/repo", target: "https://bob:new@example.invalid/org/repo/info/refs"},
		{name: "path case", section: "https://example.invalid/Org/Repo", target: "https://example.invalid/org/repo/info/refs"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			runStrictGit(t, repo, "init", "--quiet")
			runStrictGit(t, repo, "config", "--local", "http."+tc.section+".extraHeader", "Authorization: Basic fixture")
			cmd := exec.Command("git", "-C", repo, "config", "--local", "--get-urlmatch", "http.extraHeader", tc.target)
			cmd.Env = strictFixtureGitEnv(t)
			_, err := cmd.Output()
			gitMatches := err == nil
			if got := httpConfigURLMatches(tc.section, tc.target); got != gitMatches {
				t.Fatalf("URL scope matcher disagreed with Git: matcher=%t git=%t", got, gitMatches)
			}
		})
	}
}

func runStrictGitWithoutRepositoryEnv(t *testing.T, dir string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = strictFixtureGitEnv(t)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git fixture config command failed: %s", strings.TrimSpace(string(output)))
	}
	return output
}

func codexImporter(t *testing.T) agentimport.Importer {
	t.Helper()
	for _, imp := range agentimport.All() {
		if imp.Name() == "codex" {
			return imp
		}
	}
	t.Fatal("Codex importer unavailable")
	return nil
}

func pushCheckpointStorage(t *testing.T, writer, bare, backend string) {
	t.Helper()
	if backend == "git-branch" {
		runStrictGit(t, writer, "push", "--quiet", bare, "refs/heads/entire/checkpoints/v1:refs/heads/entire/checkpoints/v1")
		return
	}
	refs := strings.Fields(string(runStrictGit(t, writer, "for-each-ref", "--format=%(refname)", "refs/entire/checkpoints")))
	if len(refs) == 0 {
		t.Fatal("writer has no git-refs checkpoints")
	}
	args := []string{"push", "--quiet", bare}
	for _, ref := range refs {
		args = append(args, ref+":"+ref)
	}
	runStrictGit(t, writer, args...)
}
