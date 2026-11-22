package assistant

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"mergelanes/internal/apps"
)

// Backend is what the assistant can see and change. Every setter sets an absolute value, so running
// a tool twice (a turn retried on another model) changes nothing the second time.
type Backend interface {
	Snapshot() (Snapshot, error)
	// SetAppLane sets an app's rule and reports whether a running MergeLanes applied it.
	SetAppLane(app string, m apps.Mode) (live bool, err error)
	SetMerging(on bool) error
	// SetLimit caps an adapter at mbps Mbit/s; 0 removes the cap.
	SetLimit(adapter string, mbps float64) (live bool, err error)
}

// Snapshot is the state the model sees at the start of every message and through get_status.
type Snapshot struct {
	Running  bool // a MergeLanes instance is running
	Merging  bool // system-wide merging is on
	Adapters []Adapter
	Apps     []App
}

type Adapter struct {
	Name      string
	Up        bool
	InMbps    float64 // through MergeLanes, last second
	OutMbps   float64
	PingMs    float64
	LimitMbps float64 // 0 = none
	Games     bool    // where lowest-ping flows go now
}

type App struct {
	Name      string           // exe name, e.g. "Spotify.exe"
	Rule      apps.Mode        // ModeAuto if none
	LastLane  string           // what its last flow got
	Flows     int64            // flows since MergeLanes started
	ByAdapter map[string]int64 // flows per adapter under its current rule
	LastSeen  time.Time        // zero for a saved rule of an app not seen since start
}

// toolSet is the "tools" entry of a request.
type toolSet struct {
	FunctionDeclarations []functionDeclaration `json:"functionDeclarations"`
}

type functionDeclaration struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

func obj(props map[string]any, required ...string) map[string]any {
	o := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		o["required"] = required
	}
	return o
}

var tools = []toolSet{{FunctionDeclarations: []functionDeclaration{
	{Name: "get_status", Description: "Live state: whether merging is on, each adapter's speed, ping and limit, and each app's lane rule with how many of its connections went over each adapter. Use it after a change or when the user wants current numbers."},
	{Name: "set_app_lane", Description: "Set which connection an app uses. Applies to the app's new connections; open ones stay where they are until the app reconnects.",
		Parameters: obj(map[string]any{
			"app":     map[string]any{"type": "string", "description": "The app's exe name from the state, e.g. Spotify.exe"},
			"lane":    map[string]any{"type": "string", "enum": []string{"auto", "merge", "fastest", "adapter"}, "description": "auto = default (games and voice on lowest ping, rest merged); merge = every connection over all adapters; fastest = all on the lowest-ping adapter; adapter = only the adapter named in 'adapter'"},
			"adapter": map[string]any{"type": "string", "description": "Adapter name from the state, only when lane is adapter"},
		}, "app", "lane")},
	{Name: "set_merging", Description: "Turn merging on or off for the whole computer. Off sends all traffic over the system's normal connection.",
		Parameters: obj(map[string]any{"on": map[string]any{"type": "boolean"}}, "on")},
	{Name: "set_speed_limit", Description: "Cap how fast MergeLanes uses one adapter, in Mbit/s. 0 removes the cap.",
		Parameters: obj(map[string]any{
			"adapter": map[string]any{"type": "string", "description": "Adapter name from the state"},
			"mbps":    map[string]any{"type": "number", "description": "Mbit/s, 0 for no limit"},
		}, "adapter", "mbps")},
}}}

// Action is one change a tool made, for the chat window to show under the reply.
type Action struct {
	Tool    string         `json:"tool"`
	Summary string         `json:"summary"`
	OK      bool           `json:"ok"`
	Detail  map[string]any `json:"detail,omitempty"` // the values set, so the window can word it in its language
}

// runTool executes one function call. A bad argument comes back to the model as an error result it
// can explain or ask about, never as a Go error.
func runTool(b Backend, call functionCall) (map[string]any, *Action) {
	var args struct {
		App     string   `json:"app"`
		Lane    string   `json:"lane"`
		Adapter string   `json:"adapter"`
		On      *bool    `json:"on"`
		Mbps    *float64 `json:"mbps"`
	}
	if len(call.Args) > 0 {
		if err := json.Unmarshal(call.Args, &args); err != nil {
			return errResult("arguments are not valid JSON: " + err.Error()), nil
		}
	}
	switch call.Name {
	case "get_status":
		snap, err := b.Snapshot()
		if err != nil {
			return errResult(err.Error()), nil
		}
		return map[string]any{"result": snapshotView(snap)}, nil

	case "set_app_lane":
		snap, err := b.Snapshot()
		if err != nil {
			return errResult(err.Error()), nil
		}
		app, note := resolveApp(snap, args.App)
		if app == "" {
			return errResult(note), nil
		}
		m, err := laneMode(args.Lane)
		if err != nil {
			return errResult(err.Error()), nil
		}
		if m.Kind == apps.ModeAdapter {
			name, ok := resolveAdapter(snap, args.Adapter)
			if !ok {
				return errResult(fmt.Sprintf("there is no adapter %q; adapters are: %s", args.Adapter, adapterNames(snap))), nil
			}
			m.Adapter = name
		}
		live, err := b.SetAppLane(app, m)
		act := &Action{Tool: call.Name, Summary: app + " → " + m.Label(), OK: err == nil,
			Detail: map[string]any{"app": app, "lane": laneWord(m.Kind), "adapter": m.Adapter}}
		if err != nil {
			return errResult(err.Error()), act
		}
		res := map[string]any{"app": app, "rule": m.Label(), "applied_now": live}
		if note != "" {
			res["note"] = note
		}
		if !live {
			res["note"] = strings.TrimSpace(note + " MergeLanes is not running, so it takes effect when it starts.")
		}
		return map[string]any{"result": res}, act

	case "set_merging":
		if args.On == nil {
			return errResult("'on' is required"), nil
		}
		err := b.SetMerging(*args.On)
		word := "off"
		if *args.On {
			word = "on"
		}
		act := &Action{Tool: call.Name, Summary: "merging " + word, OK: err == nil, Detail: map[string]any{"on": *args.On}}
		if err != nil {
			return errResult(err.Error()), act
		}
		return map[string]any{"result": map[string]any{"merging": word}}, act

	case "set_speed_limit":
		snap, err := b.Snapshot()
		if err != nil {
			return errResult(err.Error()), nil
		}
		name, ok := resolveAdapter(snap, args.Adapter)
		if !ok {
			return errResult(fmt.Sprintf("there is no adapter %q; adapters are: %s", args.Adapter, adapterNames(snap))), nil
		}
		if args.Mbps == nil || *args.Mbps < 0 || math.IsNaN(*args.Mbps) || *args.Mbps > 100000 {
			return errResult("mbps must be a number from 0 (no limit) to 100000"), nil
		}
		live, err := b.SetLimit(name, *args.Mbps)
		sum := fmt.Sprintf("%s limit %g Mbit/s", name, *args.Mbps)
		if *args.Mbps == 0 {
			sum = name + " no limit"
		}
		act := &Action{Tool: call.Name, Summary: sum, OK: err == nil, Detail: map[string]any{"adapter": name, "mbps": *args.Mbps}}
		if err != nil {
			return errResult(err.Error()), act
		}
		return map[string]any{"result": map[string]any{"adapter": name, "limit_mbps": *args.Mbps, "applied_now": live}}, act
	}
	return errResult("unknown tool " + call.Name), nil
}

func errResult(msg string) map[string]any { return map[string]any{"error": msg} }

func laneMode(lane string) (apps.Mode, error) {
	switch strings.ToLower(strings.TrimSpace(lane)) {
	case "auto", "":
		return apps.Mode{Kind: apps.ModeAuto}, nil
	case "merge":
		return apps.Mode{Kind: apps.ModeMerge}, nil
	case "fastest":
		return apps.Mode{Kind: apps.ModeFastest}, nil
	case "adapter":
		return apps.Mode{Kind: apps.ModeAdapter}, nil
	}
	return apps.Mode{}, fmt.Errorf("lane must be auto, merge, fastest or adapter, not %q", lane)
}

// resolveApp finds the exe the model means. An exact (case-insensitive, .exe optional) match wins;
// then a unique app whose name starts with or contains what was given. An exe name that matches
// nothing is accepted as a rule for an app that has not connected yet, with a note.
func resolveApp(s Snapshot, given string) (string, string) {
	k := apps.Key(given)
	if k == "" {
		return "", "no app name given"
	}
	var prefix, sub []string
	for _, a := range s.Apps {
		ak := apps.Key(a.Name)
		switch {
		case ak == k:
			return a.Name, ""
		case strings.HasPrefix(ak, k):
			prefix = append(prefix, a.Name)
		case strings.Contains(ak, k):
			sub = append(sub, a.Name)
		}
	}
	for _, c := range [][]string{prefix, sub} {
		if len(c) == 1 {
			return c[0], ""
		}
		if len(c) > 1 {
			return "", fmt.Sprintf("%q matches several apps: %s; pick one (call the tool once per app)", given, strings.Join(c, ", "))
		}
	}
	if strings.HasSuffix(strings.ToLower(strings.TrimSpace(given)), ".exe") {
		return strings.TrimSpace(given), "this app has not connected since MergeLanes started; the rule is saved and applies when it does"
	}
	return "", fmt.Sprintf("no app called %q has connected recently; known apps: %s. Use its exe name if it is not running yet", given, appNames(s))
}

// resolveAdapter matches an adapter name ignoring case, spaces and dashes, so "wifi" is "Wi-Fi".
func resolveAdapter(s Snapshot, given string) (string, bool) {
	norm := func(v string) string {
		return strings.Map(func(r rune) rune {
			if r == ' ' || r == '-' || r == '_' {
				return -1
			}
			return r
		}, strings.ToLower(strings.TrimSpace(v)))
	}
	g := norm(given)
	if g == "" {
		return "", false
	}
	for _, a := range s.Adapters {
		if norm(a.Name) == g {
			return a.Name, true
		}
	}
	return "", false
}

func adapterNames(s Snapshot) string {
	var n []string
	for _, a := range s.Adapters {
		n = append(n, a.Name)
	}
	if len(n) == 0 {
		return "(none known: MergeLanes is not running)"
	}
	return strings.Join(n, ", ")
}

func appNames(s Snapshot) string {
	var n []string
	for _, a := range s.Apps {
		n = append(n, a.Name)
	}
	if len(n) == 0 {
		return "(none yet)"
	}
	return strings.Join(n, ", ")
}

// snapshotView is the compact JSON the model gets: rounded numbers, rules as words.
func snapshotView(s Snapshot) map[string]any {
	round := func(v float64) float64 { return math.Round(v*10) / 10 }
	var ads []map[string]any
	for _, a := range s.Adapters {
		ad := map[string]any{"name": a.Name, "up": a.Up, "in_mbps": round(a.InMbps), "out_mbps": round(a.OutMbps)}
		if a.PingMs > 0 {
			ad["ping_ms"] = math.Round(a.PingMs)
		}
		if a.LimitMbps > 0 {
			ad["limit_mbps"] = a.LimitMbps
		}
		if a.Games {
			ad["lowest_ping_lane"] = true
		}
		ads = append(ads, ad)
	}
	apList := append([]App(nil), s.Apps...)
	sort.SliceStable(apList, func(i, j int) bool { return apList[i].Flows > apList[j].Flows })
	var as []map[string]any
	for _, a := range apList {
		ap := map[string]any{"app": a.Name, "rule": a.Rule.Label()}
		if len(a.ByAdapter) > 0 {
			ap["connections_by_adapter"] = a.ByAdapter
		}
		if a.LastLane != "" {
			ap["last_connection_lane"] = a.LastLane
		}
		if !a.LastSeen.IsZero() {
			ap["last_seen_min_ago"] = math.Round(time.Since(a.LastSeen).Minutes())
		} else {
			ap["not_seen_since_start"] = true
		}
		as = append(as, ap)
	}
	v := map[string]any{"mergelanes_running": s.Running, "merging": s.Merging, "adapters": ads, "apps": as}
	return v
}

// laneWord is the lane as the tool takes it: auto, merge, fastest or adapter.
func laneWord(k apps.ModeKind) string {
	switch k {
	case apps.ModeMerge:
		return "merge"
	case apps.ModeFastest:
		return "fastest"
	case apps.ModeAdapter:
		return "adapter"
	}
	return "auto"
}
