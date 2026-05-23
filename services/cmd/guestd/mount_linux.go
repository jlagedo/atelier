//go:build linux

package main

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

// vmaddrCIDHost is the well-known vsock CID of the host (VMADDR_CID_HOST). The
// guest dials it to reach the host's Plan9/9p server for a workspace share.
const vmaddrCIDHost = 2

// mountShare mounts a host workspace share at target (the Files door, S3.1 / S6).
//
// The same guestd binary boots under two hypervisors with different file-sharing
// primitives, so the transport is chosen at runtime, not at build time: if this kernel
// has virtio-fs (Apple Virtualization.framework, S6) we mount `-t virtiofs <tag>`;
// otherwise we fall back to the 9p-over-vsock path (Hyper-V/HCS). The broker passes the
// same {port, tag, target} either way — port is used only by the 9p path.
func mountShare(port uint32, tag, target string) error {
	if err := validShareTag(tag); err != nil {
		return err
	}
	// MkdirAll: a per-session target like /sessions/<id> needs its parent created
	// too (S6.1), and an existing dir is fine.
	if err := os.MkdirAll(target, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", target, err)
	}

	if virtiofsSupported() {
		// The device tag is the virtio-fs mount tag; no options needed (read-only is
		// enforced host-side on the shared directory).
		if err := unix.Mount(tag, target, "virtiofs", 0, ""); err == nil {
			return nil
		}
		// Fall through to 9p: this kernel has virtiofs but no device for this tag
		// (e.g. a Hyper-V guest, where the host serves the share over 9p instead).
	}
	return mount9pShare(port, tag, target)
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

// unmountShare unmounts a previously-mounted share at target.
func unmountShare(target string) error {
	if err := unix.Unmount(target, 0); err != nil {
		return fmt.Errorf("unmount %s: %w", target, err)
	}
	return nil
}
