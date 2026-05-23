package vmm

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"reflect"
	"testing"

	"github.com/jlagedo/atelier/services/internal/netjail"
	"github.com/jlagedo/atelier/services/internal/vsock"
)

func testManager(d *fakeDriver) *Manager {
	return NewManagerWithDriver(slog.New(slog.NewTextHandler(io.Discard, nil)), netjail.NewAllowlist(nil), d)
}

func TestManagerCreateStoresOnlyAfterDriverSuccessAndRejectsDuplicate(t *testing.T) {
	boom := errors.New("boom")
	d := &fakeDriver{createErr: boom}
	m := testManager(d)

	cfg := VMConfig{ID: "vm0", KernelPath: "kernel", InitrdPath: "initrd", RootFSPath: "rootfs.vhd", MemoryMB: 1024, CPUCount: 1}
	if err := m.Create(context.Background(), cfg); !errors.Is(err, boom) {
		t.Fatalf("Create error = %v, want %v", err, boom)
	}
	if got := m.Count(); got != 0 {
		t.Fatalf("Count after failed create = %d, want 0", got)
	}

	d.createErr = nil
	if err := m.Create(context.Background(), cfg); err != nil {
		t.Fatalf("Create success: %v", err)
	}
	if got := m.Count(); got != 1 {
		t.Fatalf("Count after create = %d, want 1", got)
	}
	if !reflect.DeepEqual(d.created[1], cfg) {
		t.Fatalf("driver saw cfg %+v, want %+v", d.created[1], cfg)
	}

	if err := m.Create(context.Background(), cfg); err == nil {
		t.Fatal("duplicate Create error = nil, want error")
	}
	if got := len(d.created); got != 2 {
		t.Fatalf("driver create calls = %d, want 2 (failed + first success only)", got)
	}
}

func TestManagerStartCallsLifecycleThenEgressAndToleratesEgressFailure(t *testing.T) {
	boom := errors.New("egress down")
	d := &fakeDriver{egressErr: boom}
	m := testManager(d)
	if err := m.Create(context.Background(), VMConfig{ID: "vm0"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := m.Start(context.Background(), "vm0"); err != nil {
		t.Fatalf("Start with egress failure should still succeed: %v", err)
	}

	want := []string{"create:vm0", "start:vm0", "egress:vm0"}
	if !reflect.DeepEqual(d.calls, want) {
		t.Fatalf("calls = %#v, want %#v", d.calls, want)
	}
}

func TestManagerStopClosesEgressAndRemovesVM(t *testing.T) {
	closer := &fakeCloser{}
	d := &fakeDriver{egressCloser: closer}
	m := testManager(d)
	if err := m.Create(context.Background(), VMConfig{ID: "vm0"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := m.Start(context.Background(), "vm0"); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err := m.Stop(context.Background(), "vm0"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if !closer.closed {
		t.Fatal("egress closer was not closed")
	}
	if got := m.Count(); got != 0 {
		t.Fatalf("Count after stop = %d, want 0", got)
	}
}

func TestManagerAttachDetachWorkspaceForwardsShareShape(t *testing.T) {
	d := &fakeDriver{}
	m := testManager(d)
	if err := m.Create(context.Background(), VMConfig{ID: "vm0"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := m.AttachWorkspace(context.Background(), "vm0", "/host/work", true, "s1", 601); err != nil {
		t.Fatalf("AttachWorkspace: %v", err)
	}
	if got, want := d.attached[0], (WorkspaceShare{HostPath: "/host/work", ReadOnly: true, Tag: "s1", Port: 601}); got != want {
		t.Fatalf("attached share = %+v, want %+v", got, want)
	}

	if err := m.DetachWorkspace(context.Background(), "vm0", "s1", 601); err != nil {
		t.Fatalf("DetachWorkspace: %v", err)
	}
	if got, want := d.detached[0], (WorkspaceShare{Tag: "s1", Port: 601}); got != want {
		t.Fatalf("detached share = %+v, want %+v", got, want)
	}
}

func TestManagerDialGuestRejectsUnknownVMAndUsesGuestRPCPort(t *testing.T) {
	d := &fakeDriver{}
	m := testManager(d)

	if _, err := m.DialGuest(context.Background(), "missing"); err == nil {
		t.Fatal("DialGuest missing VM error = nil, want error")
	}
	if len(d.dialPorts) != 0 {
		t.Fatalf("driver dial calls for missing VM = %d, want 0", len(d.dialPorts))
	}

	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	d.dialConn = client
	if err := m.Create(context.Background(), VMConfig{ID: "vm0"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	conn, err := m.DialGuest(context.Background(), "vm0")
	if err != nil {
		t.Fatalf("DialGuest: %v", err)
	}
	if conn != client {
		t.Fatal("DialGuest returned a different connection than the driver provided")
	}
	if got := d.dialPorts[0]; got != vsock.GuestRPCPort {
		t.Fatalf("dial port = %d, want %d", got, vsock.GuestRPCPort)
	}
}

type fakeDriver struct {
	calls []string

	createErr error
	startErr  error
	stopErr   error
	egressErr error

	egressCloser *fakeCloser
	dialConn     net.Conn
	dialErr      error

	created   []VMConfig
	attached  []WorkspaceShare
	detached  []WorkspaceShare
	dialPorts []uint32
}

func (d *fakeDriver) Create(_ context.Context, cfg VMConfig) error {
	d.calls = append(d.calls, "create:"+cfg.ID)
	d.created = append(d.created, cfg)
	return d.createErr
}

func (d *fakeDriver) Start(_ context.Context, id string) error {
	d.calls = append(d.calls, "start:"+id)
	return d.startErr
}

func (d *fakeDriver) Stop(_ context.Context, id string) error {
	d.calls = append(d.calls, "stop:"+id)
	return d.stopErr
}

func (d *fakeDriver) DialGuest(_ context.Context, id string, port uint32) (net.Conn, error) {
	d.calls = append(d.calls, "dial:"+id)
	d.dialPorts = append(d.dialPorts, port)
	if d.dialErr != nil {
		return nil, d.dialErr
	}
	if d.dialConn != nil {
		return d.dialConn, nil
	}
	c1, c2 := net.Pipe()
	_ = c2.Close()
	return c1, nil
}

func (d *fakeDriver) AttachWorkspace(_ context.Context, id string, share WorkspaceShare) error {
	d.calls = append(d.calls, "attach:"+id)
	d.attached = append(d.attached, share)
	return nil
}

func (d *fakeDriver) DetachWorkspace(_ context.Context, id string, share WorkspaceShare) error {
	d.calls = append(d.calls, "detach:"+id)
	d.detached = append(d.detached, share)
	return nil
}

func (d *fakeDriver) StartEgress(_ context.Context, id string, _ *netjail.Allowlist) (io.Closer, error) {
	d.calls = append(d.calls, "egress:"+id)
	if d.egressErr != nil {
		return nil, d.egressErr
	}
	if d.egressCloser == nil {
		d.egressCloser = &fakeCloser{}
	}
	return d.egressCloser, nil
}

type fakeCloser struct {
	closed bool
}

func (c *fakeCloser) Close() error {
	c.closed = true
	return nil
}
