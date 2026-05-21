// Package netjail is the Network door (design.md §10, §8 Hop 3 channel 2 — S4.1):
// the guest has no real NIC, so the host runs a user-mode TCP/IP stack
// (gvisor-tap-vsock) over hvsock and IS the guest's entire network. This file is
// the egress policy: a runtime-settable, default-deny allowlist that the gvisor
// DNS + TCP/UDP forwarder consult so the guest can reach ONLY allowlisted
// destinations, every decision audited at the privileged boundary.
package netjail

import (
	"context"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"
)

// pinTTL is how long an IP stays reachable after an allowlisted DNS lookup
// returned it. Long enough for a normal connect-after-resolve, short enough that
// a tightened policy takes effect quickly.
const pinTTL = 5 * time.Minute

// Allowlist is the egress policy (design.md §10 Network). It enforces a hostname
// allowlist via DNS-pinning: Resolve answers DNS ONLY for allowlisted names and
// records (pins) the IPs it hands out; AllowIP then permits an outbound
// connection ONLY to a pinned IP. A destination reached by raw IP (no allowlisted
// lookup) is therefore never pinned and is denied — closing the direct-IP escape
// that a name-only or DNS-only allowlist would leave open. Empty allowlist =
// deny everything (fail-closed). It satisfies the gvisor-tap-vsock EgressFilter
// hook (Resolve + AllowIP) and is safe for concurrent use.
type Allowlist struct {
	log *slog.Logger

	// lookup resolves a hostname to IPs (overridable in tests). Defaults to the
	// host's real resolver, so CDN names (pypi/Fastly) with rotating IPs work.
	lookup func(ctx context.Context, name string) ([]net.IP, error)
	// now is the clock (overridable in tests) for pin expiry.
	now func() time.Time

	mu     sync.Mutex
	allow  []string             // allowed name suffixes, lowercased (empty = deny all)
	pinned map[string]time.Time // ip string -> expiry
}

// NewAllowlist returns a default-deny allowlist (nothing reachable until Set).
func NewAllowlist(log *slog.Logger) *Allowlist {
	if log == nil {
		log = slog.Default()
	}
	return &Allowlist{
		log:    log,
		lookup: defaultLookup,
		now:    time.Now,
		pinned: make(map[string]time.Time),
	}
}

func defaultLookup(ctx context.Context, name string) ([]net.IP, error) {
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, name)
	if err != nil {
		return nil, err
	}
	ips := make([]net.IP, 0, len(addrs))
	for _, a := range addrs {
		ips = append(ips, a.IP)
	}
	return ips, nil
}

// Set replaces the allowlist with names (host suffixes; "" entries ignored) and
// drops all pins so a tightened policy takes effect at once — names removed from
// the policy stop resolving immediately, and their IPs re-pin only on a fresh
// allowlisted lookup. An empty/nil list closes the door (deny all).
func (a *Allowlist) Set(names []string) {
	norm := make([]string, 0, len(names))
	for _, n := range names {
		n = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(n)), ".")
		if n != "" {
			norm = append(norm, n)
		}
	}
	a.mu.Lock()
	a.allow = norm
	a.pinned = make(map[string]time.Time)
	a.mu.Unlock()
	a.log.Info("egress policy set", "door", "network", "allow", norm)
}

// allowed reports whether name (already lowercased, no trailing dot) is the
// allowlist or a subdomain of an allowlisted suffix.
func (a *Allowlist) allowed(name string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, s := range a.allow {
		if name == s || strings.HasSuffix(name, "."+s) {
			return true
		}
	}
	return false
}

// Resolve answers a guest DNS query (the gvisor DNS server's hook). For an
// allowlisted name it resolves via the host resolver, pins every returned IP, and
// returns (ips, true); for anything else it returns (nil, false) so the DNS
// server replies NXDOMAIN. Audited either way (door=network).
func (a *Allowlist) Resolve(name string) ([]net.IP, bool) {
	q := strings.TrimSuffix(strings.ToLower(name), ".")
	if !a.allowed(q) {
		a.log.Warn("egress resolve denied", "door", "network", "decision", "deny", "op", "resolve", "host", q)
		return nil, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ips, err := a.lookup(ctx, q)
	if err != nil {
		// Allowlisted but unresolvable right now: ok=true (not NXDOMAIN) with no
		// records, so the client can retry rather than cache a negative.
		a.log.Warn("egress resolve failed", "door", "network", "decision", "allow", "op", "resolve", "host", q, "err", err)
		return nil, true
	}
	a.pin(ips)
	a.log.Info("egress resolve allowed", "door", "network", "decision", "allow", "op", "resolve", "host", q, "ips", ipsString(ips))
	return ips, true
}

// AllowIP reports whether an outbound connection to ip:port is permitted (the
// gvisor forwarder's hook). Permitted only if ip was pinned by a recent
// allowlisted lookup. Audited (door=network).
func (a *Allowlist) AllowIP(ip net.IP, port uint16) bool {
	if a.isPinned(ip) {
		a.log.Info("egress connect allowed", "door", "network", "decision", "allow", "op", "connect", "ip", ip.String(), "port", port)
		return true
	}
	a.log.Warn("egress connect denied", "door", "network", "decision", "deny", "op", "connect", "ip", ip.String(), "port", port)
	return false
}

func (a *Allowlist) pin(ips []net.IP) {
	exp := a.now().Add(pinTTL)
	a.mu.Lock()
	for _, ip := range ips {
		a.pinned[ip.String()] = exp
	}
	a.mu.Unlock()
}

func (a *Allowlist) isPinned(ip net.IP) bool {
	key := ip.String()
	a.mu.Lock()
	defer a.mu.Unlock()
	exp, ok := a.pinned[key]
	if !ok {
		return false
	}
	if a.now().After(exp) {
		delete(a.pinned, key)
		return false
	}
	return true
}

func ipsString(ips []net.IP) string {
	parts := make([]string, len(ips))
	for i, ip := range ips {
		parts[i] = ip.String()
	}
	return strings.Join(parts, ",")
}
