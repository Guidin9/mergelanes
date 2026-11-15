package engine

import (
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// stamp times a probe's handshake. Go's clock on Windows moves in steps of up to 15.6 ms on an idle
// machine, which would round most link latencies to 0 or 15.6 ms; the performance counter does not.
type stamp int64

var (
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	procQPC  = kernel32.NewProc("QueryPerformanceCounter")
	procQPF  = kernel32.NewProc("QueryPerformanceFrequency")

	qpcFreq = func() int64 {
		var f int64
		if r, _, _ := procQPF.Call(uintptr(unsafe.Pointer(&f))); r == 0 || f <= 0 {
			return 0
		}
		return f
	}()
)

func qpc() int64 {
	var c int64
	procQPC.Call(uintptr(unsafe.Pointer(&c)))
	return c
}

func clockNow() stamp { return stamp(qpc()) }

func (s stamp) elapsed() time.Duration {
	if qpcFreq == 0 {
		return 0
	}
	d := qpc() - int64(s)
	return time.Duration(d/qpcFreq)*time.Second + time.Duration(d%qpcFreq*int64(time.Second)/qpcFreq)
}
