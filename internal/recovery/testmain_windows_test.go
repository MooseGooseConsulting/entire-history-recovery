package recovery

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MooseGooseConsulting/entire-history-recovery/internal/snapshot"
)

const testChildEnv = "ENTIRE_RECOVERY_TEST_CHILD"

func TestMain(m *testing.M) {
	if os.Getenv(testChildEnv) == "1" {
		os.Exit(m.Run())
	}

	root, err := createPrivateTestRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "create private parent-owned recovery test root failed")
		os.Exit(1)
	}

	args := append([]string(nil), os.Args[1:]...)
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(filteredTestEnv(os.Environ()), testChildEnv+"=1", testParentRootEnv+"="+root)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	runErr := cmd.Run()
	cleanupErr := os.RemoveAll(root)
	if cleanupErr != nil {
		fmt.Fprintln(os.Stderr, "remove parent-owned recovery test root:", cleanupErr)
	}
	exitCode := 0
	if runErr != nil {
		fmt.Fprintln(os.Stderr, "isolated recovery test process failed:", runErr)
		exitCode = 1
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
	}
	if cleanupErr != nil && exitCode == 0 {
		exitCode = 1
	}
	os.Exit(exitCode)
}

func createPrivateTestRoot() (string, error) {
	for range 5 {
		suffix := make([]byte, 12)
		if _, err := rand.Read(suffix); err != nil {
			return "", err
		}
		root := filepath.Join(os.TempDir(), "entire-recovery-"+hex.EncodeToString(suffix))
		if _, err := os.Lstat(root); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return "", err
		}
		if err := snapshot.EnsurePrivateDir(root); err != nil {
			return "", err
		}
		return root, nil
	}
	return "", fmt.Errorf("could not allocate a unique private test root")
}

func filteredTestEnv(env []string) []string {
	filtered := make([]string, 0, len(env))
	for _, entry := range env {
		key, _, ok := strings.Cut(entry, "=")
		if ok && (key == testChildEnv || key == testParentRootEnv) {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}
