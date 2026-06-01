//go:build !darwin || !debugconsole

package main

import "errors"

// runConsole is a stub outside debug builds: the interactive debug console is gated by the
// `debugconsole` build tag (set only for --config=debug) and rides the VZ hvc1 serial port,
// so it exists only in a debug-built atelierctl on macOS. (HCS analog is a TODO.)
func runConsole(string) error {
	return errors.New("the debug console is only available in debug builds (-tags debugconsole) on macOS")
}
