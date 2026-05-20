//go:build windows

package hcs

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

// winDriver drives utility VMs through the computecore.dll bindings, tracking
// the live system handle for each VM id.
type winDriver struct {
	mu      sync.Mutex
	systems map[string]hcsSystem
}

// New returns the Windows HCS driver.
func New() Driver {
	return &winDriver{systems: make(map[string]hcsSystem)}
}

// Create authors-then-realizes the compute system: doc is the JSON from
// MakeLCOWDoc. The VM exists but is not yet running after this returns.
func (d *winDriver) Create(_ context.Context, id string, doc []byte) error {
	system, err := hcsCreateComputeSystem(id, string(doc))
	if err != nil {
		return err
	}
	d.mu.Lock()
	d.systems[id] = system
	d.mu.Unlock()
	return nil
}

// Start boots the VM and blocks until start completes.
func (d *winDriver) Start(_ context.Context, id string) error {
	system, err := d.handle(id)
	if err != nil {
		return err
	}
	return hcsStartComputeSystem(system, "")
}

// Stop terminates the VM and releases its handle.
func (d *winDriver) Stop(_ context.Context, id string) error {
	system, err := d.handle(id)
	if err != nil {
		return err
	}
	termErr := hcsTerminateComputeSystem(system, "")
	closeErr := hcsCloseComputeSystem(system)

	d.mu.Lock()
	delete(d.systems, id)
	d.mu.Unlock()

	if termErr != nil {
		return termErr
	}
	return closeErr
}

// RuntimeID returns the compute system's RuntimeId GUID — the partition identity
// the host dials over hvsock (Hop 3). It is assigned by HCS and differs from the
// friendly id passed to Create.
func (d *winDriver) RuntimeID(_ context.Context, id string) (string, error) {
	system, err := d.handle(id)
	if err != nil {
		return "", err
	}
	doc, err := hcsGetComputeSystemProperties(system)
	if err != nil {
		return "", err
	}
	var props struct {
		RuntimeID string `json:"RuntimeId"`
	}
	if err := json.Unmarshal([]byte(doc), &props); err != nil {
		return "", fmt.Errorf("hcs: parse properties for %q: %w", id, err)
	}
	if props.RuntimeID == "" {
		return "", fmt.Errorf("hcs: no RuntimeId in properties for %q", id)
	}
	return props.RuntimeID, nil
}

// handle returns the tracked system handle for id, opening an existing system
// if we don't already hold one (e.g. after a broker restart).
func (d *winDriver) handle(id string) (hcsSystem, error) {
	d.mu.Lock()
	system, ok := d.systems[id]
	d.mu.Unlock()
	if ok {
		return system, nil
	}

	system, err := hcsOpenComputeSystem(id)
	if err != nil {
		return 0, err
	}
	d.mu.Lock()
	d.systems[id] = system
	d.mu.Unlock()
	return system, nil
}
