package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"mergelanes/internal/apps"
	"mergelanes/internal/engine"
)

// The elevated process can't be signalled from a normal shell, so control goes through files in
// its state directory: status.json (written once a second while on) and marker files ("on", "off",
// "quit") and requests ("limit", "app") in ctl/ that the running instance consumes.

// macStateDir is where the macOS tray keeps its status and markers. It is under /var/run because
// the tray runs as root but is driven by a normal user, so the directory has to be root-owned.
const macStateDir = "/var/run/mergelanes"

// macLimitsDir is where the caps live on macOS: /var/run is emptied at boot, and a metered-tether
// cap that forgets itself after a reboot is worse than no cap at all. Application Support is the
// usual place for a setting that has to stay.
const macLimitsDir = "/Library/Application Support/MergeLanes"

type Status struct {
	PID     int
	Mode    string
	V6      string
	Since   time.Time
	Updated time.Time
	NICs    []engine.SlotStats
	Apps    []apps.AppStat `json:",omitempty"`
}

// DefaultDir is where status, markers, limits and app rules live: see dir_windows.go and
// dir_other.go.
func DefaultDir() string { return defaultDir() }

func Write(dir string, st Status) {
	b, err := json.Marshal(st)
	if err != nil {
		return
	}
	tmp := filepath.Join(dir, "status.json.tmp")
	if os.WriteFile(tmp, b, 0o644) == nil {
		os.Rename(tmp, filepath.Join(dir, "status.json"))
	}
}

func Clear(dir string) { os.Remove(filepath.Join(dir, "status.json")) }

// Read returns the status only if it is fresh, i.e. a live process is writing it.
func Read(dir string) (*Status, bool) {
	b, err := os.ReadFile(filepath.Join(dir, "status.json"))
	if err != nil {
		return nil, false
	}
	var st Status
	if json.Unmarshal(b, &st) != nil || time.Since(st.Updated) > 3*time.Second {
		return nil, false
	}
	return &st, true
}

func SendMarker(dir, name string) error { return dropFile(dir, name, nil) }

// dropFile puts a request into ctl/ under name. It is written to a new file with a random name and
// renamed into place, so a file or link someone planted under the final name is replaced, never
// written through: the sender may be elevated (an admin shell), and ctl/ is open to every user.
func dropFile(dir, name string, b []byte) error {
	ctl := filepath.Join(dir, "ctl")
	if err := os.MkdirAll(ctl, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(ctl, name+".*.tmp")
	if err != nil {
		return err
	}
	_, werr := f.Write(b)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Rename(f.Name(), filepath.Join(ctl, name))
	}
	if werr != nil {
		os.Remove(f.Name())
	}
	return werr
}

// TakeMarker consumes a marker, reporting whether it was there.
func TakeMarker(dir, name string) bool { return os.Remove(filepath.Join(dir, "ctl", name)) == nil }
