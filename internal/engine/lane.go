package engine

import (
	"math"
	"math/rand/v2"
	"strings"
	"time"
)

// Lane says which adapters a new flow may use. The zero value merges: the flow goes to whichever
// adapter the balancer picks, as every flow did before lanes existed.
type Lane struct {
	Kind    LaneKind
	Adapter string // LanePinned: the adapter's name, e.g. "Ethernet"
}

// LaneKind is how a flow's adapter is chosen.
type LaneKind uint8

const (
	// LaneMerge balances the flow over every adapter that works (the default).
	LaneMerge LaneKind = iota
	// LaneFastest puts the flow on the adapter with the lowest measured latency. It stays there
	// until another adapter is clearly faster, so a game's flows do not hop between links.
	LaneFastest
	// LanePinned puts the flow on Adapter. When Adapter is missing, has no internet or fails to
	// connect, the flow falls back to the fastest of the other adapters.
	LanePinned
)

func (l Lane) String() string {
	switch l.Kind {
	case LaneFastest:
		return "lowest ping"
	case LanePinned:
		return "only " + l.Adapter
	}
	return "merged"
}

// rttSwitch is how much faster another adapter has to be before LaneFastest leaves the current one.
// Without the margin two links with similar latency would trade places on every probe.
const rttSwitch = 0.75

// NoteRTT records a latency measurement (a TCP connect through the adapter), smoothed so one slow
// handshake does not move every game to another link.
func (s *Scheduler) NoteRTT(sl *Slot, f Family, d time.Duration) {
	if d <= 0 {
		return
	}
	us := d.Microseconds()
	if us == 0 {
		us = 1
	}
	if old := sl.rtt[f].Load(); old > 0 {
		us = int64(math.Round(0.7*float64(old) + 0.3*float64(us)))
	}
	sl.rtt[f].Store(us)
}

// RTT is the smoothed latency of the adapter for one family, 0 if it has not been measured yet.
func (sl *Slot) RTT(f Family) time.Duration {
	return time.Duration(sl.rtt[f].Load()) * time.Microsecond
}

// AcquireLane picks an adapter for a new flow in the given lane, skipping the ones in skip (adapters
// this flow already failed on). The caller must ReleaseFor it, like with AcquireFor.
func (s *Scheduler) AcquireLane(f Family, lane Lane, skip map[string]struct{}) *Slot {
	return s.AcquireLaneTo(f, lane, skip, "")
}

// AcquireLaneTo is AcquireLane for a TCP flow to dst (host:port); release it with ReleaseTo and the
// same dst. Only merged flows are spread by destination (see AcquireTo), but flows in every lane are
// counted, so the counts stay true when a pinned app and a merged one talk to the same server.
func (s *Scheduler) AcquireLaneTo(f Family, lane Lane, skip map[string]struct{}, dst string) *Slot {
	switch lane.Kind {
	case LanePinned:
		s.mu.Lock()
		sl := s.pinnedLocked(f, lane.Adapter, skip)
		if sl != nil {
			s.takeLocked(sl, f)
			s.noteDstLocked(sl, f, dst)
		}
		s.mu.Unlock()
		if sl != nil {
			return sl
		}
		return s.acquireFastest(f, skip, dst)
	case LaneFastest:
		return s.acquireFastest(f, skip, dst)
	}
	return s.AcquireTo(f, skip, dst)
}

// pinnedLocked returns the named adapter if it can carry the flow right now. An adapter that has
// not been probed yet counts as usable, the same as for merged flows.
func (s *Scheduler) pinnedLocked(f Family, name string, skip map[string]struct{}) *Slot {
	if name == "" {
		return nil
	}
	now := s.now()
	for _, sl := range s.slots {
		if !strings.EqualFold(sl.key, name) || !sl.present || sl.ip(f) == nil {
			continue
		}
		if _, no := skip[sl.key]; no {
			return nil
		}
		if now.Before(sl.downUntil[f]) || sl.state[f] == stateDown {
			return nil
		}
		return sl
	}
	return nil
}

func (s *Scheduler) acquireFastest(f Family, skip map[string]struct{}, dst string) *Slot {
	s.mu.Lock()
	defer s.mu.Unlock()
	pool := s.poolLocked(f, skip)
	if len(pool) == 0 {
		return nil
	}
	var cur, best *Slot
	for _, sl := range pool {
		if sl.key == s.primary[f] {
			cur = sl
		}
		if r := sl.rtt[f].Load(); r > 0 && (best == nil || r < best.rtt[f].Load()) {
			best = sl
		}
	}
	pick := cur
	switch {
	case best == nil && cur == nil:
		// Nothing measured yet: any adapter, but then stay on it until a measurement says otherwise.
		pick = pool[rand.IntN(len(pool))]
	case cur == nil:
		pick = best
	case best != nil && best != cur:
		if c := cur.rtt[f].Load(); c == 0 || float64(best.rtt[f].Load()) < rttSwitch*float64(c) {
			pick = best
		}
	}
	// A retry that skips the primary (it just failed for this flow) must not move the others.
	if len(skip) == 0 {
		s.primary[f] = pick.key
	}
	s.takeLocked(pick, f)
	s.noteDstLocked(pick, f, dst)
	return pick
}

// Primary is the adapter LaneFastest flows of family f currently go to, "" before the first one.
func (s *Scheduler) Primary(f Family) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.primary[f]
}
