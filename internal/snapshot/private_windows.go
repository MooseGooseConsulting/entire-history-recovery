//go:build windows

package snapshot

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strings"
)

// The static script gets paths through an environment variable, never via
// interpolated PowerShell source. Get-Acl compares SID objects, independent of
// localized principal names.
const privateACLScript = `$ErrorActionPreference = 'Stop'
$env:PSModulePath = (Join-Path $PSHOME 'Modules') + [IO.Path]::PathSeparator + $env:PSModulePath
Import-Module Microsoft.PowerShell.Security -ErrorAction Stop
$p = $env:ENTIRE_RECOVERY_PRIVATE_DIR
$a = Get-Acl -LiteralPath $p
$who = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value
if (-not $a.AreAccessRulesProtected) { throw 'DACL inheritance enabled' }
if ($a.GetOwner([Security.Principal.SecurityIdentifier]).Value -ne $who) { throw 'Wrong directory owner' }
$allow = @($who, 'S-1-5-18', 'S-1-5-32-544')
$has = $false
foreach ($r in $a.GetAccessRules($true, $true, [Security.Principal.SecurityIdentifier])) {
 if ($r.AccessControlType -eq [Security.AccessControl.AccessControlType]::Allow) {
  $sid = $r.IdentityReference.Value
  if ($sid -notin $allow) { throw ('Unapproved allow ACE ' + $sid) }
  if ($sid -eq $who) { $has = $true }
 }
}
if (-not $has) { throw 'Current user ACL missing' }
`

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
	cmd := exec.Command("powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", privateACLScript)
	env := append(os.Environ(), "ENTIRE_RECOVERY_PRIVATE_DIR="+path)
	cmd.Env = env
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("verify Windows private ACL: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}
