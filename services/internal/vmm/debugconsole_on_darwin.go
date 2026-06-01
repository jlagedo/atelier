//go:build darwin && debugconsole

package vmm

import (
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"

	vz "github.com/Code-Hex/vz/v3"
)

// This file is compiled ONLY into debug builds (-tags debugconsole; build-all sets it for
// --config=debug). It adds an interactive root shell into the VM over a SECOND virtio
// console (hvc1), for debugging the guest OS itself (kernel, init.sh, mounts, runner). The
// shell runs as root OUTSIDE the bwrap/Landlock/seccomp cage, so it MUST NOT exist in a
// release image — the gate is the build tag, not a runtime flag: release builds compile
// debugconsole_off_darwin.go instead, where the seam is inert and this code is absent.

// debugCmdLineToken is appended to the kernel cmdline in debug builds. init.sh keys the
// guest debug shell off it — the broker→guest handshake that hvc1 was actually wired — so a
// debug rootfs booted by a release broker (no hvc1) never tries to open a missing device.
const debugCmdLineToken = "atelier.debug=1"

// debugConsoleToken is the seam the always-compiled driver consults for the cmdline. In a
// debug build the console is always on, so this always returns the token.
func debugConsoleToken() string { return debugCmdLineToken }

// newDebugConsolePort is the seam the always-compiled driver calls to add the hvc1 device.
// In a debug build it always builds the console + its host socket and returns the closer +
// the serial-port config to append to the VM's serial ports.
func newDebugConsolePort(id string, log *slog.Logger) (io.Closer, *vz.VirtioConsoleDeviceSerialPortConfiguration, error) {
	dc, cfg, err := newDarwinDebugConsole(debugSockPath(id), log)
	if err != nil {
		return nil, nil, err
	}
	log.Warn("debug console enabled (dev-only, cage-bypassing root shell)", "sock", debugSockPath(id))
	return dc, cfg, nil
}

// debugSockPath is the unix socket the debug-console client (atelierctl console) dials,
// deterministic from the VM id so the client can compute it from -id alone.
func debugSockPath(id string) string {
	return filepath.Join(os.TempDir(), "atelier-console-"+id+".sock")
}

// darwinDebugConsole is the host end of the hvc1 console, bridged to a unix-socket listener
// the operator attaches to (atelierctl console) rather than scanned into the log like the
// hvc0 boot console (darwinConsole). The same four pipes back the VZ attachment.
type darwinDebugConsole struct {
	inR, inW   *os.File
	outR, outW *os.File
	ln         net.Listener
	sockPath   string

	mu   sync.Mutex
	conn net.Conn // the single attached client, if any
}

// newDarwinDebugConsole creates the console pipes + VZ attachment and opens a unix socket
// that an interactive client dials. A stale socket file from a crashed prior run is removed
// first so Listen does not fail with "address already in use".
func newDarwinDebugConsole(sockPath string, log *slog.Logger) (*darwinDebugConsole, *vz.VirtioConsoleDeviceSerialPortConfiguration, error) {
	inR, inW, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		_ = inR.Close()
		_ = inW.Close()
		return nil, nil, err
	}

	att, err := vz.NewFileHandleSerialPortAttachment(inR, outW)
	if err != nil {
		closeAll(inR, inW, outR, outW)
		return nil, nil, err
	}
	cfg, err := vz.NewVirtioConsoleDeviceSerialPortConfiguration(att)
	if err != nil {
		closeAll(inR, inW, outR, outW)
		return nil, nil, err
	}

	_ = os.Remove(sockPath)
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		closeAll(inR, inW, outR, outW)
		return nil, nil, err
	}

	c := &darwinDebugConsole{inR: inR, inW: inW, outR: outR, outW: outW, ln: ln, sockPath: sockPath}
	go c.fanOutput() // guest -> current client (one long-lived reader, decoupled from attach)
	go c.serve(log)  // client -> guest (one attach at a time)
	return c, cfg, nil
}

// fanOutput reads the guest's console output forever and writes it to whichever client is
// currently attached (best-effort; bytes are dropped while nobody is attached — the boot log
// still goes to hvc0 regardless). Decoupling this from the per-client accept loop means a
// detach never blocks on a quiet guest, so reattaching is immediate.
func (c *darwinDebugConsole) fanOutput() {
	buf := make([]byte, 4096)
	for {
		n, err := c.outR.Read(buf)
		if n > 0 {
			c.mu.Lock()
			if c.conn != nil {
				_, _ = c.conn.Write(buf[:n])
			}
			c.mu.Unlock()
		}
		if err != nil {
			return // outR closed (Close)
		}
	}
}

// serve accepts one interactive client at a time and pumps its bytes to inW (the guest's
// console input) until it disconnects. A second dial replaces the first.
func (c *darwinDebugConsole) serve(log *slog.Logger) {
	for {
		conn, err := c.ln.Accept()
		if err != nil {
			return // listener closed (Close)
		}
		c.mu.Lock()
		if c.conn != nil {
			_ = c.conn.Close()
		}
		c.conn = conn
		c.mu.Unlock()
		log.Info("debug console attached", "sock", c.sockPath)

		_, _ = io.Copy(c.inW, conn) // client -> guest; blocks until this client disconnects

		c.mu.Lock()
		if c.conn == conn {
			c.conn = nil
		}
		c.mu.Unlock()
		_ = conn.Close()
		log.Info("debug console detached", "sock", c.sockPath)
	}
}

// Close stops the listener, drops any attached client, removes the socket file, and tears
// down the pipes. Call only after the VM has stopped writing the console.
func (c *darwinDebugConsole) Close() error {
	_ = c.ln.Close()
	c.mu.Lock()
	if c.conn != nil {
		_ = c.conn.Close()
	}
	c.mu.Unlock()
	_ = os.Remove(c.sockPath)
	_ = c.inW.Close()
	_ = c.inR.Close()
	_ = c.outW.Close()
	return c.outR.Close()
}

func closeAll(files ...*os.File) {
	for _, f := range files {
		_ = f.Close()
	}
}
