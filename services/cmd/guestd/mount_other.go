//go:build !linux

package main

import "errors"

// guestd only ever runs in the Linux guest, but cmd/guestd must still build on
// the Windows dev box (go build ./...). The real 9p mount lives in
// mount_linux.go; these stubs keep the package compiling elsewhere.

func mountShare(uint32, string, string) error { return errors.New("mount: linux only") }
func unmountShare(string) error               { return errors.New("unmount: linux only") }
