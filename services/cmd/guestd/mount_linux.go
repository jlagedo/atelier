//go:build linux

package main

import (
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

// vmaddrCIDHost is the well-known vsock CID of the host (VMADDR_CID_HOST). The
// guest dials it to reach the host's Plan9/9p server for a workspace share.
const vmaddrCIDHost = 2

// mountShare mounts a host Plan9/9p share at target (the Files door, S3.1).
//
// HCS serves the share over hvsock, so we dial AF_VSOCK to the host on the
// share's port, then mount 9p over that connected socket with trans=fd (the
// kernel's 9p client has no hvsock transport, so it rides our fd). This mirrors
// hcsshim's guest-side plan9.Mount. A shell can't pass an fd to mount(2), which
// is why this lives in guestd. Invoked by the broker over our control plane
// after it adds the share to the running VM via ModifyComputeSystem.
func mountShare(port uint32, tag, target string) error {
	if err := unix.Mkdir(target, 0o755); err != nil && !errors.Is(err, unix.EEXIST) {
		return fmt.Errorf("mkdir %s: %w", target, err)
	}

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

// unmountShare unmounts a previously-mounted share at target.
func unmountShare(target string) error {
	if err := unix.Unmount(target, 0); err != nil {
		return fmt.Errorf("unmount %s: %w", target, err)
	}
	return nil
}
