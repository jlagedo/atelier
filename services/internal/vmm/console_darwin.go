//go:build darwin

package vmm

import (
	"bufio"
	"log/slog"
	"os"

	vz "github.com/Code-Hex/vz/v3"
)

// darwinConsole is the host end of a VM's virtio serial console (hvc0). Unlike
// the Windows COM1-over-named-pipe model, Virtualization.framework writes the
// console to an os.File handle; we wire that to a pipe and log each line, the
// darwin analog of console_windows.go.
//
// Two pipes back the VZ attachment:
//   - out: the guest's console output. The VM writes outW; the pump reads outR.
//   - in:  the guest's console input. The VM reads inR; we hold inW open but
//     never write, so guest reads block instead of hitting a spurious EOF.
type darwinConsole struct {
	inR, inW   *os.File
	outR, outW *os.File
}

// newDarwinConsole creates the console pipes, builds the VZ serial-port
// attachment, and starts a goroutine pumping guest output into log line by line.
func newDarwinConsole(log *slog.Logger) (*darwinConsole, *vz.VirtioConsoleDeviceSerialPortConfiguration, error) {
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
		_ = inR.Close()
		_ = inW.Close()
		_ = outR.Close()
		_ = outW.Close()
		return nil, nil, err
	}
	cfg, err := vz.NewVirtioConsoleDeviceSerialPortConfiguration(att)
	if err != nil {
		_ = inR.Close()
		_ = inW.Close()
		_ = outR.Close()
		_ = outW.Close()
		return nil, nil, err
	}

	c := &darwinConsole{inR: inR, inW: inW, outR: outR, outW: outW}
	go c.pump(log)
	return c, cfg, nil
}

// pump logs the guest's serial output one line at a time until the pipe closes.
func (c *darwinConsole) pump(log *slog.Logger) {
	sc := bufio.NewScanner(c.outR)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		log.Info("console", "line", sc.Text())
	}
}

// Close tears down both pipes; closing outR ends the pump goroutine. Call only
// after the VM has stopped so the framework is no longer writing the console.
func (c *darwinConsole) Close() error {
	_ = c.inW.Close()
	_ = c.inR.Close()
	_ = c.outW.Close()
	return c.outR.Close()
}
