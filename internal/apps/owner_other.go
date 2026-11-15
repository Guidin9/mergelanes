//go:build !windows

package apps

import "net/netip"

// Owner is not implemented outside Windows yet: every flow counts as an unknown app, so the auto
// rules (realtime UDP on the lowest-latency adapter, the rest merged) still apply, but per-app
// rules do not.
func Owner(udp bool, src netip.AddrPort) string { return "" }
