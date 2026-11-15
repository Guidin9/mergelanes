package engine

// Black-box tests for lanes (merged / lowest ping / pinned) and RTT smoothing, written from the
// spec only. Helpers are prefixed "spec" so they cannot collide with names in lane.go.

import (
	"math"
	"net"
	"testing"
	"time"

	"mergelanes/internal/nics"
)

var specFastest = Lane{Kind: LaneFastest}

func specPinned(name string) Lane { return Lane{Kind: LanePinned, Adapter: name} }

func specNic46(name, ip4, ip6 string) nics.Nic {
	n := nic(name, ip4, 1)
	n.IP6 = net.ParseIP(ip6)
	return n
}

func specSlot(t *testing.T, s *Scheduler, name string) *Slot {
	t.Helper()
	for _, sl := range s.slots {
		if sl.Name == name {
			return sl
		}
	}
	t.Fatalf("no slot named %q", name)
	return nil
}

func specSkip(names ...string) map[string]struct{} {
	m := map[string]struct{}{}
	for _, n := range names {
		m[n] = struct{}{}
	}
	return m
}

// specOnce acquires one flow on lane l, releases it and returns the adapter's name.
func specOnce(t *testing.T, s *Scheduler, f Family, l Lane, skip map[string]struct{}) string {
	t.Helper()
	sl := s.AcquireLane(f, l, skip)
	if sl == nil {
		t.Fatalf("AcquireLane(fam %d, %v, %v) = nil, want an adapter", f, l, skip)
	}
	s.ReleaseFor(sl, f)
	return sl.Name
}

func specStats(t *testing.T, s *Scheduler, name string) SlotStats {
	t.Helper()
	for _, st := range s.Snapshot() {
		if st.Name == name {
			return st
		}
	}
	t.Fatalf("no SlotStats for %q", name)
	return SlotStats{}
}

func specNear(t *testing.T, what string, got, want time.Duration) {
	t.Helper()
	if d := got - want; d < -time.Microsecond || d > time.Microsecond {
		t.Fatalf("%s = %v, want %v (±1µs)", what, got, want)
	}
}

// --- Lane ---

func TestSpecLaneKindsAndString(t *testing.T) {
	var zero Lane
	if zero.Kind != LaneMerge {
		t.Fatal("the zero Lane must be LaneMerge")
	}
	for _, c := range []struct {
		l    Lane
		want string
	}{
		{Lane{}, "merged"},
		{Lane{Kind: LaneMerge}, "merged"},
		{Lane{Kind: LaneFastest}, "lowest ping"},
		{Lane{Kind: LanePinned, Adapter: "Wi-Fi 2"}, "only Wi-Fi 2"},
		{Lane{Kind: LanePinned, Adapter: "Ethernet"}, "only Ethernet"},
	} {
		if got := c.l.String(); got != c.want {
			t.Errorf("%#v.String() = %q, want %q", c.l, got, c.want)
		}
	}
}

// --- NoteRTT / RTT ---

func TestSpecNoteRTTSmoothing(t *testing.T) {
	s := NewScheduler([]nics.Nic{specNic46("A", "10.0.0.1", "2001:db8::1")})
	a := s.slots[0]
	if a.RTT(Fam4) != 0 || a.RTT(Fam6) != 0 {
		t.Fatalf("new adapter must be unmeasured, got %v / %v", a.RTT(Fam4), a.RTT(Fam6))
	}
	s.NoteRTT(a, Fam4, 100*time.Millisecond)
	if got := a.RTT(Fam4); got != 100*time.Millisecond {
		t.Fatalf("first sample must be taken as-is: got %v, want 100ms", got)
	}
	s.NoteRTT(a, Fam4, 200*time.Millisecond) // 0.7*100 + 0.3*200 = 130
	specNear(t, "after second sample", a.RTT(Fam4), 130*time.Millisecond)
	s.NoteRTT(a, Fam4, 30*time.Millisecond) // 0.7*130 + 0.3*30 = 100
	specNear(t, "after third sample", a.RTT(Fam4), 100*time.Millisecond)

	s.NoteRTT(a, Fam4, 0)
	s.NoteRTT(a, Fam4, -time.Second)
	specNear(t, "after non-positive samples", a.RTT(Fam4), 100*time.Millisecond)

	if a.RTT(Fam6) != 0 {
		t.Fatalf("IPv4 samples leaked into IPv6: %v", a.RTT(Fam6))
	}
	s.NoteRTT(a, Fam6, 40*time.Millisecond)
	if got := a.RTT(Fam6); got != 40*time.Millisecond {
		t.Fatalf("IPv6 first sample = %v, want 40ms", got)
	}
	specNear(t, "IPv4 after an IPv6 sample", a.RTT(Fam4), 100*time.Millisecond)
}

func TestSpecNoteRTTFirstNonPositiveSampleStaysUnmeasured(t *testing.T) {
	s := NewScheduler([]nics.Nic{nic("A", "10.0.0.1", 1)})
	a := s.slots[0]
	s.NoteRTT(a, Fam4, 0)
	s.NoteRTT(a, Fam4, -5*time.Millisecond)
	if got := a.RTT(Fam4); got != 0 {
		t.Fatalf("d <= 0 must be ignored, RTT = %v", got)
	}
	s.NoteRTT(a, Fam4, 8*time.Millisecond)
	if got := a.RTT(Fam4); got != 8*time.Millisecond {
		t.Fatalf("first real sample must be taken as-is after ignored ones, got %v", got)
	}
}

func TestSpecRTTMicrosecondResolution(t *testing.T) {
	s := NewScheduler([]nics.Nic{nic("A", "10.0.0.1", 1)})
	a := s.slots[0]
	s.NoteRTT(a, Fam4, 1234567*time.Nanosecond)
	got := a.RTT(Fam4)
	if got%time.Microsecond != 0 {
		t.Fatalf("RTT %v is not at microsecond resolution", got)
	}
	if got < 1234*time.Microsecond || got > 1235*time.Microsecond {
		t.Fatalf("RTT %v, want ~1.234567ms", got)
	}
}

func TestSpecSnapshotReportsRTTInMilliseconds(t *testing.T) {
	s := NewScheduler([]nics.Nic{
		specNic46("A", "10.0.0.1", "2001:db8::1"),
		specNic46("B", "10.0.0.2", "2001:db8::2"),
	})
	a := specSlot(t, s, "A")
	s.NoteRTT(a, Fam4, 25*time.Millisecond)
	s.NoteRTT(a, Fam6, 40500*time.Microsecond)
	st := specStats(t, s, "A")
	if math.Abs(st.RTTms-25) > 1e-6 || math.Abs(st.RTT6ms-40.5) > 1e-6 {
		t.Fatalf("RTTms/RTT6ms = %v/%v, want 25/40.5", st.RTTms, st.RTT6ms)
	}
	if b := specStats(t, s, "B"); b.RTTms != 0 || b.RTT6ms != 0 {
		t.Fatalf("unmeasured adapter must report 0 ms, got %v/%v", b.RTTms, b.RTT6ms)
	}
}

// --- AcquireLane: common ---

func TestSpecAcquireLaneNilWithoutFamily(t *testing.T) {
	s := NewScheduler([]nics.Nic{nic("A", "10.0.0.1", 1), nic("B", "10.0.0.2", 1)})
	for _, l := range []Lane{{}, specFastest, specPinned("A")} {
		if got := s.AcquireLane(Fam6, l, nil); got != nil {
			t.Fatalf("%v: no adapter has IPv6, want nil, got %s", l, got.Name)
		}
	}
	empty := NewScheduler(nil)
	for _, l := range []Lane{{}, specFastest, specPinned("A")} {
		if got := empty.AcquireLane(Fam4, l, nil); got != nil {
			t.Fatalf("%v: no adapters at all, want nil, got %s", l, got.Name)
		}
	}
}

func TestSpecAcquireLaneCountsFlows(t *testing.T) {
	for _, l := range []Lane{{}, specFastest, specPinned("B")} {
		t.Run(l.String(), func(t *testing.T) {
			s := NewScheduler([]nics.Nic{nic("A", "10.0.0.1", 1), nic("B", "10.0.0.2", 1)})
			sl := s.AcquireLane(Fam4, l, nil)
			if sl == nil {
				t.Fatal("got nil")
			}
			st := specStats(t, s, sl.Name)
			if st.Active != 1 || st.Total != 1 {
				t.Fatalf("after acquire Active/Total = %d/%d, want 1/1", st.Active, st.Total)
			}
			s.ReleaseFor(sl, Fam4)
			st = specStats(t, s, sl.Name)
			if st.Active != 0 || st.Total != 1 {
				t.Fatalf("after release Active/Total = %d/%d, want 0/1", st.Active, st.Total)
			}
		})
	}
}

// --- LaneMerge ---

func TestSpecMergeLaneBalancesLikeAcquireFor(t *testing.T) {
	s := NewScheduler([]nics.Nic{nic("A", "10.0.0.1", 3), nic("B", "10.0.0.2", 1)})
	picks := make([]*Slot, 8)
	for i := range picks {
		picks[i] = s.AcquireLane(Fam4, Lane{}, nil)
		if picks[i] == nil {
			t.Fatal("merge lane returned nil")
		}
	}
	if countBy(picks, "A") != 6 || countBy(picks, "B") != 2 {
		t.Fatalf("want 6/2 by weight, got A=%d B=%d", countBy(picks, "A"), countBy(picks, "B"))
	}
	if got := s.AcquireLane(Fam4, Lane{}, specSkip("A")); got == nil || got.Name != "B" {
		t.Fatalf("merge lane must honour skip, got %v", got)
	}
	if got := s.AcquireLane(Fam4, Lane{}, specSkip("A", "B")); got != nil {
		t.Fatalf("everything skipped: want nil, got %s", got.Name)
	}

	even := NewScheduler([]nics.Nic{nic("A", "10.0.0.1", 1), nic("B", "10.0.0.2", 1)})
	for range 6 {
		even.AcquireLane(Fam4, Lane{}, nil)
	}
	if a, b := specStats(t, even, "A").Active, specStats(t, even, "B").Active; a != 3 || b != 3 {
		t.Fatalf("equal weights: want 3/3, got %d/%d", a, b)
	}
}

func TestSpecMergeLaneUsesHealthTiers(t *testing.T) {
	s := NewScheduler([]nics.Nic{nic("A", "10.0.0.1", 1), nic("B", "10.0.0.2", 1)})
	s.SetState(specSlot(t, s, "A"), Fam4, true)
	s.SetState(specSlot(t, s, "B"), Fam4, false)
	for i := range 4 {
		if got := s.AcquireLane(Fam4, Lane{}, nil); got == nil || got.Name != "A" {
			t.Fatalf("flow %d: merge lane must avoid the down adapter, got %v", i, got)
		}
	}
}

// --- LanePinned ---

func TestSpecPinnedUsesNamedAdapterEvenIfUnprobedAndSlow(t *testing.T) {
	s := NewScheduler([]nics.Nic{nic("A", "10.0.0.1", 1), nic("B", "10.0.0.2", 1), nic("C", "10.0.0.3", 1)})
	a, b, c := specSlot(t, s, "A"), specSlot(t, s, "B"), specSlot(t, s, "C")
	s.SetState(a, Fam4, true)
	s.SetState(c, Fam4, true)
	s.NoteRTT(a, Fam4, 5*time.Millisecond)
	s.NoteRTT(c, Fam4, 5*time.Millisecond)
	s.NoteRTT(b, Fam4, 300*time.Millisecond)
	for i := range 5 { // not released: load must not move a pinned flow
		got := s.AcquireLane(Fam4, specPinned("B"), nil)
		if got == nil || got.Name != "B" {
			t.Fatalf("flow %d: pinned to never-probed B, got %v", i, got)
		}
	}
	if st := specStats(t, s, "B"); st.Active != 5 || st.Total != 5 {
		t.Fatalf("B Active/Total = %d/%d, want 5/5", st.Active, st.Total)
	}
	if p := s.Primary(Fam4); p != "" {
		t.Fatalf("pinned acquires to a usable adapter are not LaneFastest acquires; Primary = %q, want \"\"", p)
	}
}

// specPinnedFallbackScheduler: A up 50ms, B never probed 1ms, C up 20ms.
func specPinnedFallbackScheduler(t *testing.T) (*Scheduler, *time.Time) {
	t.Helper()
	s := NewScheduler([]nics.Nic{nic("A", "10.0.0.1", 1), nic("B", "10.0.0.2", 1), nic("C", "10.0.0.3", 1)})
	clock := time.Unix(1000, 0)
	s.now = func() time.Time { return clock }
	a, b, c := specSlot(t, s, "A"), specSlot(t, s, "B"), specSlot(t, s, "C")
	s.SetState(a, Fam4, true)
	s.SetState(c, Fam4, true)
	s.NoteRTT(a, Fam4, 50*time.Millisecond)
	s.NoteRTT(b, Fam4, 1*time.Millisecond)
	s.NoteRTT(c, Fam4, 20*time.Millisecond)
	return s, &clock
}

func TestSpecPinnedFallsBackWhenDown(t *testing.T) {
	s, _ := specPinnedFallbackScheduler(t)
	s.SetState(specSlot(t, s, "B"), Fam4, false)
	if got := specOnce(t, s, Fam4, specPinned("B"), nil); got != "C" {
		t.Fatalf("pinned adapter down: want lowest-ping fallback C, got %s", got)
	}
	s.SetState(specSlot(t, s, "B"), Fam4, true)
	if got := specOnce(t, s, Fam4, specPinned("B"), nil); got != "B" {
		t.Fatalf("pinned adapter back up: want B, got %s", got)
	}
}

func TestSpecPinnedFallsBackDuringFailCooldown(t *testing.T) {
	s, clock := specPinnedFallbackScheduler(t)
	s.Fail(specSlot(t, s, "B"), Fam4, 5*time.Second)
	if got := specOnce(t, s, Fam4, specPinned("B"), nil); got != "C" {
		t.Fatalf("pinned adapter in cooldown: want fallback C, got %s", got)
	}
	*clock = clock.Add(6 * time.Second)
	if got := specOnce(t, s, Fam4, specPinned("B"), nil); got != "B" {
		t.Fatalf("cooldown over: want B again, got %s", got)
	}
}

func TestSpecPinnedHonoursSkip(t *testing.T) {
	s, _ := specPinnedFallbackScheduler(t)
	if got := specOnce(t, s, Fam4, specPinned("B"), specSkip("B")); got != "C" {
		t.Fatalf("pinned adapter skipped: want fallback C, got %s", got)
	}
	if got := specOnce(t, s, Fam4, specPinned("B"), specSkip("B", "C")); got != "A" {
		t.Fatalf("pinned and C skipped: want A, got %s", got)
	}
	if got := s.AcquireLane(Fam4, specPinned("B"), specSkip("A", "B", "C")); got != nil {
		t.Fatalf("everything skipped: want nil, got %s", got.Name)
	}
}

func TestSpecPinnedFallsBackWhenAbsent(t *testing.T) {
	s, _ := specPinnedFallbackScheduler(t)
	if got := specOnce(t, s, Fam4, specPinned("Nope"), nil); got != "C" {
		t.Fatalf("unknown adapter: want fallback C, got %s", got)
	}
	// B is busy when it vanishes, so it stays in the scheduler but is no longer present.
	busy := s.AcquireLane(Fam4, specPinned("B"), nil)
	if busy == nil || busy.Name != "B" {
		t.Fatalf("setup: want B, got %v", busy)
	}
	s.Update([]nics.Nic{nic("A", "10.0.0.1", 1), nic("C", "10.0.0.3", 1)})
	if got := specOnce(t, s, Fam4, specPinned("B"), nil); got != "C" {
		t.Fatalf("pinned adapter gone: want fallback C, got %s", got)
	}
	s.ReleaseFor(busy, Fam4)
}

func TestSpecPinnedFallsBackWithoutFamilyAddress(t *testing.T) {
	s := NewScheduler([]nics.Nic{specNic46("A", "10.0.0.1", "2001:db8::1"), nic("B", "10.0.0.2", 1)})
	if got := specOnce(t, s, Fam6, specPinned("B"), nil); got != "A" {
		t.Fatalf("pinned adapter has no IPv6: want A, got %s", got)
	}
	if got := specOnce(t, s, Fam4, specPinned("B"), nil); got != "B" {
		t.Fatalf("IPv4 flow pinned to B: want B, got %s", got)
	}
}

// --- LaneFastest ---

func TestSpecFastestPicksLowestRTT(t *testing.T) {
	s := NewScheduler([]nics.Nic{nic("A", "10.0.0.1", 1), nic("B", "10.0.0.2", 1), nic("C", "10.0.0.3", 1)})
	for name, ms := range map[string]int{"A": 50, "B": 20, "C": 80} {
		sl := specSlot(t, s, name)
		s.SetState(sl, Fam4, true)
		s.NoteRTT(sl, Fam4, time.Duration(ms)*time.Millisecond)
	}
	if p := s.Primary(Fam4); p != "" {
		t.Fatalf("Primary before any LaneFastest acquire = %q, want \"\"", p)
	}
	specOnce(t, s, Fam4, Lane{}, nil)
	if p := s.Primary(Fam4); p != "" {
		t.Fatalf("a merge acquire must not set the primary, got %q", p)
	}
	for i := range 5 { // not released: busy-ness must not move LaneFastest flows
		got := s.AcquireLane(Fam4, specFastest, nil)
		if got == nil || got.Name != "B" {
			t.Fatalf("flow %d: want lowest-RTT adapter B, got %v", i, got)
		}
	}
	if p := s.Primary(Fam4); p != "B" {
		t.Fatalf("Primary = %q, want B", p)
	}
	for _, st := range s.Snapshot() {
		if st.Primary != (st.Name == "B") {
			t.Fatalf("SlotStats %s Primary = %v", st.Name, st.Primary)
		}
	}
}

func TestSpecFastestUsesHealthTiers(t *testing.T) {
	s := NewScheduler([]nics.Nic{nic("A", "10.0.0.1", 1), nic("B", "10.0.0.2", 1), nic("C", "10.0.0.3", 1)})
	a, b, c := specSlot(t, s, "A"), specSlot(t, s, "B"), specSlot(t, s, "C")
	s.SetState(a, Fam4, true) // up, slowest
	s.NoteRTT(a, Fam4, 50*time.Millisecond)
	s.NoteRTT(b, Fam4, 10*time.Millisecond) // never probed
	s.SetState(c, Fam4, false)              // down, fastest
	s.NoteRTT(c, Fam4, 5*time.Millisecond)
	if got := specOnce(t, s, Fam4, specFastest, nil); got != "A" {
		t.Fatalf("probed-up beats never-probed and down: want A, got %s", got)
	}
	s.SetState(a, Fam4, false)
	if got := specOnce(t, s, Fam4, specFastest, nil); got != "B" {
		t.Fatalf("never-probed beats down: want B, got %s", got)
	}
	if p := s.Primary(Fam4); p != "B" {
		t.Fatalf("Primary = %q, want B", p)
	}
	s.SetState(b, Fam4, false)
	if sl := s.AcquireLane(Fam4, specFastest, nil); sl == nil {
		t.Fatal("all adapters down: LaneFastest must still hand one out")
	} else {
		s.ReleaseFor(sl, Fam4)
	}
	s.SetState(a, Fam4, true)
	if got := specOnce(t, s, Fam4, specFastest, nil); got != "A" {
		t.Fatalf("A is the only probed-up adapter again: want A, got %s", got)
	}
}

func TestSpecFastestSlowerUpAdapterBeatsFasterUnprobedPrimary(t *testing.T) {
	s := NewScheduler([]nics.Nic{nic("A", "10.0.0.1", 1), nic("B", "10.0.0.2", 1)})
	a, b := specSlot(t, s, "A"), specSlot(t, s, "B")
	s.NoteRTT(a, Fam4, 80*time.Millisecond)
	s.NoteRTT(b, Fam4, 10*time.Millisecond)
	if got := specOnce(t, s, Fam4, specFastest, nil); got != "B" {
		t.Fatalf("both unprobed: want lower RTT B, got %s", got)
	}
	s.SetState(a, Fam4, true) // A is now the only adapter in the best tier
	if got := specOnce(t, s, Fam4, specFastest, nil); got != "A" {
		t.Fatalf("B left the best tier: want A, got %s", got)
	}
	if p := s.Primary(Fam4); p != "A" {
		t.Fatalf("Primary = %q, want A", p)
	}
}

func TestSpecFastestIsStickyWithHysteresis(t *testing.T) {
	s := NewScheduler([]nics.Nic{nic("A", "10.0.0.1", 1), nic("B", "10.0.0.2", 1)})
	a, b := specSlot(t, s, "A"), specSlot(t, s, "B")
	s.NoteRTT(a, Fam4, 40*time.Millisecond)
	if got := specOnce(t, s, Fam4, specFastest, nil); got != "A" {
		t.Fatalf("measured beats unmeasured: want A, got %s", got)
	}
	s.NoteRTT(b, Fam4, 30*time.Millisecond) // exactly 0.75 x 40ms: not strictly less
	for i := range 3 {
		if got := specOnce(t, s, Fam4, specFastest, nil); got != "A" {
			t.Fatalf("flow %d: B at exactly 0.75x must not take over, got %s", i, got)
		}
	}
	if p := s.Primary(Fam4); p != "A" {
		t.Fatalf("Primary = %q, want A", p)
	}
	s.NoteRTT(b, Fam4, 20*time.Millisecond) // B ~27ms < 30ms
	if got := specOnce(t, s, Fam4, specFastest, nil); got != "B" {
		t.Fatalf("B below 0.75x primary must take over, got %s", got)
	}
	if p := s.Primary(Fam4); p != "B" {
		t.Fatalf("Primary = %q, want B", p)
	}
	for range 3 { // A: 40 -> 28.3 -> 20.1 -> 14.4ms, well below 0.75 x 27ms
		s.NoteRTT(a, Fam4, 1*time.Millisecond)
	}
	if got := specOnce(t, s, Fam4, specFastest, nil); got != "A" {
		t.Fatalf("A far below 0.75x B must win back, got %s", got)
	}
}

func TestSpecFastestKeepsPrimaryWhenOnlySlightlyBeaten(t *testing.T) {
	s := NewScheduler([]nics.Nic{nic("A", "10.0.0.1", 1), nic("B", "10.0.0.2", 1)})
	a, b := specSlot(t, s, "A"), specSlot(t, s, "B")
	s.NoteRTT(a, Fam4, 40*time.Millisecond)
	if got := specOnce(t, s, Fam4, specFastest, nil); got != "A" {
		t.Fatalf("want A, got %s", got)
	}
	s.NoteRTT(b, Fam4, 31*time.Millisecond) // lower than A, but only 0.775x
	if got := specOnce(t, s, Fam4, specFastest, nil); got != "A" {
		t.Fatalf("B at 0.775x of primary must not take over, got %s", got)
	}
	// The primary getting worse counts too: A 0.7*40+0.3*100 = 58ms, B 31 < 43.5.
	s.NoteRTT(a, Fam4, 100*time.Millisecond)
	if got := specOnce(t, s, Fam4, specFastest, nil); got != "B" {
		t.Fatalf("primary RTT rose so B < 0.75x: want B, got %s", got)
	}
}

func TestSpecFastestPrimaryLeavesPool(t *testing.T) {
	setup := func(t *testing.T) (*Scheduler, *Slot, *time.Time) {
		s := NewScheduler([]nics.Nic{nic("A", "10.0.0.1", 1), nic("B", "10.0.0.2", 1)})
		clock := time.Unix(1000, 0)
		s.now = func() time.Time { return clock }
		a, b := specSlot(t, s, "A"), specSlot(t, s, "B")
		s.SetState(a, Fam4, true)
		s.SetState(b, Fam4, true)
		s.NoteRTT(a, Fam4, 50*time.Millisecond)
		s.NoteRTT(b, Fam4, 60*time.Millisecond)
		if got := specOnce(t, s, Fam4, specFastest, nil); got != "A" {
			t.Fatalf("setup: want A, got %s", got)
		}
		return s, a, &clock
	}
	t.Run("down", func(t *testing.T) {
		s, a, _ := setup(t)
		s.SetState(a, Fam4, false)
		if got := specOnce(t, s, Fam4, specFastest, nil); got != "B" {
			t.Fatalf("primary down: want B, got %s", got)
		}
		if p := s.Primary(Fam4); p != "B" {
			t.Fatalf("Primary = %q, want B", p)
		}
		s.SetState(a, Fam4, true) // A back at 50ms, not < 0.75 x 60 = 45
		if got := specOnce(t, s, Fam4, specFastest, nil); got != "B" {
			t.Fatalf("recovered A is not 25%% faster than B: B must stay, got %s", got)
		}
	})
	t.Run("failed", func(t *testing.T) {
		s, a, clock := setup(t)
		s.Fail(a, Fam4, 5*time.Second)
		if got := specOnce(t, s, Fam4, specFastest, nil); got != "B" {
			t.Fatalf("primary in cooldown: want B, got %s", got)
		}
		*clock = clock.Add(6 * time.Second)
		if got := specOnce(t, s, Fam4, specFastest, nil); got != "B" {
			t.Fatalf("after cooldown B must stay primary, got %s", got)
		}
		if p := s.Primary(Fam4); p != "B" {
			t.Fatalf("Primary = %q, want B", p)
		}
	})
	t.Run("gone", func(t *testing.T) {
		s, _, _ := setup(t)
		s.Update([]nics.Nic{nic("B", "10.0.0.2", 1)})
		if got := specOnce(t, s, Fam4, specFastest, nil); got != "B" {
			t.Fatalf("primary gone: want B, got %s", got)
		}
		if p := s.Primary(Fam4); p != "B" {
			t.Fatalf("Primary = %q, want B", p)
		}
	})
}

func TestSpecFastestUnmeasuredLosesToMeasured(t *testing.T) {
	s := NewScheduler([]nics.Nic{nic("A", "10.0.0.1", 1), nic("B", "10.0.0.2", 1)})
	s.NoteRTT(specSlot(t, s, "B"), Fam4, 500*time.Millisecond)
	for i := range 3 {
		if got := specOnce(t, s, Fam4, specFastest, nil); got != "B" {
			t.Fatalf("flow %d: unmeasured A must lose to measured B, got %s", i, got)
		}
	}
}

func TestSpecFastestNoneMeasuredPicksOneAndKeepsIt(t *testing.T) {
	s := NewScheduler([]nics.Nic{nic("A", "10.0.0.1", 1), nic("B", "10.0.0.2", 1), nic("C", "10.0.0.3", 1)})
	first := s.AcquireLane(Fam4, specFastest, nil)
	if first == nil {
		t.Fatal("got nil")
	}
	for i := range 9 { // not released
		if got := s.AcquireLane(Fam4, specFastest, nil); got != first {
			t.Fatalf("flow %d went to %v, want the kept choice %s", i, got, first.Name)
		}
	}
	if st := specStats(t, s, first.Name); st.Active != 10 || st.Total != 10 {
		t.Fatalf("%s Active/Total = %d/%d, want 10/10", first.Name, st.Active, st.Total)
	}
	if p := s.Primary(Fam4); p != first.Name {
		t.Fatalf("Primary = %q, want %s", p, first.Name)
	}
	// The kept primary is unmeasured while another adapter becomes measured: switch.
	other := "A"
	if first.Name == "A" {
		other = "B"
	}
	s.NoteRTT(specSlot(t, s, other), Fam4, 20*time.Millisecond)
	if got := specOnce(t, s, Fam4, specFastest, nil); got != other {
		t.Fatalf("unmeasured primary vs measured %s: want %s, got %s", other, other, got)
	}
	if p := s.Primary(Fam4); p != other {
		t.Fatalf("Primary = %q, want %s", p, other)
	}
}

func TestSpecFastestSkipDoesNotChangePrimary(t *testing.T) {
	s := NewScheduler([]nics.Nic{nic("A", "10.0.0.1", 1), nic("B", "10.0.0.2", 1), nic("C", "10.0.0.3", 1), nic("D", "10.0.0.4", 1)})
	for name, ms := range map[string]int{"A": 50, "B": 60, "C": 100} {
		sl := specSlot(t, s, name)
		s.SetState(sl, Fam4, true)
		s.NoteRTT(sl, Fam4, time.Duration(ms)*time.Millisecond)
	}
	s.SetState(specSlot(t, s, "D"), Fam4, true) // D unmeasured for now
	if got := specOnce(t, s, Fam4, specFastest, nil); got != "A" {
		t.Fatalf("want A, got %s", got)
	}
	if got := specOnce(t, s, Fam4, specFastest, specSkip("A")); got != "B" {
		t.Fatalf("primary skipped: want next-lowest B, got %s", got)
	}
	if p := s.Primary(Fam4); p != "A" {
		t.Fatalf("a skip acquire changed the primary to %q, want A", p)
	}
	if got := specOnce(t, s, Fam4, specFastest, nil); got != "A" {
		t.Fatalf("after the skip acquire: want A, got %s", got)
	}
	// D becomes much faster; a skip acquire may use it but must not make it the primary.
	s.NoteRTT(specSlot(t, s, "D"), Fam4, 10*time.Millisecond)
	if got := specOnce(t, s, Fam4, specFastest, specSkip("B")); got != "D" {
		t.Fatalf("skip acquire: want D, got %s", got)
	}
	if p := s.Primary(Fam4); p != "A" {
		t.Fatalf("a skip acquire changed the primary to %q, want A", p)
	}
	if got := specOnce(t, s, Fam4, specFastest, nil); got != "D" {
		t.Fatalf("normal acquire: want D, got %s", got)
	}
	if p := s.Primary(Fam4); p != "D" {
		t.Fatalf("Primary = %q, want D", p)
	}
}

func TestSpecFastestIsPerFamily(t *testing.T) {
	s := NewScheduler([]nics.Nic{
		specNic46("A", "10.0.0.1", "2001:db8::1"),
		specNic46("B", "10.0.0.2", "2001:db8::2"),
	})
	a, b := specSlot(t, s, "A"), specSlot(t, s, "B")
	s.NoteRTT(a, Fam4, 10*time.Millisecond)
	s.NoteRTT(a, Fam6, 90*time.Millisecond)
	s.NoteRTT(b, Fam4, 90*time.Millisecond)
	s.NoteRTT(b, Fam6, 10*time.Millisecond)

	if got := specOnce(t, s, Fam6, specFastest, nil); got != "B" {
		t.Fatalf("IPv6 lowest ping: want B, got %s", got)
	}
	if p6, p4 := s.Primary(Fam6), s.Primary(Fam4); p6 != "B" || p4 != "" {
		t.Fatalf("Primary v6/v4 = %q/%q, want B/\"\"", p6, p4)
	}
	for _, st := range s.Snapshot() {
		if st.Primary {
			t.Fatalf("SlotStats.Primary is the IPv4 primary only; %s marked after an IPv6 acquire", st.Name)
		}
	}
	if got := specOnce(t, s, Fam4, specFastest, nil); got != "A" {
		t.Fatalf("IPv4 lowest ping: want A, got %s", got)
	}
	if p6, p4 := s.Primary(Fam6), s.Primary(Fam4); p6 != "B" || p4 != "A" {
		t.Fatalf("Primary v6/v4 = %q/%q, want B/A", p6, p4)
	}
	if !specStats(t, s, "A").Primary || specStats(t, s, "B").Primary {
		t.Fatal("SlotStats.Primary must mark A (the IPv4 primary) only")
	}
}
