package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"mergelanes/internal/apps"
)

// The per-app rules live in apps.json next to limits.json:
//
//	{"version": 1, "apps": {"steam": "merge", "valorant-win64-shipping": "fastest", "chrome": "adapter:Ethernet"}}
//
// Keys are apps.Key names; values are apps.ParseMode strings. A change from a terminal travels
// through ctl/app like a speed limit does, and the running instance saves the file.

const appsVersion = 1

type appsFile struct {
	Version int               `json:"version"`
	Apps    map[string]string `json:"apps"`
}

// AppsPath is where the rules are saved.
func AppsPath(dir string) string { return filepath.Join(limitsDir(dir), "apps.json") }

// LoadApps reads the saved rules. A missing file means no rules; a rule that does not parse is
// dropped rather than failing the whole file.
func LoadApps(dir string) (map[string]apps.Mode, error) {
	b, err := os.ReadFile(AppsPath(dir))
	if errors.Is(err, os.ErrNotExist) {
		return map[string]apps.Mode{}, nil
	}
	if err != nil {
		return nil, err
	}
	var f appsFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("%s: %w", AppsPath(dir), err)
	}
	out := make(map[string]apps.Mode, len(f.Apps))
	for name, s := range f.Apps {
		m, err := apps.ParseMode(s)
		if k := apps.Key(name); err == nil && k != "" && m.Kind != apps.ModeAuto {
			out[k] = m
		}
	}
	return out, nil
}

// SaveApps writes the rules atomically.
func SaveApps(dir string, rules map[string]apps.Mode) error {
	if err := os.MkdirAll(limitsDir(dir), 0o755); err != nil {
		return err
	}
	f := appsFile{Version: appsVersion, Apps: make(map[string]string, len(rules))}
	for k, m := range rules {
		f.Apps[k] = m.String()
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp := AppsPath(dir) + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return replaceFile(tmp, AppsPath(dir))
}

// AppRequest is one app's new rule, from `mergelanes app`.
type AppRequest struct {
	App  string `json:"app"`
	Mode string `json:"mode"`
}

func appRequestPath(dir string) string { return filepath.Join(dir, "ctl", "app") }

// SendApp hands a rule to a running instance.
func SendApp(dir string, req AppRequest) error {
	b, err := json.Marshal(req)
	if err != nil {
		return err
	}
	return dropFile(dir, "app", b)
}

// TakeApp consumes a pending rule request. Like TakeLimit, it treats the file as hostile: ctl/ is
// writable by users who must not control anything but this one setting.
func TakeApp(dir string) (string, apps.Mode, bool) {
	b, ok := takeRequest(appRequestPath(dir))
	if !ok {
		return "", apps.Mode{}, false
	}
	var req AppRequest
	if json.Unmarshal(b, &req) != nil || apps.Key(req.App) == "" || len(req.App) > 260 {
		return "", apps.Mode{}, false
	}
	m, err := apps.ParseMode(req.Mode)
	if err != nil || len(m.Adapter) > 256 {
		return "", apps.Mode{}, false
	}
	return req.App, m, true
}
