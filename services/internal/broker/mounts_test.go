package broker

import (
	"io"
	"log/slog"
	"testing"

	"github.com/jlagedo/atelier/services/internal/vsock"
)

// TestMountAllocAndTrack covers the S6.1 multi-share bookkeeping: the broker
// tracks several concurrent shares by tag and allocates a free vsock port per
// share (skipping ports already in use, reusing freed ones).
func TestMountAllocAndTrack(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	b := New(log, nil)

	// First auto-allocated share gets the session port base.
	a := b.addMount(mountInfo{hostPath: "/h/a", guestPath: "/sessions/a", tag: "a"})
	if a.port != vsock.SessionPlan9PortBase {
		t.Fatalf("first alloc port = %d, want %d", a.port, vsock.SessionPlan9PortBase)
	}
	// Second auto-allocated share skips the used port.
	c := b.addMount(mountInfo{hostPath: "/h/c", guestPath: "/sessions/c", tag: "c"})
	if c.port != vsock.SessionPlan9PortBase+1 {
		t.Fatalf("second alloc port = %d, want %d", c.port, vsock.SessionPlan9PortBase+1)
	}
	// An explicit port is preserved as-is.
	d := b.addMount(mountInfo{hostPath: "/h/d", guestPath: "/sessions/d", tag: "d", port: vsock.WorkspacePlan9Port})
	if d.port != vsock.WorkspacePlan9Port {
		t.Fatalf("explicit port = %d, want %d", d.port, vsock.WorkspacePlan9Port)
	}

	if !b.hasMount("a") || !b.hasMount("c") || !b.hasMount("d") {
		t.Fatal("hasMount should report all three shares attached")
	}

	// Removing a share frees its port for reuse by the next allocation.
	if _, ok := b.removeMount("a"); !ok {
		t.Fatal("removeMount(a) should report it existed")
	}
	if b.hasMount("a") {
		t.Fatal("share a should be gone after removeMount")
	}
	reuse := b.addMount(mountInfo{hostPath: "/h/e", guestPath: "/sessions/e", tag: "e"})
	if reuse.port != vsock.SessionPlan9PortBase {
		t.Fatalf("reused port = %d, want freed %d", reuse.port, vsock.SessionPlan9PortBase)
	}

	// Removing an absent share is a no-op miss.
	if _, ok := b.removeMount("nope"); ok {
		t.Fatal("removeMount(nope) should report missing")
	}
}
