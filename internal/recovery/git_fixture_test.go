package recovery

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// runHermeticGitFixture keeps test commits independent of host Git settings,
// signing defaults and hooks. The empty global config and hooks directory are
// created by the owning test under its temporary directory.
func runHermeticGitFixture(t *testing.T, dir string, args ...string) []byte {
	t.Helper()
	globalConfig := os.Getenv("RECOVERY_TEST_EMPTY_GIT_CONFIG")
	hooksPath := os.Getenv("RECOVERY_TEST_EMPTY_GIT_HOOKS")
	if globalConfig == "" || hooksPath == "" {
		t.Fatal("hermetic Git fixture paths were not set")
	}
	gitArgs := []string{"-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false", "-c", "core.hooksPath=" + hooksPath}
	gitArgs = append(gitArgs, args...)
	cmd := exec.Command("git", gitArgs...)
	cmd.Dir = dir
	cmd.Env = replaceEnv(os.Environ(), map[string]string{
		"GIT_CONFIG_NOSYSTEM": "1",
		"GIT_CONFIG_GLOBAL":   globalConfig,
		"GIT_AUTHOR_NAME":     "Fixture",
		"GIT_AUTHOR_EMAIL":    "fixture@example.invalid",
		"GIT_COMMITTER_NAME":  "Fixture",
		"GIT_COMMITTER_EMAIL": "fixture@example.invalid",
	})
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("hermetic git %v: %v: %s", args, err, out)
	}
	return out
}

func replaceEnv(env []string, values map[string]string) []string {
	filtered := make([]string, 0, len(env)+len(values))
	for _, item := range env {
		name, _, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		if _, replaced := values[name]; !replaced {
			filtered = append(filtered, item)
		}
	}
	for name, value := range values {
		filtered = append(filtered, name+"="+value)
	}
	return filtered
}
