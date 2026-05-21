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

// WorkspacePlan9Port is the vsock port the host's Plan9/9p server for the
// /workspace share listens on (design.md §8, §10 Files door — S3.1). It is set
// by the compute-system doc (Plan9Share.Port) on the host and dialed by the
// guest, which mounts the share with `trans=fd` over the resulting socket. The
// value (564, the IANA 9p port) matches hcsshim's LCOW convention; both ends
// import this so they can never drift.
const WorkspacePlan9Port uint32 = 564

// WorkspaceShareTag is the 9p share name: the host doc's Plan9Share AccessName
// and the `aname=` the guest passes when mounting. Both ends share it.
const WorkspaceShareTag = "workspace"
