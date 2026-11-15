package apps

import (
	"encoding/binary"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The owner of a flow is found the way netstat -ano does it: the app's socket is bound to the TUN's
// address and the flow's source port, and Windows' TCP and UDP tables name the process that owns
// each socket. A table is read as a whole, one per protocol (both families: a dual-stack IPv6
// socket that talks IPv4 is listed as IPv6). The app's socket exists before its first packet
// reaches the TUN, so a flow is found by any read that starts after it arrived; flows that miss at
// the same moment (a web page opening twenty connections) share one read instead of making one each.

var (
	iphlpapi                = windows.NewLazySystemDLL("iphlpapi.dll")
	procGetExtendedTcpTable = iphlpapi.NewProc("GetExtendedTcpTable")
	procGetExtendedUdpTable = iphlpapi.NewProc("GetExtendedUdpTable")
)

const (
	tcpTableOwnerPIDAll = 5 // TCP_TABLE_OWNER_PID_ALL
	udpTableOwnerPID    = 1 // UDP_TABLE_OWNER_PID

	tableMaxAge = time.Second      // a hit in an older read is not trusted: the port may have been reused
	nameMaxAge  = 10 * time.Second // PIDs are reused, so a name is only trusted this long
)

type sockKey struct {
	udp  bool
	addr netip.Addr
	port uint16
}

// sockTable is the cached table of one protocol.
type sockTable struct {
	udp  bool
	read func(udp bool) (map[sockKey]uint32, error) // readTable; tests replace it

	mu      sync.Mutex
	done    *sync.Cond
	m       map[sockKey]uint32
	at      time.Time // when the read behind m started
	gen     uint64    // the number of the read behind m
	started uint64    // reads started so far
	reading bool
}

func newSockTable(udp bool) *sockTable {
	t := &sockTable{udp: udp, read: readTable}
	t.done = sync.NewCond(&t.mu)
	return t
}

var sockets = [2]*sockTable{newSockTable(false), newSockTable(true)}

// pid finds the process that owns the socket at src. A UDP socket may be bound to the wildcard
// address (0.0.0.0 or ::, the latter also for dual-stack IPv4), so for UDP that is tried as well.
//
// Reads are numbered rather than timed: Windows' clock moves in steps of up to 15.6 ms, so a read
// and a flow that arrived just after it can carry the same time.
func (t *sockTable) pid(src netip.AddrPort) (uint32, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.m != nil && time.Since(t.at) < tableMaxAge {
		if pid, ok := t.lookup(src); ok {
			return pid, true
		}
	}
	need := t.started + 1 // only a read that starts from now on is sure to know this flow
	for t.gen < need {
		if t.reading {
			t.done.Wait()
			continue
		}
		t.reading = true
		t.started++
		n, began := t.started, time.Now()
		t.mu.Unlock()
		m, err := t.read(t.udp)
		t.mu.Lock()
		t.reading = false
		if err == nil {
			t.m = m
		}
		t.gen, t.at = n, began
		t.done.Broadcast()
	}
	return t.lookup(src)
}

func (t *sockTable) lookup(src netip.AddrPort) (uint32, bool) {
	a, port := src.Addr().Unmap(), src.Port()
	if pid, ok := t.m[sockKey{t.udp, a, port}]; ok {
		return pid, true
	}
	if !t.udp {
		return 0, false
	}
	for _, any := range []netip.Addr{netip.IPv4Unspecified(), netip.IPv6Unspecified()} {
		if pid, ok := t.m[sockKey{true, any, port}]; ok {
			return pid, true
		}
	}
	return 0, false
}

// readTable reads every socket of one protocol, both families, with its owning PID.
func readTable(udp bool) (map[sockKey]uint32, error) {
	proc, class := procGetExtendedTcpTable, uint32(tcpTableOwnerPIDAll)
	if udp {
		proc, class = procGetExtendedUdpTable, udpTableOwnerPID
	}
	m := make(map[sockKey]uint32, 512)
	for _, af := range []uint32{windows.AF_INET, windows.AF_INET6} {
		b, err := extendedTable(proc, af, class)
		if err != nil {
			return nil, err
		}
		parseTable(m, b, udp, af == windows.AF_INET6)
	}
	return m, nil
}

// extendedTable calls GetExtendedTcpTable or GetExtendedUdpTable, growing the buffer until the
// table fits (it can grow between the size query and the read).
func extendedTable(proc *windows.LazyProc, af, class uint32) ([]byte, error) {
	size := uint32(64 << 10)
	for range 8 {
		buf := make([]byte, size)
		r, _, _ := proc.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0,
			uintptr(af), uintptr(class), 0)
		switch windows.Errno(r) {
		case windows.ERROR_SUCCESS:
			return buf, nil
		case windows.ERROR_INSUFFICIENT_BUFFER:
			size += 16 << 10
		default:
			return nil, windows.Errno(r)
		}
	}
	return nil, windows.ERROR_INSUFFICIENT_BUFFER
}

// Row layouts (iphlpapi, all DWORD-aligned after the DWORD entry count). Ports are DWORDs whose
// first two bytes hold the port in network order; IPv4 addresses are in network order too.
const (
	tcp4Row = 24 // MIB_TCPROW_OWNER_PID: state, local addr, local port, remote addr, remote port, pid
	tcp6Row = 56 // MIB_TCP6ROW_OWNER_PID: local addr[16], scope, local port, remote addr[16], scope, remote port, state, pid
	udp4Row = 12 // MIB_UDPROW_OWNER_PID: local addr, local port, pid
	udp6Row = 28 // MIB_UDP6ROW_OWNER_PID: local addr[16], scope, local port, pid
)

func parseTable(m map[sockKey]uint32, b []byte, udp, v6 bool) {
	if len(b) < 4 {
		return
	}
	n := int(binary.LittleEndian.Uint32(b))
	row, addrAt, portAt, pidAt := tcp4Row, 4, 8, 20
	switch {
	case udp && v6:
		row, addrAt, portAt, pidAt = udp6Row, 0, 20, 24
	case udp:
		row, addrAt, portAt, pidAt = udp4Row, 0, 4, 8
	case v6:
		row, addrAt, portAt, pidAt = tcp6Row, 0, 20, 52
	}
	for i := range n {
		r := b[4+i*row:]
		if len(r) < row {
			return
		}
		var a netip.Addr
		if v6 {
			a = netip.AddrFrom16([16]byte(r[addrAt : addrAt+16])).Unmap()
		} else {
			a = netip.AddrFrom4([4]byte(r[addrAt : addrAt+4]))
		}
		port := binary.BigEndian.Uint16(r[portAt:])
		m[sockKey{udp, a, port}] = binary.LittleEndian.Uint32(r[pidAt:])
	}
}

type nameEntry struct {
	name string
	at   time.Time
}

var names struct {
	mu sync.Mutex
	m  map[uint32]nameEntry
}

// processName is the program's file name, e.g. "steam.exe".
func processName(pid uint32) string {
	switch pid {
	case 0:
		return ""
	case 4:
		return "System"
	}
	now := time.Now()
	names.mu.Lock()
	if e, ok := names.m[pid]; ok && now.Sub(e.at) < nameMaxAge {
		names.mu.Unlock()
		return e.name
	}
	names.mu.Unlock()

	name := imageName(pid)
	if name == "" {
		name = listedName(pid)
	}
	names.mu.Lock()
	if names.m == nil || len(names.m) > 4096 {
		names.m = map[uint32]nameEntry{}
	}
	names.m[pid] = nameEntry{name, now}
	names.mu.Unlock()
	return name
}

func imageName(pid uint32) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, 4096)
	n := uint32(len(buf))
	if windows.QueryFullProcessImageName(h, 0, &buf[0], &n) != nil {
		return ""
	}
	return filepath.Base(windows.UTF16ToString(buf[:n]))
}

// listedName finds the process in the process list, which also names processes that cannot be
// opened, such as games protected by their anti-cheat.
func listedName(pid uint32) string {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(snap)
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		if e.ProcessID == pid {
			return windows.UTF16ToString(e.ExeFile[:])
		}
	}
	return ""
}

// Owner names the program that owns the TCP or UDP socket at src, or "" if it can't be found.
// MergeLanes' own connections (the update check) count as unknown, so they stay out of the Apps menu.
func Owner(udp bool, src netip.AddrPort) string { return owner(udp, src, uint32(os.Getpid())) }

// owner is Owner with the process to ignore passed in (0: none), so tests can find their own sockets.
func owner(udp bool, src netip.AddrPort, self uint32) string {
	t := sockets[0]
	if udp {
		t = sockets[1]
	}
	pid, ok := t.pid(src)
	if !ok || pid == self {
		return ""
	}
	return processName(pid)
}
