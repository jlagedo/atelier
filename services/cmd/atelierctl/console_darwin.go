//go:build darwin && debugconsole

package main

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"os"

	"golang.org/x/sys/unix"
)

// detachByte is the client-side escape: Ctrl-] detaches without ending the guest shell
// (it just closes our socket; the guest's hvc1 stays open for the next attach).
const detachByte = 0x1d

// runConsole dials the VM's debug-console unix socket and bridges it to this terminal:
// the local TTY goes into raw mode so an interactive guest shell (job control, vi,
// arrows) works, and bytes flow both ways until the guest closes the console or the
// operator presses Ctrl-]. macOS only — the debug console is a VZ-only affordance.
func runConsole(sockPath string) error {
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return fmt.Errorf("dial %s: %w (is a debug-built VM running?)", sockPath, err)
	}
	defer func() { _ = conn.Close() }()

	if restore, err := makeRaw(int(os.Stdin.Fd())); err == nil {
		defer restore()
	}

	fmt.Fprintf(os.Stderr, "[console] attached to %s — press Ctrl-] to detach\r\n", sockPath)

	// terminal -> guest, with Ctrl-] as a local detach that never reaches the guest.
	go func() {
		buf := make([]byte, 4096)
		for {
			n, rerr := os.Stdin.Read(buf)
			if n > 0 {
				if i := bytes.IndexByte(buf[:n], detachByte); i >= 0 {
					_, _ = conn.Write(buf[:i])
					_ = conn.Close() // unblocks the guest->terminal copy below
					return
				}
				if _, werr := conn.Write(buf[:n]); werr != nil {
					return
				}
			}
			if rerr != nil {
				_ = conn.Close()
				return
			}
		}
	}()

	// guest -> terminal; returns when the guest detaches or Ctrl-] closed conn.
	_, _ = io.Copy(os.Stdout, conn)
	return nil
}

// makeRaw puts fd into cfmakeraw-style raw mode and returns a restore func. Implemented
// directly on x/sys/unix termios (darwin TIOCGETA/TIOCSETA) to avoid a golang.org/x/term
// dependency. A no-op (error) when fd is not a TTY (e.g. piped stdin).
func makeRaw(fd int) (func(), error) {
	old, err := unix.IoctlGetTermios(fd, unix.TIOCGETA)
	if err != nil {
		return nil, err
	}
	raw := *old
	raw.Iflag &^= unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP | unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON
	raw.Oflag &^= unix.OPOST
	raw.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN
	raw.Cflag &^= unix.CSIZE | unix.PARENB
	raw.Cflag |= unix.CS8
	raw.Cc[unix.VMIN] = 1
	raw.Cc[unix.VTIME] = 0
	if err := unix.IoctlSetTermios(fd, unix.TIOCSETA, &raw); err != nil {
		return nil, err
	}
	return func() { _ = unix.IoctlSetTermios(fd, unix.TIOCSETA, old) }, nil
}
