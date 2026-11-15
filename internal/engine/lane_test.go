package engine

import (
	"testing"
	"time"

	"mergelanes/internal/nics"
)

// A rule typed as "ethernet" pins to the adapter Windows calls "Ethernet".
func TestPinnedMatchesAdapterNameWithoutCase(t *testing.T) {
	s := NewScheduler([]nics.Nic{nic("Ethernet", "10.0.0.2", 1), nic("Wi-Fi", "10.0.0.3", 1)})
	for range 4 {
		sl := s.AcquireLane(Fam4, Lane{Kind: LanePinned, Adapter: "ethernet"}, nil)
		if sl == nil || sl.Name != "Ethernet" {
			t.Fatalf("pinned to %v, want Ethernet", sl)
		}
		s.ReleaseFor(sl, Fam4)
	}
	// A failed dial skips it by its real name, and the flow falls back.
	sl := s.AcquireLane(Fam4, Lane{Kind: LanePinned, Adapter: "ETHERNET"}, map[string]struct{}{"Ethernet": {}})
	if sl == nil || sl.Name != "Wi-Fi" {
		t.Fatalf("fallback went to %v, want Wi-Fi", sl)
	}
}

// A new address means a new network, whose latency is not known yet.
func TestAddressChangeForgetsLatency(t *testing.T) {
	s := NewScheduler([]nics.Nic{nic("Wi-Fi", "10.0.0.3", 1)})
	s.NoteRTT(s.slots[0], Fam4, 20*time.Millisecond)
	s.Update([]nics.Nic{nic("Wi-Fi", "10.0.0.3", 1)})
	if s.slots[0].RTT(Fam4) == 0 {
		t.Fatal("same address: latency should stay")
	}
	s.Update([]nics.Nic{nic("Wi-Fi", "192.168.1.7", 1)})
	if r := s.slots[0].RTT(Fam4); r != 0 {
		t.Errorf("latency after an address change = %v, want 0", r)
	}
}
