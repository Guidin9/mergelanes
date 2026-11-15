package state

import (
	"path/filepath"

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
	if err := platform.SecureDir(dir, false); err != nil {
		return err
	}
	return platform.SecureDir(filepath.Join(dir, "ctl"), true)
}
