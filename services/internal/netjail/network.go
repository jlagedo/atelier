package netjail

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"

	gvdhcp "github.com/containers/gvisor-tap-vsock/pkg/services/dhcp"
	gvdns "github.com/containers/gvisor-tap-vsock/pkg/services/dns"
	gvtap "github.com/containers/gvisor-tap-vsock/pkg/tap"
	"github.com/containers/gvisor-tap-vsock/pkg/transport"
	gvtypes "github.com/containers/gvisor-tap-vsock/pkg/types"
	"github.com/inetaf/tcpproxy"
	logrus "github.com/sirupsen/logrus"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/network/arp"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"
	"gvisor.dev/gvisor/pkg/waiter"

	"github.com/jlagedo/atelier/services/internal/vsock"
)

// Network is the per-VM host-side user-mode network (design.md §8 Hop 3 channel
// 2, §10 Network door — S4.1). gvisor-tap-vsock runs a full TCP/IP stack on the
// host (DHCP/DNS/forwarding) and serves it over an AF_HYPERV listener; the guest
// has NO real NIC — its gvforwarder dials this listener over vsock and bridges a
// tap device to it, so the host IS the guest's entire network.
//
// We compose gvisor-tap-vsock's building blocks rather than its all-in-one
// virtualnetwork.New, because the egress jail must own two seams the library
// doesn't expose a hook for: the DNS resolver (resolve only allowlisted names and
// pin their IPs) and the TCP forwarder (dial only pinned IPs). Both consult the
// Allowlist (default-deny), so the jail is enforced in our code at the privileged
// boundary — the canonical gVisor pattern (decide in the forwarder handler) plus
// DNS restriction (Cowork's model). UDP egress is dropped entirely (only the
// gateway's bound DHCP/DNS endpoints answer); ICMP is not forwarded.
type Network struct {
	ln  net.Listener
	log *slog.Logger
}

// Network parameters mirror gvisor-tap-vsock's canonical defaults (gvproxy): the
// guest gets 192.168.127.2 (its gvforwarder MAC has a static lease) and
// routes/resolves via the gateway .1. We deliberately omit NAT/host-virtual-IPs
// so the guest cannot reach host loopback services — egress only, jailed.
// Network parameters live in internal/vsock now (shared with the guest's static
// bring-up in cmd/guestd, so the two halves can never drift). Aliased here as consts so
// the rest of this file reads unchanged.
const (
	netSubnet     = vsock.NetworkCIDR
	netGatewayIP  = vsock.GatewayIP
	netGatewayMA  = vsock.GatewayMAC
	guestStaticIP = vsock.GuestStaticIP
	guestMAC      = vsock.GuestMAC // gvforwarder's default MAC
	netMTU        = vsock.NetworkMTU
	dnsPort       = 53
)

// Start brings up the host user-mode network and its AF_HYPERV listener on
// vsock.EgressLinkPort, ready for the guest's gvforwarder to dial. filter is the
// egress allowlist the DNS resolver and TCP forwarder consult (default-deny);
// nil means deny everything.
func Start(log *slog.Logger, filter *Allowlist) (*Network, error) {
	if log == nil {
		log = slog.Default()
	}
	if filter == nil {
		filter = NewAllowlist(log)
	}
	// Keep the library's global logrus from flooding our audit stream.
	logrus.SetLevel(logrus.WarnLevel)

	_, subnet, err := net.ParseCIDR(netSubnet)
	if err != nil {
		return nil, fmt.Errorf("netjail: subnet: %w", err)
	}

	// Link layer: the tap endpoint <-> switch the guest's gvforwarder attaches to.
	ipPool := gvtap.NewIPPool(subnet)
	ipPool.Reserve(net.ParseIP(netGatewayIP), netGatewayMA)
	ipPool.Reserve(net.ParseIP(guestStaticIP), guestMAC)

	tapEndpoint, err := gvtap.NewLinkEndpoint(false, netMTU, netGatewayMA, netGatewayIP, nil)
	if err != nil {
		return nil, fmt.Errorf("netjail: link endpoint: %w", err)
	}
	sw := gvtap.NewSwitch(false)
	tapEndpoint.Connect(sw)
	sw.Connect(tapEndpoint)

	s, err := newStack(subnet, tapEndpoint)
	if err != nil {
		return nil, err
	}

	// The egress jail: our TCP forwarder dials only allowlisted (pinned) IPs.
	// No UDP forwarder is installed, so the only reachable UDP is the gateway's
	// bound DHCP/DNS endpoints — there is no general UDP egress.
	s.SetTransportProtocolHandler(tcp.ProtocolNumber, jailedTCPForwarder(s, filter).HandlePacket)

	if err := startDNS(log, s, filter); err != nil {
		return nil, err
	}
	if err := startDHCP(s, ipPool); err != nil {
		return nil, err
	}

	ln, err := transport.Listen(egressListenURL())
	if err != nil {
		return nil, fmt.Errorf("netjail: listen: %w", err)
	}

	// The guest POSTs /connect; we hijack the conn and pump ethernet frames.
	mux := http.NewServeMux()
	mux.HandleFunc(gvtypes.ConnectPath, func(w http.ResponseWriter, _ *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "no hijack", http.StatusInternalServerError)
			return
		}
		conn, bufrw, err := hj.Hijack()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer conn.Close()
		if err := bufrw.Flush(); err != nil {
			return
		}
		_ = sw.Accept(context.Background(), conn, gvtypes.HyperKitProtocol)
	})

	n := &Network{ln: ln, log: log}
	go func() {
		if err := http.Serve(ln, mux); err != nil && !errors.Is(err, net.ErrClosed) && !errors.Is(err, http.ErrServerClosed) {
			log.Warn("netjail: serve stopped", "err", err)
		}
	}()
	log.Info("egress network up", "door", "network", "vsockPort", vsock.EgressLinkPort, "subnet", netSubnet)
	return n, nil
}

// Close shuts the listener, which unblocks the serving goroutine.
func (n *Network) Close() error {
	if n == nil || n.ln == nil {
		return nil
	}
	return n.ln.Close()
}

// newStack builds the gVisor netstack (one NIC on the gateway address), mirroring
// gvisor-tap-vsock's createStack. Spoofing + promiscuous let the gateway answer
// for the whole subnet; the single route sends everything to NIC 1.
func newStack(subnet *net.IPNet, ep stack.LinkEndpoint) (*stack.Stack, error) {
	s := stack.New(stack.Options{
		NetworkProtocols:   []stack.NetworkProtocolFactory{ipv4.NewProtocol, arp.NewProtocol},
		TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol, udp.NewProtocol},
	})
	if err := s.CreateNIC(1, ep); err != nil {
		return nil, errors.New(err.String())
	}
	if err := s.AddProtocolAddress(1, tcpip.ProtocolAddress{
		Protocol:          ipv4.ProtocolNumber,
		AddressWithPrefix: tcpip.AddrFrom4Slice(net.ParseIP(netGatewayIP).To4()).WithPrefix(),
	}, stack.AddressProperties{}); err != nil {
		return nil, errors.New(err.String())
	}
	s.SetSpoofing(1, true)
	s.SetPromiscuousMode(1, true)

	sn, err := tcpip.NewSubnet(tcpip.AddrFromSlice(subnet.IP), tcpip.MaskFromBytes(subnet.Mask))
	if err != nil {
		return nil, fmt.Errorf("netjail: subnet: %w", err)
	}
	s.SetRouteTable([]tcpip.Route{{Destination: sn, NIC: 1}})
	return s, nil
}

// jailedTCPForwarder is the egress chokepoint (the canonical gVisor pattern): it
// inspects each outbound connection request and dials the destination ONLY if the
// allowlist permits the IP (i.e. an allowlisted DNS lookup pinned it). Otherwise
// it RST/drops. Adapted from gvisor-tap-vsock's forwarder.TCP with the allow
// check added and NAT/ec2 paths removed (we run neither).
func jailedTCPForwarder(s *stack.Stack, filter *Allowlist) *tcp.Forwarder {
	return tcp.NewForwarder(s, 0, 10, func(r *tcp.ForwarderRequest) {
		id := r.ID()
		dstIP := net.IP(id.LocalAddress.AsSlice())
		if !filter.AllowIP(dstIP, id.LocalPort) {
			r.Complete(true) // send RST: destination not allowlisted
			return
		}
		outbound, err := net.Dial("tcp", net.JoinHostPort(dstIP.String(), fmt.Sprint(id.LocalPort)))
		if err != nil {
			r.Complete(true)
			return
		}
		var wq waiter.Queue
		ep, tcpErr := r.CreateEndpoint(&wq)
		r.Complete(false)
		if tcpErr != nil {
			_ = outbound.Close()
			return
		}
		remote := tcpproxy.DialProxy{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) { return outbound, nil },
		}
		remote.HandleConn(gonet.NewTCPConn(&wq, ep))
	})
}

// startDNS runs the gateway's DNS server (gvisor-tap-vsock's dns package) with an
// upstream resolver wired to the allowlist: only allowlisted names resolve (and
// their IPs are pinned for the forwarder); everything else is NXDOMAIN. This is
// the domain-allowlist + DNS-restriction half of the jail (blocks DNS-tunnel
// exfil and direct discovery of new endpoints).
func startDNS(log *slog.Logger, s *stack.Stack, filter *Allowlist) error {
	gwAddr := tcpip.AddrFrom4Slice(net.ParseIP(netGatewayIP).To4())
	udpConn, err := gonet.DialUDP(s, &tcpip.FullAddress{NIC: 1, Addr: gwAddr, Port: dnsPort}, nil, ipv4.ProtocolNumber)
	if err != nil {
		return fmt.Errorf("netjail: dns udp: %w", err)
	}
	tcpLn, err := gonet.ListenTCP(s, tcpip.FullAddress{NIC: 1, Addr: gwAddr, Port: dnsPort}, ipv4.ProtocolNumber)
	if err != nil {
		return fmt.Errorf("netjail: dns tcp: %w", err)
	}
	server, err := gvdns.NewWithUpstreamResolver(udpConn, tcpLn, nil, &pinResolver{filter: filter})
	if err != nil {
		return fmt.Errorf("netjail: dns: %w", err)
	}
	go func() {
		if err := server.Serve(); err != nil {
			log.Warn("netjail: dns udp serve stopped", "err", err)
		}
	}()
	go func() {
		if err := server.ServeTCP(); err != nil {
			log.Warn("netjail: dns tcp serve stopped", "err", err)
		}
	}()
	return nil
}

// startDHCP runs the gateway's DHCP server so the guest's gvforwarder gets its
// address, default route, and DNS (= the gateway) automatically.
func startDHCP(s *stack.Stack, ipPool *gvtap.IPPool) error {
	cfg := &gvtypes.Configuration{Subnet: netSubnet, GatewayIP: netGatewayIP, MTU: netMTU}
	server, err := gvdhcp.New(cfg, s, ipPool)
	if err != nil {
		return fmt.Errorf("netjail: dhcp: %w", err)
	}
	go func() { _ = server.Serve() }()
	return nil
}

// pinResolver adapts the Allowlist to gvisor-tap-vsock's DNS upstream-resolver
// interface: A lookups go through the allowlist (resolve + pin, or NXDOMAIN);
// every other record type is refused to shrink the exfiltration surface.
type pinResolver struct{ filter *Allowlist }

var errBlocked = &net.DNSError{Err: "blocked by egress policy", IsNotFound: true}

func (p *pinResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	ips, ok := p.filter.Resolve(host)
	if !ok {
		return nil, errBlocked
	}
	out := make([]net.IPAddr, 0, len(ips))
	for _, ip := range ips {
		out = append(out, net.IPAddr{IP: ip})
	}
	return out, nil
}

func (p *pinResolver) LookupCNAME(context.Context, string) (string, error) { return "", errBlocked }
func (p *pinResolver) LookupMX(context.Context, string) ([]*net.MX, error) { return nil, errBlocked }
func (p *pinResolver) LookupNS(context.Context, string) ([]*net.NS, error) { return nil, errBlocked }
func (p *pinResolver) LookupSRV(context.Context, string, string, string) (string, []*net.SRV, error) {
	return "", nil, errBlocked
}
func (p *pinResolver) LookupTXT(context.Context, string) ([]string, error) { return nil, errBlocked }

// egressListenURL is the AF_HYPERV listen URL for gvisor-tap-vsock's Windows
// transport: the "vsock" scheme with the service GUID derived from the link port
// (Hyper-V's vsock template "<port-8hex>-FACB-11E6-BD58-64006A7986D3"). For port
// 1024 this equals transport.DefaultURL; we build it from the shared port const
// so host and guest can never drift.
func egressListenURL() string {
	return fmt.Sprintf("vsock://%08x-FACB-11E6-BD58-64006A7986D3", vsock.EgressLinkPort)
}
