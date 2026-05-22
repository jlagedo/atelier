package broker

import (
	"io"
	"log/slog"
	"testing"
)

func newTestBroker() *Broker {
	return New(slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
}

// TestRemoveMountIf is the rollback-clobber guard: a failed attach must undo only
// the entry it added, never a different live share that already holds the tag.
func TestRemoveMountIf(t *testing.T) {
	b := newTestBroker()
	m1 := mountInfo{hostPath: "h1", guestPath: "/sessions/a", tag: "a", port: 600}
	b.addMount(m1)

	// A non-matching identity (e.g. a concurrent winner that re-took the tag) must
	// not be removed by m1's rollback.
	m2 := mountInfo{hostPath: "h2", guestPath: "/sessions/a", tag: "a", port: 601}
	if b.removeMountIf("a", m2) {
		t.Fatal("removeMountIf removed a non-matching mount")
	}
	if !b.hasMount("a") {
		t.Fatal("live mount was clobbered by a non-matching rollback")
	}

	// The exact entry is removed.
	if !b.removeMountIf("a", m1) {
		t.Fatal("removeMountIf failed to remove the matching mount")
	}
	if b.hasMount("a") {
		t.Fatal("mount still present after matching removeMountIf")
	}

	// Absent tag is a no-op.
	if b.removeMountIf("missing", m1) {
		t.Fatal("removeMountIf reported a removal for an absent tag")
	}
}

// TestVMOpLock checks the serialization granularity: one mutex per VM (so attach/
// detach for the same VM serialize) and distinct mutexes across VMs (so different
// VMs proceed concurrently).
func TestVMOpLock(t *testing.T) {
	b := newTestBroker()
	vm0a := b.vmOpLock("vm0")
	vm0b := b.vmOpLock("vm0")
	vm1 := b.vmOpLock("vm1")
	if vm0a != vm0b {
		t.Fatal("vmOpLock returned different mutexes for the same VM")
	}
	if vm0a == vm1 {
		t.Fatal("vmOpLock shared a mutex across different VMs")
	}
}
