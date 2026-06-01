//go:build darwin && !debugconsole

package vmm

import (
	"io"
	"log/slog"

	vz "github.com/Code-Hex/vz/v3"
)

// Release builds compile this inert seam instead of debugconsole_on_darwin.go: the
// debug-console code (the hvc1 root shell outside the cage) is NOT present in the binary at
// all, and these stubs make the always-compiled driver wire nothing. The gate is the
// build tag — there is no runtime flag to flip.

// debugConsoleToken returns no kernel-cmdline token, so init.sh never spawns the hvc1 shell.
func debugConsoleToken() string { return "" }

// newDebugConsolePort adds no second serial port — release VMs have only the hvc0 boot console.
func newDebugConsolePort(string, *slog.Logger) (io.Closer, *vz.VirtioConsoleDeviceSerialPortConfiguration, error) {
	return nil, nil, nil
}
