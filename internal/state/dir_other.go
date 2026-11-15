//go:build !windows

package state

import (
	"os"
	"path/filepath"
	"runtime"
)

func defaultDir() string {
	// The macOS tray runs as root but is commanded by the user, so its state can't live in a
	// user-owned directory (root would follow a planted symlink): a fixed root-owned path.
	if runtime.GOOS == "darwin" {
		return macStateDir
	}
	// On Linux the tray runs as the user (with network capabilities only), so the user's own cache
	// directory is fine.
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	return filepath.Join(base, "mergelanes")
}

// Prepare creates the state dir, with markers going in a sticky world-writable ctl/ inside it, so
// a normal user can drop one without being able to touch anything else there.
func Prepare(dir string) error {
	ctl := filepath.Join(dir, "ctl")
	if err := os.MkdirAll(ctl, 0o755); err != nil {
		return err
	}
	return os.Chmod(ctl, os.ModeSticky|0o777)
}
