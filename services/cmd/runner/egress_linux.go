//go:build linux

package main

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/jlagedo/atelier/services/internal/vsock"
)

// superviseEgress brings up the guest's only path to the network (design.md §8
// Hop 3 channel 2, §10 Network door — S4.1). The guest has no real NIC: the
// gvforwarder binary creates a tap device and bridges it over AF_VSOCK to the
// host's user-mode network (gvisor-tap-vsock), which DHCP-assigns the interface
// and forwards traffic subject to the host egress allowlist. We run it
// supervised (restart on exit) so it survives the host listener coming up after
// us and any transient drop. Egress is gated host-side (default-deny), so the
// link being up does not by itself grant the guest any reachability.
func superviseEgress(log *slog.Logger) {
	if err := ensureTun(); err != nil {
		log.Warn("tun setup failed — guest networking unavailable", "err", err)
		return
	}
	// Static guest networking (S4.1): the host reserves the guest a fixed lease for
	// guestMAC, so we configure tap0 ourselves (the image carries no DHCP client) and run
	// gvforwarder with -preexisting, which then only bridges the interface (it skips its
	// own linkUp()/dhcp()). Done once; tap0 is persistent and survives gvforwarder restarts.
	if err := configureTap0(log); err != nil {
		log.Warn("tap0 static config failed — guest networking may be unavailable", "err", err)
		// Fall through and still launch gvforwarder so the failure mode is visible on serial.
	}
	url := fmt.Sprintf("vsock://2:%d/connect", vsock.EgressLinkPort) // CID 2 = host
	go func() {
		for {
			cmd := exec.Command("/usr/sbin/gvforwarder",
				"-url", url, "-iface", "tap0",
				"-mtu", strconv.Itoa(vsock.NetworkMTU), "-preexisting")
			cmd.Stdout = os.Stderr
			cmd.Stderr = os.Stderr
			log.Info("starting gvforwarder", "url", url, "iface", "tap0", "mode", "preexisting")
			err := cmd.Run()
			log.Warn("gvforwarder exited — restarting", "err", err)
			time.Sleep(2 * time.Second)
		}
	}()
}

// configureTap0 creates and statically configures the guest's tap0 (S4.1): a persistent
// tap with the reserved MAC and IP, plus a default route via the gateway. All values come
// from internal/vsock so the guest and the host's user-mode network can't drift. No DHCP
// is used — the host hands a fixed lease for this MAC, so static config is exact. Steps
// are idempotent (a re-run tolerates "exists"/"busy") so this is safe to call again.
func configureTap0(log *slog.Logger) error {
	steps := [][]string{
		{"tuntap", "add", "dev", "tap0", "mode", "tap"},
		{"link", "set", "dev", "tap0", "address", vsock.GuestMAC},
		{"addr", "add", vsock.GuestStaticCIDR, "dev", "tap0"},
		{"link", "set", "dev", "tap0", "up"},
		{"route", "add", "default", "via", vsock.GatewayIP, "dev", "tap0"},
	}
	for _, args := range steps {
		out, err := exec.Command("ip", args...).CombinedOutput()
		if err != nil {
			msg := strings.TrimSpace(string(out))
			if strings.Contains(msg, "exists") || strings.Contains(msg, "busy") {
				log.Info("ip step already applied", "args", strings.Join(args, " "), "msg", msg)
				continue
			}
			return fmt.Errorf("ip %s: %w (%s)", strings.Join(args, " "), err, msg)
		}
	}
	log.Info("tap0 configured (static)", "ip", vsock.GuestStaticIP, "gw", vsock.GatewayIP, "mac", vsock.GuestMAC)
	return nil
}

// ensureTun loads the tun driver and makes sure /dev/net/tun exists (no udev in
// the guest, so create the node ourselves if devtmpfs didn't).
func ensureTun() error {
	_ = exec.Command("modprobe", "tun").Run() // may be built-in; ignore failure
	if _, err := os.Stat("/dev/net/tun"); err == nil {
		return nil
	}
	if err := os.MkdirAll("/dev/net", 0o755); err != nil {
		return err
	}
	// tun is char major 10, minor 200.
	if err := unix.Mknod("/dev/net/tun", unix.S_IFCHR|0o600, int(unix.Mkdev(10, 200))); err != nil && !os.IsExist(err) {
		return err
	}
	return nil
}
