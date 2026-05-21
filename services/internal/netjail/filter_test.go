package netjail

import (
	"context"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"
)

func testAllowlist(t *testing.T) *Allowlist {
	t.Helper()
	a := NewAllowlist(slog.New(slog.NewTextHandler(io.Discard, nil)))
	// Deterministic fake resolver: <name> -> a synthetic IP, plus a couple of
	// fixed mappings so pinning is checkable.
	a.lookup = func(_ context.Context, name string) ([]net.IP, error) {
		switch name {
		case "pypi.org":
			return []net.IP{net.ParseIP("151.101.0.223")}, nil
		case "files.pythonhosted.org":
			return []net.IP{net.ParseIP("151.101.1.63"), net.ParseIP("151.101.65.63")}, nil
		default:
			return []net.IP{net.ParseIP("203.0.113.7")}, nil
		}
	}
	return a
}

func TestAllowlistDefaultDeny(t *testing.T) {
	a := testAllowlist(t)
	if _, ok := a.Resolve("pypi.org"); ok {
		t.Fatal("default-deny: Resolve should be NXDOMAIN (ok=false) before any policy is set")
	}
	if a.AllowIP(net.ParseIP("151.101.0.223"), 443) {
		t.Fatal("default-deny: AllowIP should be false before any policy is set")
	}
}

func TestAllowlistResolvePinThenAllow(t *testing.T) {
	a := testAllowlist(t)
	a.Set([]string{"pypi.org"})

	ips, ok := a.Resolve("pypi.org")
	if !ok || len(ips) != 1 {
		t.Fatalf("Resolve(pypi.org): ok=%v ips=%v, want ok=true with 1 ip", ok, ips)
	}
	if !a.AllowIP(net.ParseIP("151.101.0.223"), 443) {
		t.Fatal("a pinned IP must be allowed after an allowlisted lookup")
	}
	if a.AllowIP(net.ParseIP("8.8.8.8"), 443) {
		t.Fatal("an IP that was never pinned must be denied")
	}
}

func TestAllowlistSubdomainMatch(t *testing.T) {
	a := testAllowlist(t)
	a.Set([]string{"pythonhosted.org"})

	if _, ok := a.Resolve("files.pythonhosted.org"); !ok {
		t.Fatal("a subdomain of an allowlisted suffix must resolve")
	}
	// Lookalike that merely ends with the string (no dot boundary) must NOT match.
	if _, ok := a.Resolve("evilpythonhosted.org"); ok {
		t.Fatal("a non-subdomain lookalike must not match the suffix")
	}
}

func TestAllowlistDirectIPDenied(t *testing.T) {
	a := testAllowlist(t)
	a.Set([]string{"pypi.org"}) // policy set, but no DNS lookup happens
	// A raw-IP connection (no allowlisted lookup) is never pinned -> denied.
	if a.AllowIP(net.ParseIP("151.101.0.223"), 443) {
		t.Fatal("direct-IP egress must be denied (the IP was never pinned by a lookup)")
	}
}

func TestAllowlistNonAllowlistedResolveNXDOMAIN(t *testing.T) {
	a := testAllowlist(t)
	a.Set([]string{"pypi.org"})
	if _, ok := a.Resolve("evil.com"); ok {
		t.Fatal("a non-allowlisted name must return NXDOMAIN (ok=false)")
	}
}

func TestAllowlistSetClearsPins(t *testing.T) {
	a := testAllowlist(t)
	a.Set([]string{"pypi.org"})
	a.Resolve("pypi.org")
	if !a.AllowIP(net.ParseIP("151.101.0.223"), 443) {
		t.Fatal("precondition: pinned IP should be allowed")
	}
	a.Set([]string{"files.pythonhosted.org"}) // policy change drops old pins
	if a.AllowIP(net.ParseIP("151.101.0.223"), 443) {
		t.Fatal("changing the policy must drop stale pins")
	}
}

func TestAllowlistPinExpiry(t *testing.T) {
	a := testAllowlist(t)
	now := time.Unix(1_000_000, 0)
	a.now = func() time.Time { return now }
	a.Set([]string{"pypi.org"})
	a.Resolve("pypi.org")
	if !a.AllowIP(net.ParseIP("151.101.0.223"), 443) {
		t.Fatal("precondition: freshly pinned IP should be allowed")
	}
	now = now.Add(pinTTL + time.Second) // advance past the TTL
	if a.AllowIP(net.ParseIP("151.101.0.223"), 443) {
		t.Fatal("a pin must expire after pinTTL")
	}
}
