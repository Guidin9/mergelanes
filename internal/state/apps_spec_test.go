package state

// Black-box tests for apps.json and the ctl/ app-rule drop box, written from the spec only.
// Helpers are prefixed "spec" so they cannot collide with names in apps.go.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"mergelanes/internal/apps"
)

// --- apps.json ---

func TestSpecLoadAppsWithoutAFile(t *testing.T) {
	got, err := LoadApps(t.TempDir())
	if err != nil {
		t.Fatalf("a missing apps file must not be an error: %v", err)
	}
	if got == nil {
		t.Fatal("want an empty map, got nil (callers add rules to it)")
	}
	if len(got) != 0 {
		t.Fatalf("want no rules, got %v", got)
	}
}

func TestSpecAppsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := map[string]apps.Mode{
		"steam":   {Kind: apps.ModeMerge},
		"chrome":  {Kind: apps.ModeAdapter, Adapter: "Ethernet 2"},
		"discord": {Kind: apps.ModeFastest},
	}
	if err := SaveApps(dir, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadApps(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("LoadApps = %v, want %v", got, want)
	}
	if base := filepath.Base(AppsPath(dir)); base != "apps.json" {
		t.Fatalf("AppsPath base = %q, want apps.json", base)
	}
	if st, err := os.Stat(AppsPath(dir)); err != nil || !st.Mode().IsRegular() {
		t.Fatalf("SaveApps did not write %s: %v", AppsPath(dir), err)
	}
	// Saved atomically: nothing but apps.json is left next to it.
	ents, err := os.ReadDir(filepath.Dir(AppsPath(dir)))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if e.Name() != "apps.json" {
			t.Errorf("SaveApps left %q behind", e.Name())
		}
	}
	// Saving again replaces the rules.
	if err := SaveApps(dir, map[string]apps.Mode{"steam": {Kind: apps.ModeFastest}}); err != nil {
		t.Fatal(err)
	}
	got, err = LoadApps(dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]apps.Mode{"steam": {Kind: apps.ModeFastest}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("after second save LoadApps = %v, want %v", got, want)
	}
}

func TestSpecSaveAppsEmpty(t *testing.T) {
	dir := t.TempDir()
	if err := SaveApps(dir, map[string]apps.Mode{}); err != nil {
		t.Fatal(err)
	}
	got, err := LoadApps(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("want no rules, got %v", got)
	}
}

func TestSpecAppsFileFormat(t *testing.T) {
	dir := t.TempDir()
	rules := map[string]apps.Mode{
		"steam":  {Kind: apps.ModeMerge},
		"chrome": {Kind: apps.ModeAdapter, Adapter: "Ethernet"},
	}
	if err := SaveApps(dir, rules); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(AppsPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("apps.json is not a JSON object: %v\n%s", err, b)
	}
	var version int
	if err := json.Unmarshal(raw["version"], &version); err != nil || version != 1 {
		t.Fatalf(`want "version":1, got %s (%v)`, raw["version"], err)
	}
	var modes map[string]string
	if err := json.Unmarshal(raw["apps"], &modes); err != nil {
		t.Fatalf(`want "apps" as an object of strings, got %s (%v)`, raw["apps"], err)
	}
	if want := map[string]string{"steam": "merge", "chrome": "adapter:Ethernet"}; !reflect.DeepEqual(modes, want) {
		t.Fatalf(`"apps" = %v, want %v`, modes, want)
	}
}

func TestSpecLoadAppsSanitises(t *testing.T) {
	dir := t.TempDir()
	raw := `{"version":1,"apps":{` +
		`"Steam.exe":"merge",` +
		`"chrome":"adapter:Ethernet",` +
		`"discord":"fastest",` +
		`"bad":"nonsense",` +
		`"noname":"adapter:",` +
		`"idle":"auto",` +
		`"":"merge"}}`
	if err := os.WriteFile(AppsPath(dir), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := LoadApps(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]apps.Mode{
		"steam":   {Kind: apps.ModeMerge},
		"chrome":  {Kind: apps.ModeAdapter, Adapter: "Ethernet"},
		"discord": {Kind: apps.ModeFastest},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("LoadApps = %v, want %v", got, want)
	}
}

func TestSpecLoadAppsBrokenFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(AppsPath(dir), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadApps(dir); err == nil {
		t.Fatal("want an error for a broken apps file")
	}
}

// --- ctl/ drop box ---

// specAppRequestPath drops a valid request and returns the file it landed in under dir/ctl (the
// name is not part of the spec). The request is left in place.
func specAppRequestPath(t *testing.T, dir string) string {
	t.Helper()
	if err := SendApp(dir, AppRequest{App: "probe.exe", Mode: "merge"}); err != nil {
		t.Fatal(err)
	}
	ents, err := os.ReadDir(filepath.Join(dir, "ctl"))
	if err != nil {
		t.Fatalf("SendApp must drop the request in ctl/: %v", err)
	}
	if len(ents) != 1 {
		var names []string
		for _, e := range ents {
			names = append(names, e.Name())
		}
		t.Fatalf("want exactly one request file in ctl/, got %v", names)
	}
	return filepath.Join(dir, "ctl", ents[0].Name())
}

func specMarshal(t *testing.T, req AppRequest) string {
	t.Helper()
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// specPad inserts n spaces right after the opening brace: still valid JSON when read in full.
func specPad(body string, n int) string {
	return "{" + strings.Repeat(" ", n) + body[1:]
}

func TestSpecAppRequestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if app, m, ok := TakeApp(dir); ok {
		t.Fatalf("want no request, got %q %+v", app, m)
	}
	for _, c := range []struct {
		req  AppRequest
		want apps.Mode
	}{
		{AppRequest{App: "Steam.exe", Mode: "adapter:Wi-Fi 2"}, apps.Mode{Kind: apps.ModeAdapter, Adapter: "Wi-Fi 2"}},
		{AppRequest{App: "chrome", Mode: "merge"}, apps.Mode{Kind: apps.ModeMerge}},
		{AppRequest{App: "Game.exe", Mode: "fastest"}, apps.Mode{Kind: apps.ModeFastest}},
		{AppRequest{App: "discord", Mode: "auto"}, apps.Mode{Kind: apps.ModeAuto}}, // removes a rule
	} {
		if err := SendApp(dir, c.req); err != nil {
			t.Fatal(err)
		}
		app, m, ok := TakeApp(dir)
		if !ok {
			t.Fatalf("%+v: want the request back", c.req)
		}
		if apps.Key(app) != apps.Key(c.req.App) || m != c.want {
			t.Fatalf("%+v: got %q %+v, want %q %+v", c.req, app, m, c.req.App, c.want)
		}
		if app, m, ok := TakeApp(dir); ok {
			t.Fatalf("%+v: a request must be consumed exactly once, got %q %+v again", c.req, app, m)
		}
	}
}

func TestSpecTakeAppAcceptsLimits(t *testing.T) {
	for _, c := range []struct {
		name string
		body func(t *testing.T) string
		want apps.Mode
	}{
		{"260-byte app", func(t *testing.T) string {
			return specMarshal(t, AppRequest{App: strings.Repeat("a", 260), Mode: "merge"})
		}, apps.Mode{Kind: apps.ModeMerge}},
		{"256-byte adapter", func(t *testing.T) string {
			return specMarshal(t, AppRequest{App: "steam", Mode: "adapter:" + strings.Repeat("e", 256)})
		}, apps.Mode{Kind: apps.ModeAdapter, Adapter: strings.Repeat("e", 256)}},
		{"3000-byte padded file", func(t *testing.T) string {
			return specPad(specMarshal(t, AppRequest{App: "steam", Mode: "fastest"}), 3000)
		}, apps.Mode{Kind: apps.ModeFastest}},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			p := specAppRequestPath(t, dir)
			if err := os.WriteFile(p, []byte(c.body(t)), 0o644); err != nil {
				t.Fatal(err)
			}
			_, m, ok := TakeApp(dir)
			if !ok || m != c.want {
				t.Fatalf("got %+v ok=%v, want %+v accepted", m, ok, c.want)
			}
		})
	}
}

func TestSpecTakeAppRejects(t *testing.T) {
	for _, c := range []struct {
		name string
		body func(t *testing.T) string
	}{
		{"invalid JSON", func(*testing.T) string { return "not json" }},
		{"empty app", func(t *testing.T) string { return specMarshal(t, AppRequest{App: "", Mode: "merge"}) }},
		{"261-byte app", func(t *testing.T) string {
			return specMarshal(t, AppRequest{App: strings.Repeat("a", 261), Mode: "merge"})
		}},
		{"unparsable mode", func(t *testing.T) string { return specMarshal(t, AppRequest{App: "steam", Mode: "bogus"}) }},
		{"empty mode", func(t *testing.T) string { return specMarshal(t, AppRequest{App: "steam", Mode: ""}) }},
		{"adapter without name", func(t *testing.T) string {
			return specMarshal(t, AppRequest{App: "steam", Mode: "adapter:"})
		}},
		{"257-byte adapter", func(t *testing.T) string {
			return specMarshal(t, AppRequest{App: "steam", Mode: "adapter:" + strings.Repeat("e", 257)})
		}},
		{"file padded past 4 KiB", func(t *testing.T) string {
			return specPad(specMarshal(t, AppRequest{App: "steam", Mode: "merge"}), 5000)
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			p := specAppRequestPath(t, dir)
			if err := os.WriteFile(p, []byte(c.body(t)), 0o644); err != nil {
				t.Fatal(err)
			}
			if app, m, ok := TakeApp(dir); ok {
				t.Fatalf("accepted %q %+v", app, m)
			}
		})
	}
}

func TestSpecTakeAppRejectsADirectory(t *testing.T) {
	dir := t.TempDir()
	p := specAppRequestPath(t, dir)
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(p, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, "x"), []byte(`{"app":"steam","mode":"merge"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if app, m, ok := TakeApp(dir); ok {
		t.Fatalf("a directory in place of the request was accepted: %q %+v", app, m)
	}
}
