//go:build !linux

package main

import "errors"

// runner only ever sets the clock in the Linux guest, but cmd/runner must still
// build on the Windows dev box (go build ./...). The real syscall lives in
// clock_linux.go; this stub keeps the package compiling elsewhere.

func setSystemClock(int64) error { return errors.New("setTime: linux only") }
