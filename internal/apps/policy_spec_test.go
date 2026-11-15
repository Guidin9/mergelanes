package apps

// Black-box tests for per-app modes and lane decisions, written from the spec only. Helpers are
// prefixed "spec" so they cannot collide with names in policy.go.

import (
	"net/netip"
	"reflect"
	"strings"
	"testing"
	"time"

	"mergelanes/internal/engine"
)

var (
	specSrc = netip.MustParseAddr("10.0.0.5")
	specDst = netip.MustParseAddr("203.0.113.9")
)

func specUDP(dstPort uint16) Flow {
	return Flow{UDP: true, Src: netip.AddrPortFrom(specSrc, 50000), Dst: netip.AddrPortFrom(specDst, dstPort)}
}

func specTCP(dstPort uint16) Flow {
	return Flow{UDP: false, Src: netip.AddrPortFrom(specSrc, 50001), Dst: netip.AddrPortFrom(specDst, dstPort)}
}

// specPolicy returns a policy whose owner reports *who, and a fixed clock the test can move.
func specPolicy() (p *Policy, who *string, clock *time.Time) {
	who = new(string)
	clock = new(time.Time)
	*clock = time.Unix(1_000_000, 0)
	p = New(func(bool, netip.AddrPort) string { return *who })
	p.now = func() time.Time { return *clock }
	return p, who, clock
}

// --- Mode ---

func TestSpecParseModeValid(t *testing.T) {
	for _, c := range []struct {
		in   string
		want Mode
	}{
		{"auto", Mode{Kind: ModeAuto}},
		{"merge", Mode{Kind: ModeMerge}},
		{"fastest", Mode{Kind: ModeFastest}},
		{"adapter:Wi-Fi", Mode{Kind: ModeAdapter, Adapter: "Wi-Fi"}},
		{"adapter:Ethernet 2", Mode{Kind: ModeAdapter, Adapter: "Ethernet 2"}},
		{"  merge  ", Mode{Kind: ModeMerge}},
		{"\tfastest\n", Mode{Kind: ModeFastest}},
		{"adapter:  Ethernet 2  ", Mode{Kind: ModeAdapter, Adapter: "Ethernet 2"}},
		{"  adapter:Wi-Fi  ", Mode{Kind: ModeAdapter, Adapter: "Wi-Fi"}},
	} {
		got, err := ParseMode(c.in)
		if err != nil {
			t.Errorf("ParseMode(%q) error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseMode(%q) = %+v, want %+v", c.in, got, c.want)
		}
	}
}

func TestSpecParseModeInvalid(t *testing.T) {
	for _, in := range []string{"", "   ", "adapter:", "adapter:   ", "adapter", "bogus", "merged", "fast", "lowest ping", "only Wi-Fi", "pinned:Wi-Fi", "auto merge"} {
		if m, err := ParseMode(in); err == nil {
			t.Errorf("ParseMode(%q) = %+v, want an error", in, m)
		}
	}
}

func TestSpecModeZeroIsAuto(t *testing.T) {
	var m Mode
	if m.Kind != ModeAuto {
		t.Fatal("the zero Mode must be ModeAuto")
	}
}

func TestSpecModeStringAndLabel(t *testing.T) {
	for _, c := range []struct {
		m          Mode
		str, label string
	}{
		{Mode{Kind: ModeAuto}, "auto", "Auto"},
		{Mode{Kind: ModeMerge}, "merge", "Merge"},
		{Mode{Kind: ModeFastest}, "fastest", "Lowest ping"},
		{Mode{Kind: ModeAdapter, Adapter: "Wi-Fi 2"}, "adapter:Wi-Fi 2", "Only Wi-Fi 2"},
	} {
		if got := c.m.String(); got != c.str {
			t.Errorf("%+v.String() = %q, want %q", c.m, got, c.str)
		}
		if got := c.m.Label(); got != c.label {
			t.Errorf("%+v.Label() = %q, want %q", c.m, got, c.label)
		}
		back, err := ParseMode(c.m.String())
		if err != nil || back != c.m {
			t.Errorf("ParseMode(%q) = %+v, %v; want %+v (String must be the inverse)", c.m.String(), back, err, c.m)
		}
	}
}

// --- Key ---

func TestSpecKey(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"Steam.exe", "steam"},
		{"steam", "steam"},
		{"  Chrome.EXE  ", "chrome"},
		{"Discord", "discord"},
		{"my.exe.exe", "my.exe"},
		{"exe", "exe"},
		{"", ""},
		{"   ", ""},
	} {
		if got := Key(c.in); got != c.want {
			t.Errorf("Key(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// --- IsRealtime ---

func TestSpecIsRealtime(t *testing.T) {
	for _, port := range []uint16{3074, 27015, 3478, 50000, 9000, 80, 1} {
		if !IsRealtime(specUDP(port)) {
			t.Errorf("UDP to port %d should be realtime", port)
		}
		if IsRealtime(specTCP(port)) {
			t.Errorf("TCP to port %d must never be realtime", port)
		}
	}
	for _, port := range []uint16{53, 67, 68, 123, 137, 138, 443, 853, 1900, 3702, 5353, 5355} {
		if IsRealtime(specUDP(port)) {
			t.Errorf("UDP to service port %d must not be realtime", port)
		}
		if IsRealtime(specTCP(port)) {
			t.Errorf("TCP to port %d must never be realtime", port)
		}
	}
	// Only the destination port matters.
	f := Flow{UDP: true, Src: netip.AddrPortFrom(specSrc, 53), Dst: netip.AddrPortFrom(specDst, 3074)}
	if !IsRealtime(f) {
		t.Error("UDP from port 53 to 3074 should be realtime (destination port decides)")
	}
}

// --- Decide ---

func TestSpecDecidePassesFlowToOwner(t *testing.T) {
	var gotUDP []bool
	var gotSrc []netip.AddrPort
	p := New(func(udp bool, src netip.AddrPort) string {
		gotUDP = append(gotUDP, udp)
		gotSrc = append(gotSrc, src)
		return "Game.exe"
	})
	tcp := specTCP(443)
	udp := specUDP(3074)
	if app, _ := p.Decide(tcp); app != "Game.exe" {
		t.Fatalf("app = %q, want the owner's spelling Game.exe", app)
	}
	p.Decide(udp)
	if !reflect.DeepEqual(gotUDP, []bool{false, true}) || !reflect.DeepEqual(gotSrc, []netip.AddrPort{tcp.Src, udp.Src}) {
		t.Fatalf("owner called with udp=%v src=%v, want [false true] [%v %v]", gotUDP, gotSrc, tcp.Src, udp.Src)
	}
}

func TestSpecDecideFollowsRules(t *testing.T) {
	p, who, _ := specPolicy()
	p.SetRule("Steam.exe", Mode{Kind: ModeMerge})
	p.SetRule("tcpapp", Mode{Kind: ModeFastest})
	p.SetRule("CHROME", Mode{Kind: ModeAdapter, Adapter: "Ethernet 2"})

	*who = "steam.EXE"
	app, lane := p.Decide(specUDP(27015)) // realtime-looking, but the rule says merge
	if app != "steam.EXE" || lane.Kind != engine.LaneMerge {
		t.Fatalf("merge rule: got %q %+v, want steam.EXE LaneMerge", app, lane)
	}
	*who = "TcpApp.exe"
	if _, lane := p.Decide(specTCP(443)); lane.Kind != engine.LaneFastest {
		t.Fatalf("fastest rule on a TCP flow: got %+v, want LaneFastest", lane)
	}
	*who = "chrome.exe"
	for _, f := range []Flow{specUDP(3478), specTCP(443)} {
		_, lane := p.Decide(f)
		if lane.Kind != engine.LanePinned || lane.Adapter != "Ethernet 2" {
			t.Fatalf("adapter rule: got %+v, want LanePinned Ethernet 2", lane)
		}
	}
}

func TestSpecDecideAuto(t *testing.T) {
	p, who, _ := specPolicy()
	for _, c := range []struct {
		app  string
		f    Flow
		want engine.LaneKind
	}{
		{"game.exe", specUDP(3074), engine.LaneFastest},
		{"game.exe", specTCP(3074), engine.LaneMerge},
		{"game.exe", specUDP(443), engine.LaneMerge},
		{"game.exe", specUDP(53), engine.LaneMerge},
		{"Discord.exe", specUDP(50005), engine.LaneFastest},
		{"", specUDP(3074), engine.LaneFastest},
		{"", specTCP(3074), engine.LaneMerge},
		{"", specUDP(123), engine.LaneMerge},
	} {
		*who = c.app
		app, lane := p.Decide(c.f)
		if app != c.app || lane.Kind != c.want {
			t.Errorf("auto %q UDP=%v dst=%v: got %q kind %v, want kind %v", c.app, c.f.UDP, c.f.Dst, app, lane.Kind, c.want)
		}
	}
}

func TestSpecDecideBulkUDPAppsMergeInAuto(t *testing.T) {
	p, who, _ := specPolicy()
	for _, name := range []string{"qBittorrent.exe", "uTorrent.exe", "BitTorrent.exe", "transmission-qt.exe",
		"Deluge.exe", "aria2c.exe", "Tixati.exe", "BiglyBT.exe", "qbittorrent", "deluge"} {
		*who = name
		if _, lane := p.Decide(specUDP(6881)); lane.Kind != engine.LaneMerge {
			t.Errorf("bulk-UDP app %q in auto: got %v, want LaneMerge", name, lane.Kind)
		}
	}
	// A rule still wins over the bulk list.
	p.SetRule("qbittorrent", Mode{Kind: ModeFastest})
	*who = "qBittorrent.exe"
	if _, lane := p.Decide(specUDP(6881)); lane.Kind != engine.LaneFastest {
		t.Fatalf("explicit fastest rule on a bulk app: got %v, want LaneFastest", lane.Kind)
	}
}

// --- Rules ---

func TestSpecSetRuleAndRule(t *testing.T) {
	p, _, _ := specPolicy()
	merge := Mode{Kind: ModeMerge}
	p.SetRule("Steam.exe", merge)
	for _, name := range []string{"steam", "STEAM", "steam.exe", " Steam.EXE "} {
		if got := p.Rule(name); got != merge {
			t.Errorf("Rule(%q) = %+v, want merge", name, got)
		}
	}
	if got := p.Rule("chrome"); got != (Mode{}) {
		t.Errorf("Rule of an app without a rule = %+v, want auto", got)
	}
	if got := p.Rules(); !reflect.DeepEqual(got, map[string]Mode{"steam": merge}) {
		t.Fatalf("Rules() = %v, want map[steam:merge]", got)
	}
	r := p.Rules()
	r["x"] = Mode{Kind: ModeFastest}
	delete(r, "steam")
	if got := p.Rules(); !reflect.DeepEqual(got, map[string]Mode{"steam": merge}) {
		t.Fatalf("Rules() must return a copy; after mutating it got %v", got)
	}
	p.SetRule(" steam.EXE ", Mode{Kind: ModeAuto})
	if got := p.Rules(); len(got) != 0 {
		t.Fatalf("SetRule(ModeAuto) must remove the rule, Rules() = %v", got)
	}
	if got := p.Rule("steam"); got != (Mode{}) {
		t.Fatalf("Rule after removal = %+v, want auto", got)
	}
}

func TestSpecSetRulesReplacesAll(t *testing.T) {
	p, who, _ := specPolicy()
	p.SetRule("old", Mode{Kind: ModeMerge})
	p.SetRules(map[string]Mode{
		"Chrome.exe": {Kind: ModeFastest},
		" Discord ":  {Kind: ModeAdapter, Adapter: "Ethernet"},
		"idle.exe":   {Kind: ModeAuto},
	})
	want := map[string]Mode{
		"chrome":  {Kind: ModeFastest},
		"discord": {Kind: ModeAdapter, Adapter: "Ethernet"},
	}
	if got := p.Rules(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Rules() = %v, want %v", got, want)
	}
	if got := p.Rule("old"); got != (Mode{}) {
		t.Fatalf("SetRules must replace earlier rules; Rule(old) = %+v", got)
	}
	*who = "chrome.exe"
	if _, lane := p.Decide(specTCP(443)); lane.Kind != engine.LaneFastest {
		t.Fatalf("rule from SetRules not applied: %+v", lane)
	}
	p.SetRules(nil)
	if got := p.Rules(); len(got) != 0 {
		t.Fatalf("SetRules(nil) must clear rules, got %v", got)
	}
}

// --- Recent / Note ---

func specNames(st []AppStat) []string {
	out := make([]string, len(st))
	for i, a := range st {
		out[i] = a.Name
	}
	return out
}

func TestSpecRecentWindowRulesAndOrder(t *testing.T) {
	p, who, clock := specPolicy()
	decide := func(app string, n int) {
		*who = app
		for range n {
			p.Decide(specTCP(443))
		}
	}
	t0 := *clock
	decide("Old.exe", 4)
	*clock = t0.Add(20 * time.Minute)
	t1 := *clock
	decide("b.exe", 2)
	decide("A.exe", 2)
	decide("c", 5)
	decide("Y.exe", 1)
	decide("z", 3)
	decide("", 7) // unknown owner: never listed
	p.SetRule("y", Mode{Kind: ModeMerge})
	p.SetRule("Z.exe", Mode{Kind: ModeFastest})
	p.SetRule("neverseen", Mode{Kind: ModeMerge}) // never decided: never listed
	*clock = t1.Add(time.Minute)

	got := p.Recent(10*time.Minute, 0)
	if want := []string{"z", "Y.exe", "c", "A.exe", "b.exe"}; !reflect.DeepEqual(specNames(got), want) {
		t.Fatalf("Recent order = %v, want %v", specNames(got), want)
	}

	p.SetRule("OLD", Mode{Kind: ModeAdapter, Adapter: "Wi-Fi"}) // a rule lists it regardless of age
	got = p.Recent(10*time.Minute, 0)
	if want := []string{"Old.exe", "z", "Y.exe", "c", "A.exe", "b.exe"}; !reflect.DeepEqual(specNames(got), want) {
		t.Fatalf("Recent order = %v, want %v", specNames(got), want)
	}
	wantMode := map[string]string{"Old.exe": "adapter:Wi-Fi", "z": "fastest", "Y.exe": "merge", "c": "auto", "A.exe": "auto", "b.exe": "auto"}
	wantFlows := map[string]int64{"Old.exe": 4, "z": 3, "Y.exe": 1, "c": 5, "A.exe": 2, "b.exe": 2}
	for _, a := range got {
		if a.Mode != wantMode[a.Name] {
			t.Errorf("%s Mode = %q, want %q", a.Name, a.Mode, wantMode[a.Name])
		}
		if a.Flows != wantFlows[a.Name] {
			t.Errorf("%s Flows = %d, want %d", a.Name, a.Flows, wantFlows[a.Name])
		}
		wantLast := t1
		if a.Name == "Old.exe" {
			wantLast = t0
		}
		if !a.Last.Equal(wantLast) {
			t.Errorf("%s Last = %v, want %v", a.Name, a.Last, wantLast)
		}
	}

	if top := p.Recent(10*time.Minute, 2); !reflect.DeepEqual(specNames(top), []string{"Old.exe", "z"}) {
		t.Fatalf("Recent(max=2) = %v, want [Old.exe z]", specNames(top))
	}
	if all := p.Recent(10*time.Minute, -1); len(all) != 6 {
		t.Fatalf("Recent(max=-1) must not truncate, got %v", specNames(all))
	}
	// A window that excludes every flow still lists the ruled apps.
	if narrow := p.Recent(30*time.Second, 0); !reflect.DeepEqual(specNames(narrow), []string{"Old.exe", "z", "Y.exe"}) {
		t.Fatalf("Recent(30s) = %v, want only the ruled apps [Old.exe z Y.exe]", specNames(narrow))
	}
}

func TestSpecRecentNoteRecordsLaneAndAdapter(t *testing.T) {
	p, who, _ := specPolicy()
	*who = "Game.exe"
	app, lane := p.Decide(specUDP(3074))
	p.Note(app, lane, "Wi-Fi")
	*who = "Browser.exe"
	p.Decide(specTCP(443))
	p.Note("Browser.exe", engine.Lane{Kind: engine.LanePinned, Adapter: "Ethernet"}, "Ethernet")
	p.Note("Browser.exe", engine.Lane{}, "Wi-Fi")                       // the last one counts
	p.Note("ghost.exe", engine.Lane{Kind: engine.LaneFastest}, "Wi-Fi") // never decided: no-op
	p.Note("", engine.Lane{Kind: engine.LaneFastest}, "Wi-Fi")          // no-op

	got := map[string]AppStat{}
	for _, a := range p.Recent(RecentWindow, 0) {
		got[a.Name] = a
	}
	if len(got) != 2 {
		t.Fatalf("Recent = %v, want only Game.exe and Browser.exe", got)
	}
	if g := got["Game.exe"]; g.Lane != "lowest ping" || g.Adapter != "Wi-Fi" {
		t.Errorf("Game.exe Lane/Adapter = %q/%q, want lowest ping/Wi-Fi", g.Lane, g.Adapter)
	}
	if b := got["Browser.exe"]; b.Lane != "merged" || b.Adapter != "Wi-Fi" {
		t.Errorf("Browser.exe Lane/Adapter = %q/%q, want merged/Wi-Fi", b.Lane, b.Adapter)
	}
	for name := range got {
		if strings.EqualFold(name, "ghost.exe") || name == "" {
			t.Errorf("Recent lists %q", name)
		}
	}
}

func TestSpecRecentWindowConstant(t *testing.T) {
	if RecentWindow != 10*time.Minute {
		t.Fatalf("RecentWindow = %v, want 10m", RecentWindow)
	}
}
