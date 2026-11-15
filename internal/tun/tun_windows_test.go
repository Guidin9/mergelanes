package tun

import "testing"

// The remembered weak host settings are handed to netsh as admin, so only the exact form that
// syncStrongHost writes may get through.
func TestRestoreLineAcceptsOnlyWhatWeWrite(t *testing.T) {
	for _, l := range []string{
		"ipv4 14 weakhostsend=enabled forwarding=disabled",
		"ipv6 7 weakhostsend=disabled forwarding=enabled",
	} {
		if !restoreLine.MatchString(l) {
			t.Errorf("rejected %q", l)
		}
	}
	for _, l := range []string{
		"",
		"ipv4 14 weakhostsend=enabled forwarding=disabled extra",
		"ipv4 14 metric=1 forwarding=disabled",
		"portproxy 14 weakhostsend=enabled forwarding=disabled",
		"ipv4 Wi-Fi weakhostsend=enabled forwarding=disabled",
		"ipv4 14 weakhostsend=enabled forwarding=disabled\nipv4 1 mtu=1 forwarding=enabled",
		"ipv4 14 weakhostsend=enabled forwarding=enabled store=persistent",
	} {
		if restoreLine.MatchString(l) {
			t.Errorf("accepted %q", l)
		}
	}
}
