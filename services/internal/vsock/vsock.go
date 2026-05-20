// Package vsock owns the guest↔host control-plane socket (design.md §8, Hop 3).
// The guest daemon (cmd/guestd) binds AF_VSOCK and listens on GuestRPCPort; the
// host (S2.2) connects over AF_HYPERV. Both sides import this package for the one
// shared port number, so they can never drift.
package vsock

// GuestRPCPort is the vsock port guestd's JSON-RPC server listens on. The value
// is arbitrary: our boot carries no GCS/vsockexec tail (design.md §16), so no
// Microsoft service occupies a fixed port we'd clash with.
//
// Hyper-V maps a vsock port to an AF_HYPERV service GUID via the Linux
// integration-services template "<port-as-8-hex>-facb-11e6-bd58-64006a7986d3";
// for 5000 that is 00001388-facb-11e6-bd58-64006a7986d3. The S2.2 host client
// derives it with go-winio's hvsock.VsockServiceID(GuestRPCPort).
const GuestRPCPort uint32 = 5000
