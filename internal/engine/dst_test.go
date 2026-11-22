package engine

import (
	"testing"
	"time"

	"mergelanes/internal/nics"
)

const cdn = "199.232.209.133:80"

func acquireTo(s *Scheduler, n int, dst string) []*Slot {
	out := make([]*Slot, n)
	for i := range out {
		out[i] = s.AcquireTo(Fam4, nil, dst)
	}
	return out
}

// A download opens its connections at once. B looks busier only because of chatty, tiny flows, so
// without a destination every one of them goes to A; with it they are split over both links.
func TestBurstToOneDestinationSpreads(t *testing.T) {
	s := NewScheduler([]nics.Nic{nic("A", "10.0.0.1", 1), nic("B", "10.0.0.2", 1)})
	s.SetAuto(true)
	b := s.slots[1]
	for range 8 {
		b.actF[Fam4].Add(1)
		s.track(b, Fam4)
	}
	if p := acquireTo(s, 4, ""); countBy(p, "A") != 4 {
		t.Fatalf("flows without a destination: want all on A, got A=%d B=%d", countBy(p, "A"), countBy(p, "B"))
	}
	p := acquireTo(s, 4, cdn)
	if countBy(p, "A") != 2 || countBy(p, "B") != 2 {
		t.Fatalf("burst to one destination: want 2/2, got A=%d B=%d", countBy(p, "A"), countBy(p, "B"))
	}
}

// Unequal weights, so spreading would pick the other adapter than the load does: A scores
// (3+1)/1 = 4 on load and B (0+1)/0.4 = 2.5, but with no flows to dst A would win on (0+1)/1 = 1
// against B's (0+1)/0.8.
func TestFirstFlowToDestinationIsPlacedAsBefore(t *testing.T) {
	s := NewScheduler([]nics.Nic{nic("A", "10.0.0.1", 1), nic("B", "10.0.0.2", 0.4)})
	pinA := Lane{Kind: LanePinned, Adapter: "A"}
	for range 3 {
		s.AcquireLane(Fam4, pinA, nil)
	}
	if got := s.AcquireTo(Fam4, nil, cdn); got.Name != "B" {
		t.Fatalf("first flow to a destination went to %s, want B as by load", got.Name)
	}
}

// A browser keeps idle keep-alive sockets to a CDN address that many sites share. They must not
// steer a new download to that address onto the other, busy link.
func TestIdleFlowsToDestinationDoNotSteer(t *testing.T) {
	s := NewScheduler([]nics.Nic{nic("A", "10.0.0.1", 1), nic("B", "10.0.0.2", 1)})
	s.SetAuto(true)
	a, b := s.slots[0], s.slots[1]
	pinA := Lane{Kind: LanePinned, Adapter: "A"}
	for range 10 {
		s.trackTo(s.AcquireLaneTo(Fam4, pinA, nil, cdn), Fam4, cdn)
	}
	s.Tick(time.Second)
	s.Tick(time.Second) // A's ten sockets to cdn are idle now
	if got := a.busy(Fam4); got != 0 {
		t.Fatalf("setup: A's sockets should be idle, busy=%d", got)
	}
	for range 30 {
		b.actF[Fam4].Add(1)
		s.track(b, Fam4) // fresh, so busy
	}
	p := acquireTo(s, 4, cdn)
	if countBy(p, "A") != 2 || countBy(p, "B") != 2 {
		t.Fatalf("want 2/2 despite A's idle sockets to the same address, got A=%d B=%d", countBy(p, "A"), countBy(p, "B"))
	}
}

// In static mode every open flow counts, idle or not, like the open-flow load does.
func TestStaticModeCountsEveryFlowToDestination(t *testing.T) {
	s := NewScheduler([]nics.Nic{nic("A", "10.0.0.1", 1), nic("B", "10.0.0.2", 1)})
	pinA := Lane{Kind: LanePinned, Adapter: "A"}
	for range 3 {
		s.trackTo(s.AcquireLaneTo(Fam4, pinA, nil, cdn), Fam4, cdn)
	}
	s.Tick(time.Second)
	s.Tick(time.Second)
	if p := acquireTo(s, 2, cdn); countBy(p, "B") != 2 {
		t.Fatalf("want both on B (A holds 3 to cdn), got A=%d B=%d", countBy(p, "A"), countBy(p, "B"))
	}
}

func TestDestinationSpreadFollowsWeights(t *testing.T) {
	s := NewScheduler([]nics.Nic{nic("A", "10.0.0.1", 1), nic("B", "10.0.0.2", 0.4)})
	p := acquireTo(s, 5, cdn)
	// Spread weight 0.8 for B: A, B, A, B, A.
	if countBy(p, "A") != 3 || countBy(p, "B") != 2 {
		t.Fatalf("want A=3 B=2, got A=%d B=%d", countBy(p, "A"), countBy(p, "B"))
	}
}

// A link at the idle-recovery cap (half the best) may be as fast: it gets an equal share.
func TestUnprovenLinkGetsEqualShare(t *testing.T) {
	s := NewScheduler([]nics.Nic{nic("A", "10.0.0.1", 1), nic("B", "10.0.0.2", 0.5)})
	if p := acquireTo(s, 4, cdn); countBy(p, "A") != 2 || countBy(p, "B") != 2 {
		t.Fatalf("want 2/2, got A=%d B=%d", countBy(p, "A"), countBy(p, "B"))
	}
}

// A clearly slow link still gets only a small share of a download's connections.
func TestSlowLinkGetsFewFlows(t *testing.T) {
	s := NewScheduler([]nics.Nic{nic("A", "10.0.0.1", 1), nic("B", "10.0.0.2", 0.1)})
	if p := acquireTo(s, 6, cdn); countBy(p, "A") != 5 || countBy(p, "B") != 1 {
		t.Fatalf("want A=5 B=1, got A=%d B=%d", countBy(p, "A"), countBy(p, "B"))
	}
}

// A holds 2 flows to cdn, B 5 flows to nowhere in particular. Another destination's first flow
// follows the load (A); one more flow to cdn goes to B, which has none of cdn's.
func TestDestinationsAreIndependent(t *testing.T) {
	s := NewScheduler([]nics.Nic{nic("A", "10.0.0.1", 1), nic("B", "10.0.0.2", 1)})
	for range 2 {
		s.AcquireLaneTo(Fam4, Lane{Kind: LanePinned, Adapter: "A"}, nil, cdn)
	}
	for range 5 {
		s.AcquireLane(Fam4, Lane{Kind: LanePinned, Adapter: "B"}, nil)
	}
	if got := s.AcquireTo(Fam4, nil, "203.0.113.7:443"); got.Name != "A" {
		t.Fatalf("another destination's first flow went to %s, want A as by load", got.Name)
	}
	if got := s.AcquireTo(Fam4, nil, cdn); got.Name != "B" {
		t.Fatalf("a flow to cdn went to %s, want B (A already holds cdn's flows)", got.Name)
	}
}

func TestReleaseToForgetsDestination(t *testing.T) {
	s := NewScheduler([]nics.Nic{nic("A", "10.0.0.1", 1), nic("B", "10.0.0.2", 1)})
	p := acquireTo(s, 3, cdn)
	for _, sl := range p {
		s.ReleaseTo(sl, Fam4, cdn)
	}
	for _, sl := range s.slots {
		if n := len(sl.sib[Fam4]); n != 0 {
			t.Fatalf("%s still counts %d destinations after every flow was released", sl.Name, n)
		}
		if got := sl.actF[Fam4].Load(); got != 0 {
			t.Fatalf("%s still counts %d open flows", sl.Name, got)
		}
	}
	// Releasing one of two keeps the other counted.
	x := acquireTo(s, 2, cdn)
	s.ReleaseTo(x[0], Fam4, cdn)
	if got := x[1].sib[Fam4][cdn]; got != 1 {
		t.Fatalf("the open flow should still be counted once, got %d", got)
	}
}

func TestDestinationSpreadSkipsDeadAdapter(t *testing.T) {
	s := NewScheduler([]nics.Nic{nic("A", "10.0.0.1", 1), nic("B", "10.0.0.2", 1)})
	s.SetState(s.slots[0], Fam4, true)
	s.SetState(s.slots[1], Fam4, false)
	if p := acquireTo(s, 4, cdn); countBy(p, "A") != 4 {
		t.Fatalf("a dead adapter must not get flows: A=%d B=%d", countBy(p, "A"), countBy(p, "B"))
	}
	skip := map[string]struct{}{s.slots[0].key: {}}
	s.SetState(s.slots[1], Fam4, true)
	if got := s.AcquireTo(Fam4, skip, cdn); got.Name != "B" {
		t.Fatalf("skip set ignored: got %s", got.Name)
	}
}

// A pinned app and a merged one can talk to the same server: the pinned flows count too.
func TestPinnedFlowsCountForDestination(t *testing.T) {
	s := NewScheduler([]nics.Nic{nic("A", "10.0.0.1", 1), nic("B", "10.0.0.2", 1)})
	pin := Lane{Kind: LanePinned, Adapter: "A"}
	for range 2 {
		if got := s.AcquireLaneTo(Fam4, pin, nil, cdn); got.Name != "A" {
			t.Fatalf("pinned flow went to %s", got.Name)
		}
	}
	if got := s.AcquireLaneTo(Fam4, Lane{}, nil, cdn); got.Name != "B" {
		t.Fatalf("merged flow to the same server should go to B, got %s", got.Name)
	}
	if got := s.slots[0].sib[Fam4][cdn]; got != 2 {
		t.Fatalf("A should count its 2 pinned flows, got %d", got)
	}
}
