//go:build windows

package snapshot

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"unsafe"

	"golang.org/x/sys/windows"
)

func protectNewDirectory(path string) error {
	current, err := user.Current()
	if err != nil || current.Uid == "" {
		return fmt.Errorf("cannot identify Windows user SID: %v", err)
	}
	// Mutating command runs only for a new empty directory. Existing paths are
	// verify-only in EnsurePrivateDir.
	args := []string{path, "/inheritance:r", "/grant:r", "*" + current.Uid + ":(OI)(CI)F", "*S-1-5-18:(OI)(CI)F", "*S-1-5-32-544:(OI)(CI)F"}
	if output, err := exec.Command("icacls", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("protect new Windows directory: %w: %s", err, string(output))
	}
	return verifyPrivateDirectory(path)
}

func verifyPrivateDirectory(path string) error {
	root, err := os.OpenRoot(path)
	if err != nil {
		return fmt.Errorf("open Windows private directory: %w", err)
	}
	defer root.Close()
	return verifyPrivateRoot(root)
}

func verifyPrivateRoot(root *os.Root) error {
	dir, err := root.Open(".")
	if err != nil {
		return fmt.Errorf("open anchored private directory: %w", err)
	}
	defer dir.Close()
	sd, err := windows.GetSecurityInfo(windows.Handle(dir.Fd()), windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("read anchored private directory ACL: %w", err)
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return fmt.Errorf("read anchored private directory owner: %w", err)
	}
	if owner == nil {
		return fmt.Errorf("anchored private directory owner is missing")
	}
	tokenUser, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return fmt.Errorf("identify current Windows user: %w", err)
	}
	if !owner.Equals(tokenUser.User.Sid) {
		return fmt.Errorf("private directory is not owned by current user")
	}
	control, _, err := sd.Control()
	if err != nil {
		return fmt.Errorf("read anchored private directory ACL flags: %w", err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		return fmt.Errorf("private directory ACL must be protected")
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return fmt.Errorf("read anchored private directory ACL: %w", err)
	}
	if dacl == nil {
		return fmt.Errorf("private directory ACL is empty and grants unrestricted access")
	}
	system, err := windows.StringToSid("S-1-5-18")
	if err != nil {
		return fmt.Errorf("parse SYSTEM SID: %w", err)
	}
	admins, err := windows.StringToSid("S-1-5-32-544")
	if err != nil {
		return fmt.Errorf("parse Administrators SID: %w", err)
	}
	hasOwnerFullControl := false
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return fmt.Errorf("read private directory ACL entry: %w", err)
		}
		switch ace.Header.AceType {
		case windows.ACCESS_DENIED_ACE_TYPE:
			continue
		case windows.ACCESS_ALLOWED_ACE_TYPE:
		default:
			return fmt.Errorf("private directory ACL has unsupported allow entry type %d", ace.Header.AceType)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !sid.IsValid() {
			return fmt.Errorf("private directory ACL contains invalid SID")
		}
		if !sid.Equals(owner) && !sid.Equals(system) && !sid.Equals(admins) {
			return fmt.Errorf("private directory ACL grants access to an unapproved principal")
		}
		const fileAllAccess = windows.ACCESS_MASK(0x001F01FF)
		if sid.Equals(owner) && ace.Mask&fileAllAccess == fileAllAccess {
			hasOwnerFullControl = true
		}
	}
	if !hasOwnerFullControl {
		return fmt.Errorf("private directory ACL lacks full control for current user")
	}
	return nil
}
