//go:build !windows

package engine

import "time"

// stamp times a probe's handshake; see clock_windows.go for why it is not just time.Now there.
type stamp time.Time

func clockNow() stamp { return stamp(time.Now()) }

func (s stamp) elapsed() time.Duration { return time.Since(time.Time(s)) }
