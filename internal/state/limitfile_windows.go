package state

import (
	"os"

	"golang.org/x/sys/windows"
)

// openLimitFile opens a request for reading without following a symlink or junction: users may
// add files to ctl\ (and, with Developer Mode, symlinks), so a request could point anywhere. The
// link itself is opened instead, and readLimitRequest rejects whatever is not a regular file.
func openLimitFile(path string) (*os.File, error) {
	p16, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(p16, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(h), path), nil
}
