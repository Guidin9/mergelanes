package apps

import (
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// These run against Windows' real socket tables: the test process opens sockets and must find
// itself as their owner.

func testExe(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Base(exe)
}

func TestOwnerFindsOwnTCPConnection(t *testing.T) {
	for _, network := range []string{"tcp4", "tcp6"} {
		addr := "127.0.0.1:0"
		if network == "tcp6" {
			addr = "[::1]:0"
		}
		ln, err := net.Listen(network, addr)
		if err != nil {
			t.Skipf("%s: %v", network, err)
		}
		go func() {
			if c, err := ln.Accept(); err == nil {
				defer c.Close()
				c.Read(make([]byte, 1))
			}
		}()
		c, err := net.Dial(network, ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		src := netip.MustParseAddrPort(c.LocalAddr().String())
		if got, want := owner(false, src, 0), testExe(t); !strings.EqualFold(got, want) {
			t.Errorf("%s: Owner(%v) = %q, want %q", network, src, got, want)
		}
		c.Close()
		ln.Close()
	}
}

func TestOwnerFindsWildcardUDPSocket(t *testing.T) {
	// Apps often bind UDP to 0.0.0.0, so the table has the wildcard address, while the flow's source
	// is the TUN's address.
	pc, err := net.ListenPacket("udp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	port := uint16(pc.LocalAddr().(*net.UDPAddr).Port)
	src := netip.AddrPortFrom(netip.MustParseAddr("198.18.0.1"), port)
	if got, want := owner(true, src, 0), testExe(t); !strings.EqualFold(got, want) {
		t.Errorf("Owner(udp %v) = %q, want %q", src, got, want)
	}
	// The same port over TCP is not this socket.
	if got := owner(false, src, 0); got != "" && strings.EqualFold(got, testExe(t)) {
		t.Errorf("a UDP socket must not answer for TCP, got %q", got)
	}
}

func TestOwnerUnknownSocket(t *testing.T) {
	// Port 1 on the TUN's address: nothing owns it.
	if got := Owner(false, netip.MustParseAddrPort("198.18.0.1:1")); got != "" {
		t.Errorf("Owner of an unused socket = %q, want empty", got)
	}
}

func TestParseTableLayouts(t *testing.T) {
	put32 := func(b []byte, v uint32) { b[0], b[1], b[2], b[3] = byte(v), byte(v>>8), byte(v>>16), byte(v>>24) }
	port := func(b []byte, p uint16) { b[0], b[1], b[2], b[3] = byte(p>>8), byte(p), 0, 0 }

	tcp4 := make([]byte, 4+tcp4Row)
	put32(tcp4, 1)
	r := tcp4[4:]
	put32(r[0:], 5) // ESTABLISHED
	copy(r[4:8], []byte{198, 18, 0, 1})
	port(r[8:], 50000)
	put32(r[20:], 1234)

	udp6 := make([]byte, 4+udp6Row)
	put32(udp6, 1)
	r = udp6[4:]
	a6 := netip.MustParseAddr("fd6e:6d65:7267::1").As16()
	copy(r[0:16], a6[:])
	port(r[20:], 3074)
	put32(r[24:], 99)

	m := map[sockKey]uint32{}
	parseTable(m, tcp4, false, false)
	parseTable(m, udp6, true, true)
	if pid := m[sockKey{false, netip.MustParseAddr("198.18.0.1"), 50000}]; pid != 1234 {
		t.Errorf("tcp4 row: pid %d, want 1234 (table %v)", pid, m)
	}
	if pid := m[sockKey{true, netip.MustParseAddr("fd6e:6d65:7267::1"), 3074}]; pid != 99 {
		t.Errorf("udp6 row: pid %d, want 99 (table %v)", pid, m)
	}
	// A count larger than the buffer must not read past it.
	put32(tcp4, 1000)
	parseTable(map[sockKey]uint32{}, tcp4, false, false)
}

// Flows that miss at the same moment share one read, and a hit in a fresh table needs no read.
func TestSockTableSharesReads(t *testing.T) {
	src := netip.MustParseAddrPort("198.18.0.1:40000")
	var reads atomic.Int32
	tb := newSockTable(false)
	tb.read = func(bool) (map[sockKey]uint32, error) {
		reads.Add(1)
		time.Sleep(20 * time.Millisecond)
		return map[sockKey]uint32{{false, src.Addr(), src.Port()}: 7}, nil
	}
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if pid, ok := tb.pid(src); !ok || pid != 7 {
				t.Errorf("pid = %d %v, want 7", pid, ok)
			}
		}()
	}
	wg.Wait()
	if n := reads.Load(); n > 2 {
		t.Errorf("%d reads for 20 simultaneous misses, want at most 2", n)
	}
	before := reads.Load()
	tb.pid(src)
	if reads.Load() != before {
		t.Error("a hit in a fresh table read it again")
	}
}

// MergeLanes' own sockets count as unknown, so its update check stays out of the Apps menu.
func TestOwnerIgnoresItself(t *testing.T) {
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	src := netip.MustParseAddrPort(pc.LocalAddr().String())
	if got := Owner(true, src); got != "" {
		t.Errorf("Owner of our own socket = %q, want empty", got)
	}
	if got := owner(true, src, 0); !strings.EqualFold(got, testExe(t)) {
		t.Errorf("owner(..., 0) = %q, want %q", got, testExe(t))
	}
}
