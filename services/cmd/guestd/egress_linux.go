//go:build linux

package main

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
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
	url := fmt.Sprintf("vsock://2:%d/connect", vsock.EgressLinkPort) // CID 2 = host
	go func() {
		for {
			cmd := exec.Command("/usr/sbin/gvforwarder", "-url", url, "-iface", "tap0", "-mtu", "1500")
			cmd.Stdout = os.Stderr
			cmd.Stderr = os.Stderr
			log.Info("starting gvforwarder", "url", url)
			err := cmd.Run()
			log.Warn("gvforwarder exited — restarting", "err", err)
			time.Sleep(2 * time.Second)
		}
	}()
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
