package state

import (
	"path/filepath"
	"strings"

	"mergelanes/internal/platform"
)

// defaultDir is %ProgramData%\MergeLanes: the tray runs elevated, so it must not write, delete or
// read requests in a folder the signed-in user's programs can change (see platform.SecureDir).
func defaultDir() string { return filepath.Join(platform.ProgramDataDir(), "MergeLanes") }

// Prepare creates the state dir and its ctl\ drop box with their access lists, and resets them if
// they were tampered with. Only the elevated instance can do that; for anyone else it does nothing
// (a normal user can still drop requests in ctl\ once the elevated instance has made it).
func Prepare(dir string) error {
	if !platform.IsAdmin() {
		return nil
	}
	// Only the default folder may be replaced when something else is found there; a folder the user
	// named with -state-dir is theirs, so that is an error instead.
	mayReplace := strings.EqualFold(filepath.Clean(dir), filepath.Clean(defaultDir()))
	if err := platform.SecureDir(dir, false, mayReplace); err != nil {
		return err
	}
	// ctl\ is inside a folder only administrators can change, so whatever is there may be replaced.
	return platform.SecureDir(filepath.Join(dir, "ctl"), true, true)
}
