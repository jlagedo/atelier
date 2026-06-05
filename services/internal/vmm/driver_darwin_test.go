//go:build darwin

package vmm

import (
	"errors"
	"fmt"
	"syscall"
	"testing"

	vz "github.com/Code-Hex/vz/v3"
)

func TestClampBoundsValue(t *testing.T) {
	tests := []struct {
		name            string
		v, lo, hi, want uint64
	}{
		{"below lo -> lo", 5, 10, 100, 10},
		{"above hi -> hi", 500, 10, 100, 100},
		{"in range -> unchanged", 42, 10, 100, 42},
		{"at lo -> lo", 10, 10, 100, 10},
		{"at hi -> hi", 100, 10, 100, 100},
	}
	for _, tt := range tests {
		if got := clamp(tt.v, tt.lo, tt.hi); got != tt.want {
			t.Errorf("%s: clamp(%d,%d,%d) = %d, want %d", tt.name, tt.v, tt.lo, tt.hi, got, tt.want)
		}
	}
}

func TestIsTransientDialError(t *testing.T) {
	// A plain Go error (the net.FileConn path on the fresh fd) is never an *vz.NSError,
	// so it counts as a transient boot-window race worth retrying.
	if !isTransientDialError(errors.New("file conn failed")) {
		t.Error("plain Go error should be transient")
	}
	if !isTransientDialError(fmt.Errorf("wrapped: %w", errors.New("x"))) {
		t.Error("wrapped plain Go error should be transient")
	}

	transient := []syscall.Errno{syscall.ECONNRESET, syscall.ECONNREFUSED, syscall.ENOTCONN, syscall.EBADF}
	for _, errno := range transient {
		err := &vz.NSError{Code: int(errno), Domain: "test"}
		if !isTransientDialError(err) {
			t.Errorf("NSError code %d (%v) should be transient", errno, errno)
		}
		if !isTransientDialError(fmt.Errorf("wrapped: %w", err)) {
			t.Errorf("wrapped NSError code %d should be transient", errno)
		}
	}

	// A clearly-fatal framework NSError must fail fast, not retry the whole budget.
	if isTransientDialError(&vz.NSError{Code: int(syscall.EPERM), Domain: "test"}) {
		t.Error("NSError EPERM should be terminal")
	}
}
