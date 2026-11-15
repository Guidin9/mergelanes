package platform

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The tray runs elevated, but is driven by the signed-in user from a normal shell, and everything
// the user's programs can write is in reach of malware running as that user. So nothing the elevated
// process writes, deletes or runs may live in a folder the user can change: not %TEMP%, not
// %LOCALAPPDATA%. Its files live in %ProgramData%\MergeLanes instead, which only SYSTEM and
// Administrators can change; its ctl\ subfolder is a drop box where users may add request files.

const (
	// SYSTEM and Administrators: full control. Users: read.
	stateSDDL = "O:BAD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;0x1200a9;;;BU)"
	// The same, plus users may add files (not folders), and keep control of the files they add.
	// 0x1200ab is list, add file, read attributes and extended attributes, traverse, read control.
	dropSDDL = "O:BAD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;;0x1200ab;;;BU)(A;OIIO;FA;;;CO)"
)

// SecureDir makes path a plain folder owned by Administrators (or SYSTEM) whose access list is
// reset to the one above (dropBox: the ctl\ variant). Whatever was planted there instead is moved
// out of the way, never followed: a junction or symlink is removed (not what it points to), and a
// folder someone else created is renamed aside. It must run elevated.
func SecureDir(path string, dropBox bool) error {
	sddl := stateSDDL
	if dropBox {
		sddl = dropSDDL
	}
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return err
	}
	p16, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	attrs, err := windows.GetFileAttributes(p16)
	switch {
	case errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_PATH_NOT_FOUND):
		return createDir(path, sd)
	case err != nil:
		return fmt.Errorf("%s: %w", path, err)
	case attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || attrs&windows.FILE_ATTRIBUTE_DIRECTORY == 0:
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("%s is not a plain folder and could not be removed: %w", path, err)
		}
		return createDir(path, sd)
	}
	trusted, err := ownedByAdmins(path)
	if err != nil {
		return err
	}
	if !trusted {
		aside := fmt.Sprintf("%s.untrusted-%d", path, time.Now().Unix())
		if err := os.Rename(path, aside); err != nil {
			return fmt.Errorf("%s was not created by MergeLanes and could not be moved aside: %w", path, err)
		}
		return createDir(path, sd)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}

func createDir(path string, sd *windows.SECURITY_DESCRIPTOR) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	p16, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	sa := windows.SecurityAttributes{SecurityDescriptor: sd}
	sa.Length = uint32(unsafe.Sizeof(sa))
	if err := windows.CreateDirectory(p16, &sa); err != nil {
		return fmt.Errorf("creating %s: %w", path, err)
	}
	return nil
}

func ownedByAdmins(path string) (bool, error) {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return false, fmt.Errorf("%s: %w", path, err)
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return false, err
	}
	return owner.IsWellKnown(windows.WinBuiltinAdministratorsSid) || owner.IsWellKnown(windows.WinLocalSystemSid), nil
}

// ProgramDataDir is %ProgramData%, from the shell's known folders rather than the environment,
// which the user controls.
func ProgramDataDir() string {
	if d, err := windows.KnownFolderPath(windows.FOLDERID_ProgramData, 0); err == nil && d != "" {
		return d
	}
	return `C:\ProgramData`
}

// installedUnderProgramFiles reports whether exe sits in Program Files, where only administrators
// can replace it.
func installedUnderProgramFiles(exe string) bool {
	exe = strings.ToLower(filepath.Clean(exe))
	for _, id := range []*windows.KNOWNFOLDERID{windows.FOLDERID_ProgramFiles, windows.FOLDERID_ProgramFilesX86} {
		d, err := windows.KnownFolderPath(id, 0)
		if err == nil && d != "" && strings.HasPrefix(exe, strings.ToLower(filepath.Clean(d))+`\`) {
			return true
		}
	}
	return false
}

// SystemTool is the full path of a Windows tool such as netsh.exe, so an elevated process never
// runs a same-named program from a folder on the user's PATH.
func SystemTool(name string) string {
	dir, err := windows.GetSystemDirectory()
	if err != nil {
		return name
	}
	return filepath.Join(dir, name)
}

// PowerShell is the full path of Windows PowerShell.
func PowerShell() string { return SystemTool(`WindowsPowerShell\v1.0\powershell.exe`) }
