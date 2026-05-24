//go:build linux

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/jlagedo/atelier/services/internal/vsock"
)

// vmaddrCIDHost is the well-known vsock CID of the host (VMADDR_CID_HOST). The
// guest dials it to reach the host's Plan9/9p server for a workspace share.
const vmaddrCIDHost = 2

// mountShare mounts a host workspace share at target (the Files door, S3.1 / S6 / S7).
//
// The same guestd binary boots under two hypervisors with different file-sharing
// primitives, so the transport is chosen at runtime, not at build time: if this kernel
// has virtio-fs (Apple Virtualization.framework) we mount the one virtio-fs device
// (mountVirtiofsShare); otherwise we fall back to the 9p-over-vsock path (Hyper-V/HCS).
// The broker passes the same {port, tag, target} either way — port is used only by the
// 9p path; the virtio-fs path uses the single device tag, not the per-session tag.
func mountShare(port uint32, tag, target string) error {
	if err := validShareTag(tag); err != nil {
		return err
	}

	if virtiofsSupported() {
		if err := mountVirtiofsShare(target); err == nil {
			return nil
		}
		// Fall through to 9p: this kernel has virtiofs in /proc/filesystems but no
		// device for the workspace tag (e.g. a Hyper-V guest served over 9p).
	}

	// MkdirAll: a per-session target like /sessions/<id> needs its parent created
	// too (S6.1), and an existing dir is fine. The virtio-fs path mkdirs its own
	// mountpoint (the base or the legacy target), so this is only for 9p.
	if err := os.MkdirAll(target, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", target, err)
	}
	return mount9pShare(port, tag, target)
}

// mountVirtiofsShare mounts the single host virtio-fs device for a workspace share (S6/S7).
//
// The device carries ONE config-time tag (vsock.WorkspaceShareTag), so that — not the
// per-session share tag — is always the mount source. The share's shape decides where the
// files land:
//
//   - Legacy single workspace (root-level target like /workspace): the device's
//     SingleDirectoryShare puts the directory at the device root, so we mount the device
//     directly at the target (the S6 shape).
//   - Per-session (target /sessions/<tag>): the device's MultipleDirectoryShare exposes each
//     session as a named subdir, so we mount the device ONCE at the base (/sessions) and the
//     subdir <tag> appears on its own — the broker adds the host share (SetShare) before
//     calling us. A second session finds the base already mounted and no-ops, because S7
//     verified a share added after the base is mounted is visible with no remount.
func mountVirtiofsShare(target string) error {
	base := filepath.Dir(target)
	if base == "/" {
		// Legacy: mount the device at the target itself.
		if err := os.MkdirAll(target, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", target, err)
		}
		return unix.Mount(vsock.WorkspaceShareTag, target, "virtiofs", 0, "")
	}
	// Per-session: mount the device once at the base; <base>/<tag> appears from the share.
	if err := os.MkdirAll(base, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", base, err)
	}
	mounted, err := isVirtiofsMount(base)
	if err != nil {
		return err
	}
	if mounted {
		return nil // base already mounted; the new subdir is already visible
	}
	return unix.Mount(vsock.WorkspaceShareTag, base, "virtiofs", 0, "")
}

// mount9pShare mounts a host Plan9/9p share at target over a connected vsock fd.
//
// HCS serves the share over hvsock, so we dial AF_VSOCK to the host on the share's port,
// then mount 9p over that connected socket with trans=fd (the kernel's 9p client has no
// hvsock transport, so it rides our fd). This mirrors hcsshim's guest-side plan9.Mount.
// A shell can't pass an fd to mount(2), which is why this lives in guestd.
func mount9pShare(port uint32, tag, target string) error {
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM, 0)
	if err != nil {
		return fmt.Errorf("vsock socket: %w", err)
	}
	if err := unix.Connect(fd, &unix.SockaddrVM{CID: vmaddrCIDHost, Port: port}); err != nil {
		unix.Close(fd)
		return fmt.Errorf("vsock connect port %d: %w", port, err)
	}

	const msize = 65536
	data := fmt.Sprintf("trans=fd,rfdno=%d,wfdno=%d,msize=%d,version=9p2000.L,aname=%s",
		fd, fd, msize, tag)
	if err := unix.Mount(target, target, "9p", 0, data); err != nil {
		unix.Close(fd)
		return fmt.Errorf("mount 9p at %s: %w", target, err)
	}
	// The mount took its own reference to the fd; ours is no longer needed.
	unix.Close(fd)
	return nil
}

// virtiofsSupported reports whether this kernel can mount virtio-fs (the macOS files
// door, S6). image/guest/init.sh modprobes virtiofs at boot; under Hyper-V it is absent
// and the 9p path is used instead.
func virtiofsSupported() bool {
	data, err := os.ReadFile("/proc/filesystems")
	if err != nil {
		return false
	}
	return strings.Contains(string(data), "virtiofs")
}

// isVirtiofsMount reports whether dir is already a virtio-fs mountpoint, so a second
// per-session attach doesn't re-mount the shared base (S7: a share added after the base is
// mounted is visible without a remount). It scans /proc/mounts — field[1] is the mountpoint
// (octal-escaped), field[2] the fstype.
func isVirtiofsMount(dir string) (bool, error) {
	data, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return false, fmt.Errorf("read /proc/mounts: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		if unescapeMount(fields[1]) == dir && fields[2] == "virtiofs" {
			return true, nil
		}
	}
	return false, nil
}

// unescapeMount decodes the octal escapes (\040 space, \011 tab, \012 newline, \134
// backslash) that /proc/mounts uses for special characters in a mountpoint path.
func unescapeMount(s string) string {
	if !strings.ContainsRune(s, '\\') {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+4 <= len(s) {
			if v, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(v))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// validShareTag rejects a tag that isn't a safe virtio-fs mount tag / 9p aname (it also
// becomes a directory name under a MultipleDirectoryShare). The host driver validates the
// same way; guestd trusts only the broker, but the check is cheap defense in depth.
func validShareTag(tag string) error {
	if tag == "" || len(tag) >= 36 {
		return fmt.Errorf("invalid share tag %q", tag)
	}
	for i := 0; i < len(tag); i++ {
		c := tag[i]
		ok := c == '-' || c == '_' || c == '.' ||
			(c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
		if !ok {
			return fmt.Errorf("invalid share tag %q", tag)
		}
	}
	return nil
}

// unmountShare unmounts a previously-mounted share at target. Under virtio-fs a per-session
// share is a subdir of the single device mounted at the base (S7), not its own mountpoint:
// dropping the host share (broker DetachWorkspace → SetShare) makes the subdir's contents
// vanish on their own, and the base mount stays for any other live sessions (it is torn down
// with the VM). So the guest does nothing for a per-session virtio-fs detach. The legacy
// /workspace share and every 9p share are real mountpoints and are unmounted normally.
func unmountShare(target string) error {
	if virtiofsSupported() && filepath.Dir(target) != "/" {
		return nil
	}
	if err := unix.Unmount(target, 0); err != nil {
		return fmt.Errorf("unmount %s: %w", target, err)
	}
	return nil
}
