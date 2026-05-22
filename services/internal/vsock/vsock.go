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
// and the `aname=` the guest passes when mounting. Both ends share it. It is the
// default (legacy single-share) tag; concurrent per-session shares (S6.1) use
// distinct tags + ports allocated by the broker off the bases below.
const WorkspaceShareTag = "workspace"

// SessionPlan9PortBase is the first vsock port the broker hands out for
// concurrent per-session 9p shares (S6.1): slot N uses Base+N. It sits above the
// default workspace port (564) and below the egress link (1024), leaving room for
// many simultaneous session mounts in the one shared VM.
const SessionPlan9PortBase uint32 = 600

// EgressLinkPort is the vsock port carrying the guest's user-mode network link
// (design.md §8 Hop 3 channel 2, §10 Network door — S4.1). The guest's
// gvforwarder dials AF_VSOCK CID 2 (the host) on this port and POSTs /connect to
// the host's gvisor-tap-vsock virtualnetwork, which then serves as the guest's
// entire network (DHCP/DNS/forward + egress allowlist — all host-controlled). On
// the host the AF_HYPERV listener uses the service GUID derived from this port
// (winio.VsockServiceID(EgressLinkPort)); the guest uses the raw port. Both ends
// import it so they can never drift. 1024 is gvforwarder's default link port.
const EgressLinkPort uint32 = 1024

// Guest network parameters (design.md §10 Network door — S4.1). The host's user-mode
// network (internal/netjail) and the guest's static bring-up (cmd/guestd/egress_linux)
// both import these so they can never drift. Values mirror gvisor-tap-vsock's canonical
// gvproxy defaults: the gateway owns .1, the guest has a reserved static lease at .2, and
// there is no general DHCP/NAT (egress only, jailed). Networking is configured statically
// in the guest — guestd brings tap0 up with these values and runs gvforwarder with
// -preexisting (so no DHCP client is needed in the image).
const (
	// NetworkCIDR is the guest subnet in CIDR form (host side parses it for the netstack).
	NetworkCIDR = "192.168.127.0/24"
	// GatewayIP is the host-side gateway/router and the only DNS resolver address.
	GatewayIP = "192.168.127.1"
	// GatewayMAC is the gateway's link address (the host gvisor endpoint).
	GatewayMAC = "5a:94:ef:e4:0c:dd"
	// GuestStaticIP is the guest's reserved address on tap0.
	GuestStaticIP = "192.168.127.2"
	// GuestStaticCIDR is GuestStaticIP with the subnet prefix, for `ip addr add` on tap0.
	GuestStaticCIDR = "192.168.127.2/24"
	// GuestMAC is the guest tap0 MAC (gvforwarder's default; reserved host-side so the
	// static lease matches). guestd sets it explicitly since -preexisting skips linkUp.
	GuestMAC = "5a:94:ef:e4:0c:ee"
	// NetworkMTU is the link MTU for tap0 and the host endpoint.
	NetworkMTU = 1500
)
