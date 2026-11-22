// Package apps decides which lane each new flow gets, by the app that opened it.
//
// With no rules at all ("auto") realtime UDP, which is what games and voice chat send, stays on the
// adapter with the lowest latency, and everything else is merged over every adapter. A rule picked
// in the tray (or with `mergelanes app`) overrides that for one app: merge everything, keep
// everything on the lowest-latency adapter, or keep everything on one adapter with a fallback.
package apps

import (
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"

	"mergelanes/internal/engine"
)

// ModeKind is what the user chose for an app.
type ModeKind uint8

const (
	ModeAuto    ModeKind = iota // realtime UDP on the lowest-latency adapter, the rest merged
	ModeMerge                   // every flow merged, UDP too (torrent clients, download managers)
	ModeFastest                 // every flow on the lowest-latency adapter
	ModeAdapter                 // every flow on Adapter, falling back to the fastest other one
)

// Mode is an app's rule.
type Mode struct {
	Kind    ModeKind
	Adapter string // ModeAdapter only
}

// ParseMode reads a rule as it is saved and typed: "auto", "merge", "fastest" or "adapter:<name>".
func ParseMode(s string) (Mode, error) {
	switch v := strings.TrimSpace(s); {
	case v == "auto":
		return Mode{}, nil
	case v == "merge":
		return Mode{Kind: ModeMerge}, nil
	case v == "fastest":
		return Mode{Kind: ModeFastest}, nil
	case strings.HasPrefix(v, "adapter:") && strings.TrimSpace(v[len("adapter:"):]) != "":
		return Mode{Kind: ModeAdapter, Adapter: strings.TrimSpace(v[len("adapter:"):])}, nil
	}
	return Mode{}, fmt.Errorf(`unknown mode %q: want auto, merge, fastest or adapter:<name>`, s)
}

// String is the saved form, the inverse of ParseMode.
func (m Mode) String() string {
	switch m.Kind {
	case ModeMerge:
		return "merge"
	case ModeFastest:
		return "fastest"
	case ModeAdapter:
		return "adapter:" + m.Adapter
	}
	return "auto"
}

// Label is how the tray and `mergelanes apps` show the rule.
func (m Mode) Label() string {
	switch m.Kind {
	case ModeMerge:
		return "Merge"
	case ModeFastest:
		return "Lowest ping"
	case ModeAdapter:
		return "Only " + m.Adapter
	}
	return "Auto"
}

// RecentWindow is how long an app stays in the tray's Apps menu and in `mergelanes apps` after its
// last new flow. Apps with a rule always stay.
const RecentWindow = 10 * time.Minute

// Flow is a new connection as the TUN sees it: Src is the app's own socket.
type Flow struct {
	UDP      bool
	Src, Dst netip.AddrPort
}

// OwnerFunc names the program that owns the socket at src, e.g. "steam.exe", or "" if unknown.
type OwnerFunc func(udp bool, src netip.AddrPort) string

// AppStat is one app's recent activity, for the tray and `mergelanes apps`.
type AppStat struct {
	Name    string    // the program, as the OS names it, e.g. "VALORANT-Win64-Shipping.exe"
	Mode    string    // its rule, "auto" if none (ParseMode form)
	Lane    string    // what its last flow got: "merged", "lowest ping" or "only Ethernet"
	Adapter string    // the adapter its last flow went out on
	Flows   int64     // flows since MergeLanes started
	Last    time.Time // when it last opened a flow
	// ByAdapter counts the app's connected flows per adapter since MergeLanes started or its rule
	// last changed, so "which connection does it use" has an answer for a merged app too.
	ByAdapter map[string]int64 `json:",omitempty"`
}

// Policy is safe for concurrent use: Decide runs once per new flow on the TUN's goroutines.
type Policy struct {
	owner OwnerFunc
	now   func() time.Time

	mu     sync.Mutex
	rules  map[string]Mode // by Key(name)
	recent map[string]*AppStat
}

// New makes a policy that finds apps with owner; nil means this OS's lookup (Owner).
func New(owner OwnerFunc) *Policy {
	if owner == nil {
		owner = Owner
	}
	return &Policy{owner: owner, now: time.Now, rules: map[string]Mode{}, recent: map[string]*AppStat{}}
}

// Key is how apps are matched: case-insensitive, with or without ".exe", so "Steam.exe", "steam.exe"
// and "steam" are one app.
func Key(name string) string {
	k := strings.ToLower(strings.TrimSpace(name))
	return strings.TrimSuffix(k, ".exe")
}

// mergeByDefault are apps whose UDP is bulk transfer, not realtime: torrent clients (uTP, DHT) and
// download managers. In auto they are merged completely.
var mergeByDefault = map[string]bool{
	"qbittorrent": true, "utorrent": true, "bittorrent": true, "transmission-qt": true,
	"transmission-gtk": true, "transmission-daemon": true, "deluge": true, "deluged": true,
	"tixati": true, "biglybt": true, "vuze": true, "aria2c": true, "motrix": true, "fdm": true,
	"freedownloadmanager": true, "idman": true, "picotorrent": true, "qbittorrent-nox": true,
}

// notRealtime are UDP destination ports that carry no game or voice traffic: DNS, DHCP, NTP,
// NetBIOS, QUIC (HTTP/3, i.e. web pages and downloads), DNS over QUIC, SSDP, WS-Discovery, mDNS,
// LLMNR. Everything else on UDP is treated as realtime.
var notRealtime = map[uint16]bool{
	53: true, 67: true, 68: true, 123: true, 137: true, 138: true, 443: true, 853: true,
	1900: true, 3702: true, 5353: true, 5355: true,
}

// IsRealtime reports whether an auto-mode flow should stay on the lowest-latency adapter.
func IsRealtime(f Flow) bool { return f.UDP && !notRealtime[f.Dst.Port()] }

// Decide picks the lane for a new flow. app is the owning program ("" if unknown) and goes back
// into Note once the flow is connected.
func (p *Policy) Decide(f Flow) (app string, lane engine.Lane) {
	app = p.owner(f.UDP, f.Src)
	k := Key(app)
	p.mu.Lock()
	m, ruled := p.rules[k]
	if app != "" {
		st := p.recent[k]
		if st == nil {
			st = &AppStat{Name: app}
			p.recent[k] = st
		}
		st.Flows++
		st.Last = p.now()
	}
	p.mu.Unlock()
	if !ruled {
		m = Mode{}
	}
	switch m.Kind {
	case ModeMerge:
		return app, engine.Lane{Kind: engine.LaneMerge}
	case ModeFastest:
		return app, engine.Lane{Kind: engine.LaneFastest}
	case ModeAdapter:
		return app, engine.Lane{Kind: engine.LanePinned, Adapter: m.Adapter}
	}
	if !mergeByDefault[k] && IsRealtime(f) {
		return app, engine.Lane{Kind: engine.LaneFastest}
	}
	return app, engine.Lane{Kind: engine.LaneMerge}
}

// Note records where an app's flow went, so the tray can show it.
func (p *Policy) Note(app string, lane engine.Lane, adapter string) {
	if app == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if st := p.recent[Key(app)]; st != nil {
		st.Lane, st.Adapter = lane.String(), adapter
		if adapter != "" {
			if st.ByAdapter == nil {
				st.ByAdapter = map[string]int64{}
			}
			st.ByAdapter[adapter]++
		}
	}
}

// SetRule sets an app's rule; ModeAuto removes it.
func (p *Policy) SetRule(app string, m Mode) {
	k := Key(app)
	if k == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if old := p.rules[k]; old != m {
		if st := p.recent[k]; st != nil {
			st.ByAdapter = nil // count where flows go under the new rule
		}
	}
	if m.Kind == ModeAuto {
		delete(p.rules, k)
	} else {
		p.rules[k] = m
	}
}

// SetRules replaces every rule, e.g. with the saved ones at startup. Keys are names as saved.
func (p *Policy) SetRules(rules map[string]Mode) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.rules = make(map[string]Mode, len(rules))
	for name, m := range rules {
		if k := Key(name); k != "" && m.Kind != ModeAuto {
			p.rules[k] = m
		}
	}
}

// Rules returns the rules by Key, for saving.
func (p *Policy) Rules() map[string]Mode {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make(map[string]Mode, len(p.rules))
	for k, m := range p.rules {
		out[k] = m
	}
	return out
}

// Rule returns an app's rule (ModeAuto if it has none).
func (p *Policy) Rule(app string) Mode {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.rules[Key(app)]
}

// Recent lists the apps that opened a flow within the last `within`, most flows first, at most max
// of them (0 = no limit). Apps with a rule come first, so a pinned app never drops off the menu.
func (p *Policy) Recent(within time.Duration, max int) []AppStat {
	p.mu.Lock()
	cut := p.now().Add(-within)
	var out []AppStat
	for k, st := range p.recent {
		_, ruled := p.rules[k]
		if !ruled && st.Last.Before(cut) {
			continue
		}
		s := *st
		s.Mode = p.rules[k].String()
		if st.ByAdapter != nil {
			s.ByAdapter = make(map[string]int64, len(st.ByAdapter))
			for a, n := range st.ByAdapter {
				s.ByAdapter[a] = n
			}
		}
		out = append(out, s)
	}
	p.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		ri, rj := out[i].Mode != "auto", out[j].Mode != "auto"
		if ri != rj {
			return ri
		}
		if out[i].Flows != out[j].Flows {
			return out[i].Flows > out[j].Flows
		}
		return Key(out[i].Name) < Key(out[j].Name)
	})
	if max > 0 && len(out) > max {
		out = out[:max]
	}
	return out
}
