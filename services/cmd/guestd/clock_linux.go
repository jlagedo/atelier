//go:build linux

package main

import "golang.org/x/sys/unix"

// setSystemClock steps CLOCK_REALTIME to the host's wall-clock time (the setTime
// door). VZ has no Linux time-sync and the slim virtual-hwe kernel ships no
// built-in RTC, so the guest boots at 1970 and drifts on every host sleep; the
// host pushes the truth here. guestd runs as root, so settimeofday(2) is allowed.
// ms is always positive (guarded by the handler), so no negative-nsec normalize.
func setSystemClock(ms int64) error {
	return unix.ClockSettime(unix.CLOCK_REALTIME, &unix.Timespec{
		Sec:  ms / 1000,
		Nsec: (ms % 1000) * 1_000_000,
	})
}
