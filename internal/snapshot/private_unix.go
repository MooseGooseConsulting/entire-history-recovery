//go:build !windows

package snapshot

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"strconv"
	"syscall"
)

func protectNewDirectory(string) error { return nil } // mode 0700 is applied atomically by os.Mkdir

func verifyPrivateDirectory(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("cannot verify private directory owner")
	}
	current, err := user.Current()
	if err != nil {
		return fmt.Errorf("identify private directory owner: %w", err)
	}
	uid, err := strconv.ParseUint(current.Uid, 10, 32)
	if err != nil || uint32(uid) != stat.Uid {
		return errors.New("private directory is not owned by current user")
	}
	if info.Mode().Perm()&0077 != 0 || info.Mode().Perm()&0700 != 0700 {
		return fmt.Errorf("private directory permissions must be owner rwx only: got %04o", info.Mode().Perm())
	}
	return nil
}
